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
			}`),
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
}

const tcibname = "testClusterImplBalancer"

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

// Two functionalities:
// Verify certain methods get correctly closed - how do you get a ref to the clusterimplchild? (read od.Child directly)
// Also needs to be able to ping the od balancer's CC methods, wait can't you just call this directly from here
type testClusterImplBalancerConfig struct {
	serviceconfig.LoadBalancingConfig
}

// cc.UpdateState etc.
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
	// Need to verify this call...use a channel?...all of these will need verification
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

// waitForClientConnUpdate verifies if the testEDSBalancer receives the
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

func (tb *testClusterImplBalancer) waitForSubConnUpdate(ctx context.Context, wantSCS subConnWithState) error {
	scs, err := tb.scStateCh.Receive(ctx)
	if err != nil {
		return err
	}
	gotSCS := scs.(subConnWithState)
	if !cmp.Equal(gotSCS, wantSCS, cmp.AllowUnexported(subConnWithState{})) {
		return fmt.Errorf("received SubConnState: %+v, want %+v", gotSCS, wantSCS)
	}
	return nil
}

func (tb *testClusterImplBalancer) waitForClose(ctx context.Context) error {
	if _, err := tb.closeCh.Receive(ctx); err != nil {
		return err
	}
	return nil
}

// to construct new heap memory: closeCh := testutils.NewChannel()
// need to write this and the cc somewhere
// do it in build?

// *Triage other balancers to see how/what they test


// TestUpdateClientConnState invokes the UpdateClientConnState method on the
// odBalancer with different inputs and verifies that the child balancer is built
// and updated properly.
func (s) TestUpdateClientConnState(t *testing.T) {
	// setup
	od, _ := setup(t) // can also have a cancel returned from this that does od.Close() itself
	defer od.Close() // this will leak a goroutine otherwise

	od.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			// noop || valid/full fields default fields**
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

	// The only other downstream effect is it creates entries in address map ** should we test creation and deletion of address map?


	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// verify that the child balancer received the update - or read into a variable
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
			// noop || valid/full fields default fields**
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



	// one created SubConn with a certain address, switch to another, also can test the knobs on address map?
	scw, err := od.NewSubConn([]resolver.Address{
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
	_, ok := scw.(*subConnWrapper)
	if !ok {
		t.Fatalf("SubConn passed downward should have been a subConnWrapper")
	}

	// newSubConn call should wrap it - make sure it a. pings Client Conn
	// and b. sends down a scw to child
	od.UpdateAddresses(scw /*I think even though not typecast, still technically a type that implements, so just pass the ref to an interface*/, []resolver.Address{
		{
			Addr: "address2",
		},
	})

	// verify that update addresses gets forwarded to ClientConn
	select {
	case <-ctx.Done():
		t.Fatal("timeout waiting for UpdateAddresses to be called on test Client Conn")
	case <-tcc.UpdateAddressesAddrsCh:
	}

	// What is the expected thing to happen? Delete map entry and create new one: <- how to verify this...
	// *** do this verification after it passes lol and no nil pointer exceptions

	// delete old map entry - how do we verify this?!?!?
	// create new map entry - perhaps test an invariant of the obj (i.e. old one had 1 1 2 for call counter, s/ and now 0, 0, 0)?!?!?!


	// s/ scw to multiple addresses


	// s/ scw back to single address

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


2. func (s) TestUpdateAddresses(t *testing.T) {
	// setup to what state?

	// first test case: one created SubConn with a certain address,
	// UpdateAddresses that created SubConn to a new address, should delete old map
	// entry and create new ones
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

	// need addresses to count...
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
		if diff := cmp.Diff((*bucket)(obj.callCounter.activeBucket), bucketWant); diff != "" { // no need for atomic read because not concurrent with Done() call
			t.Fatalf("callCounter is different than expected, diff (-got +want): %v", diff)
		}
		od.newMu.Unlock()
	}

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
		// picker should not count, as the bucket should have 0 0 0, this also makes sure the
		// bucket gets cleared because interval timer algo never ran so no timerStartTime
		bucketWant := &bucket{}
		if diff := cmp.Diff((*bucket)(obj.callCounter.activeBucket), bucketWant); diff != "" { // no need for atomic read because not concurrent with Done() call
			t.Fatalf("callCounter is different than expected, diff (-got +want): %v", diff)
		}
		od.newMu.Unlock()

		// picker should not count, activeBucket should be 0 0 0 after two Done() invokes


		// 0 0 0 also tests clearing logic
	}


	// still need to test knobs of atomic reads/writes
}

// get these two tests working ^^^ *Done, plus finish how to test odAddrs update (either check map length or check invariant of system)
// mock the timer

type rrPicker struct {
	scs []balancer.SubConn

	next int
}

func (rrp *rrPicker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	sc := rrp.scs[rrp.next]
	rrp.next = (rrp.next + 1) % len(rrp.scs)
	return balancer.PickResult{SubConn: sc}, nil
}


func (s) TestEjectSuccessRate(t *testing.T) {
	/*defer func(af func(d time.Duration, f func()) *time.Timer) {
		afterFunc = af
	}(afterFunc)

	// can verify duration in overriden function - separate test
	afterFunc = func(_ time.Duration) *time.Timer {
		// can verify duration in overriden function for the 5 +  3 = 8 case and also
		// verify first duration passed is 3
		// how to make five seconds pass...? Then send a new config and it should start it for |2 - 4|

		// selectively trigger the interval timer

		// pass this
		return time.AfterFunc()
	}*/ // prevent interval timer from going off with manual call and a max interval. Use this logic to verify duration passed in.

	// Setup the system to a point where it will eject addresses
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
			SuccessRateEjection: &SuccessRateEjection{
				StdevFactor:           1900, // I don't know what you need to set this too to get it off the mean.
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

	// pick creates closure...
	// invoke Done callback for different subconns per address (different subconns require multiple
	// scw picked, can't use constPicker), maybe rr picker
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

		// for 0 - 15, set each upstream subchannel to have five successes each.
		// This should cause none of the addresses to be ejected as none of them
		// are outliers.
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

		od.intervalTimerAlgorithm() // should clear activeBuckets right, fresh start.

		// verify activeBuckets was cleared?

		// verify no UpdateSubConnState() call on the child, as no addresses got ejected.
		sCtx, cancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer cancel()
		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("no SubConn update should have been sent (no SubConn got ejected)")
		}

		// Since the address is not yet ejected, a SubConn update should forward down to the child. (doesn't matter which subconn you pick)
		od.UpdateSubConnState(scw1.(*subConnWrapper).SubConn, balancer.SubConnState{
			ConnectivityState: connectivity.Connecting,
		})

		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(ctx, subConnWithState{
			sc: scw1.(*subConnWrapper).SubConn,
			state: balancer.SubConnState{ConnectivityState: connectivity.Connecting},
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
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

		// Now that the address is ejected, need to test that SubConn updates
		// stop being forwarded downward. (sc update hasn't been tested beforehand either)

		// UpdateSubConnState that scw with CONNECTING
		od.UpdateSubConnState(pi.SubConn, balancer.SubConnState{
			ConnectivityState: connectivity.Connecting,
		})

		// make sure it doesn't send down
		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("SubConn update should not have been forwarded (the SubConn is ejected)")
		}


		// Maybe add uneject test to this and have it send out recent state (figure out uneject...round robin, more tests, )
		// trigger uneject how:

		defer func (n func() time.Time) {
			now = n
		}(now)

		now = func() time.Time {
			return time.Now().Add(time.Second * 1000) // will cause to always uneject
		}

		// have it return a time that will definitely hit interval

		// "after ejection_timestamp" (<- persisted per address)

		// timestamp gets written/recorded when the address gets ejected
		// diff
		// diff
		// new time being read, after a certain configured uneject period...
		od.intervalTimerAlgorithm() // need to eject and trigger it..


		// Yeah just put uneject here, you'll have to set it up anyway if you test it, override (inject) time.Now() or After...

		// unejected SubConn should report latest persisted state - which is connecting from earlier?
		if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(ctx, subConnWithState{ // read child into local var?
			sc: pi.SubConn,
			state: balancer.SubConnState{ConnectivityState: connectivity.Connecting},
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}

	}

}

func (s) TestEjectFailureRate(t *testing.T) {

}

// test uneject, does this branch off and go past this ^^^ or seperate test?

// scenario within the algorithm that leads to addresses being unejected...:
// "If the address is ejected, and the current time is after ejection_timestamp
// + min(base_ejection_time * multiplier, max(base_ejection_time,
// max_ejection_time)), un-eject the address."

// verify by testing with recent state being forwarded down

// Also need to test that SubConn updates continue to get forwarded down


// Also need to test UpdateAddresses eject/uneject




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
/*func (s) TestDurationOfInterval(t *testing.T) {
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