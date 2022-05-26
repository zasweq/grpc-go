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
	"fmt"
	"github.com/google/go-cmp/cmp/cmpopts"
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

	// updateClientConnState
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

	// The only other downstream effect is it creates entries in address map ** should we test creation and deletion of address map


	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// verify that the child balancer received the update - or read into a variable
	od.child.(*testClusterImplBalancer).waitForClientConnUpdate(ctx, balancer.ClientConnState{
		BalancerConfig: testClusterImplBalancerConfig{},
	})
}

/*
func (s) TestUpdateAddresses(t *testing.T) {
	// setup to what state?

	// first test case: one created SubConn with a certain address,
	// UpdateAddresses that created SubConn to a new address, should delete old map
	// entry and create new ones
}

func (s) TestUpdateState(t *testing.T) {
	// UpdateState with noop sends no counting picker plus correct connectivity state
	// UpdateState with reg sends counting picker plus correct connectivity state
	od, tcc := setup(t)


	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Connecting,
		Picker: /*newErrPicker?,
	})
}

// Ping UpdateClientConnState with a no-op config and then flip.
// Combine with UpdateState() permutations so that you test the most updated
// picker sent to TestCC
func (s) TestNoopPickerFlips(t *testing.T) {
	od, tcc := setup(t)

	tcc.NewPickerCh // receive off of this and verify it's a certain picker, either no-op or not
	// also test the knob of a certain connectivity state being pinged, two synced knobs
}*/