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
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/grpctest"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/resolver"
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
	parser := bb{}

	tests := []struct {
		name    string
		input   string
		wantCfg serviceconfig.LoadBalancingConfig
		wantErr string
	}{
		{
			name:    "noop-lb-config",
			input:   `{"interval": 9223372036854775807}`,
			wantCfg: &LBConfig{Interval: 1<<63 - 1},
		},
		{
			name: "good-lb-config",
			input: `{
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
			}`,
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
			input:   `{"interval": -10}`,
			wantErr: "LBConfig.Interval = -10ns; must be >= 0",
		},
		{
			name:    "base-ejection-time-is-negative",
			input:   `{"baseEjectionTime": -10}`,
			wantErr: "LBConfig.BaseEjectionTime = -10ns; must be >= 0",
		},
		{
			name:    "max-ejection-time-is-negative",
			input:   `{"maxEjectionTime": -10}`,
			wantErr: "LBConfig.MaxEjectionTime = -10ns; must be >= 0",
		},
		{
			name:    "max-ejection-percent-is-greater-than-100",
			input:   `{"maxEjectionPercent": 150}`,
			wantErr: "LBConfig.MaxEjectionPercent = 150; must be <= 100",
		},
		{
			name: "enforcement-percentage-success-rate-is-greater-than-100",
			input: `{
				"successRateEjection": {
					"enforcementPercentage": 150
				}
			}`,
			wantErr: "LBConfig.SuccessRateEjection.EnforcementPercentage = 150; must be <= 100",
		},
		{
			name: "failure-percentage-threshold-is-greater-than-100",
			input: `{
				"failurePercentageEjection": {
					"threshold": 150
				}
			}`,
			wantErr: "LBConfig.FailurePercentageEjection.Threshold = 150; must be <= 100",
		},
		{
			name: "enforcement-percentage-failure-percentage-ejection-is-greater-than-100",
			input: `{
				"failurePercentageEjection": {
					"enforcementPercentage": 150
				}
			}`,
			wantErr: "LBConfig.FailurePercentageEjection.EnforcementPercentage = 150; must be <= 100",
		},
		{
			name: "child-policy",
			input: `{
				"childPolicy": [
				{
					"xds_cluster_impl_experimental": {
						"cluster": "test_cluster"
					}
				}
			]
			}`,
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
			gotCfg, gotErr := parser.ParseConfig(json.RawMessage(test.input))
			if gotErr != nil && !strings.Contains(gotErr.Error(), test.wantErr) {
				t.Fatalf("ParseConfig(%v) = %v, wantErr %v", string(test.input), gotErr, test.wantErr)
			}
			if (gotErr != nil) != (test.wantErr != "") {
				t.Fatalf("ParseConfig(%v) = %v, wantErr %v", test.input, gotErr, test.wantErr)
			}
			if test.wantErr != "" {
				return
			}
			if diff := cmp.Diff(gotCfg, test.wantCfg); diff != "" {
				t.Fatalf("parseConfig(%v) got unexpected output, diff (-got +want): %v", string(test.input), diff)
			}
		})
	}
}

func (lbc *LBConfig) Equal(lbc2 *LBConfig) bool {
	if !lbc.EqualIgnoringChildPolicy(lbc2) {
		return false
	}
	return cmp.Equal(lbc.ChildPolicy, lbc2.ChildPolicy)
}


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

// TestUpdateState tests that an UpdateState call gets forwarded to the
// ClientConn.
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
	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker: &testutils.TestConstPicker{},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

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
	}
}

// TestClose tests the Close operation on the Outlier Detection Balancer. The
// Close operation should close the child.
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
}

// TestUpdateAddresses tests the functionality of UpdateAddresses and any
// changes in the addresses/plurality of those addresses for a SubConn.
func (s) TestUpdateAddresses(t *testing.T) {
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
		BalancerConfig: &LBConfig{
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
	child := od.child.(*testClusterImplBalancer)
	if err := child.waitForClientConnUpdate(ctx, balancer.ClientConnState{
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

	scw1, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address1",
		},
	}, balancer.NewSubConnOptions{})

	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	select {
	case <-ctx.Done():
		t.Fatal("timeout waiting for NewSubConn to be called on test Client Conn")
	case <-tcc.NewSubConnCh:
	}
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

	// Setup the system to where one address is ejected and one address
	// isn't.
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
		// Eject the second address.
		pi, err = picker.Pick(balancer.PickInfo{})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		od.intervalTimerAlgorithm()
		// verify UpdateSubConnState() got called with TRANSIENT_FAILURE for child in address that was ejected
		if err := child.waitForSubConnUpdate(ctx, subConnWithState{ // read child into local var?
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
	if err := child.waitForSubConnUpdate(ctx, subConnWithState{ // read child into local var?
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
	if err := child.waitForSubConnUpdate(ctx, subConnWithState{
		sc: scw1,
		state: balancer.SubConnState{ConnectivityState: connectivity.Idle}, // If you uneject a SubConn that hasn't received a UpdateSubConnState, this is recent state. This seems fine or is this wrong?
	}); err != nil {
		t.Fatalf("Error waiting for Sub Conn update: %v", err)
	}

	// A change of addresses from multiple to multiple should be a no-op, as the
	// address continues to be ignored by outlier detection.
	od.UpdateAddresses(scw1, []resolver.Address{
		{
			Addr: "address2",
		},
		{
			Addr: "address3",
		},
	})
	// Verify no downstream effects.
	sCtx, cancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	if err := od.child.(*testClusterImplBalancer).waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
		t.Fatalf("no SubConn update should have been sent (no SubConn got ejected/unejected)")
	}

	// A shift from multiple to single, where the new single address is ejected
	// should cause the SubConn to be ejected.
	od.UpdateAddresses(scw1, []resolver.Address{
		{
			Addr: "address2",
		},
	})
	// verify scw1 got ejected (UpdateSubConnState called with TRANSIENT FAILURE)
	if err := child.waitForSubConnUpdate(ctx, subConnWithState{
		sc: scw1,
		state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure}, // Represents ejected
	}); err != nil {
		t.Fatalf("Error waiting for Sub Conn update: %v", err)
	}
}




// Three important pieces of functionality run() synchronizes in regards to UpdateState calls towards grpc:

// 1. On a config update, checking if the picker was actually created or not.
// 2. Keeping track of the most recent connectivity state sent from the child (determined by UpdateState()).
//       * This will always forward up with most recent noopCfg bit
// 3. Keeping track of the most recent no-op config bit (determined by UpdateClientConnState())
//       * Will only forward up if no-op config bit changed and picker was already created


// TestPicker tests the Picker updates sent upward to grpc from the Outlier
// Detection Balancer. Two things can trigger a picker update, an
// UpdateClientConnState call (can flip the no-op config bit that affects
// Picker) and an UpdateState call (determines the connectivity state sent
// upward).
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

	child := od.child.(*testClusterImplBalancer)
	if err := child.waitForClientConnUpdate(ctx, balancer.ClientConnState{
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
		od.mu.Lock()
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
		od.mu.Unlock()
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
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})

	if err := child.waitForClientConnUpdate(ctx, balancer.ClientConnState{
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
		od.mu.Lock()
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
		od.mu.Unlock()
	}

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
		od.mu.Lock()
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
		od.mu.Unlock()
	}
}

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
	child := od.child.(*testClusterImplBalancer)
	if err := child.waitForClientConnUpdate(ctx, balancer.ClientConnState{
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
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw2, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address2",
		},
	}, balancer.NewSubConnOptions{})
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
		if err := child.waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("no SubConn update should have been sent (no SubConn got ejected)")
		}

		// Since no addresses are ejected, a SubConn update should forward down
		// to the child.
		od.UpdateSubConnState(scw1.(*subConnWrapper).SubConn, balancer.SubConnState{
			ConnectivityState: connectivity.Connecting,
		})

		if err := child.waitForSubConnUpdate(ctx, subConnWithState{
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
		if err := child.waitForSubConnUpdate(ctx, subConnWithState{
			sc: pi.SubConn, // Same SubConn present in address that had failures, since same ref to pi.Done(error)
			state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure}, // Represents ejected
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}
		// Only one address should be ejected.
		sCtx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer cancel()
		if err := child.waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("Only one SubConn update should have been sent (only one SubConn got ejected)")
		}

		// Now that an address is ejected, SubConn updates for SubConns using
		// that address should not be forwarded downward. These SubConn updates
		// will be cached to update the child sometime in the future when the
		// address gets unejected.
		od.UpdateSubConnState(pi.SubConn.(*subConnWrapper).SubConn, balancer.SubConnState{
			ConnectivityState: connectivity.Connecting,
		})
		if err := child.waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
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
		if err := child.waitForSubConnUpdate(ctx, subConnWithState{
			sc: pi.SubConn,
			state: balancer.SubConnState{ConnectivityState: connectivity.Connecting},
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}

	}

}

// TestEjectFailureRate tests the functionality of the interval timer
// algorithm of ejecting SubConns when configured with
// FailurePercentageEjection.
func (s) TestEjectFailureRate(t *testing.T) {
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

	child := od.child.(*testClusterImplBalancer)

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := child.waitForClientConnUpdate(ctx, balancer.ClientConnState{
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
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw2, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address2",
		},
	}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw3, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address3",
		},
	}, balancer.NewSubConnOptions{})
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
		if err := child.waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
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
		od.intervalTimerAlgorithm()

		// verify UpdateSubConnState() got called with TRANSIENT_FAILURE for
		// child in address that was ejected.
		if err := child.waitForSubConnUpdate(ctx, subConnWithState{
			sc: pi.SubConn,
			state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure}, // Represents ejected
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}

		// verify only one got ejected
		sCtx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer cancel()
		if err := child.waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("Only one SubConn update should have been sent (only one SubConn got ejected)")
		}
	}
}

// TestDurationOfInterval tests the configured interval timer. On the first
// config received, the Outlier Detection balancer should configure the timer
// with whatever is directly specified on the config. On subsequent configs
// received, the Outlier Detection balancer should configure the timer with
// whatever interval is configred minus the difference between the current time
// and the previous start timestamp. For a no-op configuration, the timer should
// not be configred at all.
func (s) TestDurationOfInterval(t *testing.T) {
	defer func(af func(d time.Duration, f func()) *time.Timer) {
		afterFunc = af
	}(afterFunc)

	durationChan := testutils.NewChannel()
	afterFunc = func(dur time.Duration, _ func()) *time.Timer {
		durationChan.Send(dur)
		return time.NewTimer(1<<63 - 1)
	}


	od, _ := setup(t)
	defer od.Close()

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
	// The configured duration should be 8 seconds - what the balancer was
	// configured with.
	if dur.Seconds() != 8 {
		t.Fatalf("configured duration should have been 8 seconds to start timer")
	}

	defer func (n func() time.Time) {
		now = n
	}(now)
	now = func() time.Time {
		return time.Now().Add(time.Second * 5)
	}

	// UpdateClientConnState with an interval of 9 seconds. Due to 5 seconds
	// already passing (from overriden time.Now function), this should start an
	// interval timer of ~4 seconds.
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
	d, err = durationChan.Receive(ctx)
	if err != nil {
		t.Fatalf("Error receiving duration from afterFunc() call: %v", err)
	}
	dur = d.(time.Duration)
	if dur.Seconds() < 3.5 || 4.5 < dur.Seconds() {
		t.Fatalf("configured duration should have been around 4 seconds to start timer")
	}

	// UpdateClientConnState with a no-op config. This shouldn't configure the
	// interval timer at all due to it being a no-op.
	od.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			Interval: 10 * time.Second,
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
	// No timer should have been started.
	sCtx, cancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	_, err = durationChan.Receive(sCtx)
	if err == nil {
		t.Fatal("No timer should have started.")
	}
}

// TestConcurrentPickerCountsWithIntervalTimer tests concurrent picker updates
// (writing to the callCounter) and the interval timer algorithm, which reads
// the callCounter.
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
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw2, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address2",
		},
	}, balancer.NewSubConnOptions{})
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
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
			time.Sleep(1 * time.Nanosecond)
		}
	}()

	// You'll also need cleanup for any invariants of the algorithm...like ejecting and putting shit on channels
	// you'll see this come up if it actually breaks.


	od.intervalTimerAlgorithm() // two swaps
	od.intervalTimerAlgorithm()



	close(finished) // Don't spawn a goroutine you can't exit.
}

// TestConcurrentOperations calls different operations on the balancer in
// separate goroutines to test for any race conditions and deadlocks. It also
// uses a child balancer which verifies that no operations on the child get
// called after the child balancer is closed.
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

	scw1, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address1",
		},
	}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw2, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address2",
		},
	}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	scw3, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address3",
		},
	}, balancer.NewSubConnOptions{})
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker: &rrPicker{
			scs: []balancer.SubConn{scw2, scw3},
		},
	}) // Move stuff to like setup basic, setupX, setupY (can even depend on setupX if wrapper but might not too), ugh also got to do duration test

	var picker balancer.Picker
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case picker = <-tcc.NewPickerCh:
	}

	finished := make(chan struct{})
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
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
			time.Sleep(1 * time.Nanosecond)
		}
	}()

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


	// call Outlier Detection's balancer.ClientConn operations asynchrously.
	// balancer.ClientConn operations have no guarantee from the API to be
	// called synchronously.
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

	go func() {
		od.NewSubConn([]resolver.Address{{Addr: "address4"}}, balancer.NewSubConnOptions{})
	}()

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


	// Call balancer.Balancers synchronously in this goroutine, upholding the
	// balancer.Balancer API guarantee.
	od.UpdateClientConnState(balancer.ClientConnState{ // This will delete addresses and flip to no op
		ResolverState: resolver.State{
			Addresses: []resolver.Address{
				{
					Addr: "address1",
				},
			},
		},
		BalancerConfig: &LBConfig{
			Interval: 1<<63 - 1,
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})

	// mention no check for child type switching, but should be ok since should? be guaranteed to be cluster_impl balancer

	// Call balancer.Balancers synchronously in this goroutine, upholding the
	// balancer.Balancer API guarantee.
	od.UpdateSubConnState(scw1, balancer.SubConnState{
		ConnectivityState: connectivity.Connecting,
	})
	od.ResolverError(errors.New("some error"))
	od.ExitIdle()
	od.Close()
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
	// To fail the test if any balancer.Balancer operation gets called after
	// Close().
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