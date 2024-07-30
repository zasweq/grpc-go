/*
 *
 * Copyright 2023 gRPC authors.
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

package weightedroundrobin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils/roundrobin"
	"google.golang.org/grpc/internal/testutils/stats"
	"google.golang.org/grpc/orca"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/resolver"

	wrr "google.golang.org/grpc/balancer/weightedroundrobin"
	iwrr "google.golang.org/grpc/balancer/weightedroundrobin/internal"

	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

const defaultTestTimeout = 10 * time.Second
const weightUpdatePeriod = 50 * time.Millisecond
const weightExpirationPeriod = time.Minute
const oobReportingInterval = 10 * time.Millisecond

func init() {
	iwrr.AllowAnyWeightUpdatePeriod = true
}

func boolp(b bool) *bool          { return &b }
func float64p(f float64) *float64 { return &f }
func stringp(s string) *string    { return &s }

var (
	perCallConfig = iwrr.LBConfig{
		EnableOOBLoadReport:     boolp(false),
		OOBReportingPeriod:      stringp("0.005s"),
		BlackoutPeriod:          stringp("0s"),
		WeightExpirationPeriod:  stringp("60s"),
		WeightUpdatePeriod:      stringp(".050s"),
		ErrorUtilizationPenalty: float64p(0),
	}
	oobConfig = iwrr.LBConfig{
		EnableOOBLoadReport:     boolp(true),
		OOBReportingPeriod:      stringp("0.005s"),
		BlackoutPeriod:          stringp("0s"), // does this even create the possibility of it being within blackout period...
		WeightExpirationPeriod:  stringp("60s"),
		WeightUpdatePeriod:      stringp(".050s"),
		ErrorUtilizationPenalty: float64p(0), // this makes it simply qps/utilization...
	}
	// config here for metrics...how to get per call I guess
	// do it through same mechanism...need to make an RPC I guess?

	// weight will be 0 if in blackout period...
	testMetricsConfig = iwrr.LBConfig{
		EnableOOBLoadReport:     boolp(false), // weights come in per request...
		OOBReportingPeriod:      stringp("0.005s"), // ignored
		BlackoutPeriod:          stringp("0s"), // need to make this non zero if you want to test third metric...
		WeightExpirationPeriod:  stringp("60s"), // make this hittable by test...
		WeightUpdatePeriod:      stringp(".050s"), // how often the scheduler runs...non deterministic...
		ErrorUtilizationPenalty: float64p(0), // makes weight qps/utilization...
	}

	// Even if blackout/weight expiration don't hit...
	// can still have deterministic metrics test, just expect 0 for some stuff...


)

type testServer struct {
	*stubserver.StubServer

	oobMetrics  orca.ServerMetricsRecorder // Attached to the OOB stream.
	callMetrics orca.CallMetricsRecorder   // Attached to per-call metrics.
}

type reportType int

const (
	reportNone reportType = iota
	reportOOB
	reportCall
	reportBoth
)

func startServer(t *testing.T, r reportType) *testServer {
	t.Helper()

	smr := orca.NewServerMetricsRecorder()
	cmr := orca.NewServerMetricsRecorder().(orca.CallMetricsRecorder)

	ss := &stubserver.StubServer{
		EmptyCallF: func(ctx context.Context, in *testpb.Empty) (*testpb.Empty, error) {
			if r := orca.CallMetricsRecorderFromContext(ctx); r != nil {
				// Copy metrics from what the test set in cmr into r.
				sm := cmr.(orca.ServerMetricsProvider).ServerMetrics()
				r.SetApplicationUtilization(sm.AppUtilization)
				r.SetQPS(sm.QPS)
				r.SetEPS(sm.EPS)
			}
			return &testpb.Empty{}, nil
		},
	}

	var sopts []grpc.ServerOption
	if r == reportCall || r == reportBoth {
		sopts = append(sopts, orca.CallMetricsServerOption(nil))
	}

	if r == reportOOB || r == reportBoth {
		oso := orca.ServiceOptions{
			ServerMetricsProvider: smr,
			MinReportingInterval:  10 * time.Millisecond,
		}
		internal.ORCAAllowAnyMinReportingInterval.(func(so *orca.ServiceOptions))(&oso)
		sopts = append(sopts, stubserver.RegisterServiceServerOption(func(s *grpc.Server) {
			if err := orca.Register(s, oso); err != nil {
				t.Fatalf("Failed to register orca service: %v", err)
			}
		}))
	}

	if err := ss.StartServer(sopts...); err != nil {
		t.Fatalf("Error starting server: %v", err)
	}
	t.Cleanup(ss.Stop)

	return &testServer{
		StubServer:  ss,
		oobMetrics:  smr,
		callMetrics: cmr,
	}
}

func svcConfig(t *testing.T, wrrCfg iwrr.LBConfig) string {
	t.Helper()
	m, err := json.Marshal(wrrCfg)
	if err != nil {
		t.Fatalf("Error marshaling JSON %v: %v", wrrCfg, err)
	}
	sc := fmt.Sprintf(`{"loadBalancingConfig": [ {%q:%v} ] }`, wrr.Name, string(m))
	t.Logf("Marshaled service config: %v", sc)
	return sc
}


// Scale a lot of these basic smoke tests up

// Tests basic functionality with one address.  With only one address, load
// reporting doesn't affect routing at all.
func (s) TestBalancer_OneAddress(t *testing.T) {
	testCases := []struct {
		rt  reportType
		cfg iwrr.LBConfig
	}{
		{rt: reportNone, cfg: perCallConfig},
		{rt: reportCall, cfg: perCallConfig},
		{rt: reportOOB, cfg: oobConfig},
	}

	// Numerous ones of these t-tests...
	// Could test for each one, I think deterministic in emissions...

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("reportType:%v", tc.rt), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
			defer cancel()

			srv := startServer(t, tc.rt)

			sc := svcConfig(t, tc.cfg)
			if err := srv.StartClient(grpc.WithDefaultServiceConfig(sc)/*scale basic smoke test here up with fake stats handler...*/); err != nil {
				t.Fatalf("Error starting client: %v", err)
			}

			// what? I think spams a bunch of weights...

			// but should never not be usable? But that emits 0 so maybe just check weight function...

			// Updates weights around 10 times...argh that is the scheduler update...so creates non determinism...


			// Perform many RPCs to ensure the LB policy works with 1 address.
			for i := 0; i < 100; i++ {
				srv.callMetrics.SetQPS(float64(i))
				srv.oobMetrics.SetQPS(float64(i))
				if _, err := srv.Client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
					t.Fatalf("Error from EmptyCall: %v", err)
				}
				time.Sleep(time.Millisecond) // Delay; test will run 100ms and should perform ~10 weight updates
			}
		})
	}
}

// For other tests say what metrics are being tested/expected emissions...

// TestWRRMetricsBasic tests metrics emitted from the WRR balancer. It
// configures a weighted round robin balancer as the top level balancer of a
// ClientConn, and configures a fake stats handler on the ClientConn to receive
// metrics. It verifies stats emitted from the Weighted Round Robin Balancer on
// balancer startup case and from the first scheduler update? are as expected.


// Need a client to provide mr, unless you create mr and pass the mr to the helper...
// and client comes coupled with WRR operations you need like weights etc.

func (s) TestWRRMetricsBasic(t *testing.T) {

	// all of these come out *every scheduler update*, which after the first one
	// is off nondeterministic time.Sleeps...but can't trigger expired unless time.Sleeps so poll for those...

	// Could do this orthogonal to picker update: (or call weight directly? with a smoke test)

	// 4 metrics:
	// fallback (in scheduler): (if one sc, this hits immediately, also need to induce when more than one, but not enough weights, I think that needs eventual consistency...)

	// 3 weight: (in weight)
	// 0 because in blackout (blackout in config)
	// 0 because weight expired (this is determined by config as well...)
	// the weight itself...float


	// Target whatever is passed into balancer...rn he has cc take care of it and have the layering work that way...
	// One sc, one scheduler update from it being built out
	// fallback 1
	// one blackout




	// Next scheduler update (how to trigger deterministicallly...)...




	// No matter whether picker or not if not invoke weight directly...
	// will either need oob or per request infra to invoke weights on scs...

	// per request seems more deterministic...triggered by request
	// knobs are how often sched reupdates,
	// blackout period,
	// stale period...


	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	srv := startServer(t, reportCall)
	sc := svcConfig(t, testMetricsConfig) // what configures how many SubConns are part of system?

	// create a tmr here with all of the metrics enabled...
	mr := stats.NewTestMetricsRecorder(t, []string{"grpc.lb.wrr.rr_fallback", "grpc.lb.wrr.endpoint_weight_not_yet_usable", "grpc.lb.wrr.endpoint_weight_stale", "grpc.lb.wrr.endpoint_weights"})
	if err := srv.StartClient(grpc.WithDefaultServiceConfig(sc), grpc.WithStatsHandler(mr)/*scale basic smoke test here up with fake stats handler...*/); err != nil {
		t.Fatalf("Error starting client: %v", err)
	}
	srv.callMetrics.SetQPS(float64(1)) // or do this before...how does this affect the algorithm?

	if _, err := srv.Client.EmptyCall(ctx, &testpb.Empty{}); err != nil {
		t.Fatalf("Error from EmptyCall: %v", err)
	}

	// And expect 0 for some...albiet deterministically...doesn't hit another scheduler update so ok here...
	// Perhaps write why I expect 0 for some...
	mr.AssertDataForMetric("grpc.lb.wrr.rr_fallback", 1) // This should be 1 here, and 0 above since this hits from rrFallbackHandle...

	// This setNow and time.Sleep operation cause weights to expire - one or both?
	// I think both expire - just check this one metric and see if it works...
	// test all metrics in each function or just one per for specificity...?
	mr.AssertDataForMetric("grpc.lb.wrr.endpoint_weight_stale", 1) // How does this have a stale endpoint...there's one addr?

	// Endpoint weight not yet usable hits above and doesn't change? How to have assertions at each step?
	// Maybe a helper that takes 4 expected...how to do this for histos?
	mr.AssertDataForMetric("grpc.lb.wrr.endpoint_weight_not_yet_usable", 0) // Distinguish between 0 and unset? Yeah the issue is this passes even though this never got emitted...

	// How do you assert histos? I think this goes 0 0 now, but earlier it actaully emits the weights which I think are in a 10:1 ratio...scale up RLS unit tests too?
	// Base usage of histos off channels?

	mr.AssertDataForMetric("grpc.lb.wrr.endpoint_weights", 0/*Assertion comes after rounding etc...use the distribution to see what implies this...*/)
	// Doug will respond if this is expected ^^^, could refactor these 4 checks into a helper (and cleanup)


	// Deterministic time.Sleep if I want to retrigger scheduler update running...

	// poll here...

} // no need to mess with registred instruments since these registered instruments should always be present if balancer is imported, tests should work on registered instruments on top of these...

// Orthogonally could work on e2e test...

// Tests two addresses with ORCA reporting disabled (should fall back to pure
// RR).
func (s) TestBalancer_TwoAddresses_ReportingDisabled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	srv1 := startServer(t, reportNone)
	srv2 := startServer(t, reportNone)

	sc := svcConfig(t, perCallConfig)
	if err := srv1.StartClient(grpc.WithDefaultServiceConfig(sc)); err != nil {
		t.Fatalf("Error starting client: %v", err)
	}
	addrs := []resolver.Address{{Addr: srv1.Address}, {Addr: srv2.Address}}
	srv1.R.UpdateState(resolver.State{Addresses: addrs})

	// Perform many RPCs to ensure the LB policy works with 2 addresses.
	for i := 0; i < 20; i++ {
		roundrobin.CheckRoundRobinRPCs(ctx, srv1.Client, addrs)
	}
}

// Tests two addresses with per-call ORCA reporting enabled.  Checks the
// backends are called in the appropriate ratios.
func (s) TestBalancer_TwoAddresses_ReportingEnabledPerCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	srv1 := startServer(t, reportCall)
	srv2 := startServer(t, reportCall)

	// srv1 starts loaded and srv2 starts without load; ensure RPCs are routed
	// disproportionately to srv2 (10:1).
	srv1.callMetrics.SetQPS(10.0)
	srv1.callMetrics.SetApplicationUtilization(1.0)

	srv2.callMetrics.SetQPS(10.0)
	srv2.callMetrics.SetApplicationUtilization(.1)

	sc := svcConfig(t, perCallConfig)
	if err := srv1.StartClient(grpc.WithDefaultServiceConfig(sc)); err != nil {
		t.Fatalf("Error starting client: %v", err)
	}
	addrs := []resolver.Address{{Addr: srv1.Address}, {Addr: srv2.Address}}
	srv1.R.UpdateState(resolver.State{Addresses: addrs})

	// Call each backend once to ensure the weights have been received.
	ensureReached(ctx, t, srv1.Client, 2)

	// Wait for the weight update period to allow the new weights to be processed.
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 10})
}

// Tests two addresses with OOB ORCA reporting enabled.  Checks the backends
// are called in the appropriate ratios.
func (s) TestBalancer_TwoAddresses_ReportingEnabledOOB(t *testing.T) {
	testCases := []struct {
		name       string
		utilSetter func(orca.ServerMetricsRecorder, float64)
	}{{
		name: "application_utilization",
		utilSetter: func(smr orca.ServerMetricsRecorder, val float64) {
			smr.SetApplicationUtilization(val)
		},
	}, {
		name: "cpu_utilization",
		utilSetter: func(smr orca.ServerMetricsRecorder, val float64) {
			smr.SetCPUUtilization(val)
		},
	}, {
		name: "application over cpu",
		utilSetter: func(smr orca.ServerMetricsRecorder, val float64) {
			smr.SetApplicationUtilization(val)
			smr.SetCPUUtilization(2.0) // ignored because ApplicationUtilization is set
		},
	}}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
			defer cancel()

			srv1 := startServer(t, reportOOB)
			srv2 := startServer(t, reportOOB)

			// srv1 starts loaded and srv2 starts without load; ensure RPCs are routed
			// disproportionately to srv2 (10:1).
			srv1.oobMetrics.SetQPS(10.0)
			tc.utilSetter(srv1.oobMetrics, 1.0)

			srv2.oobMetrics.SetQPS(10.0)
			tc.utilSetter(srv2.oobMetrics, 0.1)

			sc := svcConfig(t, oobConfig)
			if err := srv1.StartClient(grpc.WithDefaultServiceConfig(sc)); err != nil {
				t.Fatalf("Error starting client: %v", err)
			}
			addrs := []resolver.Address{{Addr: srv1.Address}, {Addr: srv2.Address}}
			srv1.R.UpdateState(resolver.State{Addresses: addrs})

			// Call each backend once to ensure the weights have been received.
			ensureReached(ctx, t, srv1.Client, 2)

			// Wait for the weight update period to allow the new weights to be processed.
			time.Sleep(weightUpdatePeriod)
			checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 10})
		})
	}
}

// Tests two addresses with OOB ORCA reporting enabled, where the reports
// change over time.  Checks the backends are called in the appropriate ratios
// before and after modifying the reports.
func (s) TestBalancer_TwoAddresses_UpdateLoads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	srv1 := startServer(t, reportOOB)
	srv2 := startServer(t, reportOOB)

	// srv1 starts loaded and srv2 starts without load; ensure RPCs are routed
	// disproportionately to srv2 (10:1).
	srv1.oobMetrics.SetQPS(10.0)
	srv1.oobMetrics.SetApplicationUtilization(1.0)

	srv2.oobMetrics.SetQPS(10.0)
	srv2.oobMetrics.SetApplicationUtilization(.1)

	sc := svcConfig(t, oobConfig)
	if err := srv1.StartClient(grpc.WithDefaultServiceConfig(sc)); err != nil {
		t.Fatalf("Error starting client: %v", err)
	}
	addrs := []resolver.Address{{Addr: srv1.Address}, {Addr: srv2.Address}}
	srv1.R.UpdateState(resolver.State{Addresses: addrs})

	// Call each backend once to ensure the weights have been received.
	ensureReached(ctx, t, srv1.Client, 2)

	// Wait for the weight update period to allow the new weights to be processed.
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 10})

	// Update the loads so srv2 is loaded and srv1 is not; ensure RPCs are
	// routed disproportionately to srv1.
	srv1.oobMetrics.SetQPS(10.0)
	srv1.oobMetrics.SetApplicationUtilization(.1)

	srv2.oobMetrics.SetQPS(10.0)
	srv2.oobMetrics.SetApplicationUtilization(1.0)

	// Wait for the weight update period to allow the new weights to be processed.
	time.Sleep(weightUpdatePeriod + oobReportingInterval)
	checkWeights(ctx, t, srvWeight{srv1, 10}, srvWeight{srv2, 1})
}

// Tests two addresses with OOB ORCA reporting enabled, then with switching to
// per-call reporting.  Checks the backends are called in the appropriate
// ratios before and after the change.
func (s) TestBalancer_TwoAddresses_OOBThenPerCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	srv1 := startServer(t, reportBoth)
	srv2 := startServer(t, reportBoth)

	// srv1 starts loaded and srv2 starts without load; ensure RPCs are routed
	// disproportionately to srv2 (10:1).
	srv1.oobMetrics.SetQPS(10.0)
	srv1.oobMetrics.SetApplicationUtilization(1.0)

	srv2.oobMetrics.SetQPS(10.0)
	srv2.oobMetrics.SetApplicationUtilization(.1)

	// For per-call metrics (not used initially), srv2 reports that it is
	// loaded and srv1 reports low load.  After confirming OOB works, switch to
	// per-call and confirm the new routing weights are applied.
	srv1.callMetrics.SetQPS(10.0)
	srv1.callMetrics.SetApplicationUtilization(.1)

	srv2.callMetrics.SetQPS(10.0)
	srv2.callMetrics.SetApplicationUtilization(1.0)

	sc := svcConfig(t, oobConfig)
	if err := srv1.StartClient(grpc.WithDefaultServiceConfig(sc)); err != nil {
		t.Fatalf("Error starting client: %v", err)
	}
	addrs := []resolver.Address{{Addr: srv1.Address}, {Addr: srv2.Address}}
	srv1.R.UpdateState(resolver.State{Addresses: addrs})

	// Call each backend once to ensure the weights have been received.
	ensureReached(ctx, t, srv1.Client, 2)

	// Wait for the weight update period to allow the new weights to be processed.
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 10})

	// Update to per-call weights.
	c := svcConfig(t, perCallConfig)
	parsedCfg := srv1.R.CC.ParseServiceConfig(c)
	if parsedCfg.Err != nil {
		panic(fmt.Sprintf("Error parsing config %q: %v", c, parsedCfg.Err))
	}
	srv1.R.UpdateState(resolver.State{Addresses: addrs, ServiceConfig: parsedCfg})

	// Wait for the weight update period to allow the new weights to be processed.
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv1, 10}, srvWeight{srv2, 1})
}

// Tests two addresses with OOB ORCA reporting enabled and a non-zero error
// penalty applied.
func (s) TestBalancer_TwoAddresses_ErrorPenalty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	srv1 := startServer(t, reportOOB)
	srv2 := startServer(t, reportOOB)

	// srv1 starts loaded and srv2 starts without load; ensure RPCs are routed
	// disproportionately to srv2 (10:1).  EPS values are set (but ignored
	// initially due to ErrorUtilizationPenalty=0).  Later EUP will be updated
	// to 0.9 which will cause the weights to be equal and RPCs to be routed
	// 50/50.
	srv1.oobMetrics.SetQPS(10.0)
	srv1.oobMetrics.SetApplicationUtilization(1.0)
	srv1.oobMetrics.SetEPS(0)
	// srv1 weight before: 10.0 / 1.0 = 10.0
	// srv1 weight after:  10.0 / 1.0 = 10.0

	srv2.oobMetrics.SetQPS(10.0)
	srv2.oobMetrics.SetApplicationUtilization(.1)
	srv2.oobMetrics.SetEPS(10.0)
	// srv2 weight before: 10.0 / 0.1 = 100.0
	// srv2 weight after:  10.0 / 1.0 = 10.0

	sc := svcConfig(t, oobConfig)
	if err := srv1.StartClient(grpc.WithDefaultServiceConfig(sc)); err != nil {
		t.Fatalf("Error starting client: %v", err)
	}
	addrs := []resolver.Address{{Addr: srv1.Address}, {Addr: srv2.Address}}
	srv1.R.UpdateState(resolver.State{Addresses: addrs})

	// Call each backend once to ensure the weights have been received.
	ensureReached(ctx, t, srv1.Client, 2)

	// Wait for the weight update period to allow the new weights to be processed.
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 10})

	// Update to include an error penalty in the weights.
	newCfg := oobConfig
	newCfg.ErrorUtilizationPenalty = float64p(0.9)
	c := svcConfig(t, newCfg)
	parsedCfg := srv1.R.CC.ParseServiceConfig(c)
	if parsedCfg.Err != nil {
		panic(fmt.Sprintf("Error parsing config %q: %v", c, parsedCfg.Err))
	}
	srv1.R.UpdateState(resolver.State{Addresses: addrs, ServiceConfig: parsedCfg})

	// Wait for the weight update period to allow the new weights to be processed.
	time.Sleep(weightUpdatePeriod + oobReportingInterval)
	checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 1})
}

// Tests that the blackout period causes backends to use 0 as their weight
// (meaning to use the average weight) until the blackout period elapses.
func (s) TestBalancer_TwoAddresses_BlackoutPeriod(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	var mu sync.Mutex
	start := time.Now()
	now := start
	setNow := func(t time.Time) {
		mu.Lock()
		defer mu.Unlock()
		now = t
	}

	setTimeNow(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	t.Cleanup(func() { setTimeNow(time.Now) })

	testCases := []struct {
		blackoutPeriodCfg *string
		blackoutPeriod    time.Duration
	}{{
		blackoutPeriodCfg: stringp("1s"), // this overwrites 0 default blackout config...
		blackoutPeriod:    time.Second,
	}, {
		blackoutPeriodCfg: nil,
		blackoutPeriod:    10 * time.Second, // the default
	}}
	for _, tc := range testCases {
		setNow(start)
		srv1 := startServer(t, reportOOB)
		srv2 := startServer(t, reportOOB)

		// srv1 starts loaded and srv2 starts without load; ensure RPCs are routed
		// disproportionately to srv2 (10:1).
		srv1.oobMetrics.SetQPS(10.0)
		srv1.oobMetrics.SetApplicationUtilization(1.0)

		srv2.oobMetrics.SetQPS(10.0)
		srv2.oobMetrics.SetApplicationUtilization(.1)

		cfg := oobConfig
		cfg.BlackoutPeriod = tc.blackoutPeriodCfg
		sc := svcConfig(t, cfg)
		if err := srv1.StartClient(grpc.WithDefaultServiceConfig(sc)); err != nil {
			t.Fatalf("Error starting client: %v", err)
		}
		addrs := []resolver.Address{{Addr: srv1.Address}, {Addr: srv2.Address}}
		srv1.R.UpdateState(resolver.State{Addresses: addrs})

		// Call each backend once to ensure the weights have been received.
		ensureReached(ctx, t, srv1.Client, 2)

		// Wait for the weight update period to allow the new weights to be processed.
		time.Sleep(weightUpdatePeriod)
		// During the blackout period (1s) we should route roughly 50/50.
		checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 1})

		// Advance time to right before the blackout period ends and the weights
		// should still be zero.
		setNow(start.Add(tc.blackoutPeriod - time.Nanosecond))
		// Wait for the weight update period to allow the new weights to be processed.
		time.Sleep(weightUpdatePeriod)
		checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 1})

		// Advance time to right after the blackout period ends and the weights
		// should now activate.
		setNow(start.Add(tc.blackoutPeriod))
		// Wait for the weight update period to allow the new weights to be processed.
		time.Sleep(weightUpdatePeriod)
		checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 10})
	}
}

// Tests that the weight expiration period causes backends to use 0 as their
// weight (meaning to use the average weight) once the expiration period
// elapses. After the weight expires, the expected metrics to be emitted from
// WRR are also configured.
func (s) TestBalancer_TwoAddresses_WeightExpiration(t *testing.T) {
	// For rls too :)
	// This scenario can be used to test an emission of the third metric...yeah :)

	// what to check on sh? Most recent...
	mr := stats.NewTestMetricsRecorder(t, []string{"grpc.lb.wrr.rr_fallback", "grpc.lb.wrr.endpoint_weight_not_yet_usable", "grpc.lb.wrr.endpoint_weight_stale", "grpc.lb.wrr.endpoint_weights"})

	// doesn't lose anything int to float...
	// Could pull out this call to a helper that takes 4 values and asserts all are as expected...
	/*mr.AssertDataForMetric("grpc.lb.wrr.rr_fallback", /*what should this number be for fallback?)
	// for the metric below, should I just use this test to scope this or find another test I can plug in...
	mr.AssertDataForMetric("grpc.lb.wrr.endpoint_weight_not_yet_usable", /*what should this number be for this?) // Distinguish between 0 and unset?
	// The metric below is nicely scoped to this test...
	mr.AssertDataForMetric("grpc.lb.wrr.endpoint_weight_stale", /*what should this number be for this? - this is the sceanrio under test)
	mr.AssertDataForMetric("grpc.lb.wrr.endpoint_weights", /*Assertion comes after rounding etc...use the distribution to see what implies this...)



	// need assertions for a descriptor...I think this package has access to descriptor ref...e2e can I just use the name in OTel emissions to check this?

	// fuck no ref to the handle...
	// but coullddddd key it on name?

	// handle passed in yeah key it on metric name -> map[label representation] -> value

	// because pass in a metrics struct to the fake metrics recorder
	// anyway...already tested that handles can be used as keys in metrics
	// registry tests and OTel tests...yeah do that

	endpointWeightStaleHandle
	mr.assert*/


	// and scale up assertions maybe using Yijie's way by adding a
	// hash...map[desc]map[hash (k/v for labels]recording point
	// target set in this case to whatever stub server is configured with
	// locality doesn't apply since not part of xDS System...

	// What should the value be? 1 or 2....

	// one loaded one not so 1 for that metric
	// what endpoint weights

	// at the end of function hits third metric somehow...




	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	var mu sync.Mutex
	start := time.Now()
	now := start
	setNow := func(t time.Time) {
		mu.Lock()
		defer mu.Unlock()
		now = t
	}
	setTimeNow(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	t.Cleanup(func() { setTimeNow(time.Now) })

	srv1 := startServer(t, reportBoth)
	srv2 := startServer(t, reportBoth)

	// srv1 starts loaded and srv2 starts without load; ensure RPCs are routed
	// disproportionately to srv2 (10:1).  Because the OOB reporting interval
	// is 1 minute but the weights expire in 1 second, routing will go to 50/50
	// after the weights expire.
	srv1.oobMetrics.SetQPS(10.0)
	srv1.oobMetrics.SetApplicationUtilization(1.0)

	srv2.oobMetrics.SetQPS(10.0)
	srv2.oobMetrics.SetApplicationUtilization(.1)

	cfg := oobConfig
	cfg.OOBReportingPeriod = stringp("60s")
	sc := svcConfig(t, cfg)
	if err := srv1.StartClient(grpc.WithDefaultServiceConfig(sc), grpc.WithStatsHandler(mr)); err != nil {
		t.Fatalf("Error starting client: %v", err)
	}
	addrs := []resolver.Address{{Addr: srv1.Address}, {Addr: srv2.Address}}
	srv1.R.UpdateState(resolver.State{Addresses: addrs})

	// Call each backend once to ensure the weights have been received.
	ensureReached(ctx, t, srv1.Client, 2)

	// Wait for the weight update period to allow the new weights to be processed.
	time.Sleep(weightUpdatePeriod) // sleeps here...weight update period, not simulated time for events?
	checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 10})

	// Advance what time.Now returns to the weight expiration time minus 1s to
	// ensure all weights are still honored.
	setNow(start.Add(weightExpirationPeriod - time.Second))

	// Wait for the weight update period to allow the new weights to be processed.
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 10})

	// Right here also have assertions...
	// If I do have helper though will need to figure out what to do for histoo

	// this falls back already? Must get triggered above...yeah needs to wait for endpoint updates?
	mr.AssertDataForMetric("grpc.lb.wrr.rr_fallback", 1) // This should be 1 here, and 0 above since this hits from rrFallbackHandle...

	// This setNow and time.Sleep operation cause weights to expire - one or both?
	// I think both expire - just check this one metric and see if it works...
	// test all metrics in each function or just one per for specificity...?
	// does this not expire?
	mr.AssertDataForMetric("grpc.lb.wrr.endpoint_weight_stale", 1) // both endpoints expire I think...

	// Endpoint weight not yet usable hits above and doesn't change? How to have assertions at each step?
	// Maybe a helper that takes 4 expected...how to do this for histos?

	// Is this deterministic...there's quite a few time.Sleeps littered throughout codebase...
	mr.AssertDataForMetric("grpc.lb.wrr.endpoint_weight_not_yet_usable", 0/*does this have any data points? not at this moment but when test is warming up it will...but it persists it around*/) // Distinguish between 0 and unset?

	// How do you assert histos? I think this goes 0 0 now, but earlier it actaully emits the weights which I think are in a 10:1 ratio...scale up RLS unit tests too?
	mr.AssertDataForMetric("grpc.lb.wrr.endpoint_weights", 100/*Assertion comes after rounding etc...use the distribution to see what implies this...*/)
	// Need to rewrite assertion to persist full histos...again there's a lot of time.Sleeps...
	// is this deterministic...variable number of scheduler updates?

	// Getting 0, I don't know if it's hitting scheduler?

	// Does it emit metrics? Print there...


	// Before factoring out into 4 just get a smoke test to work...one working/compiled maybe...

	// and then populate a few more test cases, including in between the operations above...



	// e2e test - how to get it working, just needs to
	// actually run a scheduler update...
	// can I just get away with barely any configuration? And have it run all the default weights...

	// ping Doug asking if I can do it in this package to reuse helpers




	// Advance what time.Now returns to the weight expiration time plus 1s to
	// ensure all weights expired and addresses are routed evenly.
	setNow(start.Add(weightExpirationPeriod + time.Second))

	// Wait for the weight expiration period so the weights have expired.
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 1}) // wtf why is this failing now...


	mr.AssertDataForMetric("grpc.lb.wrr.rr_fallback", 1) // This should be 1 here, and 0 above since this hits from rrFallbackHandle...

	// This setNow and time.Sleep operation cause weights to expire - one or both?
	// I think both expire - just check this one metric and see if it works...
	// test all metrics in each function or just one per for specificity...?
	mr.AssertDataForMetric("grpc.lb.wrr.endpoint_weight_stale", 2) // both endpoints expire I think...

	// Endpoint weight not yet usable hits above and doesn't change? How to have assertions at each step?
	// Maybe a helper that takes 4 expected...how to do this for histos?
	mr.AssertDataForMetric("grpc.lb.wrr.endpoint_weight_not_yet_usable", 2) // Distinguish between 0 and unset?

	// How do you assert histos? I think this goes 0 0 now, but earlier it actaully emits the weights which I think are in a 10:1 ratio...scale up RLS unit tests too?
	mr.AssertDataForMetric("grpc.lb.wrr.endpoint_weights", 1/*Assertion comes after rounding etc...use the distribution to see what implies this...*/)


	// Doesn't block forever now since increased buffer size, are emissions deterministic (relates to where to put assertions, lots of math)

	// Also need to calculate based of formula :) but it looks like weights
	// given to rr algorithm do this for you...

	// Can this fail at call site?

	// send out unit tests as separate PR to e2e?
	// test_metrics_recorder.go:80: unexpected data for metric grpc.lb.wrr.endpoint_weight_stale, got: 1, want: 2
	// test_metrics_recorder.go:80: unexpected data for metric grpc.lb.wrr.endpoint_weight_stale, got: 1, want: 2

	// test_metrics_recorder.go:80: unexpected data for metric grpc.lb.wrr.endpoint_weights, got: 10, want: 100
	// test_metrics_recorder.go:80: unexpected data for metric grpc.lb.wrr.endpoint_weights, got: 10, want: 100

	// Two different failures, but this is due to the time.Sleeps and no polls, so snapshot is nondeterministic...
	// Rewrite unit tests...

	// Or for smoke test could do one with simpler operations and see if I need
	// to poll/build off channel or not...


	// Could relax these checks to what comes out at the end/eventual
	// consistency since we'll have smoke tests for endpoint weights in helpers
	// above...

	// Or could test weights for granular checks + smoke check for fallback in
	// two cases...(see if this scaling up works, this would take some setup wrt
	// scs and inducing weights)

	// Only label is locality + target...
	// no locality and target is trivial just need one assertion (for all metrics?) for that I think...


	// For e2e, could just test metrics emit *something...*



}

// Tests logic surrounding subchannel management.
func (s) TestBalancer_AddressesChanging(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	srv1 := startServer(t, reportBoth)
	srv2 := startServer(t, reportBoth)
	srv3 := startServer(t, reportBoth)
	srv4 := startServer(t, reportBoth)

	// srv1: weight 10
	srv1.oobMetrics.SetQPS(10.0)
	srv1.oobMetrics.SetApplicationUtilization(1.0)
	// srv2: weight 100
	srv2.oobMetrics.SetQPS(10.0)
	srv2.oobMetrics.SetApplicationUtilization(.1)
	// srv3: weight 20
	srv3.oobMetrics.SetQPS(20.0)
	srv3.oobMetrics.SetApplicationUtilization(1.0)
	// srv4: weight 200
	srv4.oobMetrics.SetQPS(20.0)
	srv4.oobMetrics.SetApplicationUtilization(.1)

	sc := svcConfig(t, oobConfig)
	if err := srv1.StartClient(grpc.WithDefaultServiceConfig(sc)); err != nil {
		t.Fatalf("Error starting client: %v", err)
	}
	srv2.Client = srv1.Client
	addrs := []resolver.Address{{Addr: srv1.Address}, {Addr: srv2.Address}, {Addr: srv3.Address}}
	srv1.R.UpdateState(resolver.State{Addresses: addrs})

	// Call each backend once to ensure the weights have been received.
	ensureReached(ctx, t, srv1.Client, 3)
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 10}, srvWeight{srv3, 2})

	// Add backend 4
	addrs = append(addrs, resolver.Address{Addr: srv4.Address})
	srv1.R.UpdateState(resolver.State{Addresses: addrs})
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 10}, srvWeight{srv3, 2}, srvWeight{srv4, 20})

	// Shutdown backend 3.  RPCs will no longer be routed to it.
	srv3.Stop()
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv2, 10}, srvWeight{srv4, 20})

	// Remove addresses 2 and 3.  RPCs will no longer be routed to 2 either.
	addrs = []resolver.Address{{Addr: srv1.Address}, {Addr: srv4.Address}}
	srv1.R.UpdateState(resolver.State{Addresses: addrs})
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv1, 1}, srvWeight{srv4, 20})

	// Re-add 2 and remove the rest.
	addrs = []resolver.Address{{Addr: srv2.Address}}
	srv1.R.UpdateState(resolver.State{Addresses: addrs})
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv2, 10})

	// Re-add 4.
	addrs = append(addrs, resolver.Address{Addr: srv4.Address})
	srv1.R.UpdateState(resolver.State{Addresses: addrs})
	time.Sleep(weightUpdatePeriod)
	checkWeights(ctx, t, srvWeight{srv2, 10}, srvWeight{srv4, 20})
}

func ensureReached(ctx context.Context, t *testing.T, c testgrpc.TestServiceClient, n int) {
	t.Helper()
	reached := make(map[string]struct{})
	for len(reached) != n {
		var peer peer.Peer
		if _, err := c.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(&peer)); err != nil {
			t.Fatalf("Error from EmptyCall: %v", err)
		}
		reached[peer.Addr.String()] = struct{}{}
	}
}

type srvWeight struct {
	srv *testServer
	w   int
}

const rrIterations = 100

// checkWeights does rrIterations RPCs and expects the different backends to be
// routed in a ratio as determined by the srvWeights passed in.  Allows for
// some variance (+/- 2 RPCs per backend).
func checkWeights(ctx context.Context, t *testing.T, sws ...srvWeight) {
	t.Helper()

	c := sws[0].srv.Client

	// Replace the weights with approximate counts of RPCs wanted given the
	// iterations performed.
	weightSum := 0
	for _, sw := range sws {
		weightSum += sw.w
	}
	for i := range sws {
		sws[i].w = rrIterations * sws[i].w / weightSum
	}

	for attempts := 0; attempts < 10; attempts++ {
		serverCounts := make(map[string]int)
		for i := 0; i < rrIterations; i++ {
			var peer peer.Peer
			if _, err := c.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(&peer)); err != nil {
				t.Fatalf("Error from EmptyCall: %v; timed out waiting for weighted RR behavior?", err)
			}
			serverCounts[peer.Addr.String()]++
		}
		if len(serverCounts) != len(sws) {
			continue
		}
		success := true
		for _, sw := range sws {
			c := serverCounts[sw.srv.Address]
			if c < sw.w-2 || c > sw.w+2 {
				success = false
				break
			}
		}
		if success {
			t.Logf("Passed iteration %v; counts: %v", attempts, serverCounts)
			return
		}
		t.Logf("Failed iteration %v; counts: %v; want %+v", attempts, serverCounts, sws)
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Failed to route RPCs with proper ratio")
}

func init() {
	setTimeNow(time.Now)
	iwrr.TimeNow = timeNow
}

var timeNowFunc atomic.Value // func() time.Time

func timeNow() time.Time {
	return timeNowFunc.Load().(func() time.Time)()
}

func setTimeNow(f func() time.Time) {
	timeNowFunc.Store(f)
}

// On start client, pass a new metrics recorder (with a ref in test to do assertions on...)

// and see the 4 metrics...the metrics recorder creates of metrics registry at the time...instruments are already registered

// read the tests and see what the metrics would be if they were emitted
// those 4...scale up all? think of other sceanrios for tests...

// this is full e2e infra makes RPC's to backend and asserts on the distribution of these RPC's

// target but this isn't in a full e2e system so don't need locality here...

// deploy xDS tree alongside OTel (need those helpers...)


// If I configure the client with a mock metrics recorder...

// these sceanrios should just work out of the box...

// already have e2e scenarios here...
// Interesting tests to hook on?
// Emit from a few tests?
// Write what the 4 metrics should be from these interesting scenarios/tests...
