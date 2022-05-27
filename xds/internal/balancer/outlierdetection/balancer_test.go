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

var defaultTestTimeout = 5 * time.Second

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

// get these two tests working ^^^, plus finish how to test odAddrs update
// mock the timer

func (s) TestEjectSuccessRate(t *testing.T) {
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
			Interval:           10 * time.Second,
			BaseEjectionTime:   30 * time.Second,
			MaxEjectionTime:    300 * time.Second,
			MaxEjectionPercent: 10, // will still allow one right? Should I test each of these knobs?
			SuccessRateEjection: &SuccessRateEjection{
				StdevFactor:           1900,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         100,
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
	// Verify new subconn on tcc here?
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
		Picker: /*rr across scw1, scw2, scw3 etc.*/,
	})


	// Trigger interval timer...mock Duration?

	// First interval (how to trigger - mock?) no addresses ejected

	// second interval (how to trigger - mock?) eject address they fall out of the std dev/mean

}

func (s) TestEjectFailureRate(t *testing.T) {

}

// test uneject, does this branch off and go past this ^^^ or seperate test?



// Goals/Output of Outlier Detection algorithm:

// at the end after a predefined set of conditions about the system (kept in data structure):

// based on a random number generated (NEED TO MOCK!) between 1-100 wait just
// set an enforcement percentage of 100 random guaranteed to be < 100, eject an
// address (for each scsw scw.eject) at the end of success rate/failure
// percentage algorithms

// Is it default before it gets here...yeah sets default in xdsclient?



// at the end of the interval, uneject certain addresses (for each scsw scw.eject).


// verify scws have been ejected/unejected by verifying that UpdateSubConnState()
// gets called on child with TF for eject and recent state for uneject






// testUpdateAddresses to an ejected addresses


// Test interval timer triggering and 5 + 3 = 8 thing...?




// Also need to test the UpdateSubConnState run() synchronization somehow...ejected/unejected SubConns put on a channel
// to eventually consistent state being forwarded down...

// read all of the buffer, last update should be desired update...
