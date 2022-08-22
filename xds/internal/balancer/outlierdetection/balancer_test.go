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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
	testpb "google.golang.org/grpc/test/grpc_testing"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/grpctest"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/serviceconfig"
	"google.golang.org/grpc/xds/internal/balancer/clusterimpl"
)

var (
	defaultTestTimeout      = 5 * time.Second
	defaultTestShortTimeout = 10 * time.Millisecond
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// TestParseConfig verifies the ParseConfig() method in the Outlier Detection
// Balancer.
func (s) TestParseConfig(t *testing.T) {
	stub.Register(errParseConfigName, stub.BalancerFuncs{
		ParseConfig: func(json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
			return nil, errors.New("some error")
		},
	})

	parser := bb{}

	tests := []struct {
		name    string
		input   string
		wantCfg serviceconfig.LoadBalancingConfig
		wantErr string
	}{
		{
			name: "noop-lb-config",
			input: `{
				"interval": 9223372036854775807,
				"childPolicy": [
				{
					"xds_cluster_impl_experimental": {
						"cluster": "test_cluster"
					}
				}
				]
			}`,
			wantCfg: &LBConfig{
				Interval: 1<<63 - 1,
				ChildPolicy: &internalserviceconfig.BalancerConfig{
					Name: "xds_cluster_impl_experimental",
					Config: &clusterimpl.LBConfig{
						Cluster: "test_cluster",
					},
				},
			},
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
				},
                "childPolicy": [
				{
					"xds_cluster_impl_experimental": {
						"cluster": "test_cluster"
					}
				}
				]
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
				ChildPolicy: &internalserviceconfig.BalancerConfig{
					Name: "xds_cluster_impl_experimental",
					Config: &clusterimpl.LBConfig{
						Cluster: "test_cluster",
					},
				},
			},
		},
		{
			name:    "interval-is-negative",
			input:   `{"interval": -10}`,
			wantErr: "OutlierDetectionLoadBalancingConfig.interval = -10ns; must be >= 0",
		},
		{
			name:    "base-ejection-time-is-negative",
			input:   `{"baseEjectionTime": -10}`,
			wantErr: "OutlierDetectionLoadBalancingConfig.base_ejection_time = -10ns; must be >= 0",
		},
		{
			name:    "max-ejection-time-is-negative",
			input:   `{"maxEjectionTime": -10}`,
			wantErr: "OutlierDetectionLoadBalancingConfig.max_ejection_time = -10ns; must be >= 0",
		},
		{
			name:    "max-ejection-percent-is-greater-than-100",
			input:   `{"maxEjectionPercent": 150}`,
			wantErr: "OutlierDetectionLoadBalancingConfig.max_ejection_percent = 150; must be <= 100",
		},
		{
			name: "enforcement-percentage-success-rate-is-greater-than-100",
			input: `{
				"successRateEjection": {
					"enforcementPercentage": 150
				}
			}`,
			wantErr: "OutlierDetectionLoadBalancingConfig.SuccessRateEjection.enforcement_percentage = 150; must be <= 100",
		},
		{
			name: "failure-percentage-threshold-is-greater-than-100",
			input: `{
				"failurePercentageEjection": {
					"threshold": 150
				}
			}`,
			wantErr: "OutlierDetectionLoadBalancingConfig.FailurePercentageEjection.threshold = 150; must be <= 100",
		},
		{
			name: "enforcement-percentage-failure-percentage-ejection-is-greater-than-100",
			input: `{
				"failurePercentageEjection": {
					"enforcementPercentage": 150
				}
			}`,
			wantErr: "OutlierDetectionLoadBalancingConfig.FailurePercentageEjection.enforcement_percentage = 150; must be <= 100",
		},
		{
			name: "child-policy-not-present",
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
			wantErr: "OutlierDetectionLoadBalancingConfig.child_policy must be present",
		},
		{
			name: "child-policy-present-but-parse-error",
			input: `{
				"interval": 9223372036854775807,
				"childPolicy": [
				{
					"errParseConfigBalancer": {
						"cluster": "test_cluster"
					}
				}
			]
			}`,
			wantErr: "error parsing loadBalancingConfig for policy \"errParseConfigBalancer\"",
		},
		{
			name: "no-supported-child-policy",
			input: `{
				"interval": 9223372036854775807,
				"childPolicy": [
				{
					"doesNotExistBalancer": {
						"cluster": "test_cluster"
					}
				}
			]
			}`,
			wantErr: "invalid loadBalancingConfig: no supported policies found",
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
				t.Fatalf("ParseConfig(%v) = %v, wantErr %v", test.input, gotErr, test.wantErr)
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

const errParseConfigName = "errParseConfigBalancer"
const tcibname = "testClusterImplBalancer"
const verifyBalancerName = "verifyBalancer"
const twoUpdateStateBalancerName = "twoUpdateStateBalancer"


/*
func init() {
	balancer.Register(errParseConfigBuilder{})
	balancer.Register(errParseConfigBuilder{})
	balancer.Register(tcibBuilder{})
	balancer.Register(verifyBalancerBuilder{})
	balancer.Register(twoUpdateStateBalancerBuilder{})
}
type errParseConfigBuilder struct{}

func (errParseConfigBuilder) Build(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
	return nil
}

func (errParseConfigBuilder) Name() string {
	return "errParseConfigBalancer"
}

func (errParseConfigBuilder) ParseConfig(c json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	return nil, errors.New("some error")
}

func setup(t *testing.T) (*outlierDetectionBalancer, *testutils.TestClientConn, func()) {
	t.Helper()
	builder := balancer.Get(Name)
	if builder == nil {
		t.Fatalf("balancer.Get(%q) returned nil", Name)
	}
	tcc := testutils.NewTestClientConn(t)
	odB := builder.Build(tcc, balancer.BuildOptions{})
	return odB.(*outlierDetectionBalancer), tcc, func() {
		odB.Close()
	}
}

const tcibname = "testClusterImplBalancer"
const verifyBalancerName = "verifyBalancer"
const twoUpdateStateBalancerName = "twoUpdateStateBalancer"

type tcibBuilder struct{}

func (tcibBuilder) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	return &testClusterImplBalancer{
		ccsCh:         testutils.NewChannel(),
		scStateCh:     testutils.NewChannel(),
		resolverErrCh: testutils.NewChannel(),
		closeCh:       testutils.NewChannel(),
		exitIdleCh:    testutils.NewChannel(),
		cc:            cc,
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
	if !cmp.Equal(gotSCS, wantSCS, cmp.AllowUnexported(subConnWithState{}, testutils.TestSubConn{}, subConnWrapper{}, addressInfo{}), cmpopts.IgnoreFields(subConnWrapper{}, "scUpdateCh")) {
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
	internal.RegisterOutlierDetectionBalancerForTesting()
	od, _ := setup(t) // need to switch this to receive a function which you Close() as well
	defer func() {
		od.Close() // this will leak a goroutine otherwise
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

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
				Name:   tcibname,
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
}*/

func setup(t *testing.T) (*outlierDetectionBalancer, *testutils.TestClientConn, func()) {
	t.Helper()
	builder := balancer.Get(Name)
	if builder == nil {
		t.Fatalf("balancer.Get(%q) returned nil", Name)
	}
	tcc := testutils.NewTestClientConn(t)
	odB := builder.Build(tcc, balancer.BuildOptions{})
	return odB.(*outlierDetectionBalancer), tcc, func() {
		odB.Close()
	}
}

type balancerConfig struct {
	serviceconfig.LoadBalancingConfig
}

// If you used a stub balancer here, you could push this update from the child
// balancer's UpdateClientConnState (i.e. the UpdateState call itself)

// Covers both UpdateClientConnState tests and UpdateState test

// TestUpdateClientConnState2 tests the basic UpdateClientConnStateFunctionality
// of the Outlier Detection Balancer. On the first receipt of a good config, the
// balancer is expected to eventually create a child and send the child it's
// configuration. When a new configuration comes in that changes the child's
// type which reports READY immediately, the first child balancer should be
// closed and the second child balancer should receive it's first config update.
func (s) TestUpdateClientConnState2(t *testing.T) {
	ccsCh := testutils.NewChannel()
	closeCh := testutils.NewChannel()

	// Stub registers UpdateState with READY to move out of graceful switch, delete old
	stub.Register(tcibname, stub.BalancerFuncs{
		UpdateClientConnState: func(bd *stub.BalancerData, _ balancer.ClientConnState) error {
			ccsCh.Send(nil) // or do we want to verify the config, I think it's fine
			return nil
		},
		Close: func(bd *stub.BalancerData) {
			closeCh.Send(nil)
		},
	})


	stub.Register(verifyBalancerName, stub.BalancerFuncs{
		// Wait the UpdateState needs to be sent in the second registered balancer
		UpdateClientConnState: func(bd *stub.BalancerData, _ balancer.ClientConnState) error {
			// UpdateState inline to READY to complete graceful switch process
			// synchronously from any UpdateClientConnState call. Will this
			// block or should we expect this to get to TCC? If it does does the
			// test below about UpdateState() become negated then?

			// Can we merge this call with the UpdateState() call below?

			bd.ClientConn.UpdateState(balancer.State{
				ConnectivityState: connectivity.Ready,
				Picker: &testutils.TestConstPicker{},
			})
			ccsCh.Send(nil)
			return nil
		},
		// Do we want to verify Close on the second one as well?
		Close: func(bd *stub.BalancerData) {
			closeCh.Send(nil)
		},
	})

	internal.RegisterOutlierDetectionBalancerForTesting()
	od, tcc, _ := setup(t)
	defer internal.UnregisterOutlierDetectionBalancerForTesting() // could also register and unregister in cleanup()

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
				Name:   tcibname,
				Config: balancerConfig{}, // Do I even need this type now a days?
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// first arg is if you want to verify something in UpdateClientConnState
	_, err := ccsCh.Receive(ctx)
	if err != nil {
		t.Fatalf("timed out waiting for UpdateClientConnState on the first child balancer: %v", err)
	}

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
				Name:   verifyBalancerName,
				Config: balancerConfig{},
			},
		},
	})
	// Verify inline UpdateState() call eventually makes it's way to the Test
	// Client Conn.
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case state := <-tcc.NewStateCh:
		if state != connectivity.Ready {
			t.Fatalf("ClientConn received connectivity state %v, want %v", state, connectivity.Ready)
		}
	}

	// Verify the first child balancer closed.
	_, err = closeCh.Receive(ctx)
	if err != nil {
		t.Fatalf("timed out waiting for the first child balancer to be closed: %v", err)
	}
	// Verify the second child balancer received it's first config update.
	_, err = ccsCh.Receive(ctx)
	if err != nil {
		t.Fatalf("timed out waiting for UpdateClientConnState on the second child balancer: %v", err)
	}
	print("628")
	// Closing the Outlier Detection Balancer should close the newly created
	// child.
	od.Close()
	_, err = closeCh.Receive(ctx)
	if err != nil {
		t.Fatalf("timed out waiting for the second child balancer to be closed: %v", err)
	}
}

// UpdateAddresses is quite complicated, I don't know how I'll do it with stubs, I like UpdateAddresses just simplify


// TestDuration I honestly think is fine outside of the fact that I need to rewrite conditional.
// copy paste here


// TestPicker is quite complicated and specific on the synchronization of the counting bit
// how do I test the correctness of the picker behavior without looking
// at internal state? This one try and encapsulate in E2E style test, like the counting stuff
// and RR across certain addresses

// TestPicker
func (s) TestPicker(t *testing.T) {

}

type subConnWithState struct {
	sc    balancer.SubConn
	state balancer.SubConnState
}

func scwsEqual(gotSCWS subConnWithState, wantSCWS subConnWithState) error {
	if !cmp.Equal(gotSCWS, wantSCWS, cmp.AllowUnexported(subConnWithState{}, testutils.TestSubConn{}, subConnWrapper{}, addressInfo{}), cmpopts.IgnoreFields(subConnWrapper{}, "scUpdateCh")) {
		return fmt.Errorf("received SubConnState: %+v, want %+v", gotSCWS, wantSCWS)
	}
	return nil
}

type rrPicker struct {
	scs  []balancer.SubConn
	next int
}

func (rrp *rrPicker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	sc := rrp.scs[rrp.next]
	rrp.next = (rrp.next + 1) % len(rrp.scs)
	return balancer.PickResult{SubConn: sc}, nil
}

// UpdateAddresses
// Four test cases
//     Single to single Addr1 -> Addr2, I don't know, this seems fine

// Refactor setup into a common func

// TestDurationOfInterval tests the configured interval timer. On the first
// config received, the Outlier Detection balancer should configure the timer
// with whatever is directly specified on the config. On subsequent configs
// received, the Outlier Detection balancer should configure the timer with
// whatever interval is configured minus the difference between the current time
// and the previous start timestamp. For a no-op configuration, the timer should
// not be configured at all.
func (s) TestDurationOfInterval(t *testing.T) {
	stub.Register(tcibname, stub.BalancerFuncs{})

	internal.RegisterOutlierDetectionBalancerForTesting()
	od, _, _ := setup(t)
	defer func(af func(d time.Duration, f func()) *time.Timer) {
		od.Close()
		afterFunc = af
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}(afterFunc)

	durationChan := testutils.NewChannel()
	afterFunc = func(dur time.Duration, _ func()) *time.Timer {
		durationChan.Send(dur)
		return time.NewTimer(1<<63 - 1)
	}

	od.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			Interval:           8 * time.Second,
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
				Name:   tcibname,
				Config: balancerConfig{},
			},
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
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

	defer func(n func() time.Time) {
		now = n
	}(now)
	now = func() time.Time {
		return time.Now().Add(time.Second * 5)
	}

	// UpdateClientConnState with an interval of 9 seconds. Due to 5 seconds
	// already passing (from overridden time.Now function), this should start an
	// interval timer of ~4 seconds.
	od.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			Interval:           9 * time.Second,
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
				Name:   tcibname,
				Config: balancerConfig{},
			},
		},
	})

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
				Name:   tcibname,
				Config: balancerConfig{},
			},
		},
	})

	ctx, cancel = context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// No timer should have been started.
	sCtx, cancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer cancel()
	_, err = durationChan.Receive(sCtx)
	if err == nil {
		t.Fatal("No timer should have started.")
	}
}

func (s) TestEjectUnejectSuccessRate(t *testing.T) {
	scsCh := testutils.NewChannel()
	var scw1, scw2, scw3 balancer.SubConn
	var err error
	stub.Register(tcibname, stub.BalancerFuncs{
		UpdateClientConnState: func(bd *stub.BalancerData, _ balancer.ClientConnState) error {
			scw1, err = bd.ClientConn.NewSubConn([]resolver.Address{
				{
					Addr: "address1",
				},
			}, balancer.NewSubConnOptions{})
			if err != nil {
				t.Fatalf("error in od.NewSubConn call: %v", err)
			}
			scw2, err = bd.ClientConn.NewSubConn([]resolver.Address{
				{
					Addr: "address2",
				},
			}, balancer.NewSubConnOptions{})
			if err != nil {
				t.Fatalf("error in od.NewSubConn call: %v", err)
			}
			scw3, err = bd.ClientConn.NewSubConn([]resolver.Address{
				{
					Addr: "address3",
				},
			}, balancer.NewSubConnOptions{})
			if err != nil {
				t.Fatalf("error in od.NewSubConn call: %v", err)
			}
			return nil
		},
		UpdateSubConnState: func(_ *stub.BalancerData, sc balancer.SubConn, state balancer.SubConnState) {
			scsCh.Send(subConnWithState{
				sc: sc,
				state: state,
			})
		},
	})

	internal.RegisterOutlierDetectionBalancerForTesting()
	od, tcc, _ := setup(t)
	defer func(){
		od.Close()
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

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
				Threshold:             50,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name:   tcibname,
				Config: balancerConfig{},
			},
		},
	})

	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker: &rrPicker{
			scs: []balancer.SubConn{scw1, scw2, scw3},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case picker := <-tcc.NewPickerCh:
		// Set each of the three upstream addresses to have five successes each.
		// This should cause none of the addresses to be ejected as none of them
		// are outliers according to the success rate algorithm.
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
		if _, err := scsCh.Receive(sCtx); err == nil {
			t.Fatalf("no SubConn update should have been sent (no SubConn got ejected)")
		}

		// Since no addresses are ejected, a SubConn update should forward down
		// to the child.
		od.UpdateSubConnState(scw1.(*subConnWrapper).SubConn, balancer.SubConnState{
			ConnectivityState: connectivity.Connecting,
		})

		gotSCWS, err := scsCh.Receive(ctx)
		if err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}
		// verify data in scws is scw1 and connecting
		err = scwsEqual(gotSCWS.(subConnWithState), subConnWithState{
			sc:    scw1,
			state: balancer.SubConnState{ConnectivityState: connectivity.Connecting},
		})

		// Set two of the upstream addresses to have five successes each, and
		// one of the upstream addresses to have five failures. This should
		// cause the address which has five failures to be ejected according the
		// SuccessRateAlgorithm.
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
		gotSCWS, err = scsCh.Receive(ctx)
		if err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}
		err = scwsEqual(gotSCWS.(subConnWithState), subConnWithState{
			sc:    scw3,
			state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure},
		})
		// Only one address should be ejected.
		sCtx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer cancel()
		if _, err := scsCh.Receive(sCtx); err == nil {
			t.Fatalf("Only one SubConn update should have been sent (only one SubConn got ejected)")
		}

		// Now that an address is ejected, SubConn updates for SubConns using
		// that address should not be forwarded downward. These SubConn updates
		// will be cached to update the child sometime in the future when the
		// address gets unejected.
		od.UpdateSubConnState(pi.SubConn, balancer.SubConnState{
			ConnectivityState: connectivity.Connecting,
		})
		sCtx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer cancel()
		if _, err := scsCh.Receive(sCtx); err == nil {
			t.Fatalf("SubConn update should not have been forwarded (the SubConn is ejected)")
		}

		// Override now to cause the interval timer algorithm to always uneject a SubConn.
		defer func(n func() time.Time) {
			now = n
		}(now)

		now = func() time.Time {
			return time.Now().Add(time.Second * 1000) // will cause to always uneject addresses which are ejected
		}
		od.intervalTimerAlgorithm()

		// unejected SubConn should report latest persisted state - which is
		// connecting from earlier.
		gotSCWS, err = scsCh.Receive(ctx)
		if err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}
		err = scwsEqual(gotSCWS.(subConnWithState), subConnWithState{
			sc:    scw3,
			state: balancer.SubConnState{ConnectivityState: connectivity.Connecting},
		})
	}
}

func (s) TestEjectFailureRate(t *testing.T) {
	scsCh := testutils.NewChannel()
	var scw1, scw2, scw3 balancer.SubConn
	var err error
	stub.Register(tcibname, stub.BalancerFuncs{
		UpdateClientConnState: func(bd *stub.BalancerData, _ balancer.ClientConnState) error {
			scw1, err = bd.ClientConn.NewSubConn([]resolver.Address{
				{
					Addr: "address1",
				},
			}, balancer.NewSubConnOptions{})
			if err != nil {
				t.Fatalf("error in od.NewSubConn call: %v", err)
			}
			scw2, err = bd.ClientConn.NewSubConn([]resolver.Address{
				{
					Addr: "address2",
				},
			}, balancer.NewSubConnOptions{})
			if err != nil {
				t.Fatalf("error in od.NewSubConn call: %v", err)
			}
			scw3, err = bd.ClientConn.NewSubConn([]resolver.Address{
				{
					Addr: "address3",
				},
			}, balancer.NewSubConnOptions{})
			if err != nil {
				t.Fatalf("error in od.NewSubConn call: %v", err)
			}
			return nil
		},
		UpdateSubConnState: func(_ *stub.BalancerData, sc balancer.SubConn, state balancer.SubConnState) {
			scsCh.Send(subConnWithState{
				sc: sc,
				state: state,
			})
		},
	})

	internal.RegisterOutlierDetectionBalancerForTesting()
	od, tcc, _ := setup(t)
	defer func(){
		od.Close()
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

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
				Name:   tcibname,
				Config: balancerConfig{},
			},
		},
	})

	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker: &rrPicker{
			scs: []balancer.SubConn{scw1, scw2, scw3},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case picker := <-tcc.NewPickerCh:
		// Set each upstream address to have five successes each. This should
		// cause none of the addresses to be ejected as none of them are below
		// the failure percentage threshold.
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
		sCtx, cancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer cancel()
		if _, err := scsCh.Receive(sCtx); err == nil {
			t.Fatalf("no SubConn update should have been sent (no SubConn got ejected)")
		}

		// Set two upstream addresses to have five successes each, and one
		// upstream address to have five failures. This should cause the address
		// with five failures to be ejected according to the Failure Percentage
		// Algorithm.
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

		// verify UpdateSubConnState() got called with TRANSIENT_FAILURE for
		// child in address that was ejected.
		gotSCWS, err := scsCh.Receive(ctx)
		if err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}
		err = scwsEqual(gotSCWS.(subConnWithState), subConnWithState{
			sc:    scw3,
			state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure},
		})

		// verify only one address got ejected
		sCtx, cancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer cancel()
		if _, err := scsCh.Receive(sCtx); err == nil {
			t.Fatalf("Only one SubConn update should have been sent (only one SubConn got ejected)")
		}
	}
}

// TestConcurrentOperations calls different operations on the balancer in
// separate goroutines to test for any race conditions and deadlocks. It also
// uses a child balancer which verifies that no operations on the child get
// called after the child balancer is closed.
func (s) TestConcurrentOperations(t *testing.T) {
	// Verify balancer does this - makes sure operations don't happen after Close()
	closed := grpcsync.NewEvent()
	stub.Register(verifyBalancerName, stub.BalancerFuncs{
		UpdateClientConnState: func(*stub.BalancerData, balancer.ClientConnState) error {
			if closed.HasFired() {
				t.Fatal("UpdateClientConnState was called after Close(), which breaks the balancer API")
			}
			return nil
		},
		ResolverError: func(*stub.BalancerData, error) {
			if closed.HasFired() {
				t.Fatal("ResolverError was called after Close(), which breaks the balancer API")
			}
		},
		UpdateSubConnState: func(*stub.BalancerData, balancer.SubConn, balancer.SubConnState) {
			if closed.HasFired() {
				t.Fatal("UpdateSubConnState was called after Close(), which breaks the balancer API")
			}
		},
		Close: func(*stub.BalancerData) {
			closed.Fire()
		},
		ExitIdle: func(*stub.BalancerData) {
			if closed.HasFired() {
				t.Fatal("ExitIdle was called after Close(), which breaks the balancer API")
			}
		},
	})
	internal.RegisterOutlierDetectionBalancerForTesting()
	od, tcc, _ := setup(t)
	defer func() {
		od.Close()
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

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
			SuccessRateEjection: &SuccessRateEjection{ // Have both Success Rate and Failure Percentage to step through all the interval timer code
				StdevFactor:           500,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			FailurePercentageEjection: &FailurePercentageEjection{
				Threshold:             50,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name:   verifyBalancerName,
				Config: balancerConfig{},
			},
		},
	})
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
	})

	var picker balancer.Picker
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case picker = <-tcc.NewPickerCh:
	}

	finished := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-finished:
				return
			default:
			}
			pi, err := picker.Pick(balancer.PickInfo{})
			if err != nil {
				continue
			}
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
			time.Sleep(1 * time.Nanosecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
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
	wg.Add(1)
	go func() {
		defer wg.Done()
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		od.NewSubConn([]resolver.Address{{Addr: "address4"}}, balancer.NewSubConnOptions{})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		od.RemoveSubConn(scw1)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
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
				Name:   verifyBalancerName,
				Config: balancerConfig{},
			},
		},
	})

	// Call balancer.Balancers synchronously in this goroutine, upholding the
	// balancer.Balancer API guarantee.
	od.UpdateSubConnState(scw1.(*subConnWrapper).SubConn, balancer.SubConnState{
		ConnectivityState: connectivity.Connecting,
	})
	od.ResolverError(errors.New("some error"))
	od.ExitIdle()
	od.Close()
	close(finished)
	wg.Wait()
}


// e2e style tests

// First, spin up a server backend similar to Easwar's.
// Without an address this defaults to localhost:0 and then
// you can use the 5 addresses string, pass that to CheckRoundRobinRPCs

// Success rate 12345 123 12345

// Failure rate 12345 123 12345

// Noop config 12345 123 12345 (fail 3 backends) continue 12345 (another call to CheckRoundRobinRPC's)




// setup()
// spin up 5  (1 good, 2 good, 3 good, 4 errors, 5 errors)

// Setup spins up 5 test backends, each listening on a port on localhost. 3 of
// the backends are configured to always work as intended and 2 are configured
// to always return errors. This scenario of some backends working and some not
// is used to test common Outlier Detection scenarios.
func setupBackends(t *testing.T) ([]string, []*stubserver.StubServer/*stubserver.StubServer/*or can you just return addresses does the caller need any other state other than the addresses?*/) {
	t.Helper()

	backends := make([]*stubserver.StubServer, 5)
	addresses := make([]string, 5)
	// Construct and start 3 working backends.
	for i := 0; i < 3; i++ {
		backend := &stubserver.StubServer{
			EmptyCallF: func(ctx context.Context, in *testpb.Empty) (*testpb.Empty, error) {
				return &testpb.Empty{}, nil
			},
		}
		if err := backend.StartServer(/*if you want knobs on options pass in through function*/); err != nil {
			t.Fatalf("Failed to start backend: %v", err)
		}
		t.Logf("Started good TestService backend at: %q", backend.Address)
		backends[i] = backend
		addresses[i] = backend.Address
	}

	// Construct and start 2 failing backends.
	for i := 3; i < 5; i++ {
		backend := &stubserver.StubServer{
			EmptyCallF: func(ctx context.Context, in *testpb.Empty) (*testpb.Empty, error) {
				return nil, errors.New("some error")
			},
		}
		if err := backend.StartServer(/*if you want knobs on options pass in through function*/); err != nil {
			t.Fatalf("Failed to start backend: %v", err)
		}
		t.Logf("Started good TestService backend at: %q", backend.Address)
		backends[i] = backend
		addresses[i] = backend.Address
	}

	// t.cleanup is very flaky, so you can manually call backend.Stop()
	/*t.Cleanup(func() {
		for _, backend := range backends {
			backend.Stop()
		}
	})*/
	return addresses, backends/*, backends <- then caller closes this, see if this fixes*/
}

// something similar to e2e test util in regards to round robin

// three pass iteration verification?

// checkRoundRobinRPCs verifies that EmptyCall RPCs on the given ClientConn,
// connected to a server exposing the test.grpc_testing.TestService, are
// roundrobin-ed across the given backend addresses.
//
// Returns a non-nil error if context deadline expires before RPCs start to get
// roundrobin-ed across the given backends.
func checkRoundRobinRPCs(ctx context.Context, client testpb.TestServiceClient, addrs []resolver.Address) error {
	// Check for Round Robin = logically
	// three iterations of the same peers addresses

	wantAddrCount := make(map[string]int)
	for _, addr := range addrs {
		wantAddrCount[addr.Addr]++
	}
	// ctx should be called with 5 seocnd timeout
	for ; ctx.Err() == nil; <-time.After(time.Millisecond) {
		// Perform 3 iterations.
		var iterations [][]string
		for i := 0; i < 3; i++ {
			iteration := make([]string, len(addrs))
			for c := 0; c < len(addrs); c++ {
				var peer peer.Peer
				client.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(&peer))
				// if this doesn't work yikes
				if peer.Addr != nil {
					iteration[c] = peer.Addr.String() // fuck, does this get populated on an RPC error, peer information AFTER RPC completes
				}
			}
			iterations = append(iterations, iteration)
		}
		// Ensure the the first iteration contains all addresses in addrs.
		gotAddrCount := make(map[string]int)
		for _, addr := range iterations[0] {
			gotAddrCount[addr]++
		}
		if diff := cmp.Diff(gotAddrCount, wantAddrCount); diff != "" {
			logger.Infof("non-roundrobin, got address count in one iteration: %v, want: %v, Diff: %s", gotAddrCount, wantAddrCount, diff)
			continue
		}
		// Ensure all three iterations contain the same addresses.
		if !cmp.Equal(iterations[0], iterations[1]) || !cmp.Equal(iterations[0], iterations[2]) {
			logger.Infof("non-roundrobin, first iter: %v, second iter: %v, third iter: %v", iterations[0], iterations[1], iterations[2])
			continue
		}
		fmt.Printf("wow Round Robin helper returned well")
		return nil
	}
	return fmt.Errorf("Timeout when waiting for roundrobin distribution of RPCs across addresses: %v", addrs)
}

// After finishing this look at flaky test and Easwar's PR

// TestSuccessRateAlgorithm tests the SuccessRateEjection functionality of the
// Outlier Detection Balancer. The balancer is configured with a
// SuccessRateEjection algorithm, and connects to 5 upstreams. 2 of the
// upstreams are unhealthy. At some point in time, the Outlier Detection
// Balancer should only send RPC's in a Round Robin like fashion across the
// three healthy upstreams.
func (s) TestSuccessRateAlgorithm(t *testing.T) {
	internal.RegisterOutlierDetectionBalancerForTesting()
	defer internal.UnregisterOutlierDetectionBalancerForTesting()
	addresses, backends := setupBackends(t)
	defer func() {
		for _, backend := range backends {
			backend.Stop()
		}
	}()

	mr := manual.NewBuilderWithScheme("od-e2e")
	defer mr.Close()

	// Figure out the correct stdevFactor to configure the component with

	// If you want this could be knob since the other logic is exactly the same

	odServiceConfigJSON := `
{
  "loadBalancingConfig": [
    {
      "outlier_detection_experimental": {
        "interval": 50000000,
		"baseEjectionTime": 100000000,
		"maxEjectionTime": 300000000000,
		"maxEjectionPercent": 33,
		"successRateEjection": {
			"stdevFactor": 50,
			"enforcementPercentage": 100,
			"minimumHosts": 3,
			"requestVolume": 5
		},
        "childPolicy": [
		{
			"round_robin": {}
		}
		]
      }
    }
  ]
}`
	sc := internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(odServiceConfigJSON)

	mr.InitialState(resolver.State{
		Addresses: []resolver.Address{
			{
				Addr: addresses[0],
			},
			{
				Addr: addresses[1],
			},
			{
				Addr: addresses[2],
			},
			{
				Addr: addresses[3],
			},
			{
				Addr: addresses[4],
			},
		},
		ServiceConfig: sc,
	})
	cc, err := grpc.Dial(mr.Scheme()+":///", grpc.WithResolvers(mr), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.Dial() failed: %v", err)
	}
	defer cc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	testServiceClient := testpb.NewTestServiceClient(cc)

	// why rr over 01234 rpc's
	err = checkRoundRobinRPCs(ctx, testServiceClient, []resolver.Address{
		{ // maybe switch to return just a [] of address strings - that's how they're used anyway
			Addr: addresses[0], // deterministic since slice - first 3 ok, last 2 not
		},
		{
			Addr: addresses[1],
		},
		{
			Addr: addresses[2],
		},
		{
			Addr: addresses[3],
		},
		{
			Addr: addresses[4],
		},
	})
	if err != nil {
		t.Fatalf("error in expected round robin: %v", err)
	}

	// comment here why 012 rr
	err = checkRoundRobinRPCs(ctx, testServiceClient, []resolver.Address{
		{ // maybe switch to return just a [] of address strings - that's how they're used anyway
			Addr: addresses[0], // deterministic since slice - first 3 ok, last 2 not
		},
		{
			Addr: addresses[1],
		},
		{
			Addr: addresses[2],
		},
	})
	if err != nil {
		t.Fatalf("error in expected round robin: %v", err)
	}

	err = checkRoundRobinRPCs(ctx, testServiceClient, []resolver.Address{
		{ // maybe switch to return just a [] of address strings - that's how they're used anyway
			Addr: addresses[0], // deterministic since slice - first 3 ok, last 2 not
		},
		{
			Addr: addresses[1],
		},
		{
			Addr: addresses[2],
		},
		{
			Addr: addresses[3],
		},
		{
			Addr: addresses[4],
		},
	})
	if err != nil {
		t.Fatalf("error in expected round robin: %v", err)
	}
}

// TestFailurePercentageAlgorithm tests the FailurePercentageEjection
// functionality of the Outlier Detection Balancer. The balancer is configured
// with a FailurePercentageEjection algorithm, and connects to 5 upstreams. 2 of
// the upstreams are unhealthy. At some point in time, the Outlier Detection
// Balancer should only send RPC's in a Round Robin like fashion across the
// three healthy upstreams.
func (s) TestFailurePercentageAlgorithm(t *testing.T) {
	internal.RegisterOutlierDetectionBalancerForTesting()
	defer internal.UnregisterOutlierDetectionBalancerForTesting()
	// call setupBackends()
	addresses, backends := setupBackends(t) // perhaps this isn't getting closed
	defer func() {
		for _, backend := range backends {
			backend.Stop()
		}
	}()

	// configure resolver and top level lb policy on the client conn with the
	// manual resolver with config call
	mr := manual.NewBuilderWithScheme("od-e2e")
	defer mr.Close()

	// wait this is specific per test case, have setup take the json string as
	// parameter as a knob to the function, the function with the same functionality?

	/*outlierDetectionConfig := &LBConfig{
		ChildPolicy: /*round robin balancer as child,
	}*/

	// json = []byte(string), string wrapped in a byte array
	// this below is for success rate, make a var for success rate and failures percentage

	/*
	"successRateEjection": {
				"stdevFactor": 1900,
				"enforcementPercentage": 100,
				"minimumHosts": 5,
				"requestVolume": 100
			},
	*/

	// don't have access to od object so can't call the algo, top level balancer
	// of the Client Conn.

	// need to find a way to take away determinism with relation to the
	// configuration vs. the Round Robinness of verification.

	odServiceConfigJSON := `
{
  "loadBalancingConfig": [
    {
      "outlier_detection_experimental": {
        "interval": 50000000,
		"baseEjectionTime": 100000000,
		"maxEjectionTime": 300000000000,
		"maxEjectionPercent": 33,
		"failurePercentageEjection": {
			"threshold": 50,
			"enforcementPercentage": 100,
			"minimumHosts": 3,
			"requestVolume": 5
		},
        "childPolicy": [
		{
			"round_robin": {}
		}
		]
      }
    }
  ]
}`
	print("marshaling service config")
	// marshal to JSON...? does string = JSON i.e. byte[]
	sc := internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(odServiceConfigJSON)

	// internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(scJSON)

	// end result service config
	print("initial state: backends[0].Address: %v", backends[0])
	mr.InitialState(resolver.State{ // i.e. stub the resolver
		Addresses: []resolver.Address{
			{ // maybe switch to return just a [] of address strings - that's how they're used anyway
				Addr: addresses[0], // deterministic since map - first 3 ok, last 2 not
			},
			{
				Addr: addresses[1],
			},
			{
				Addr: addresses[2],
			},
			{
				Addr: addresses[3],
			},
			{
				Addr: addresses[4],
			},
		},
		ServiceConfig: sc/*outlier detection + round robin as a child this is end result how to prepare this?*/,
	}) // what is relation of service config to balancer config?

	// how does the list [] of length 5 fit into this puzzle after you configure this top level lb policy?

	// Also this stuff happens for each test, make this part of setup

	// one iteration of the list
	/*err := backends[0].StartClient()
	if err != nil {
		t.Fatalf("error starting client: %v", err)
	}
	backends[0].CC.*/ // no, it needs to have a single client conn load balanced accross multiple backends
	print("grpc dial")
	cc, err := grpc.Dial(mr.Scheme()+":///", grpc.WithResolvers(mr), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.Dial() failed: %v", err)
	}
	defer cc.Close()
	// wrap cc in the NewTestServiceClient
	// ss.Client = testpb.NewTestServiceClient(cc)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	testServiceClient := testpb.NewTestServiceClient(cc)
	// func CheckRoundRobinRPCs(ctx context.Context, client testgrpc.TestServiceClient, addrs []resolver.Address) error {
	// At first should rr across 12345, assert it does that
	print("before check round robin RPC's")
	err = checkRoundRobinRPCs(ctx, testServiceClient, []resolver.Address{
		{ // maybe switch to return just a [] of address strings - that's how they're used anyway
			Addr: addresses[0], // deterministic since slice - first 3 ok, last 2 not
		},
		{
			Addr: addresses[1],
		},
		{
			Addr: addresses[2],
		},
		{
			Addr: addresses[3],
		},
		{
			Addr: addresses[4],
		},
	})
	if err != nil {
		t.Fatalf("error in expected round robin: %v", err)
	}
	print("yo i finished")

	// Will ^^^ that assertion of rr accross 12345 guarantee to trigger vv? Honestly just get ^^^ working lol

	// Then assert it rr across 123

	// After calling the five upstreams, two of them constantly error
	// and should eventually be ejected for a period of time. This period of time
	// should cause the RPC's to be round robined only across the three that remain.

	// Maybe talk about race condition in this comment ^^^
	err = checkRoundRobinRPCs(ctx, testServiceClient, []resolver.Address{
		{ // maybe switch to return just a [] of address strings - that's how they're used anyway
			Addr: addresses[0], // deterministic since slice - first 3 ok, last 2 not
		},
		{
			Addr: addresses[1],
		},
		{
			Addr: addresses[2],
		},
	})
	if err != nil {
		t.Fatalf("error in expected round robin: %v", err)
	} // WOW THIS WORKS JUST SOMETHING ISN'T CLEANING UP CORRECTLY


	// After a few more intervals, the ejected addresses/upstreams should be
	// unejected due to the failure percentage algorithm.

	// assert rr across 5 backends - this doesn't work, how does it/how often
	// does it uneject addresses, I feel it should be covered by the context deadline

	// if too hard to triage why this doesn't work just get rid of this check?

	// noop config - first check it doesn't count and doesn't eject addresses
	// then check when you flip it and it starts counting, it actually ejects
	// similar to failure percentage.

	// Do I have a test for the unejection of addresses?

	err = checkRoundRobinRPCs(ctx, testServiceClient, []resolver.Address{
		{ // maybe switch to return just a [] of address strings - that's how they're used anyway
			Addr: addresses[0], // deterministic since slice - first 3 ok, last 2 not
		},
		{
			Addr: addresses[1],
		},
		{
			Addr: addresses[2],
		},
		{
			Addr: addresses[3],
		},
		{
			Addr: addresses[4],
		},
	})
	if err != nil {
		t.Fatalf("error in expected round robin: %v", err)
	}
} // THIS WORKED I'M CLOSE SHOULD BE ABLE TO FINISH THIS TODAY, NEED TO DO THIS FOR OTHER TWO (IDK HOW TO MAKE THIS NONDETERMINISTIC LOL HOPEFULLY IT'S NOT TOO BAD)
// WAIT BUT THIS STILL LEAKS LOL (doesn't clean up correctly?)

// Outlier Detection -> child Round Robin


// for noop config change in uus if too hard can make sure rr accross all 5 backends even with error sent from 2

// child sending redundant updates, and always wrap and forward that is fine if
// wanted to suppress that we would fix it in child

// don't send half conn picker

// as a result of a client conn state, half picker

// force update to happen at the end, always get an update, do it regardless
// always push it no matter what
// most recent noop config bit + picker update if there
// Always send

// when we get config you can read that and just send upward
// set bit at beginning (read update state)
// then config

// forward update at end if something sent

// if you need to update picker then you have to guarantee
// UpdateState (from config update), can't rely on update

// if call didn't happen you have to

// Any updates that might've come from child (inhibited) + require call at the end

// or do it with an async worker bool in a run()

// bool before UpdateClientConnState

// same bool in goroutine that is handling childs update

// Allow whatever happens naturally to happen, at the end if no UpdateState()
// call then call manually (maybe based on a || )

// RLS - single update channel, UCCS set field to inhibit picker update, push state updates
// to channel, push update state on channel

// push event to run() have that update state and set bool,
// send on channel

// assume UpdateClientConnState downward triggers UpdateState

// goroutine
// ------UpdateClientConnState() called

// just within the call, from any goroutine, not necessarily the
// the same one, can be from any goroutine

// just needs to happen before UpdateClientConnState() returns

// ------UpdateClientConnState() returns


/*
// TestUpdateClientConnStateDifferentType invokes the UpdateClientConnState
// method on the odBalancer with two different types and verifies that the child
// balancer is built and updated properly on the first, and the second update
// closes the child and builds a new one.
func (s) TestUpdateClientConnStateDifferentType(t *testing.T) {
	internal.RegisterOutlierDetectionBalancerForTesting()
	od, _ := setup(t)
	defer func() {
		od.Close() // this will leak a goroutine otherwise
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

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
				Name:   tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	ciChild := od.child.(*testClusterImplBalancer)
	// The child balancer should be created and forwarded the ClientConn update
	// from the first successful UpdateClientConnState call.
	if err := ciChild.waitForClientConnUpdate(ctx, balancer.ClientConnState{
		BalancerConfig: testClusterImplBalancerConfig{},
	}); err != nil {
		t.Fatalf("Error waiting for Client Conn update: %v", err)
	}

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
				Name:   verifyBalancerName,
				Config: verifyBalancerConfig{},
			},
		},
	})

	// Verify previous child balancer closed.
	if err := ciChild.waitForClose(ctx); err != nil { // When you switch this, graceful switch doesn't close it until it goes !READY
		t.Fatalf("Error waiting for Close() call on child balancer %v", err)
	}
}

// TestUpdateState tests that an UpdateState call gets forwarded to the
// ClientConn.
func (s) TestUpdateState(t *testing.T) {
	internal.RegisterOutlierDetectionBalancerForTesting()
	od, tcc := setup(t)
	defer func() {
		od.Close()
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

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
				Name:   tcibname,
				Config: testClusterImplBalancerConfig{},
			},
		},
	})
	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker:            &testutils.TestConstPicker{},
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
	internal.RegisterOutlierDetectionBalancerForTesting()
	defer func() {
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()
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
				Name:   tcibname,
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
	internal.RegisterOutlierDetectionBalancerForTesting()
	od, tcc := setup(t)
	defer func() {
		od.Close()
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

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
				Name:   tcibname,
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
		if err != nil {
			t.Fatalf("Picker.Pick should not have errored")
		}
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
		od.intervalTimerAlgorithm()
		// verify UpdateSubConnState() got called with TRANSIENT_FAILURE for
		// child with address that was ejected.
		if err := child.waitForSubConnUpdate(ctx, subConnWithState{
			sc:    scw2,
			state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure}, // Represents ejected
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}
	}

	// Update scw1 to another address that is currently ejected. This should
	// cause scw1 to get ejected.
	od.UpdateAddresses(scw1, []resolver.Address{
		{
			Addr: "address2",
		},
	})

	// verify that update addresses gets forwarded to ClientConn.
	select {
	case <-ctx.Done():
		t.Fatal("timeout while waiting for a UpdateState call on the ClientConn")
	case <-tcc.UpdateAddressesAddrsCh:
	}
	// verify scw1 got ejected (UpdateSubConnState called with TRANSIENT
	// FAILURE).
	if err := child.waitForSubConnUpdate(ctx, subConnWithState{
		sc:    scw1,
		state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure}, // Represents ejected
	}); err != nil {
		t.Fatalf("Error waiting for Sub Conn update: %v", err)
	}

	// Update scw1 to multiple addresses. This should cause scw1 to get
	// unejected, as is it no longer being tracked for Outlier Detection.
	od.UpdateAddresses(scw1, []resolver.Address{
		{
			Addr: "address1",
		},
		{
			Addr: "address2",
		},
	})
	// verify scw2 got unejected (UpdateSubConnState called with recent state).
	if err := child.waitForSubConnUpdate(ctx, subConnWithState{
		sc:    scw1,
		state: balancer.SubConnState{ConnectivityState: connectivity.Idle}, // If you uneject a SubConn that hasn't received a UpdateSubConnState, IDLE is recent state. This seems fine or is this wrong?
	}); err != nil {
		t.Fatalf("Error waiting for Sub Conn update: %v", err)
	}

	// Update scw1 to a different multiple addresses list. A change of addresses
	// in which the plurality goes from multiple to multiple should be a no-op,
	// as the address continues to be ignored by outlier detection.
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

	// Update scw1 back to a single address, which is ejected. This should cause
	// the SubConn to be re-ejected.

	od.UpdateAddresses(scw1, []resolver.Address{
		{
			Addr: "address2",
		},
	})
	// verify scw1 got ejected (UpdateSubConnState called with TRANSIENT FAILURE).
	if err := child.waitForSubConnUpdate(ctx, subConnWithState{
		sc:    scw1,
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
	internal.RegisterOutlierDetectionBalancerForTesting()
	od, tcc := setup(t)
	defer func() {
		od.Close()
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

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
				Name:   tcibname,
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

	scw, err := od.NewSubConn([]resolver.Address{
		{
			Addr: "address1",
		},
	}, balancer.NewSubConnOptions{})

	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

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
		addrInfo, ok := od.addrs["address1"]
		if !ok {
			t.Fatal("map entry for address: address1 not present in map")
		}
		bucketWant := &bucket{
			numSuccesses: 1,
			numFailures:  1,
		}
		if diff := cmp.Diff((*bucket)(addrInfo.callCounter.activeBucket), bucketWant); diff != "" { // no need for atomic read because not concurrent with Done() call from picker
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
				Name:   tcibname,
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
	// case is ready.
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
		addrInfo, ok := od.addrs["address1"]
		if !ok {
			t.Fatal("map entry for address: address1 not present in map")
		}

		// The active bucket should be the same as before the no-op
		// configuration came in because the interval timer algorithm didn't run
		// in between ClientConn updates and the picker should not count, as the
		// outlier detection balancer is configured with a no-op configuration.
		bucketWant := &bucket{
			numSuccesses: 1,
			numFailures:  1,
		}
		if diff := cmp.Diff((*bucket)(addrInfo.callCounter.activeBucket), bucketWant); diff != "" { // no need for atomic read because not concurrent with Done() call
			od.mu.Unlock()
			t.Fatalf("callCounter is different than expected, diff (-got +want): %v", diff)
		}
		od.mu.Unlock()
	}

	// UpdateState with a connecting state. This new most recent connectivity
	// state should be forwarded to the Client Conn, alongside the most recent
	// noop config bit which is true.
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
		addrInfo, ok := od.addrs["address1"]
		if !ok {
			t.Fatal("map entry for address: address1 not present in map")
		}

		// The active bucket should be the same as before the no-op
		// configuration came in because the interval timer algorithm didn't run
		// in between ClientConn updates and the picker should not count, as the
		// outlier detection balancer is configured with a no-op configuration.
		bucketWant := &bucket{
			numSuccesses: 1,
			numFailures:  1,
		}
		if diff := cmp.Diff((*bucket)(addrInfo.callCounter.activeBucket), bucketWant); diff != "" { // no need for atomic read because not concurrent with Done() call
			t.Fatalf("callCounter is different than expected, diff (-got +want): %v", diff)
		}
		od.mu.Unlock()
	}
}

type rrPicker struct {
	scs  []balancer.SubConn
	next int
}

func (rrp *rrPicker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	sc := rrp.scs[rrp.next]
	rrp.next = (rrp.next + 1) % len(rrp.scs)
	return balancer.PickResult{SubConn: sc}, nil
}

// TestEjectUnejectSuccessRate tests the functionality of the interval timer
// algorithm of ejecting/unejecting SubConns when configured with
// SuccessRateEjection. It also tests a desired invariant of a SubConnWrapper
// being ejected or unejected, which is to either forward or not forward SubConn
// updates from grpc.
func (s) TestEjectUnejectSuccessRate(t *testing.T) {
	internal.RegisterOutlierDetectionBalancerForTesting()
	// Setup the outlier detection balancer to a point where it will be in a
	// situation to potentially eject addresses.
	od, tcc := setup(t)
	defer func() {
		od.Close()
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

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
				Name:   tcibname,
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
		// Set each of the three upstream addresses to have five successes each.
		// This should cause none of the addresses to be ejected as none of them
		// are outliers according to the success rate algorithm.
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
			sc:    scw1,
			state: balancer.SubConnState{ConnectivityState: connectivity.Connecting},
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}

		// Set two of the upstream addresses to have five successes each, and
		// one of the upstream addresses to have five failures. This should
		// cause the address which has five failures to be ejected according the
		// SuccessRateAlgorithm.
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
			sc:    scw3,
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
		od.UpdateSubConnState(pi.SubConn, balancer.SubConnState{
			ConnectivityState: connectivity.Connecting,
		})
		if err := child.waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("SubConn update should not have been forwarded (the SubConn is ejected)")
		}

		// Override now to cause the interval timer algorithm to always uneject a SubConn.
		defer func(n func() time.Time) {
			now = n
		}(now)

		now = func() time.Time {
			return time.Now().Add(time.Second * 1000) // will cause to always uneject addresses which are ejected
		}
		od.intervalTimerAlgorithm()

		// unejected SubConn should report latest persisted state - which is
		// connecting from earlier.
		if err := child.waitForSubConnUpdate(ctx, subConnWithState{
			sc:    scw3,
			state: balancer.SubConnState{ConnectivityState: connectivity.Connecting},
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}
	}
}

// TestEjectUnejectSuccessRateFromNoopConfig tests that any ejected Addresses
// are unejected upon the receipt of a no-op Outlier Detection Configuration.
func (s) TestEjectUnejectSuccessRateFromNoopConfig(t *testing.T) {
	internal.RegisterOutlierDetectionBalancerForTesting()
	// Setup the outlier detection balancer to a point where it will be in a
	// situation to potentially eject addresses.
	od, tcc := setup(t)
	defer func() {
		od.Close()
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

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
				Name:   tcibname,
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
		// Set two of the upstream addresses to have five successes each, and
		// one of the upstream addresses to have five failures. This should
		// cause the address which has five failures to be ejected according the
		// SuccessRateAlgorithm.
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
			sc:    scw3,
			state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure}, // Represents ejected
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}
		// Only one address should be ejected.
		sCtx, sCancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer sCancel()
		if err := child.waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("Only one SubConn update should have been sent (only one SubConn got ejected)")
		}
		// Now that an address is ejected, SubConn updates for SubConns using
		// that address should not be forwarded downward. These SubConn updates
		// will be cached to update the child sometime in the future when the
		// address gets unejected.
		od.UpdateSubConnState(pi.SubConn, balancer.SubConnState{
			ConnectivityState: connectivity.Connecting,
		})
		if err := child.waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("SubConn update should not have been forwarded (the SubConn is ejected)")
		}
		// Update the Outlier Detection Balancer with a no-op configuration.
		// This should cause any ejected addresses to become unejected.
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
				Interval: 1<<63 - 1, // so the interval will never run unless called manually in test.
				ChildPolicy: &internalserviceconfig.BalancerConfig{
					Name:   tcibname,
					Config: testClusterImplBalancerConfig{},
				},
			},
		})
		// unejected SubConn should report latest persisted state - which is
		// connecting from earlier.
		if err := child.waitForSubConnUpdate(ctx, subConnWithState{
			sc:    scw3,
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
	internal.RegisterOutlierDetectionBalancerForTesting()
	od, tcc := setup(t)
	defer func() {
		internal.UnregisterOutlierDetectionBalancerForTesting()
		defer od.Close()
	}()

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
				Threshold:             50,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name:   tcibname,
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
		// Set each upstream address to have five successes each. This should
		// cause none of the addresses to be ejected as none of them are below
		// the failure percentage threshold.
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

		sCtx, cancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
		defer cancel()
		if err := child.waitForSubConnUpdate(sCtx, subConnWithState{}); err == nil {
			t.Fatalf("no SubConn update should have been sent (no SubConn got ejected)")
		}

		// Set two upstream addresses to have five successes each, and one
		// upstream address to have five failures. This should cause the address
		// with five failures to be ejected according to the Failure Percentage
		// Algorithm.
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

		// verify UpdateSubConnState() got called with TRANSIENT_FAILURE for
		// child in address that was ejected.
		if err := child.waitForSubConnUpdate(ctx, subConnWithState{
			sc:    scw3,
			state: balancer.SubConnState{ConnectivityState: connectivity.TransientFailure}, // Represents ejected
		}); err != nil {
			t.Fatalf("Error waiting for Sub Conn update: %v", err)
		}

		// verify only one address got ejected
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
// whatever interval is configured minus the difference between the current time
// and the previous start timestamp. For a no-op configuration, the timer should
// not be configured at all.
func (s) TestDurationOfInterval(t *testing.T) {
	internal.RegisterOutlierDetectionBalancerForTesting()
	od, _ := setup(t)
	defer func(af func(d time.Duration, f func()) *time.Timer) {
		od.Close()
		afterFunc = af
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}(afterFunc)

	durationChan := testutils.NewChannel()
	afterFunc = func(dur time.Duration, _ func()) *time.Timer {
		durationChan.Send(dur)
		return time.NewTimer(1<<63 - 1)
	}

	od.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			Interval:           8 * time.Second,
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
				Name:   tcibname,
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

	defer func(n func() time.Time) {
		now = n
	}(now)
	now = func() time.Time {
		return time.Now().Add(time.Second * 5)
	}

	// UpdateClientConnState with an interval of 9 seconds. Due to 5 seconds
	// already passing (from overridden time.Now function), this should start an
	// interval timer of ~4 seconds.
	od.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			Interval:           9 * time.Second,
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
				Name:   tcibname,
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
				Name:   tcibname,
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
	internal.RegisterOutlierDetectionBalancerForTesting()
	od, tcc := setup(t)
	defer func() {
		defer od.Close()
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

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
			SuccessRateEjection: &SuccessRateEjection{ // Have both Success Rate and Failure Percentage to step through all the interval timer code
				StdevFactor:           500,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			FailurePercentageEjection: &FailurePercentageEjection{
				Threshold:             50,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name:   tcibname,
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
	if err != nil {
		t.Fatalf("error in od.NewSubConn call: %v", err)
	}

	od.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker: &rrPicker{
			scs: []balancer.SubConn{scw1, scw2, scw3},
		},
	})

	var picker balancer.Picker
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case picker = <-tcc.NewPickerCh:
	}

	// Spawn a goroutine that constantly picks and invokes the Done callback
	// counting for successful and failing RPC's.
	finished := make(chan struct{})
	go func() {
		// constantly update the picker to test for no race conditions causing
		// corrupted memory (have concurrent pointer reads/writes).
		for {
			select {
			case <-finished:
				return
			default:
			}
			pi, err := picker.Pick(balancer.PickInfo{})
			if err != nil {
				continue
			}
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
			time.Sleep(1 * time.Nanosecond)
		}
	}()

	od.intervalTimerAlgorithm() // causes two swaps on the callCounter
	od.intervalTimerAlgorithm()
	close(finished)
}

// TestConcurrentOperations calls different operations on the balancer in
// separate goroutines to test for any race conditions and deadlocks. It also
// uses a child balancer which verifies that no operations on the child get
// called after the child balancer is closed.
func (s) TestConcurrentOperations(t *testing.T) {
	internal.RegisterOutlierDetectionBalancerForTesting()
	od, tcc := setup(t)
	defer func() {
		od.Close()
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

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
			SuccessRateEjection: &SuccessRateEjection{ // Have both Success Rate and Failure Percentage to step through all the interval timer code
				StdevFactor:           500,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			FailurePercentageEjection: &FailurePercentageEjection{
				Threshold:             50,
				EnforcementPercentage: 100,
				MinimumHosts:          3,
				RequestVolume:         3,
			},
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name:   verifyBalancerName,
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
	})

	var picker balancer.Picker
	select {
	case <-ctx.Done():
		t.Fatalf("timeout while waiting for a UpdateState call on the ClientConn")
	case picker = <-tcc.NewPickerCh:
	}

	finished := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-finished:
				return
			default:
			}
			pi, err := picker.Pick(balancer.PickInfo{})
			if err != nil {
				continue
			}
			pi.Done(balancer.DoneInfo{})
			pi.Done(balancer.DoneInfo{Err: errors.New("some error")})
			time.Sleep(1 * time.Nanosecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
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
	wg.Add(1)
	go func() {
		defer wg.Done()
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		od.NewSubConn([]resolver.Address{{Addr: "address4"}}, balancer.NewSubConnOptions{})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		od.RemoveSubConn(scw1)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
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
				Name:   verifyBalancerName,
				Config: verifyBalancerConfig{},
			},
		},
	})

	// Call balancer.Balancers synchronously in this goroutine, upholding the
	// balancer.Balancer API guarantee.
	od.UpdateSubConnState(scw1.(*subConnWrapper).SubConn, balancer.SubConnState{
		ConnectivityState: connectivity.Connecting,
	})
	od.ResolverError(errors.New("some error"))
	od.ExitIdle()
	od.Close()
	close(finished)
	wg.Wait()
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

// TestUpdateClientConnStateSinglePickerUpdate tests that on an
// UpdateClientConnState call on the Outlier Detection Balancer, only a single
// picker update is sent back.
func (s) TestUpdateClientConnStateSinglePickerUpdate(t *testing.T) {
	internal.RegisterOutlierDetectionBalancerForTesting()
	od, tcc := setup(t)
	defer func() {
		od.Close()
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()
	od.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			Interval: 10 * time.Second,
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name:   twoUpdateStateBalancerName,
				Config: twoUpdateStateBalancerConfig{},
			},
		},
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
	case <-tcc.NewPickerCh:
	}

	sCtx, sCancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer sCancel()

	// Should only send one update.
	select {
	case <-tcc.NewStateCh:
		t.Fatalf("only one picker update should have gotten sent")
	case <-sCtx.Done():
	}

	sCtx, sCancel = context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer sCancel()
	select {
	case <-tcc.NewStateCh:
		t.Fatalf("only one picker update should have gotten sent")
	case <-sCtx.Done():
	}
}

type twoUpdateStateBalancerBuilder struct{}

func (twoUpdateStateBalancerBuilder) Build(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
	return &twoUpdateStateBalancer{
		cc: cc,
	}
}

func (twoUpdateStateBalancerBuilder) Name() string {
	return "twoUpdateStateBalancer"
}

type twoUpdateStateBalancerConfig struct {
	serviceconfig.LoadBalancingConfig
}

// twoUpdateStateBalancer sends two UpdateState calls inline in
// UpdateClientConnState(). This helps to verify that only a single picker
// update gets sent upward as a result of the call.
type twoUpdateStateBalancer struct {
	t  *testing.T
	cc balancer.ClientConn
}

func (tusb *twoUpdateStateBalancer) UpdateClientConnState(ccs balancer.ClientConnState) error {
	tusb.cc.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker:            &testutils.TestConstPicker{},
	})
	tusb.cc.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker:            &testutils.TestConstPicker{},
	})
	return nil
}

func (tusb *twoUpdateStateBalancer) ResolverError(err error) {}

func (tusb *twoUpdateStateBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
}

func (tusb *twoUpdateStateBalancer) Close() {}
*/