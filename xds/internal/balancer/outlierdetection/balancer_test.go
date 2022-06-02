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

package outlierdetection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/resolver"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/internal/grpctest"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/serviceconfig"
	"google.golang.org/grpc/xds/internal/balancer/clusterimpl"
)

var (
	defaultTestTimeout = 5 * time.Second
	defaultTestShortTimeout = 10 * time.Millisecond
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// TestParseConfig verifies the ParseConfig() method in the CDS balancer.
func (s) TestParseConfig(t *testing.T) {
	bb := balancer.Get(Name)
	if bb == nil {
		t.Fatalf("balancer.Get(%q) returned nil", Name)
	}
	parser, ok := bb.(balancer.ConfigParser)
	if !ok {
		t.Fatalf("balancer %q does not implement the ConfigParser interface", Name)
	}

	tests := []struct {
		name    string
		input   json.RawMessage
		wantCfg serviceconfig.LoadBalancingConfig
		wantErr bool
	}{
		{
			name:    "noop-lb-config",
			input:   json.RawMessage(`{"interval": 9223372036854775807}`),
			wantCfg: &LBConfig{Interval: 1<<63 - 1},
		},
		{
			name: "good-lb-config",
			input: json.RawMessage(`{
				"interval": 10000000000,
				"baseEjectionTime": 30000000000,
				"maxEjectionTime": 300000000000,
				"maxEjectionPercent": 10,
				"successRateEjection": {
					"stdevFactor": 1900,
					"enforcementPercentage": 100,
					"minimumHosts": 5,
					"requestVolume": 100
				},
				"failurePercentageEjection": {
					"threshold": 85,
					"enforcementPercentage": 5,
					"minimumHosts": 5,
					"requestVolume": 50
				}
			}`), // This implies you don't need a child policy...
			wantCfg: &LBConfig{
				Interval:           10 * time.Second,
				BaseEjectionTime:   30 * time.Second,
				MaxEjectionTime:    300 * time.Second,
				MaxEjectionPercent: 10,
				SuccessRateEjection: &SuccessRateEjection{
					StdevFactor:           1900,
					EnforcementPercentage: 100,
					MinimumHosts:          5,
					RequestVolume:         100,
				},
				FailurePercentageEjection: &FailurePercentageEjection{
					Threshold:             85,
					EnforcementPercentage: 5,
					MinimumHosts:          5,
					RequestVolume:         50,
				},
			},
		},
		{
			name:    "interval-is-negative",
			input:   json.RawMessage(`{"interval": -10}`),
			wantErr: true,
		},
		{
			name:    "base-ejection-time-is-negative",
			input:   json.RawMessage(`{"baseEjectionTime": -10}`),
			wantErr: true,
		},
		{
			name:    "max-ejection-time-is-negative",
			input:   json.RawMessage(`{"maxEjectionTime": -10}`),
			wantErr: true,
		},
		{
			name:    "max-ejection-percent-is-greater-than-100",
			input:   json.RawMessage(`{"maxEjectionPercent": 150}`),
			wantErr: true,
		},
		{
			name: "enforcing-success-rate-is-greater-than-100",
			input: json.RawMessage(`{
				"successRateEjection": {
					"enforcingSuccessRate": 100,
				},
			}`),
			wantErr: true,
		},
		{
			name: "failure-percentage-threshold-is-greater-than-100",
			input: json.RawMessage(`{
				"failurePercentageEjection": {
					"threshold": 150,
				},
			}`),
			wantErr: true,
		},
		{
			name: "enforcing-failure-percentage-is-greater-than-100",
			input: json.RawMessage(`{
				"failurePercentageEjection": {
					"enforcingFailurePercentage": 150,
				},
			}`),
			wantErr: true,
		},
		{
			name: "child-policy",
			input: json.RawMessage(`{
				"childPolicy": [
				{
					"xds_cluster_impl_experimental": {
						"cluster": "test_cluster"
					}
				}
			]
			}`),
			wantCfg: &LBConfig{
				ChildPolicy: &internalserviceconfig.BalancerConfig{
					Name: "xds_cluster_impl_experimental",
					Config: &clusterimpl.LBConfig{
						Cluster: "test_cluster",
					},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotCfg, gotErr := parser.ParseConfig(test.input)
			if (gotErr != nil) != test.wantErr {
				t.Fatalf("ParseConfig(%v) = %v, wantErr %v", string(test.input), gotErr, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if diff := cmp.Diff(gotCfg, test.wantCfg); diff != "" {
				t.Fatalf("parseConfig(%v) got unexpected output, diff (-got +want): %v", string(test.input), diff)
			}
		})
	}
}


// test wrapped picker somehow (balancer style test or can we make it more granular with knobs)

// scw.obj.callCounter.activeBucket.numSuccess/Failures++ test this concurrently with IntervalTImerAlgo and swap()?
// scw.obj.callCounter.activeBucket.requestVolume++

// Methods/behaviors to check:



// mock ClientConn here (priority LB Policy), wait just have this be
// testutils.NewClientConn(t), all this is doing is verifying calls upward
// toward it, this calling down into the OD Balancer is manual and you do it
// yourself.
func setup(t *testing.T) (*outlierDetectionBalancer, *testutils.TestClientConn) {
	t.Helper()
	builder := balancer.Get(Name)
	if builder == nil {
		t.Fatalf("balancer.Get(%q) returned nil", Name)
	}
	tcc := testutils.NewTestClientConn(t)
	odB := builder.Build(tcc, balancer.BuildOptions{})
	return odB.(*outlierDetectionBalancer), tcc
}


func init() {
	balancer.Register(tcibBuilder{})
	balancer.Register(verifyBalancerBuilder{})
}

const tcibname = "testClusterImplBalancer"
const verifyBalancerName = "verifyBalancer"

type tcibBuilder struct{}

func (tcibBuilder) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	return &testClusterImplBalancer{
		ccsCh: testutils.NewChannel(),
		scStateCh: testutils.NewChannel(),
		resolverErrCh: testutils.NewChannel(),
		closeCh: testutils.NewChannel(),
		exitIdleCh: testutils.NewChannel(),
		cc: cc,
	}
}

func (tcibBuilder) Name() string {
	return tcibname
}

// Also needs to be able to ping the od balancer's CC methods, wait can't you
// just call this directly from here, or call it through this balancer (reason
// we needed for graceful switch was to get it from current or pending, I think
// what I have here is fine).
type testClusterImplBalancerConfig struct {
	serviceconfig.LoadBalancingConfig
}

type testClusterImplBalancer struct {
	// ccsCh is a channel used to signal the receipt of a ClientConn update.
	ccsCh *testutils.Channel
	// scStateCh is a channel used to signal the receipt of a SubConn update.
	scStateCh *testutils.Channel
	// resolverErrCh is a channel used to signal a resolver error.
	resolverErrCh *testutils.Channel
	// closeCh is a channel used to signal the closing of this balancer.
	closeCh    *testutils.Channel
	exitIdleCh *testutils.Channel
	// cc is the balancer.ClientConn passed to this test balancer as part of the
	// Build() call.
	cc balancer.ClientConn
}

type subConnWithState struct {
	sc    balancer.SubConn
	state balancer.SubConnState
}

func (tb *testClusterImplBalancer) UpdateClientConnState(ccs balancer.ClientConnState) error {
	tb.ccsCh.Send(ccs)
	return nil
}

func (tb *testClusterImplBalancer) ResolverError(err error) {
	tb.resolverErrCh.Send(err)
}

func (tb *testClusterImplBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	tb.scStateCh.Send(subConnWithState{sc: sc, state: state})
}

func (tb *testClusterImplBalancer) Close() {
	tb.closeCh.Send(struct{}{})
}

// waitForClientConnUpdate verifies if the testClusterImplBalancer receives the
// provided ClientConnState within a reasonable amount of time.
func (tb *testClusterImplBalancer) waitForClientConnUpdate(ctx context.Context, wantCCS balancer.ClientConnState) error {
	ccs, err := tb.ccsCh.Receive(ctx)
	if err != nil {
		return err
	}
	gotCCS := ccs.(balancer.ClientConnState)
	if diff := cmp.Diff(gotCCS, wantCCS, cmpopts.IgnoreFields(resolver.State{}, "Attributes")); diff != "" {
		return fmt.Errorf("received unexpected ClientConnState, diff (-got +want): %v", diff)
	}
	return nil
}

// waitForSubConnUpdate verifies if the testClusterImplBalancer receives the
// provided SubConn and SubConnState within a reasonable amount of time.
func (tb *testClusterImplBalancer) waitForSubConnUpdate(ctx context.Context, wantSCS subConnWithState) error {
	scs, err := tb.scStateCh.Receive(ctx)
	if err != nil {
		return err
	}
	gotSCS := scs.(subConnWithState)
	if !cmp.Equal(gotSCS, wantSCS, cmp.AllowUnexported(subConnWithState{}, testutils.TestSubConn{}, subConnWrapper{}, object{}), cmpopts.IgnoreFields(subConnWrapper{}, "scUpdateCh")) {
		return fmt.Errorf("received SubConnState: %+v, want %+v", gotSCS, wantSCS)
	}
	return nil
}

// waitForClose verifies if the testClusterImplBalancer receives a Close call
// within a reasonable amount of time.
func (tb *testClusterImplBalancer) waitForClose(ctx context.Context) error {
	if _, err := tb.closeCh.Receive(ctx); err != nil {
		return err
	}
	return nil
}


// TestUpdateClientConnState invokes the UpdateClientConnState method on the
// odBalancer with different inputs and verifies that the child balancer is built
// and updated properly.
func (s) TestUpdateClientConnState(t *testing.T) {
	od, _ := setup(t)
	defer od.Close() // this will leak a goroutine otherwise

	od.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			Interval:           10 * time.Second,
			BaseEjectionTime:   30 * time.Second,
			MaxEjectionTime:    300 * time.Second,
			MaxEjectionPercent: 10,
			SuccessRateEjection: &SuccessRateEjection{
				StdevFactor:           1900,
				EnforcementPercentage: 100,
				MinimumHosts:          5,
				RequestVolume:         100,
			},
			FailurePercentageEjection: &FailurePercentageEjection{
				Threshold:             85,
				EnforcementPercentage: 5,
				MinimumHosts:          5,
				RequestVolume:         50,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// The child balancer should be created and forwarded the ClientConn update
	// from the first successful UpdateClientConnState call.
	if err := od.child.(*testClusterImplBalancer).waitForClientConnUpdate(ctx, balancer.ClientConnState{
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}
}

func (s) TestUpdateState(t *testing.T) {
	od, tcc := setup(t)
	defer od.Close()

	od.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			Interval:           10 * time.Second,
			BaseEjectionTime:   30 * time.Second,
			MaxEjectionTime:    300 * time.Second,
			MaxEjectionPercent: 10,
			SuccessRateEjection: &SuccessRateEjection{
				StdevFactor:           1900,
				EnforcementPercentage: 100,
				MinimumHosts:          5,
				RequestVolume:         100,
			},
			FailurePercentageEjection: &FailurePercentageEjection{
				Threshold:             85,
				EnforcementPercentage: 5,
				MinimumHosts:          5,
				RequestVolume:         50,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})
	// child getting update doesn't matter but that is normal flow, noopPicker being nil won't happen UpdateClientConnState() write, new invariant of system -> UpdateState()

	// all UpdateState does is write to a channel for it to send
	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker: &testutils.TestConstPicker{},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case picker := <-tcc.NewPickerCh:
		pi, err := picker.Pick(balancer.PickInfo{})
		if err != nil {
			t.Fatalf("Picker.Pick should not have errored")
		}
		pi.Done(balancer.DoneInfo{}) // make sure this doesn't cause any problems...
	}
	// verify a picker gets sent to CC...use the picker to make sure it works

	// also need to make sure Done() works...

	// also need to test counting somehow but orthgonal to this test...

	// Maybe test sync stuff too here (i.e. noopPickerFlips)

	// Do UpdateAddresses first and then revisit ^^^
}

// Flow I want to test: single -> single (can add same here and check for no-op) changed
// single -> multiple
// multiple -> multiple no-op
// multiple -> single

// setup for the test: one address should be ejected, one shouldn't be can mess around with it that way


func (s) TestUpdateAddresses(t *testing.T) {
	// setup to what state?
	od, tcc := setup(t)
	defer od.Close()

	od.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
				{
					Addr: "address2",
				},
			},
		},
		BalancerConfig: &LBConfig{ // TODO: S/ variable
			Interval:           10 * time.Second,
			BaseEjectionTime:   30 * time.Second,
			MaxEjectionTime:    300 * time.Second,
			MaxEjectionPercent: 10,
			FailurePercentageEjection: &FailurePercentageEjection{ // have this eject one but not the other
				Threshold:             50,
				EnforcementPercentage: 100,
				MinimumHosts:          2,
				RequestVolume:         3,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := od.child.(*testClusterImplBalancer).waitForClientConnUpdate(ctx, balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
				{
					Addr: "address2",
				},
			},
		},
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}



	// one created SubConn with a certain address, switch to another, also can test the knobs on address map?
	scw1, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address1",
		},
	}, balancer.NewSubConnOptions{})

	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	// verify that NewSubConn gets called on client conn
	select {
	case <-ctx.Done():
		t.Fatal("timeout waiting for NewSubConn to be called on test Client Conn")
	case <-tcc.NewSubConnCh:
	}
	// verify scw is actually a scw
	_, ok := scw1.(*subConnWrapper)
	if !ok {
		t.Fatalf("SubConn passed downward should have been a subConnWrapper")
	}

	scw2, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address2",
		},
	}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker: &rrPicker{
			scs: []balancer.SubConn{scw1, scw2},
		},
	})

	// make it so that one address is ejected and the other isn't (move eject/uneject upward)

	select {
	case <-ctx.Done():
		t.Fatal("timeout while waiting for a UpdateState call on the ClientConn")
	case picker := <-tcc.NewPickerCh:
		pi, err := picker.Pick(balancer.PickInfo{})
		if err != nil {
			t.Fatalf("Picker.Pick should not have errored")
		}
		pi.Done(balancer.DoneInfo{})
		pi.Done(balancer.DoneInfo{})
		pi.Done(balancer.DoneInfo{})
		pi.Done(balancer.DoneInfo{})
		pi.Done(balancer.DoneInfo{})
		// second address ejected
		pi, err = picker.Pick(balancer.PickInfo{})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		od.intervalTimerAlgorithm()
		// verify UpdateSubConnState() got called with TRANSIENT_FAILURE for child in address that was ejected
		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(ctx, subConnWithState{ // read child into local var?
			sc: pi.SubConn,
			state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure}, // Represents ejected
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}
	}


	// Flow I want to test:
	// single -> single (can add same here and check for no-op) changed (make single -> single make it ejected)
	od.UpdateAddresses(scw1, []resolver.Address{
		{
			Addr: "address2",
		},
	})

	// verify that update addresses gets forwarded to ClientConn
	select {
	case <-ctx.Done():
		t.Fatal("timeout while waiting for a UpdateState call on the ClientConn")
	case <-tcc.UpdateAddressesAddrsCh:
	}
	// verify scw1 got ejected (UpdateSubConnState called with TRANSIENT FAILURE)
	if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(ctx, subConnWithState{ // read child into local var?
		sc: scw1,
		state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure}, // Represents ejected
	}); err != nil {
		t.Fatalf("Error waiting for Sub Conn update: %v", err)
	}


	// single -> multiple (should uneject though right...talk about why for each end condition)
	od.UpdateAddresses(scw1, []resolver.Address{
		{
			Addr: "address1",
		},
		{
			Addr: "address2",
		},
	})
	// verify scw2 got unejected (UpdateSubConnState called with recent state)
	if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(ctx, subConnWithState{
		sc: scw1,
		state: balancer.SubConnState{ConnectivityState: connectivity.Idle}, // If you uneject a SubConn that hasn't received a UpdateSubConnState, this is recent state. This seems fine or is this wrong?
	}); err != nil {
		t.Fatalf("Error waiting for Sub Conn update: %v", err)
	}

	// multiple -> multiple no-op
	od.UpdateAddresses(scw1, []resolver.Address{
		{
			Addr: "address2",
		},
		{
			Addr: "address3",
		},
	})

	// No downstream effects
	sCtx, cancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
		t.Fatalf("no SubConn update should have been sent (no SubConn got ejected/unejected)")
	}


	// multiple -> single (move to an ejected address, eject it)
	od.UpdateAddresses(scw1, []resolver.Address{
		{
			Addr: "address2",
		},
	})
	// verify scw1 got ejected (UpdateSubConnState called with TRANSIENT FAILURE)
	if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(ctx, subConnWithState{ // read child into local var?
		sc: scw1,
		state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure}, // Represents ejected
	}); err != nil {
		t.Fatalf("Error waiting for Sub Conn update: %v", err)
	}




	// newSubConn call should wrap it - make sure it a. pings Client Conn
	// and b. sends down a scw to child

	// What is the expected thing to happen? Delete map entry and create new one: <- how to verify this...
	// *** do this verification after it passes lol and no nil pointer exceptions

	// delete old map entry - how do we verify this?!?!?
	// create new map entry - perhaps test an invariant of the obj (i.e. old one had 1 1 2 for call counter, s/ and now 0, 0, 0)?!?!?!


	// s/ scw to multiple addresses - removes from addresses map entry and clears call counter

	// s/ multiple - no-op

	// s/ scw back to single address - created map entry if needed bla bla bla

	// two things: switching back to single address which is ejected should cause TF to be reported, correct?

	// Also, I don't know about creating a map entry for addresses that scw Switch To. What actually gets tracked in map
	// for upstream addresses is determined by UpdateClientConnState(), why should an arbitary SubConn be able to configure
	// new addresses to be counted.

	// If no one responds soon: I'm gonna have it report a TF, and I'm gonna
	// have it not create a map entry and continue to be ignored by Outlier
	// Detection to keep it consistent with UpdateClientConnState not having the
	// address a SubConn was created with and ignoring it in NewSubConn().



	// cause it to get ejected, unejected, really main things you can test I think

	// Switching to from one address with certain invariant effects to another address
	// with other certain invariant effects (such as being ejected/unejected...). This
	// is a way you can test

}

/*

1. func (s) TestUpdateState(t *testing.T) {
	// UpdateState with noop sends no counting picker plus correct connectivity state
	// UpdateState with reg sends counting picker plus correct connectivity state
	od, tcc := setup(t)


	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Connecting,
		Picker: /*newErrPicker?,
	})
}

2. Do something with UpdateSubConn/RemoveSubConn (or combine with ^^^)?, UpdateSubConnState and desired downstream effects?

// Ping UpdateClientConnState with a no-op config and then flip.
// Combine with UpdateState() permutations so that you test the most updated
// picker sent to TestCC
func (s) TestNoopPickerFlips(t *testing.T) {
	od, tcc := setup(t)

	tcc.NewPickerCh // receive off of this and verify it's a certain picker, either no-op or not
	// also test the knob of a certain connectivity state being pinged, two synced knobs

	// how to verify this? You call done twice with err being nil, see if it counts or not (for both initial picker, should have correct counts and no-op picker)


}*/

func (s) TestPicker(t *testing.T) {
	od, tcc := setup(t)
	defer od.Close()

	od.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
			},
		},
		BalancerConfig: &LBConfig{ // TODO: S/ variable
			Interval:           10 * time.Second,
			BaseEjectionTime:   30 * time.Second,
			MaxEjectionTime:    300 * time.Second,
			MaxEjectionPercent: 10,
			SuccessRateEjection: &SuccessRateEjection{
				StdevFactor:           1900,
				EnforcementPercentage: 100,
				MinimumHosts:          5,
				RequestVolume:         100,
			},
			FailurePercentageEjection: &FailurePercentageEjection{
				Threshold:             85,
				EnforcementPercentage: 5,
				MinimumHosts:          5,
				RequestVolume:         50,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	if err := od.child.(*testClusterImplBalancer).waitForClientConnUpdate(ctx, balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
			},
		},
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}

	// create a new subconn with address 1
	// one created SubConn with a certain address, switch to another, also can test the knobs on address map?
	scw, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address1", // s/ variable?
		},
	}, balancer.NewSubConnOptions{})

	// no verification newsubconn on cc? will this block later?

	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	// const picker always return that scw, that scw should be attached to object....
	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker: &testutils.TestConstPicker{
			SC: scw,
		},
	})

	// Should forward the connectivity State to Client Conn.
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case state := <-tcc.NewStateCh:
		if state != connectivity.Ready {
			t.Fatalf("ClientConn received connectivity state %v, want %v", state, connectivity.Ready)
		}
	}

	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case picker := <-tcc.NewPickerCh:
		pi, err := picker.Pick(balancer.PickInfo{})
		if err != nil {
			t.Fatalf("Picker.Pick should not have errored")
		}
		pi.Done(balancer.DoneInfo{})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		od.newMu.Lock()
		val, ok := od.odAddrs.Get(resolver.Address{ // s/ "obj" to "mapEntry"
			Addr: "address1", // s/ variable?
		})
		if !ok {
			t.Fatal("map entry for address: address1 not present in map")
		}
		obj, ok := val.(*object)
		if !ok {
			t.Fatal("map value isn't obj type")
		}
		bucketWant := &bucket{
			numSuccesses: 1,
			numFailures: 1,
			requestVolume: 2,
		}
		if diff := cmp.Diff((*bucket)(obj.callCounter.activeBucket), bucketWant); diff != "" { // no need for atomic read because not concurrent with Done() call from picker
			t.Fatalf("callCounter is different than expected, diff (-got +want): %v", diff)
		}
		od.newMu.Unlock()
	}
	// ^^^ VERIFY CONNECTIVITY.READY, write a comment about run() synchronizing a. whether the picker was actually already created or not
	// b. the recent connectivity state
	// c. the bit that represents no-op configuration or not

	// Three important pieces of functionality run() synchronizes in regards to UpdateState calls towards grpc:

	// 1. On a config update, checking if the picker was actually created or not.
	// 2. Keeping track of the most recent connectivity state sent from the child (determined by UpdateState()).
	//       * This will always forward up with most recent noopCfg bit
	// 3. Keeping track of the most recent no-op config bit (determined by UpdateClientConnState())
	//       * Will only forward up if no-op config bit changed and picker was already created

	// UpdateClientConnState with a noop config
	od.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
			},
		},
		BalancerConfig: &LBConfig{
			Interval: 1<<63 - 1,
			// how are these knobs not set???, passed as such right "noop config will be generated"
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})

	if err := od.child.(*testClusterImplBalancer).waitForClientConnUpdate(ctx, balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
			},
		},
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}

	// The connectivity state sent to the Client Conn should be the persisted
	// recent state received from the last UpdateState() call, which in this
	// case is connecting.
	select {
	case <-ctx.Done():
		t.Fatal("timeout waiting for picker update on ClientConn, should have updated because no-op config changed on UpdateClientConnState")
	case state := <-tcc.NewStateCh:
		if state != connectivity.Ready {
			t.Fatalf("ClientConn received connectivity state %v, want %v", state, connectivity.Ready)
		}
	}


	select {
	case <-ctx.Done():
		t.Fatal("timeout waiting for picker update on ClientConn, should have updated because no-op config changed on UpdateClientConnState")
	case picker := <-tcc.NewPickerCh:
		pi, err := picker.Pick(balancer.PickInfo{})
		if err != nil {
			t.Fatalf("Picker.Pick should not have errored")
		}
		pi.Done(balancer.DoneInfo{})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		od.newMu.Lock()
		val, ok := od.odAddrs.Get(resolver.Address{ // s/ "obj" to "mapEntry"
			Addr: "address1", // s/ variable?
		})
		if !ok {
			t.Fatal("map entry for address: address1 not present in map")
		}
		obj, ok := val.(*object)
		if !ok {
			t.Fatal("map value isn't obj type")
		}

		// The active bucket should be cleared because the interval timer
		// algorithm didn't run in between ClientConn updates and the picker
		// should not count, as the outlier detection balancer is configured
		// with a no-op configuration.
		bucketWant := &bucket{}
		if diff := cmp.Diff((*bucket)(obj.callCounter.activeBucket), bucketWant); diff != "" { // no need for atomic read because not concurrent with Done() call
			t.Fatalf("callCounter is different than expected, diff (-got +want): %v", diff)
		}
		od.newMu.Unlock()
	}

	// we have balancer.State then LBConfig
	// do we want Maybe another LB Config that swiches the bit?
	// or probably just balancer.State that sends another no-op picker due to bit being
	// now set by UpdateClientConnState(), or not?

	// UpdateState with a connecting state. This new most recent connectivity state
	// should be forwarded to the Client Conn, alongside the most recent noop config bit
	// which is true.
	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Connecting,
		Picker: &testutils.TestConstPicker{
			SC: scw,
		},
	})

	// Should forward the most recent connectivity State to Client Conn.
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case state := <-tcc.NewStateCh:
		if state != connectivity.Connecting {
			t.Fatalf("ClientConn received connectivity state %v, want %v", state, connectivity.Connecting)
		}
	}

	// Should forward the picker containing the most recent no-op config bit.
	select {
	case <-ctx.Done():
		t.Fatal("timeout while waiting for a UpdateState call on the ClientConn")
	case picker := <-tcc.NewPickerCh:
		pi, err := picker.Pick(balancer.PickInfo{})
		if err != nil {
			t.Fatalf("Picker.Pick should not have errored")
		}
		pi.Done(balancer.DoneInfo{})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		od.newMu.Lock()
		val, ok := od.odAddrs.Get(resolver.Address{ // s/ "obj" to "mapEntry"
			Addr: "address1", // s/ variable?
		})
		if !ok {
			t.Fatal("map entry for address: address1 not present in map")
		}
		obj, ok := val.(*object)
		if !ok {
			t.Fatal("map value isn't obj type")
		}

		// The active bucket should be cleared because the interval timer
		// algorithm didn't run in between ClientConn updates and the picker
		// should not count, as the outlier detection balancer is configured
		// with a no-op configuration.
		bucketWant := &bucket{}
		if diff := cmp.Diff((*bucket)(obj.callCounter.activeBucket), bucketWant); diff != "" { // no need for atomic read because not concurrent with Done() call
			t.Fatalf("callCounter is different than expected, diff (-got +want): %v", diff)
		}
		od.newMu.Unlock()
	}



	// Can you do this dance with ejected/unejected SubConn's?

	// UpdateAddresses back and forth being ejected and unejected and see if it
	// reports correct state downward, this is already tested in test eject
	// though.


	// still need to test knobs of atomic reads/writes
}

// get these two tests working ^^^ *Done, plus finish how to test odAddrs update
// (either check map length or check invariant of system like callCounter), also
// add functionality to that

type rrPicker struct {
	scs []balancer.SubConn

	next int
}

func (rrp *rrPicker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	sc := rrp.scs[rrp.next]
	rrp.next = (rrp.next + 1) % len(rrp.scs)
	return balancer.PickResult{SubConn: sc}, nil
}

// TestEjectUnejectSuccessRate tests the functionality of the interval timer
// algorithm of ejecting/unejecting SubConns when configured with
// SuccessRateEjection. It also tests the invariant of a SubConnWrapper being
// ejected or unejected, which is to either forward or not forward SubConn
// updates from grpc.
func (s) TestEjectUnejectSuccessRate(t *testing.T) {
	// Setup the outlier detection balancer to a point where it will be in a
	// situation to potentially eject addresses.
	od, tcc := setup(t)
	defer od.Close()

	od.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
				{
					Addr: "address2",
				},
				{
					Addr: "address3",
				},
			},
		},
		BalancerConfig: &LBConfig{
			Interval:           1<<63 - 1, // so the interval will never run unless called manually in test.
			BaseEjectionTime:   30 * time.Second,
			MaxEjectionTime:    300 * time.Second,
			MaxEjectionPercent: 10,
			SuccessRateEjection: &SuccessRateEjection{
				StdevFactor:           500,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// maybe read to a local var
	if err := od.child.(*testClusterImplBalancer).waitForClientConnUpdate(ctx, balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
				{
					Addr: "address2",
				},
				{
					Addr: "address3",
				},
			},
		},
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}

	scw1, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address1",
		},
	}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}
	// Verify new subconn on tcc here? Not like UpdateClientConnState where it sends on channel so not required...but would be nice to have
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw2, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address2",
		},
	}, balancer.NewSubConnOptions{})
	// Verify new subconn on tcc here?
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw3, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address3",
		},
	}, balancer.NewSubConnOptions{})
	// Verify new subconn on tcc here?
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker: &rrPicker{
			scs: []balancer.SubConn{scw1, scw2, scw3},
		},
	})

	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case picker := <-tcc.NewPickerCh:
		// Set each of the three upstream subchannels to have five successes
		// each. This should cause none of the addresses to be ejected as none
		// of them are outliers according to the success rate algorithm.
		for i := 0; i < 3; i++ {
			pi, err := picker.Pick(balancer.PickInfo{})
			if err != nil {
				t.Fatalf("Picker.Pick should not have errored")
			}
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
		}

		od.intervalTimerAlgorithm()

		// verify no UpdateSubConnState() call on the child, as no addresses got
		// ejected (ejected address will cause an UpdateSubConnState call).
		sCtx, cancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer cancel()
		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("no SubConn update should have been sent (no SubConn got ejected)")
		}

		// Since no addresses are ejected, a SubConn update should forward down
		// to the child.
		od.UpdateSubConnState(scw1.(*subConnWrapper).SubConn, balancer.SubConnState{
			ConnectivityState: connectivity.Connecting,
		})

		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(ctx, subConnWithState{
			sc: scw1,
			state: balancer.SubConnState{ConnectivityState: connectivity.Connecting},
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}

		// Set two of the upstream subchannels to have five successes each, and one of the upstreams
		// to have five failures. This should cause the address which has five failures to be ejected according
		// the SuccessRateAlgorithm.
		for i := 0; i < 2; i++ {
			pi, err := picker.Pick(balancer.PickInfo{})
			if err != nil {
				t.Fatalf("Picker.Pick should not have errored")
			}
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
		}
		pi, err := picker.Pick(balancer.PickInfo{})
		if err != nil {
			t.Fatalf("Picker.Pick should not have errored")
		}
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})

		// should eject address that always errored.
		od.intervalTimerAlgorithm()
		// Due to the address being ejected, the SubConn with that address
		// should be ejected, meaning a TRANSIENT_FAILURE connectivity state
		// gets reported to the child.
		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(ctx, subConnWithState{
			sc: pi.SubConn, // Same SubConn present in address that had failures, since same ref to pi.Done(error)
			state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure}, // Represents ejected
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}
		// Only one address should be ejected.
		sCtx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer cancel()
		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("Only one SubConn update should have been sent (only one SubConn got ejected)")
		}

		// Now that an address is ejected, SubConn updates for SubConns using
		// that address should not be forwarded downward. These SubConn updates
		// will be cached to update the child sometime in the future when the
		// address gets unejected.
		od.UpdateSubConnState(pi.SubConn.(*subConnWrapper).SubConn, balancer.SubConnState{
			ConnectivityState: connectivity.Connecting,
		})
		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("SubConn update should not have been forwarded (the SubConn is ejected)")
		}

		// Override now to cause the interval timer algorithm to always uneject a SubConn.
		defer func (n func() time.Time) {
			now = n
		}(now)

		now = func() time.Time {
			return time.Now().Add(time.Second * 1000) // will cause to always uneject addresses which are ejected
		}
		od.intervalTimerAlgorithm()

		// unejected SubConn should report latest persisted state - which is
		// connecting from earlier.
		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(ctx, subConnWithState{ // read child into local var?
			sc: pi.SubConn,
			state: balancer.SubConnState{ConnectivityState: connectivity.Connecting},
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}

	}

}

func (s) TestEjectFailureRate(t *testing.T) {
	od, tcc := setup(t)
	defer od.Close()

	// UpdateClientConnState with SuccessRateEjection set with knobs you want
	od.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
				{
					Addr: "address2",
				},
				{
					Addr: "address3",
				},
			},
		},
		BalancerConfig: &LBConfig{
			Interval:           1<<63 - 1, // so the interval will never run unless called manually in test.
			BaseEjectionTime:   30 * time.Second,
			MaxEjectionTime:    300 * time.Second,
			MaxEjectionPercent: 10, // will still allow one right? Should I test each of these knobs?
			FailurePercentageEjection: &FailurePercentageEjection{
				Threshold: 50,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// maybe read
	if err := od.child.(*testClusterImplBalancer).waitForClientConnUpdate(ctx, balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
				{
					Addr: "address2",
				},
				{
					Addr: "address3",
				},
			},
		},
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}

	scw1, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address1",
		},
	}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}
	// Verify new subconn on tcc here? Not like UpdateClientConnState where it sends on channel so not required...but would be nice to have
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw2, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address2",
		},
	}, balancer.NewSubConnOptions{})
	// Verify new subconn on tcc here?
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw3, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address3",
		},
	}, balancer.NewSubConnOptions{})
	// Verify new subconn on tcc here?
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker: &rrPicker{
			scs: []balancer.SubConn{scw1, scw2, scw3},
		},
	})

	select {
	case <-ctx.Done():
		t.Fatal("timeout while waiting for a UpdateState call on the ClientConn")
	case picker := <-tcc.NewPickerCh:
		// Same exact setup and validations outside of explicit UpdateSubConnState calls.

		// for 0 - 15, set each upstream subchannel to have five successes each.
		// This should cause none of the addresses to be ejected as none of them
		// are below the failure percentage threshold.
		for i := 0; i < 3; i++ {
			pi, err := picker.Pick(balancer.PickInfo{})
			if err != nil {
				t.Fatalf("Picker.Pick should not have errored")
			}
			pi.Done(balancer.DoneInfo{}) // call this five times, or rr over all
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
		}

		od.intervalTimerAlgorithm()

		sCtx, cancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer cancel()
		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("no SubConn update should have been sent (no SubConn got ejected)")
		}

		// for 0 - 15, set two upstream subchannels to have five successes each,
		// the other one none, should get ejected
		for i := 0; i < 2; i++ {
			pi, err := picker.Pick(balancer.PickInfo{})
			if err != nil {
				t.Fatalf("Picker.Pick should not have errored")
			}
			pi.Done(balancer.DoneInfo{}) // call this five times, or rr over all
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{})
		}
		// have the other ones be 5 failures
		pi, err := picker.Pick(balancer.PickInfo{})
		if err != nil {
			t.Fatalf("Picker.Pick should not have errored")
		}
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")}) // call this five times, or rr over all
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})

		// should eject address that always errored.
		od.intervalTimerAlgorithm() // waits for it to return - runs uneject as well, needs to not uneject after the first pass

		// verify UpdateSubConnState() got called with TRANSIENT_FAILURE for child in address that was ejected
		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(ctx, subConnWithState{ // read child into local var?
			sc: pi.SubConn,
			state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure}, // Represents ejected
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}

		// verify only one got ejected
		sCtx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer cancel()
		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("Only one SubConn update should have been sent (only one SubConn got ejected)")
		}
	}
}

// need a cleanup at a certain point

// test uneject, does this branch off and go past this ^^^ or seperate test?

// scenario within the algorithm that leads to addresses being unejected...:
// "If the address is ejected, and the current time is after ejection_timestamp
// + min(base_ejection_time * multiplier, max(base_ejection_time,
// max_ejection_time)), un-eject the address."

// verify by testing with recent state being forwarded down

// Also need to test that SubConn updates continue to get forwarded down


// Also need to test UpdateAddresses eject/uneject



// fork goroutine and call operations spammy
// it's hard to induce unless you do it multiple times
// grpc package reconnects closing connections doing RPC's spamming RPC's
// hoping it doesn't do RPC failures

// first job is building container having test do multiple rounds
// of different versions of bootstrap generator



// Goals/Output of Outlier Detection algorithm:

// at the end after a predefined set of conditions about the system (kept in data structure):

// based on a random number generated (NEED TO MOCK!) between 1-100 wait just
// set an enforcement percentage of 100 random guaranteed to be < 100, eject an
// address (for each scsw scw.eject) at the end of success rate/failure
// percentage algorithms, just had this 100 :)

// Is it default before it gets here...yeah sets default in xdsclient?



// at the end of the interval, uneject certain addresses (for each scsw scw.eject).


// verify scws have been ejected/unejected by verifying that UpdateSubConnState()
// gets called on child with TF for eject and recent state for uneject






// testUpdateAddresses to an ejected addresses


// Test interval timer triggering and 5 + 3 = 8 thing...? OVERRIDE TIME.NOW

// TestDurationOfInterval tests the configured interval timer.
// The first interval timer should configure the timer with whatever
// is directly on the config. The second interval should configure it with whatever diff
/*func (s) TestDurationOfInterval(t *testing.T) { (do we even want this?)
	// read time.Now here or somewhere else?

	// you don't need to pass control of timer to this testing goroutine, as you
	// can just call intervalTimerAlgorithm directly...

	// pass back a timer to trigger it at certain times, don't trigger after first
	// interval.
	defer func(af func(d time.Duration, f func()) *time.Timer) {
		afterFunc = af
		// return a never firing timer
	}(afterFunc)

	durationChan := testutils.NewChannel()
	// can verify duration in overriden function - separate test
	afterFunc = func(dur time.Duration, _ func()) *time.Timer {
		// can verify duration in overriden function for the 5 + 3 = 8 case and also
		// verify first duration passed is 8
		// how to make five seconds pass...? Then send a new config and it should start it for 3 - |2 - 4|?, mock time.Now to return something
		durationChan.Send(dur)

		// selectively trigger the interval timer

		// pass this
		return time.NewTimer(/*max duration so wil never fire)
	} // prevent interval timer from going off with manual call and a max interval. Use this logic to verify duration passed in.


	od, tcc := setup(t)
	defer od.Close()

	// configure od balancer with config that specifies 3 seconds eject rate
	od.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			Interval: 8 * time.Second,
			BaseEjectionTime:   30 * time.Second,
			MaxEjectionTime:    300 * time.Second,
			MaxEjectionPercent: 10,
			SuccessRateEjection: &SuccessRateEjection{
				StdevFactor:           1900,
				EnforcementPercentage: 100,
				MinimumHosts:          5,
				RequestVolume:         100,
			},
			FailurePercentageEjection: &FailurePercentageEjection{
				Threshold:             85,
				EnforcementPercentage: 5,
				MinimumHosts:          5,
				RequestVolume:         50,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := od.child.(*testClusterImplBalancer).waitForClientConnUpdate(ctx, balancer.ClientConnState{
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}

	d, err := durationChan.Receive(ctx)
	if err != nil {
		t.Fatalf("Error receiving duration from afterFunc() call: %v", err)
	}
	dur := d.(time.Duration)
	// this should be 8 seconds - what the balancer was configured with
	if dur.Seconds() != 8 {
		t.Fatalf("configured duration should have been 8 seconds to start timer")
	}


	// Override time.Now{} here to return time.Now{} + 5 (these tests should run in nanoseconds)

	// UpdateClientConnState with an interval of 9 seconds. Due to having 5 seconds already passed, this should
	// start an interval timer of ~4 seconds..
	od.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			Interval: 9 * time.Second,
			BaseEjectionTime:   30 * time.Second,
			MaxEjectionTime:    300 * time.Second,
			MaxEjectionPercent: 10,
			SuccessRateEjection: &SuccessRateEjection{
				StdevFactor:           1900,
				EnforcementPercentage: 100,
				MinimumHosts:          5,
				RequestVolume:         100,
			},
			FailurePercentageEjection: &FailurePercentageEjection{
				Threshold:             85,
				EnforcementPercentage: 5,
				MinimumHosts:          5,
				RequestVolume:         50,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})

	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := od.child.(*testClusterImplBalancer).waitForClientConnUpdate(ctx, balancer.ClientConnState{
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}
	// should be around ~ 4
	d, err = durationChan.Receive(ctx)
	if err != nil {
		t.Fatalf("Error receiving duration from afterFunc() call: %v", err)
	}
	dur = d.(time.Duration)
	if dur.Seconds() /*!= somewhere close to 4 {
		t.Fatalf("configured duration should have been around 4 seconds to start timer")
	}


	// if you override with time.Now{} it will be called in the algorithm itself in the balancer code

	// 5 second context timeout sure may glitch in regards to instructions but good enough
	// for bounding state


	// override time.Now to time.Now + 5 seconds. on next config update

	// either not start in future || start, this is super flaky...I guess there's
	// sleeps in other tests though

	// mocktime etc...how do we do 5 + 3 = 8.


	// also need to test the scenario where it's configured with no-op config, it shouldn't be configured at all (pull configs to var?):
	// UpdateClientConnState no-op
	od.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			Interval: 10 * time.Second,
		},
	})

	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := od.child.(*testClusterImplBalancer).waitForClientConnUpdate(ctx, balancer.ClientConnState{
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}
	// try and receive from the durationCh, should error since
	// no-op config so no timer should start
	sCtx, cancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	_, err = durationChan.Receive(sCtx)
	if err == nil {
		t.Fatal("No timer should have started.")
	}
}*/






// TODO:
// Finish duration test
// Look through code at any other functionality that needs to be tested (think of how many problems testing eject/uneject brought up)
// Finish UpdateAddresses, any other parts of tests need to be updated
// The other picker no-op (or whatever state that is that gets synced in run. I think same flow but ABA it's ABAB or something)
// Picker atomic pointer thing is weird, but relates to VVV

// Another bucket:
// Test concurrent operations somehow
// UpdateSubConnState and Eject eventual consistency after 100 calls? *Thought about in UpdatePicker testing, see example there for how
// I want to test picker.Pick with interval timer go func() picker.Pick every ms or something
// as interval timer algorithm is running (run with race detector)
// other race conditions...

// cleanup, e2e, then done :)





// Also need to test the UpdateSubConnState run() synchronization somehow...ejected/unejected SubConns put on a channel
// to eventually consistent state being forwarded down...

// read all of the buffer, last update should be desired update...


// Test picker synchronization (we have it for no-op, is that only thing we need to test for than run() goroutine

// Test Picker.Pick racing with IntervalTimerAlgo

// Call a bunch of operations concurrently. Close() needs to come synchronsouly after though right?

// Test child != nil and closing and not sending subconn updates after close (do this async)
// perhaps test closing as well - niling child etc. what other behaviors? Verify Close() gets called on child. Stuff can't happen downstream...


func (s) TestClose(t *testing.T) {
	od, _ := setup(t)

	od.UpdateClientConnState(balancer.ClientConnState{ // could pull this out to the setup() call as well, maybe a wrapper on what is currently there...
		BalancerConfig: &LBConfig{
			Interval:           10 * time.Second,
			BaseEjectionTime:   30 * time.Second,
			MaxEjectionTime:    300 * time.Second,
			MaxEjectionPercent: 10,
			SuccessRateEjection: &SuccessRateEjection{
				StdevFactor:           1900,
				EnforcementPercentage: 100,
				MinimumHosts:          5,
				RequestVolume:         100,
			},
			FailurePercentageEjection: &FailurePercentageEjection{
				Threshold:             85,
				EnforcementPercentage: 5,
				MinimumHosts:          5,
				RequestVolume:         50,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	ciChild := od.child.(*testClusterImplBalancer)
	if err := ciChild.waitForClientConnUpdate(ctx, balancer.ClientConnState{
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}




	od.Close()

	// Verify child balancer closed.
	if err := ciChild.waitForClose(ctx); err != nil {
		t.Fatalf("Error waiting for Close() call on child balancer %v", err)
	}

	// SubConn updates shouldn't forward, does this matter that much...? you'd have to set up a system with it
}

// Test
// goroutine 1: interval timer goroutine 2: something constantly calling Done() callback

func (s) TestConcurrentPickerCountsWithIntervalTimer(t *testing.T) {
	// Set system up to a point where you can actually test this (3 addresses,
	// five subconns in each address, both success + failure algorithms):
	od, tcc := setup(t)
	defer od.Close()

	od.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
				{
					Addr: "address2",
				},
				{
					Addr: "address3",
				},
			},
		},
		BalancerConfig: &LBConfig{
			Interval:           1<<63 - 1, // so the interval will never run unless called manually in test.
			BaseEjectionTime:   30 * time.Second,
			MaxEjectionTime:    300 * time.Second,
			MaxEjectionPercent: 10,
			SuccessRateEjection: &SuccessRateEjection{ // Have both to step through all the interval timer code
				StdevFactor:           500,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			FailurePercentageEjection: &FailurePercentageEjection{
				Threshold: 50,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := od.child.(*testClusterImplBalancer).waitForClientConnUpdate(ctx, balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
				{
					Addr: "address2",
				},
				{
					Addr: "address3",
				},
			},
		},
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}

	scw1, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address1",
		},
	}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}
	// Verify new subconn on tcc here? Not like UpdateClientConnState where it sends on channel so not required...but would be nice to have
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw2, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address2",
		},
	}, balancer.NewSubConnOptions{})
	// Verify new subconn on tcc here?
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw3, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address3",
		},
	}, balancer.NewSubConnOptions{})
	// Verify new subconn on tcc here?
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker: &rrPicker{
			scs: []balancer.SubConn{scw1, scw2, scw3},
		},
	}) // Move stuff to like setup basic, setupX, setupY (can even depend on setupX if wrapper but might not too)

	var picker balancer.Picker
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case picker = <-tcc.NewPickerCh:
	}



	// Spawn a goroutine that constantly picks and invokes the Done callback counting for successful
	// and failing RPC's.
	finished := make(chan struct{})
	go func() {
		// constantly update the picker - again, the just is just to write to deprecated memory rather than swap the pointers directly (my way is same functional equivalent)
		// and write to the inactive being constantly read by the picker algorithm. Testing for no memory race conditions though with corrupted memory (have concurrent pointer reads/writes)

		// rr picker over three subconns
		// reads pi.sc and then
		// updates with good RPC || failing RPC (either one each iteration or update both same iteration)
		for {
			select {
			case <-finished:
				return
			default:
			}
			pi, err := picker.Pick(balancer.PickInfo{})
			if err != nil {
				t.Fatalf("Picker.Pick should not have errored")
			}
			pi.Done(balancer.DoneInfo{}) // this will prevent weird shit like ejecting/unejecting no because it could still potentially be way off
			pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
			// time.Sleep maybe 1ms
			time.Sleep(1 * time.Nanosecond) // can play around with this knob
		}


	}()

	// You'll also need cleanup for any invariants of the algorithm...like ejecting and putting shit on channels
	// you'll see this come up if it actually breaks.


	// call interval timer algorithm twice in this goroutine for the two swaps

	od.intervalTimerAlgorithm()

	od.intervalTimerAlgorithm()



	close(finished) // Don't spawn a goroutine you can't exit.
}

// e2e test dependent on config pr, maybe send this one out separately before e2e and ping tell them to hurry up and I can link it after

// Seems like all I need now is finish/refine UpdateAddresses and any other race conditions in tests
// and then yeah finish cleanup and SEND PR :)

// VVV

// verify nothing comes after close
// Eventual consistency of UpdateSubConnStates?

// Also fork goroutines and call a bunch of operations concurrently, I guess you can use that to verify
// nothing comes after close as well.

// TestConcurrentOperations calls different operations on the balancer in
// separate goroutines (which all use finished to exit) to test for any race
// conditions and deadlocks. It also uses a child balancer which verifies that
// no operations on the child get called after the balancer is closed. (see verify balancer)

func (s) TestConcurrentOperations(t *testing.T) {
	// After setup (same setup), 3 upstreams with 3 subconns per update, move to a helper function
	od, tcc := setup(t)
	defer od.Close()

	od.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
				{
					Addr: "address2",
				},
				{
					Addr: "address3",
				},
			},
		},
		BalancerConfig: &LBConfig{
			Interval:           1<<63 - 1, // so the interval will never run unless called manually in test.
			BaseEjectionTime:   30 * time.Second,
			MaxEjectionTime:    300 * time.Second,
			MaxEjectionPercent: 10,
			SuccessRateEjection: &SuccessRateEjection{ // Have both to step through all the interval timer code
				StdevFactor:           500,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			FailurePercentageEjection: &FailurePercentageEjection{
				Threshold: 50,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: verifyBalancerName,
				Config: verifyBalancerConfig{},
			},
		},
	})

	od.child.(*verifyBalancer).t = t

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	/*if err := od.child.(*testClusterImplBalancer).waitForClientConnUpdate(ctx, balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
				{
					Addr: "address2",
				},
				{
					Addr: "address3",
				},
			},
		},
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}*/

	scw1, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address1",
		},
	}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}
	// Verify new subconn on tcc here? Not like UpdateClientConnState where it sends on channel so not required...but would be nice to have
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw2, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address2",
		},
	}, balancer.NewSubConnOptions{})
	// Verify new subconn on tcc here?
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw3, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address3",
		},
	}, balancer.NewSubConnOptions{})
	// Verify new subconn on tcc here?
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker: &rrPicker{
			scs: []balancer.SubConn{scw2, scw3},
		},
	}) // Move stuff to like setup basic, setupX, setupY (can even depend on setupX if wrapper but might not too)

	var picker balancer.Picker
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case picker = <-tcc.NewPickerCh:
	}

	finished := make(chan struct{}) // or some way to exit all the threads
	go func() {
		for {
			select {
			case <-finished:
				return
			default:
			}
			pi, err := picker.Pick(balancer.PickInfo{})
			if err != nil {
				t.Fatalf("Picker.Pick should not have errored")
			}
			pi.Done(balancer.DoneInfo{}) // this will prevent weird shit like ejecting/unejecting no because it could still potentially be way off
			pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
			// time.Sleep maybe 1ms
			time.Sleep(1 * time.Nanosecond) // can play around with this knob
		}
	}()

	// interval timer can be concurrent (does time.AfterFunc()) spawn a new goroutine?

	// go func() {od.intervalTimerAlgorithm()} ()
	go func() {
		for {
			select {
			case <-finished:
				return
			default:
			}
			od.intervalTimerAlgorithm()
		}
	}()


	// balancer.ClientConn can calls be concurrent
	// go func {ClientConn operation 1} () UpdateState
	go func() {
		for {
			select {
			case <-finished:
				return
			default:
			}
			od.UpdateState(balancer.State{
				ConnectivityState: connectivity.Ready,
				Picker: &rrPicker{
					scs: []balancer.SubConn{scw2, scw3},
				},
			})
			time.Sleep(1 * time.Nanosecond)
		}
	}()
	// go func {ClientConn operation 2} () NewSubConn
	go func() {
		od.NewSubConn([]resolver.Address{{Addr: "address4"}}, balancer.NewSubConnOptions{})
	}()

	// go func {ClientConn operation 3} () RemoveSubConn (can't run in while true lol only one) how to make sure this exits
	go func() {
		od.RemoveSubConn(scw1)
	}()

	go func() {
		od.UpdateAddresses(scw2, []resolver.Address{
			{
				Addr: "address3",
			},
		})
	}()


	/*go func() {
		for {
			select {
			case <-finished:
				return
			case <-od.child.(*testClusterImplBalancer).scStateCh.:
			}
		}
	}()*/


	// Only mock balancer has a blocking channel send

	// Balancer.Balancer operations can't get called concurrently, one at a time
	// (in this goroutine with a happens before relationship):
	// balancer.Balancer operation 1
	// delete addresses and flip to no op
	od.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
			},
		},
		BalancerConfig: &LBConfig{
			Interval: 1<<63 - 1,
			// how are these knobs not set???, passed as such right "noop config will be generated"
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	}/*can do something interesting like flip to no-op or even delete addresses*/)

	// mention no check for child type switching, but should be ok since should? be guaranteed to be cluster_impl balancer

	// balancer.Balancer operation 2

	// balancer.Balancer operation 3
	od.UpdateSubConnState(scw1, balancer.SubConnState{
		ConnectivityState: connectivity.Connecting,
	}) // This will potentially send on a blocking channel. THis problem wouldn't exist with a verify balancer, just switch it to verify balancer

	od.ResolverError(errors.New("some error"))
	od.ExitIdle()
	od.Close()
	// Does this need to wait for the spawned goroutines to exit before exiting?

	close(finished)
}

type verifyBalancerConfig struct {
	serviceconfig.LoadBalancingConfig
}

type verifyBalancerBuilder struct{}

func (verifyBalancerBuilder) Build(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
	return &verifyBalancer{
		closed: grpcsync.NewEvent(),
	}
}

func (verifyBalancerBuilder) Name() string {
	return verifyBalancerName
}

// verifyBalancer is a balancer that verifies after a Close() call,
// no other balancer.Balancer methods are called afterward.
type verifyBalancer struct {
	closed *grpcsync.Event
	// To fail the test if any operation gets called gets called after Close().
	t *testing.T
}

func (vb *verifyBalancer) UpdateClientConnState(ccs balancer.ClientConnState) error {
	if vb.closed.HasFired() {
		vb.t.Fatal("UpdateClientConnState was called after Close(), which breaks the balancer API")
	}
	return nil
}

func (vb *verifyBalancer) ResolverError(err error) {
	if vb.closed.HasFired() {
		vb.t.Fatal("ResolverError was called after Close(), which breaks the balancer API")
	}
}

func (vb *verifyBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	if vb.closed.HasFired() {
		vb.t.Fatal("UpdateSubConnState was called after Close(), which breaks the balancer API")
	}
}

func (vb *verifyBalancer) Close() {
	vb.closed.Fire()
}

func (vb *verifyBalancer) ExitIdle() {
	if vb.closed.HasFired() {
		vb.t.Fatal("ExitIdle was called after Close(), which breaks the balancer API")
	}
}

// Wait but this thing runs update addresses ^^^, this test and update addresses
// (also need to merge the PR against A50) + ready to send out and merge on top
// of e2e test.


// Do update addresses change at the end after sending out PR against A50, make sure to save state before then.