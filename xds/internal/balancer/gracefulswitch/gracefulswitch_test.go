/*
 *
 * Copyright 2022 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package gracefulswitch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

const (
	defaultTestTimeout      = 5 * time.Second
	defaultTestShortTimeout = 10 * time.Millisecond
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

func setup(t *testing.T) (*testutils.TestClientConn, *Balancer) {
	tcc := testutils.NewTestClientConn(t)
	return tcc, NewGracefulSwitchBalancer(tcc, balancer.BuildOptions{})
}

// TestSuccessfulFirstUpdate tests a basic scenario for the Graceful Switch Load
// Balancer, where it is setup with a balancer which should populate the Current
// Load Balancer. Any ClientConn updates should then be forwarded to this
// Current Load Balancer.
func (s) TestSuccessfulFirstUpdate(t *testing.T) {
	_, gsb := setup(t)
	builder := balancer.Get(balancerName1)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName1)
	}
	if err := gsb.SwitchTo(builder); err != nil {
		t.Fatalf("Balancer.SwitchTo failed with error: %v", err)
	}
	if gsb.balancerCurrent == nil {
		t.Fatal("balancerCurrent was not built out when a correct SwitchTo() call should've triggered the balancer to build")
	}
	// This will be used to update the Graceful Switch Balancer. This update
	// should simply be forwarded down to the Current Load Balancing Policy.
	ccs := balancer.ClientConnState{
		BalancerConfig: mockBalancer1Config{},
	}

	// Updating ClientConnState should forward the update exactly as is to the
	// current balancer.
	if err := gsb.UpdateClientConnState(ccs); err != nil {
		t.Fatalf("Balancer.UpdateClientConnState(%v) failed with error: %v", ccs, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	if err := gsb.balancerCurrent.Balancer.(*mockBalancer1).waitForClientConnUpdate(ctx, ccs); err != nil {
		t.Fatalf("error in ClientConnState update: %v", err)
	}
}

// TestTwoBalancersSameType tests the scenario where there is a graceful switch
// load balancer setup with a current and pending Load Balancer of the same
// type. Any ClientConn update should be forwarded to the current if no pending,
// and the only the pending if the graceful switch balancer contains both a
// current and pending. The pending Load Balancer should also swap into current
// whenever it updates with a connectivity state other than CONNECTING.
func (s) TestTwoBalancersSameType(t *testing.T) {
	tcc, gsb := setup(t)
	// This will be used to update the Graceful Switch Balancer. This update
	// should simply be forwarded down to either the Current or Pending Load
	// Balancing Policy.
	ccs := balancer.ClientConnState{
		BalancerConfig: mockBalancer1Config{},
	}

	builder := balancer.Get(balancerName1)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName1)
	}
	gsb.SwitchTo(builder)
	gsb.UpdateClientConnState(ccs)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := gsb.balancerCurrent.Balancer.(*mockBalancer1).waitForClientConnUpdate(ctx, ccs); err != nil {
		t.Fatalf("error in ClientConnState update: %v", err)
	}

	// The current balancer reporting READY should cause this state
	// to be forwarded to the Client Conn.
	gsb.balancerCurrent.Balancer.(*mockBalancer1).updateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker:            &neverErrPicker{},
	})

	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a part UpdateState call on the ClientConn")
	case state := <-tcc.NewStateCh:
		if state != connectivity.Ready {
			t.Fatal("wanted connectivity state ready")
		}
	}

	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a part UpdateState call on the ClientConn")
	case picker := <-tcc.NewPickerCh:
		// Should receive a never err picker.
		_, err := picker.Pick(balancer.PickInfo{})
		if err != nil {
			t.Fatalf("Graceful Switch Balancer should have sent a never error picker, which is a picker that never errors, instead received err %v", err)
		}
	}

	// An explicit call to switchTo, even if the same type, should cause the
	// balancer to build a new balancer for pending.
	builder = balancer.Get(balancerName1)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName1)
	}
	gsb.SwitchTo(builder)
	if gsb.balancerPending == nil {
		t.Fatal("balancerPending was not built out when another SwitchTo() call should've triggered the pending balancer to build")
	}

	// A Client Conn update received should be forwarded to the new pending LB
	// policy, and not the current one.
	gsb.UpdateClientConnState(ccs)
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	if err := gsb.balancerCurrent.Balancer.(*mockBalancer1).waitForClientConnUpdate(ctx, ccs); err == nil {
		t.Fatal("balancerCurrent should not have received a client Conn update if there is a pending LB policy")
	}
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := gsb.balancerPending.Balancer.(*mockBalancer1).waitForClientConnUpdate(ctx, ccs); err != nil {
		t.Fatalf("error in ClientConnState update: %v", err)
	}

	// If the pending LB reports that is CONNECTING, no update should be sent to
	// the Client Conn.
	gsb.balancerPending.Balancer.(*mockBalancer1).updateState(balancer.State{
		ConnectivityState: connectivity.Connecting,
		// Picker?
	})
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	select {
	case <-tcc.NewStateCh:
		t.Fatal("Pending LB reporting CONNECTING should not forward up to the ClientConn")
	case <-ctx.Done():
	}

	// If the pending LB reports a state other than CONNECTING, the pending LB
	// is logically warmed up, and the ClientConn should be updated with the
	// State and Picker to start using the new policy. The pending LB policy
	// should also be switched into the current LB.
	gsb.balancerPending.Balancer.(*mockBalancer1).updateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker:            &neverErrPicker{},
	})

	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a part UpdateState call on the ClientConn")
	case state := <-tcc.NewStateCh:
		if state != connectivity.Ready {
			t.Fatal("wanted connectivity state ready")
		}
	}

	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a part UpdateState call on the ClientConn")
	case picker := <-tcc.NewPickerCh:
		// This picker should be the recent one sent from UpdateState(), a never
		// err picker, not the nil picker from two updateState() calls previous.
		if picker == nil {
			t.Fatalf("Graceful Switch Balancer should have sent the most recent picker from an UpdateState() call")
		}
		_, err := picker.Pick(balancer.PickInfo{})
		if err != nil {
			t.Fatalf("Graceful Switch Balancer should have sent a never error picker, which is a picker that never errors, instead received err %v", err)
		}
	}

	deadline := time.Now().Add(defaultTestTimeout)
	// Poll to see if pending was deleted (happens in forked goroutine).
	for {
		gsb.mu.Lock()
		if gsb.balancerPending == nil {
			gsb.mu.Unlock()
			break
		}
		gsb.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("balancerPending was not deleted as the pending LB reported a state other than READY, which should switch pending to current")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestCurrentNotReadyPendingUpdate tests the scenario where there is a Current
// and Pending Load Balancer setup in the Graceful Switch Load Balancer, and the
// current LB is not in the connectivity state READY. Any update from the
// pending Load Balancer should cause the Graceful Switch Load Balancer to swap
// the Pending into Current, and update the ClientConn with the Pending Load
// Balancers state.
func (s) TestCurrentNotReadyPendingUpdate(t *testing.T) {
	tcc, gsb := setup(t)
	builder := balancer.Get(balancerName1)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName1)
	}
	gsb.SwitchTo(builder)
	builder = balancer.Get(balancerName1)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName1)
	}
	gsb.SwitchTo(builder)
	if gsb.balancerPending == nil {
		t.Fatal("balancerPending was not built out when another SwitchTo() call should've triggered the pending balancer to build")
	}
	// Due to the Current Load Balancer not being in state READY, any update
	// from the Pending Load Balancer should cause that update to be forwarded
	// to the Client Conn and also cause the Pending Load Balancer to swap into
	// the current one.
	gsb.balancerPending.Balancer.(*mockBalancer1).updateState(balancer.State{
		ConnectivityState: connectivity.Connecting,
		Picker:            &neverErrPicker{},
	})
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout waiting for an UpdateState call on the Client Conn")
	case state := <-tcc.NewStateCh:
		if state != connectivity.Connecting {
			t.Fatalf("Graceful Switch Balancer should have sent the connectivity state CONNECTING received from recent update")
		}
	}
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout waiting for an UpdateState call on the Client Conn")
	case picker := <-tcc.NewPickerCh:
		// Should receive a never err picker.
		_, err := picker.Pick(balancer.PickInfo{})
		if err != nil {
			t.Fatalf("Graceful Switch Balancer should have sent a never error picker, which is a picker that never errors, instead received err %v", err)
		}
	}

	deadline := time.Now().Add(defaultTestTimeout)
	// Poll to see if pending was deleted (happens in forked goroutine).
	for {
		gsb.mu.Lock()
		if gsb.balancerPending == nil {
			gsb.mu.Unlock()
			break
		}
		gsb.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("balancerPending was not deleted as the current was in a state other than READY, which should switch pending to current")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestCurrentLeavingReady tests the scenario where there is a current and
// pending Load Balancer setup in the Graceful Switch Load Balancer, with the
// current Load Balancer being in the state READY, and the current Load Balancer
// then transitions into a state other than READY. This should cause the Pending
// Load Balancer to swap into the current load balancer, and the Client Conn to
// be updated with the cached Pending Load Balancing state. Also, once the
// current is cleared from the Graceful Switch Load Balancer, any updates sent
// should be intercepted and not forwarded to the ClientConn, as the balancer
// has already been cleared.
func (s) TestCurrentLeavingReady(t *testing.T) {
	tcc, gsb := setup(t)
	builder := balancer.Get(balancerName1)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName1)
	}
	gsb.SwitchTo(builder)
	currBal := gsb.balancerCurrent.Balancer.(*mockBalancer1)
	currBal.updateState(balancer.State{
		ConnectivityState: connectivity.Ready,
	})

	builder = balancer.Get(balancerName2)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName2)
	}
	gsb.SwitchTo(builder)
	// Sends CONNECTING, shouldn't make it's way to ClientConn.
	gsb.balancerPending.Balancer.(*mockBalancer2).updateState(balancer.State{
		ConnectivityState: connectivity.Connecting,
		Picker:            &neverErrPicker{},
	})

	// The current balancer leaving READY should cause the pending balancer to
	// swap to the current balancer. This swap from current to pending should
	// also update the ClientConn with the pending balancers cached state and
	// picker.
	currBal.updateState(balancer.State{
		ConnectivityState: connectivity.Idle,
	})

	// Sends CACHED state and picker (i.e. CONNECTING STATE + a picker you define yourself?)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a part UpdateState call on the ClientConn")
	case state := <-tcc.NewStateCh:
		if state != connectivity.Connecting {
			t.Fatal("wanted connectivity state CONNECTING")
		}
	}

	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a part UpdateState call on the ClientConn")
	case picker := <-tcc.NewPickerCh:
		// Should receive a never err picker cached from pending LB's updateState() call, which
		// was cached.
		_, err := picker.Pick(balancer.PickInfo{})
		if err != nil {
			t.Fatalf("Graceful Switch Balancer should have sent cached picker, which was a picker that never errors, instead received err %v", err)
		}
	}

	deadline := time.Now().Add(defaultTestTimeout)
	// Poll to see if pending was deleted (happens in forked goroutine).
	for {
		gsb.mu.Lock()
		if gsb.balancerPending == nil {
			gsb.mu.Unlock()
			break
		}
		gsb.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("balancerPending was not deleted as the pending LB reported a state other than READY, which should switch pending to current")
		}
		time.Sleep(time.Millisecond)
	}

	deadline = time.Now().Add(defaultTestTimeout)
	// Poll to see if current is of right type as it just got replaced by
	// MockBalancer2 (happens in forked goroutine).
	for {
		if _, ok := gsb.balancerCurrent.Balancer.(*mockBalancer2); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gsb balancerCurrent should be replaced by the balancer of mockBalancer2")
		}
		time.Sleep(time.Millisecond)
	}

	// Make sure current is of right type as it just got replaced by MockBalancer2
	if _, ok := gsb.balancerCurrent.Balancer.(*mockBalancer2); !ok {
		t.Fatal("gsb balancerCurrent should be replaced by the balancer of mockBalancer2")
	}

	// The current balancer is now cleared from the Graceful Switch Load
	// Balancer. Thus, any update from the old current should be intercepted by
	// the Graceful Switch Load Balancer and not forward up to the ClientConn.
	currBal.updateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker:            &neverErrPicker{},
	})

	// This update should not be forwarded to the ClientConn.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-tcc.NewStateCh:
		t.Fatal("UpdateState() from a cleared balancer should not make it's way to ClientConn")
	}

	_, err := currBal.newSubConn([]resolver.Address{}, balancer.NewSubConnOptions{})
	if err == nil {
		t.Fatal("newSubConn() from a cleared balancer should have returned an error")
	}

	// This newSubConn call should also not reach the ClientConn.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-tcc.NewSubConnCh:
		t.Fatal("newSubConn() from a cleared balancer should not make it's way to ClientConn")
	}
}

// TestBalancerSubconns tests the SubConn functionality of the Graceful Switch
// Load Balancer. This tests the SubConn update flow in both directions, and
// make sure updates end up at the correct component. Also, it tests that on an
// UpdateSubConnState() call from the ClientConn, the Graceful Switch Load
// Balancer forwards it to the correct child balancer.
func (s) TestBalancerSubconns(t *testing.T) {
	tcc, gsb := setup(t)
	builder := balancer.Get(balancerName1)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName1)
	}
	gsb.SwitchTo(builder)

	builder = balancer.Get(balancerName2)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName2)
	}
	gsb.SwitchTo(builder)

	// A child balancer creating a new SubConn should eventually be forwarded to
	// the ClientConn held by the graceful switch load balancer.
	sc1, err := gsb.balancerCurrent.Balancer.(*mockBalancer1).newSubConn([]resolver.Address{}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error constructing newSubConn in gsb: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an NewSubConn call on the ClientConn")
	case sc := <-tcc.NewSubConnCh:
		if !cmp.Equal(sc1, sc, cmp.AllowUnexported(testutils.TestSubConn{})) {
			t.Fatalf("NewSubConn, want %v, got %v", sc1, sc)
		}
	}

	// The other child balancer creating a new SubConn should also be eventually
	// be forwarded to the ClientConn held by the graceful switch load balancer.
	sc2, err := gsb.balancerPending.Balancer.(*mockBalancer2).newSubConn([]resolver.Address{}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error constructing newSubConn in gsb: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an NewSubConn call on the ClientConn")
	case sc := <-tcc.NewSubConnCh:
		if !cmp.Equal(sc2, sc, cmp.AllowUnexported(testutils.TestSubConn{})) {
			t.Fatalf("NewSubConn, want %v, got %v", sc2, sc)
		}
	}
	scState := balancer.SubConnState{ConnectivityState: connectivity.Ready}
	// Updating the SubConnState for sc1 should cause the graceful switch
	// balancer to forward the Update to balancerCurrent for sc1, as that is the
	// balancer that created this SubConn.
	gsb.UpdateSubConnState(sc1, scState)

	// This update should get forwarded to balancerCurrent, as that is the LB
	// that created this SubConn.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := gsb.balancerCurrent.Balancer.(*mockBalancer1).waitForSubConnUpdate(ctx, subConnWithState{sc: sc1, state: scState}); err != nil {
		t.Fatalf("error in subConn update: %v", err)
	}
	// This update should not get forwarded to balancerPending, as that is not
	// the LB that created this SubConn.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	if err := gsb.balancerPending.Balancer.(*mockBalancer2).waitForSubConnUpdate(ctx, subConnWithState{sc: sc1, state: scState}); err == nil {
		t.Fatalf("balancerPending should not have received a subconn update for sc1")
	}

	// Updating the SubConnState for sc2 should cause the graceful switch
	// balancer to forward the Update to balancerPending for sc2, as that is the
	// balancer that created this SubConn.
	gsb.UpdateSubConnState(sc2, scState)

	// This update should get forwarded to balancerPending, as that is the LB
	// that created this SubConn.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := gsb.balancerPending.Balancer.(*mockBalancer2).waitForSubConnUpdate(ctx, subConnWithState{sc: sc2, state: scState}); err != nil {
		t.Fatalf("error in subConn update: %v", err)
	}

	// This update should not get forwarded to balancerCurrent, as that is not
	// the LB that created this SubConn.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	if err := gsb.balancerCurrent.Balancer.(*mockBalancer1).waitForSubConnUpdate(ctx, subConnWithState{sc: sc2, state: scState}); err == nil {
		t.Fatalf("balancerCurrent should not have received a subconn update for sc2")
	}

	// Updating the addresses for both SubConns and removing both SubConns
	// should get forwarded to the ClientConn.

	// Updating the addresses for sc1 should get forwarded to the ClientConn.
	gsb.balancerCurrent.Balancer.(*mockBalancer1).updateAddresses(sc1, []resolver.Address{})

	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an UpdateAddresses call on the ClientConn")
	case <-tcc.UpdateAddressesAddrsCh:
	}

	// Updating the addresses for sc2 should also get forwarded to the ClientConn.
	gsb.balancerPending.Balancer.(*mockBalancer2).updateAddresses(sc2, []resolver.Address{})
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an UpdateAddresses call on the ClientConn")
	case <-tcc.UpdateAddressesAddrsCh:
	}

	// Current Balancer removing sc1 should get forwarded to the ClientConn.
	gsb.balancerCurrent.Balancer.(*mockBalancer1).removeSubConn(sc1)
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an UpdateAddresses call on the ClientConn")
	case sc := <-tcc.RemoveSubConnCh:
		if !cmp.Equal(sc1, sc, cmp.AllowUnexported(testutils.TestSubConn{})) {
			t.Fatalf("RemoveSubConn, want %v, got %v", sc1, sc)
		}
	}
	// Pending Balancer removing sc2 should get forwarded to the ClientConn.
	gsb.balancerPending.Balancer.(*mockBalancer2).removeSubConn(sc2)
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an UpdateAddresses call on the ClientConn")
	case sc := <-tcc.RemoveSubConnCh:
		if !cmp.Equal(sc2, sc, cmp.AllowUnexported(testutils.TestSubConn{})) {
			t.Fatalf("RemoveSubConn, want %v, got %v", sc2, sc)
		}
	}
}

// TestBalancerClose tests the Graceful Switch Balancer's Close() functionality.
// From the Close() call, the Graceful Switch Balancer should remove any created
// Subconns and Close() the Current and Pending Load Balancers. This Close()
// call should also cause any other events (calls to entrance functions) to be
// no-ops.
func (s) TestBalancerClose(t *testing.T) {
	// Setup gsb balancer with current, pending, and one created SubConn on both
	// current and pending.
	tcc, gsb := setup(t)
	builder := balancer.Get(balancerName1)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName1)
	}
	gsb.SwitchTo(builder)

	builder = balancer.Get(balancerName2)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName2)
	}
	gsb.SwitchTo(builder)

	sc1, err := gsb.balancerCurrent.Balancer.(*mockBalancer1).newSubConn([]resolver.Address{}, balancer.NewSubConnOptions{}) // Will eventually get back a SubConn with an identifying property id 1
	if err != nil {
		t.Fatalf("error constructing newSubConn in gsb: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an NewSubConn call on the ClientConn")
	case <-tcc.NewSubConnCh:
	}

	sc2, err := gsb.balancerPending.Balancer.(*mockBalancer2).newSubConn([]resolver.Address{}, balancer.NewSubConnOptions{}) // Will eventually get back a SubConn with an identifying property id 2
	if err != nil {
		t.Fatalf("error constructing newSubConn in gsb: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an NewSubConn call on the ClientConn")
	case <-tcc.NewSubConnCh:
	}

	currBal := gsb.balancerCurrent.Balancer.(*mockBalancer1)
	pendBal := gsb.balancerPending.Balancer.(*mockBalancer2)

	// Closing the Graceful Switch Load Balancer should lead to removing any
	// created SubConns, and closing both the current and pending Load Balancer.
	gsb.Close()

	// The order of SubConns the Graceful Switch Balancer tells the Client Conn
	// to remove is non deterministic, as it is stored in a map. However, the
	// first SubConn removed should be either sc1 or sc2.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an UpdateAddresses call on the ClientConn")
	case sc := <-tcc.RemoveSubConnCh:
		if !cmp.Equal(sc1, sc, cmp.AllowUnexported(testutils.TestSubConn{})) {
			if !cmp.Equal(sc2, sc, cmp.AllowUnexported(testutils.TestSubConn{})) {
				t.Fatalf("RemoveSubConn, want either %v or %v, got %v", sc1, sc2, sc)
			}
		}
	}

	// The Graceful Switch Balancer should then tell the Client Conn to remove
	// the other SubConn.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an UpdateAddresses call on the ClientConn")
	case sc := <-tcc.RemoveSubConnCh:
		if !cmp.Equal(sc1, sc, cmp.AllowUnexported(testutils.TestSubConn{})) {
			if !cmp.Equal(sc2, sc, cmp.AllowUnexported(testutils.TestSubConn{})) {
				t.Fatalf("RemoveSubConn, want either %v or %v, got %v", sc1, sc2, sc)
			}
		}
	}

	// The current balancer should get closed as a result of the graceful switch balancer being closed.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := currBal.waitForClose(ctx); err != nil {
		t.Fatalf("error in close: %v", err)
	}
	// The pending balancer should also get closed as a result of the graceful switch balancer being closed.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := pendBal.waitForClose(ctx); err != nil {
		t.Fatalf("error in close: %v", err)
	}

	// Also, once this event happens, trying to do anything else on both codepaths
	// should be a no-op. (Should I try and have an event happen from a lower level
	// balancer? Is this possible?)

	// Once the graceful switch balancer has been closed, any entrance function
	// should be a no-op and return errBalancerClosed if the function returns an
	// error.

	// SwitchTo() should return an error due to the Graceful Switch Balancer
	// having been closed already.
	builder = balancer.Get(balancerName1)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName1)
	}
	if err := gsb.SwitchTo(builder); err != errBalancerClosed {
		t.Fatalf("gsb.SwitchTo(%v) returned error %v, want %v", builder, err, errBalancerClosed)
	}

	// UpdateClientConnState() should return an error due to the Graceful Switch
	// Balancer having been closed already.
	ccs := balancer.ClientConnState{
		BalancerConfig: mockBalancer1Config{},
	}
	if err := gsb.UpdateClientConnState(ccs); err != errBalancerClosed {
		t.Fatalf("gsb.UpdateCLientConnState(%v) returned error %v, want %v", ccs, err, errBalancerClosed)
	}

	// After the Graceful Switch Balancer has been closed, any resolver error
	// shouldn't forward to either balancer, as the resolver error is a no-op
	// and also even if not, the balancers should have been cleared from the
	// Graceful Switch Balancer.
	gsb.ResolverError(balancer.ErrBadResolverState)
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	if err := currBal.waitForResolverError(ctx, balancer.ErrBadResolverState); err != ctx.Err() {
		t.Fatal("the current balancer should not have received the resolver error after close")
	}
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	if err := pendBal.waitForResolverError(ctx, balancer.ErrBadResolverState); err != ctx.Err() {
		t.Fatal("the pending balancer should not have received the resolver error after close")
	}
}

// TestResolverError tests the functionality of a Resolver Error. If there is a
// current balancer, but no pending, the error should be forwarded to the
// current balancer. If there is both a current and pending balancer, the error
// should be forwarded to only the pending balancer.
func (s) TestResolverError(t *testing.T) {
	_, gsb := setup(t)
	builder := balancer.Get(balancerName1)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName1)
	}
	gsb.SwitchTo(builder)
	currBal := gsb.balancerCurrent.Balancer.(*mockBalancer1)
	// If there is only a current balancer present, the resolver error should be
	// forwarded to the current balancer.
	gsb.ResolverError(balancer.ErrBadResolverState)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := currBal.waitForResolverError(ctx, balancer.ErrBadResolverState); err != nil {
		t.Fatalf("error waiting for resolver error in current balancer: %v", err)
	}

	builder = balancer.Get(balancerName1)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName1)
	}
	gsb.SwitchTo(builder)

	// If there is a pending balancer present, then a resolver error should be
	// forwarded to only the pending balancer, not the current.
	pendBal := gsb.balancerPending.Balancer.(*mockBalancer1)
	gsb.ResolverError(balancer.ErrBadResolverState)

	// The Resolver Error should not be forwarded to the current Load Balancer.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	if err := currBal.waitForResolverError(ctx, balancer.ErrBadResolverState); err != ctx.Err() {
		t.Fatal("the current balancer should not have received the resolver error after close")
	}

	// The Resolver Error should be forwarded to the pending Load Balancer.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := pendBal.waitForResolverError(ctx, balancer.ErrBadResolverState); err != nil {
		t.Fatalf("error waiting for resolver error in pending balancer: %v", err)
	}
}

// TestPendingReplacedByAnotherPending tests the scenario where a Graceful
// Switch Balancer has a current and pending Load Balancer, and receives a
// SwitchTo() call, which then replaces the pending. This should cause the
// GracefulSwitchBalancer to clear pending state, close old pending SubConns,
// and Close() the Pending balancer being replaced.
func (s) TestPendingReplacedByAnotherPending(t *testing.T) {
	tcc, gsb := setup(t)
	builder := balancer.Get(balancerName1)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName1)
	}
	gsb.SwitchTo(builder)
	currBal := gsb.balancerCurrent.Balancer.(*mockBalancer1)
	currBal.updateState(balancer.State{
		ConnectivityState: connectivity.Ready,
	})

	builder = balancer.Get(balancerName2)
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName2)
	}
	// Populate pending with a SwitchTo() call.
	gsb.SwitchTo(builder)

	pendBal := gsb.balancerPending.Balancer.(*mockBalancer2)
	sc1, err := pendBal.newSubConn([]resolver.Address{}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error constructing newSubConn in gsb: %v", err)
	}
	// This picker never returns an error, which can help this this test verify
	// whether this cached state will get cleared on a new pending balancer
	// (will replace it with a picker that always errors).
	pendBal.updateState(balancer.State{
		ConnectivityState: connectivity.Connecting,
		Picker:            &neverErrPicker{},
	})

	// Replace pending with a new pending balancer.
	builder = balancer.Get(balancerName2) // TODO: You could pass the same pointer to SwitchTo() and thus maybe add an equality check on pointer similar to Java
	if builder == nil {
		t.Fatalf("balancer.Get(%v) returned nil", balancerName2)
	}
	// Replace pending with a SwitchTo() call.
	gsb.SwitchTo(builder)
	// The pending balancer being replaced should cause the Graceful Switch
	// Balancer to Remove() any created SubConns for the old pending balancer
	// and also Close() the old pending balancer.
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a RemoveSubConn call on the ClientConn")
	case sc := <-tcc.RemoveSubConnCh:
		if !cmp.Equal(sc1, sc, cmp.AllowUnexported(testutils.TestSubConn{})) {
			t.Fatalf("RemoveSubConn, want %v, got %v", sc1, sc)
		}
	}

	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := pendBal.waitForClose(ctx); err != nil {
		t.Fatalf("error waiting for balancer close: %v", err)
	}

	// Switching the current out of READY should cause the pending LB to swap
	// into current, causing the Graceful Switch Balancer to update the
	// ClientConn with the cached pending state. Since the new pending hasn't
	// sent an Update, the default state with connectivity state CONNECTING and
	// an errPicker should be sent to the ClientConn.
	currBal.updateState(balancer.State{
		ConnectivityState: connectivity.Idle,
	})

	// The update should contain a default connectivity state CONNECTING for the
	// state of the new pending LB policy.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an UpdateState() call on the ClientConn")
	case state := <-tcc.NewStateCh:
		if state != connectivity.Connecting {
			t.Fatalf("UpdateState(), want connectivity state %v, got %v", connectivity.Connecting, state)
		}
	}
	// The update should contain a default picker ErrPicker in the picker sent
	// for the state of the new pending LB policy.
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an UpdateState() call on the ClientConn")
	case picker := <-tcc.NewPickerCh:
		_, err := picker.Pick(balancer.PickInfo{})
		if err != balancer.ErrNoSubConnAvailable {
			t.Fatalf("picker should be an error picker which always returns balancer.ErrNoSubConnAvailable, got %v", err)
		}
	}
}

// Picker which never errors here for test purposes (can fill up tests further up with this)
type neverErrPicker struct{}

func (p *neverErrPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	return balancer.PickResult{}, nil
}

// TestUpdateSubConnStateRace tests the race condition when the GracefulSwitchBalancer
// receives a SubConnUpdate concurrently with an UpdateState() call, which can cause
// the balancer to forward the update to to be closed and cleared. The balancer API
// guarantees to never call any method the balancer after a Close() call, and the test
// verifies that doesn't happen within the Graceful Switch Load Balancer.
func (s) TestUpdateSubConnStateRace(t *testing.T) {
	tcc, gsb := setup(t)
	builder := balancer.Get(verifyBalName)
	gsb.SwitchTo(builder)

	builder = balancer.Get(balancerName1)
	gsb.SwitchTo(builder)
	currBal := gsb.balancerCurrent.Balancer.(*verifyBalancer)
	currBal.t = t
	pendBal := gsb.balancerPending.Balancer.(*mockBalancer1)
	sc, err := currBal.newSubConn([]resolver.Address{}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error constructing newSubConn in gsb: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an NewSubConn call on the ClientConn")
	case <-tcc.NewSubConnCh:
	}
	// Spawn a goroutine that constantly calls UpdateSubConn for the current
	// balancer, which will get deleted in this testing goroutine.
	finished := make(chan struct{})
	go func() {
		for {
			select {
			case <-finished:
				return
			default:
			}
			gsb.UpdateSubConnState(sc, balancer.SubConnState{
				ConnectivityState: connectivity.Ready,
			})
		}
	}()
	time.Sleep(time.Millisecond)
	// This UpdateState call causes current to be closed/cleared.
	pendBal.updateState(balancer.State{
		ConnectivityState: connectivity.Ready,
	})
	// From this, either one of two things happen. Either the
	// GracefulSwitchBalancer doesn't Close() the current balancer before it
	// forwards the SubConn update to the child, and the call gets forwarded
	// down to the current balancer, or it can Close() the current balancer in
	// between reading the balancer pointer and writing to it, and in that case
	// the old current balancer should not be updated, as the balancer has
	// already been closed and the balancer API guarantees it.
	close(finished)
}

// TestInlineCallbackInBuild tests the scenario where a balancer calls back into
// the balancer.ClientConn API inline from it's build function.
func (s) TestInlineCallbackInBuild(t *testing.T) {
	tcc, gsb := setup(t)
	builder := balancer.Get(buildCallbackBalName)
	// This build call should cause all of the inline updates to forward to the
	// ClientConn.
	gsb.SwitchTo(builder)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an UpdateState() call on the ClientConn")
	case <-tcc.NewStateCh:
	}
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an NewSubConn() call on the ClientConn")
	case <-tcc.NewSubConnCh:
	}
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an UpdateAddresses() call on the ClientConn")
	case <-tcc.UpdateAddressesAddrsCh:
	}
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an RemoveSubConn() call on the ClientConn")
	case <-tcc.RemoveSubConnCh:
	}
	oldCurrent := gsb.balancerCurrent.Balancer.(*buildCallbackBal)

	// ClientConn. Since the callback reports a state READY, this new inline
	// balancer should be swapped to the current.
	builder = balancer.Get(buildCallbackBalName)
	gsb.SwitchTo(builder)
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an UpdateState() call on the ClientConn")
	case <-tcc.NewStateCh:
	}
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an NewSubConn() call on the ClientConn")
	case <-tcc.NewSubConnCh:
	}
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an UpdateAddresses() call on the ClientConn")
	case <-tcc.UpdateAddressesAddrsCh:
	}
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for an RemoveSubConn() call on the ClientConn")
	case <-tcc.RemoveSubConnCh:
	}

	deadline := time.Now().Add(defaultTestTimeout)
	// Poll to see if pending was deleted (happens in forked goroutine).
	for {
		gsb.mu.Lock()
		if gsb.balancerPending == nil {
			gsb.mu.Unlock()
			break
		}
		gsb.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("balancerPending was not deleted as the pending LB reported a READY state, which should switch pending to current")
		}
		time.Sleep(time.Millisecond)
	}

	// The old balancer should be deprecated and any calls from it should be a no-op.
	oldCurrent.newSubConn([]resolver.Address{}, balancer.NewSubConnOptions{})
	ctx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	select {
	case <-tcc.NewSubConnCh:
		t.Fatal("Deprecated LB calling NewSubConn() should not forward up to the ClientConn")
	case <-ctx.Done():
	}
}

const balancerName1 = "mock_balancer_1"
const balancerName2 = "mock_balancer_2"
const verifyBalName = "verifyNoSubConnUpdateAfterCloseBalancer"
const buildCallbackBalName = "callbackInBuildBalancer"

func init() {
	balancer.Register(bb1{})
	balancer.Register(bb2{})
	balancer.Register(vbb{})
	balancer.Register(bcb{})
}

type bb1 struct{}

func (bb1) Build(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
	return &mockBalancer1{
		ccsCh:         testutils.NewChannel(),
		scStateCh:     testutils.NewChannel(),
		resolverErrCh: testutils.NewChannel(),
		closeCh:       testutils.NewChannel(),
		cc:            cc,
	}
}

func (bb1) Name() string {
	return balancerName1
}

type mockBalancer1Config struct {
	serviceconfig.LoadBalancingConfig
}

// mockBalancer is a fake balancer used to verify different actions from
// the gracefulswitch. It contains a bunch of channels to signal different events
// to the test.
type mockBalancer1 struct {
	// ccsCh is a channel used to signal the receipt of a ClientConn update.
	ccsCh *testutils.Channel
	// scStateCh is a channel used to signal the receipt of a SubConn update.
	scStateCh *testutils.Channel
	// resolverErrCh is a channel used to signal a resolver error.
	resolverErrCh *testutils.Channel
	// closeCh is a channel used to signal the closing of this balancer.
	closeCh *testutils.Channel
	// Hold onto Client Conn wrapper to communicate with it
	cc balancer.ClientConn
}

type subConnWithState struct {
	sc    balancer.SubConn
	state balancer.SubConnState
}

func (mb1 *mockBalancer1) UpdateClientConnState(ccs balancer.ClientConnState) error {
	// Need to verify this call...use a channel?...all of these will need verification
	mb1.ccsCh.Send(ccs)
	return nil
}

func (mb1 *mockBalancer1) ResolverError(err error) {
	mb1.resolverErrCh.Send(err)
}

func (mb1 *mockBalancer1) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	mb1.scStateCh.Send(subConnWithState{sc: sc, state: state})
}

func (mb1 *mockBalancer1) Close() {
	mb1.closeCh.Send(struct{}{})
}

// waitForClientConnUpdate verifies if the mockBalancer1 receives the
// provided ClientConnState within a reasonable amount of time.
func (mb1 *mockBalancer1) waitForClientConnUpdate(ctx context.Context, wantCCS balancer.ClientConnState) error {
	ccs, err := mb1.ccsCh.Receive(ctx)
	if err != nil {
		return err
	}
	gotCCS := ccs.(balancer.ClientConnState)
	if diff := cmp.Diff(gotCCS, wantCCS, cmpopts.IgnoreFields(resolver.State{}, "Attributes")); diff != "" {
		return fmt.Errorf("received unexpected ClientConnState, diff (-got +want): %v", diff)
	}
	return nil
}

// waitForSubConnUpdate verifies if the mockBalancer1 receives the provided
// SubConn update before the context expires.
func (mb1 *mockBalancer1) waitForSubConnUpdate(ctx context.Context, wantSCS subConnWithState) error {
	scs, err := mb1.scStateCh.Receive(ctx)
	if err != nil {
		return err
	}
	gotSCS := scs.(subConnWithState)
	if !cmp.Equal(gotSCS, wantSCS, cmp.AllowUnexported(subConnWithState{}, testutils.TestSubConn{})) {
		return fmt.Errorf("received SubConnState: %+v, want %+v", gotSCS, wantSCS)
	}
	return nil
}

// waitForResolverError verifies if the mockBalancer1 receives the provided
// resolver error before the context expires.
func (mb1 *mockBalancer1) waitForResolverError(ctx context.Context, wantErr error) error {
	gotErr, err := mb1.resolverErrCh.Receive(ctx)
	if err != nil {
		return err
	}
	if gotErr != wantErr {
		return fmt.Errorf("received resolver error: %v, want %v", gotErr, wantErr)
	}
	return nil
}

// waitForClose verifies that the mockBalancer1 is closed before the context
// expires.
func (mb1 *mockBalancer1) waitForClose(ctx context.Context) error {
	if _, err := mb1.closeCh.Receive(ctx); err != nil {
		return err
	}
	return nil
}

func (mb1 *mockBalancer1) updateState(state balancer.State) {
	mb1.cc.UpdateState(state)
}

func (mb1 *mockBalancer1) newSubConn(addrs []resolver.Address, opts balancer.NewSubConnOptions) (balancer.SubConn, error) {
	return mb1.cc.NewSubConn(addrs, opts)
}

func (mb1 *mockBalancer1) updateAddresses(sc balancer.SubConn, addrs []resolver.Address) {
	mb1.cc.UpdateAddresses(sc, addrs)
}

func (mb1 *mockBalancer1) removeSubConn(sc balancer.SubConn) {
	mb1.cc.RemoveSubConn(sc)
}

type bb2 struct{}

func (bb2) Build(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
	return &mockBalancer2{
		ccsCh:         testutils.NewChannel(),
		scStateCh:     testutils.NewChannel(),
		resolverErrCh: testutils.NewChannel(),
		closeCh:       testutils.NewChannel(),
		cc:            cc,
	}
}

func (bb2) Name() string {
	return balancerName2
}

// mockBalancer is a fake balancer used to verify different actions from
// the gracefulswitch. It contains a bunch of channels to signal different events
// to the test.
type mockBalancer2 struct {
	// ccsCh is a channel used to signal the receipt of a ClientConn update.
	ccsCh *testutils.Channel
	// scStateCh is a channel used to signal the receipt of a SubConn update.
	scStateCh *testutils.Channel
	// resolverErrCh is a channel used to signal a resolver error.
	resolverErrCh *testutils.Channel
	// closeCh is a channel used to signal the closing of this balancer.
	closeCh *testutils.Channel
	// Hold onto Client Conn wrapper to communicate with it
	cc balancer.ClientConn
}

func (mb2 *mockBalancer2) UpdateClientConnState(ccs balancer.ClientConnState) error {
	mb2.ccsCh.Send(ccs)
	return nil
}

func (mb2 *mockBalancer2) ResolverError(err error) {
	mb2.resolverErrCh.Send(err)
}

func (mb2 *mockBalancer2) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	mb2.scStateCh.Send(subConnWithState{sc: sc, state: state})
}

func (mb2 *mockBalancer2) Close() {
	mb2.closeCh.Send(struct{}{})
}

// waitForSubConnUpdate verifies if the mockBalancer1 receives the provided
// SubConn update before the context expires.
func (mb2 *mockBalancer2) waitForSubConnUpdate(ctx context.Context, wantSCS subConnWithState) error {
	scs, err := mb2.scStateCh.Receive(ctx)
	if err != nil {
		return err
	}
	gotSCS := scs.(subConnWithState)
	if !cmp.Equal(gotSCS, wantSCS, cmp.AllowUnexported(subConnWithState{}, testutils.TestSubConn{})) {
		return fmt.Errorf("received SubConnState: %+v, want %+v", gotSCS, wantSCS)
	}
	return nil
}

// waitForResolverError verifies if the mockBalancer1 receives the provided
// resolver error before the context expires.
func (mb2 *mockBalancer2) waitForResolverError(ctx context.Context, wantErr error) error {
	gotErr, err := mb2.resolverErrCh.Receive(ctx)
	if err != nil {
		return err
	}
	if gotErr != wantErr {
		return fmt.Errorf("received resolver error: %v, want %v", gotErr, wantErr)
	}
	return nil
}

// waitForClose verifies that the mockBalancer1 is closed before the context
// expires.
func (mb2 *mockBalancer2) waitForClose(ctx context.Context) error {
	if _, err := mb2.closeCh.Receive(ctx); err != nil {
		return err
	}
	return nil
}

func (mb2 *mockBalancer2) updateState(state balancer.State) {
	mb2.cc.UpdateState(state)
}

func (mb2 *mockBalancer2) newSubConn(addrs []resolver.Address, opts balancer.NewSubConnOptions) (balancer.SubConn, error) {
	return mb2.cc.NewSubConn(addrs, opts)
}

func (mb2 *mockBalancer2) updateAddresses(sc balancer.SubConn, addrs []resolver.Address) {
	mb2.cc.UpdateAddresses(sc, addrs)
}

func (mb2 *mockBalancer2) removeSubConn(sc balancer.SubConn) {
	mb2.cc.RemoveSubConn(sc)
}

type vbb struct{}

func (vbb) Build(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
	return &verifyBalancer{
		closed: grpcsync.NewEvent(),
		cc:     cc,
	}
}

func (vbb) Name() string {
	return verifyBalName
}

// verifyBalancer is a balancer that verifies that after a Close() call, an
// updateSubConnState() call never happens.
type verifyBalancer struct {
	closed *grpcsync.Event
	// Hold onto the ClientConn wrapper to communicate with it.
	cc balancer.ClientConn
	// To fail the test if UpdateSubConnState gets called after Close().
	t *testing.T
}

func (vb *verifyBalancer) UpdateClientConnState(ccs balancer.ClientConnState) error {
	return nil
}

func (vb *verifyBalancer) ResolverError(err error) {}

func (vb *verifyBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	if vb.closed.HasFired() {
		vb.t.Fatal("UpdateSubConnState was called after Close(), which breaks the balancer API")
	}
}

func (vb *verifyBalancer) Close() {
	vb.closed.Fire()
}

func (vb *verifyBalancer) newSubConn(addrs []resolver.Address, opts balancer.NewSubConnOptions) (balancer.SubConn, error) {
	return vb.cc.NewSubConn(addrs, opts)
}

type bcb struct{}

func (bcb) Build(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
	b := &buildCallbackBal{cc: cc}
	b.updateState(balancer.State{
		ConnectivityState: connectivity.Connecting,
	})
	sc, err := b.newSubConn([]resolver.Address{}, balancer.NewSubConnOptions{})
	if err != nil {
		return nil
	}
	b.updateAddresses(sc, []resolver.Address{})
	b.removeSubConn(sc)
	return b
}

func (bcb) Name() string {
	return buildCallbackBalName
}

type buildCallbackBal struct {
	// Hold onto the ClientConn wrapper to communicate with it.
	cc balancer.ClientConn
}

func (bcb *buildCallbackBal) UpdateClientConnState(ccs balancer.ClientConnState) error {
	return nil
}

func (bcb *buildCallbackBal) ResolverError(err error) {}

func (bcb *buildCallbackBal) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {}

func (bcb *buildCallbackBal) Close() {}

func (bcb *buildCallbackBal) updateState(state balancer.State) {
	bcb.cc.UpdateState(state)
}

func (bcb *buildCallbackBal) newSubConn(addrs []resolver.Address, opts balancer.NewSubConnOptions) (balancer.SubConn, error) {
	return bcb.cc.NewSubConn(addrs, opts)
}

func (bcb *buildCallbackBal) updateAddresses(sc balancer.SubConn, addrs []resolver.Address) {
	bcb.cc.UpdateAddresses(sc, addrs)
}

func (bcb *buildCallbackBal) removeSubConn(sc balancer.SubConn) {
	bcb.cc.RemoveSubConn(sc)
}