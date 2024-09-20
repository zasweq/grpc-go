/*
 * Copyright 2024 gRPC authors.
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
 */

package opentelemetry_test

import (
	"context"
	"fmt"
	rlspb "google.golang.org/grpc/internal/proto/grpc_lookup_v1"
	"google.golang.org/grpc/internal/testutils/rls"
	"io"
	"testing"
	"time"

	v3clusterpb "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3endpointpb "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3clientsideweightedroundrobinpb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/client_side_weighted_round_robin/v3"
	v3wrrlocalitypb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/wrr_locality/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"google.golang.org/grpc"
	_ "google.golang.org/grpc/balancer/rls" // To register RLS Policy
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/stubserver"
	itestutils "google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	setup "google.golang.org/grpc/internal/testutils/xds/e2e/setup"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/orca"
	"google.golang.org/grpc/stats/opentelemetry"
	"google.golang.org/grpc/stats/opentelemetry/internal/testutils"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/metric/metricdata/metricdatatest"
)

const (
	defaultTestTimeout      = 5 * time.Second
	defaultTestShortTimeout = 100 * time.Millisecond
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// setupStubServer creates a stub server with OpenTelemetry component configured on client
// and server side. It returns a reader for metrics emitted from OpenTelemetry
// component and the server.
func setupStubServer(t *testing.T, methodAttributeFilter func(string) bool) (*metric.ManualReader, *stubserver.StubServer) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{Payload: &testpb.Payload{
				Body: make([]byte, len(in.GetPayload().GetBody())),
			}}, nil
		},
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			for {
				_, err := stream.Recv()
				if err == io.EOF {
					return nil
				}
			}
		},
	}

	if err := ss.Start([]grpc.ServerOption{opentelemetry.ServerOption(opentelemetry.Options{
		MetricsOptions: opentelemetry.MetricsOptions{
			MeterProvider:         provider,
			Metrics:               opentelemetry.DefaultMetrics(),
			MethodAttributeFilter: methodAttributeFilter,
		}})}, opentelemetry.DialOption(opentelemetry.Options{
		MetricsOptions: opentelemetry.MetricsOptions{
			MeterProvider: provider,
			Metrics:       opentelemetry.DefaultMetrics(),
		},
	})); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	return reader, ss
}

// TestMethodAttributeFilter tests the method attribute filter. The method
// filter set should bucket the grpc.method attribute into "other" if the method
// attribute filter specifies.
func (s) TestMethodAttributeFilter(t *testing.T) {
	maf := func(str string) bool {
		// Will allow duplex/any other type of RPC.
		return str != testgrpc.TestService_UnaryCall_FullMethodName
	}
	reader, ss := setupStubServer(t, maf)
	defer ss.Stop()

	// Make a Unary and Streaming RPC. The Unary RPC should be filtered by the
	// method attribute filter, and the Full Duplex (Streaming) RPC should not.
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if _, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{Payload: &testpb.Payload{
		Body: make([]byte, 10000),
	}}); err != nil {
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}
	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend()
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("stream.Recv received an unexpected error: %v, expected an EOF error", err)
	}
	rm := &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm)
	gotMetrics := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}

	wantMetrics := []metricdata.Metrics{
		{
			Name:        "grpc.client.attempt.started",
			Description: "Number of client call attempts started.",
			Unit:        "attempt",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(attribute.String("grpc.method", "grpc.testing.TestService/UnaryCall"), attribute.String("grpc.target", ss.Target)),
						Value:      1,
					},
					{
						Attributes: attribute.NewSet(attribute.String("grpc.method", "grpc.testing.TestService/FullDuplexCall"), attribute.String("grpc.target", ss.Target)),
						Value:      1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{
			Name:        "grpc.server.call.duration",
			Description: "End-to-end time taken to complete a call from server transport's perspective.",
			Unit:        "s",
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{ // Method should go to "other" due to the method attribute filter.
						Attributes: attribute.NewSet(attribute.String("grpc.method", "other"), attribute.String("grpc.status", "OK")),
						Count:      1,
						Bounds:     testutils.DefaultLatencyBounds,
					},
					{
						Attributes: attribute.NewSet(attribute.String("grpc.method", "grpc.testing.TestService/FullDuplexCall"), attribute.String("grpc.status", "OK")),
						Count:      1,
						Bounds:     testutils.DefaultLatencyBounds,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
	}

	testutils.CompareMetrics(ctx, t, reader, gotMetrics, wantMetrics)
}

// TestAllMetricsOneFunction tests emitted metrics from OpenTelemetry
// instrumentation component. It then configures a system with a gRPC Client and
// gRPC server with the OpenTelemetry Dial and Server Option configured
// specifying all the metrics provided by this package, and makes a Unary RPC
// and a Streaming RPC. These two RPCs should cause certain recording for each
// registered metric observed through a Manual Metrics Reader on the provided
// OpenTelemetry SDK's Meter Provider. It then makes an RPC that is unregistered
// on the Client (no StaticMethodCallOption set) and Server. The method
// attribute on subsequent metrics should be bucketed in "other".
func (s) TestAllMetricsOneFunction(t *testing.T) {
	reader, ss := setupStubServer(t, nil)
	defer ss.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// Make two RPC's, a unary RPC and a streaming RPC. These should cause
	// certain metrics to be emitted, which should be able to be observed
	// through the Metric Reader.
	if _, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{Payload: &testpb.Payload{
		Body: make([]byte, 10000),
	}}, grpc.UseCompressor(gzip.Name)); err != nil { // Deterministic compression.
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}
	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend()
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("stream.Recv received an unexpected error: %v, expected an EOF error", err)
	}

	rm := &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm)

	gotMetrics := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}

	wantMetrics := testutils.MetricData(testutils.MetricDataOptions{
		Target:                     ss.Target,
		UnaryCompressedMessageSize: float64(57),
	})
	testutils.CompareMetrics(ctx, t, reader, gotMetrics, wantMetrics)

	stream, err = ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend()
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("stream.Recv received an unexpected error: %v, expected an EOF error", err)
	}
	// This Invoke doesn't pass the StaticMethodCallOption. Thus, the method
	// attribute should become "other" on client side metrics. Since it is also
	// not registered on the server either, it should also become "other" on the
	// server metrics method attribute.
	ss.CC.Invoke(ctx, "/grpc.testing.TestService/UnregisteredCall", nil, nil, []grpc.CallOption{}...)
	ss.CC.Invoke(ctx, "/grpc.testing.TestService/UnregisteredCall", nil, nil, []grpc.CallOption{}...)
	ss.CC.Invoke(ctx, "/grpc.testing.TestService/UnregisteredCall", nil, nil, []grpc.CallOption{}...)

	rm = &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm)
	gotMetrics = map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}
	unaryMethodAttr := attribute.String("grpc.method", "grpc.testing.TestService/UnaryCall")
	duplexMethodAttr := attribute.String("grpc.method", "grpc.testing.TestService/FullDuplexCall")

	targetAttr := attribute.String("grpc.target", ss.Target)
	otherMethodAttr := attribute.String("grpc.method", "other")
	wantMetrics = []metricdata.Metrics{
		{
			Name:        "grpc.client.attempt.started",
			Description: "Number of client call attempts started.",
			Unit:        "attempt",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr),
						Value:      1,
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr, targetAttr),
						Value:      2,
					},
					{
						Attributes: attribute.NewSet(otherMethodAttr, targetAttr),
						Value:      3,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{
			Name:        "grpc.server.call.started",
			Description: "Number of server calls started.",
			Unit:        "call",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr),
						Value:      1,
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr),
						Value:      2,
					},
					{
						Attributes: attribute.NewSet(otherMethodAttr),
						Value:      3,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
	}
	for _, metric := range wantMetrics {
		val, ok := gotMetrics[metric.Name]
		if !ok {
			t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
		}
		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
		}
	}
}

// clusterWithLBConfiguration returns a cluster resource with the proto message
// passed Marshaled to an any and specified through the load_balancing_policy
// field.
func clusterWithLBConfiguration(t *testing.T, clusterName, edsServiceName string, secLevel e2e.SecurityLevel, m proto.Message) *v3clusterpb.Cluster {
	cluster := e2e.DefaultCluster(clusterName, edsServiceName, secLevel)
	cluster.LoadBalancingPolicy = &v3clusterpb.LoadBalancingPolicy{
		Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
			{
				TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
					TypedConfig: itestutils.MarshalAny(t, m),
				},
			},
		},
	}
	return cluster
}

func metricsDataFromReader(ctx context.Context, reader *metric.ManualReader) map[string]metricdata.Metrics {
	rm := &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm)
	gotMetrics := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}
	return gotMetrics
}

// the picker works?
// it exits IDLE mode

/*
   tlogger.go:116: INFO clientconn.go:314 [core] [Channel #15]Channel exiting idle mode  (t=+1.20375ms)
   e2e_test.go:647: Timeout when waiting for RPCs to fail with given error: context deadline exceeded

Doesn't fail with the error...
// See what error it fails with if at all?

*/

//     tlogger.go:116: INFO clientconn.go:314 [core] [Channel #15]Channel exiting idle mode  (t=+1.20375ms)
//    e2e_test.go:647: Timeout when waiting for RPCs to fail with given error: context deadline exceeded

// clean all this up and switch rls tests to call this...
/*
// buildBasicRLSConfig constructs a basic service config for the RLS LB policy
// with header matching rules. This expects the passed child policy name to
// have been registered by the caller.
func buildBasicRLSConfig(childPolicyName, rlsServerAddress string) *e2e.RLSConfig {
	return &e2e.RLSConfig{
		RouteLookupConfig: &rlspb.RouteLookupConfig{
			GrpcKeybuilders: []*rlspb.GrpcKeyBuilder{
				{
					Names: []*rlspb.GrpcKeyBuilder_Name{{Service: "grpc.testing.TestService"}},
					Headers: []*rlspb.NameMatcher{
						{Key: "k1", Names: []string{"n1"}},
						{Key: "k2", Names: []string{"n2"}},
					},
				},
			},
			LookupService:        rlsServerAddress,
			LookupServiceTimeout: durationpb.New(defaultTestTimeout),
			CacheSizeBytes:       1024,
		},
		RouteLookupChannelServiceConfig:  `{"loadBalancingConfig": [{"pick_first": {}}]}`,
		ChildPolicy:                      &internalserviceconfig.BalancerConfig{Name: childPolicyName},
		ChildPolicyConfigTargetFieldName: e2e.RLSChildPolicyTargetNameField,
	}
}
*/ // Do I need this whole thing? This does just work out of the box turnkey though...

// I'm assuming the config needs the route lookup config/rls keybuilder info

// rlsServer address passed it...
// Look at LookupService, pass it to this config builder

// I'm just going to pull this symbol out
/*
func buildBasicRLSConfig(childPolicyName, rlsServerAddress string) *rls.RLSConfig { // uh oh this e2e symbol might not be defined - helper to marshal RLS Config into JSON...

}*/

/*
func (s) TestRLSMetrics(t *testing.T) {
	// also throttler...also this might take symbols inside RLS...

	// Three scenarios:
	// 1. Default target metrics (is there cache metrics for this?)
	// 2. target - rls response so cache is warmed up there is metrics
	// 3. failed - is there cache metrics for this?

	// dial mr with rls as top level, rls balancer gives target
	// through rls response (needs distinct or it won't hit default target...how to make distinction?)
	// or through the default target selection
	// to setup those three sceanrios setup rls server and also setup config stuff right?

	// make a rpc while waiting, make it go through...
	// wait helper or just rewrite here
	rlsConfig := /*helper to generate own rls config here...

	reader := metric.NewManualReader() // this is what you rewrite it on
	provider := metric.NewMeterProvider(metric.WithReader(reader))

	rlsServer := rls.SetupFakeRLSServer() // this symbol is lost in a helper

	rlsServer.Address // Stick this in config - this is why he made the helpers as such....

	// pretty trivial setup system make rpc expect metrics (see what happens with respect to adaptive throttler or not)

	mo := opentelemetry.MetricsOptions{
		MeterProvider: provider,
		Metrics:       opentelemetry.DefaultMetrics().Add("grpc.lb.rls.cache_entries", "grpc.lb.rls.cache_size", "grpc.lb.rls.default_target_picks", "grpc.lb.rls.target_picks"),
	}

	// Register a manual resolver and push the RLS service config through it.
	r := startManualResolverWithConfig(t, rlsConfig) // need a mr and a rls config...
	// symbol feels easy enough to move out, could copy whole thing over...


	cc, err := grpc.NewClient(r.Scheme()+":///", grpc.WithTransportCredentials(insecure.NewCredentials()), opentelemetry.DialOption(opentelemetry.Options{MetricsOptions: mo}))
	if err != nil {
		t.Fatalf("Failed to dial local test server: %v", err)
	}
	defer cc.Close()

	// Same exact thing as picker test - setup setup, make RPC, expect metrics except this time full flow and with testing full emissions

	// Need equivalent to MakeTestRPCAndExpectReachBackend/Fail
	client := testgrpc.NewTestServiceClient(cc) // make test rpc and expect it to reach backend is probably similar...wrap and then do

	wantMetrics2 := []metricdata.Metrics{ // Default target picks...
		{
			Name:        "grpc.lb.rls.default_target_picks",
			Description: "EXPERIMENTAL. The current size of the RLS cache.",
			Unit:        "By",
			// Same labels as default target picks
			// []string{"grpc.target", "grpc.lb.rls.server_target", "grpc.lb.rls.data_plane_target", "grpc.lb.pick_result"},
			// ignore and check a subset only?
		}, // Def gets emitted

		{
			Name: "grpc.lb.rls.cache_entries",
			Description: "EXPERIMENTAL. Number of entries in the RLS cache.",
			Unit:        "entry",
			// Data 0 - does this even get emitted?
		},
		{
			Name: "grpc.lb.rls.cache_size",
			Description: "EXPERIMENTAL. The current size of the RLS cache.",
			Unit:        "By",
			// Data 0 - does this even get emitted? even for failure
		},
	}

	// Easiest one to do/smoke test, just try this one out...
	wantMetrics3 := []metricdata.Metrics{
		{
			Name:        "grpc.lb.rls.failed_picks",
			Description: "EXPERIMENTAL. Number of LB picks failed due to either a failed RLS request or the RLS channel being throttled.",
			Unit:        "pick",
			// Labels:      []string{"grpc.target", "grpc.lb.rls.server_target"},
			// Labels are deterministic - could actually test this one out easiest smoke test
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						// Labels:
						Attributes: attribute.NewSet(attribute.String("grpc.target", /*grpc target here from Dial), attribute.String("grpc.lb.rls.server_target", /*spun up fake rls server address)), // this is constant for all but three seperate test cases anyway...
						Value: 1,
					},
				},
			},
		},
		// experiment but I think no cache metrics from this...
	}

	// target picks + rls cache warmed up
	wantMetrics := []metricdata.Metrics{
		{
			Name:        "grpc.lb.rls.target_picks",
			Description: "EXPERIMENTAL. Number of LB picks sent to each RLS target. Note that if the default target is also returned by the RLS server, RPCs sent to that target from the cache will be counted in this metric, not in grpc.rls.default_target_picks.",
			Unit:        "pick",

			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						// Labels:
						Value: 1,
					},
				},
			},
		},
		{
			Name: "grpc.lb.rls.cache_entries",
			Description: "EXPERIMENTAL. Number of entries in the RLS cache.",
			Unit:        "entry",

			// In data (need to update RLS Server target)...is there a way to check labels conditionally?
			Data: metricdata.Gauge[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					// What labels do you need how to only assert on some labels?
					// Label:
					// []string{"grpc.target", "grpc.lb.rls.server_target", "grpc.lb.rls.instance_uuid"},

					// Everything is deterministic (dial and server target comes from config I think)
					// Check that but not uuid...or could hook in and override...
					{

						// Labels: []string{"grpc.target",
						// "grpc.lb.rls.server_target",
						// "grpc.lb.rls.data_plane_target", all of these three are deterministic, just need to derive...
						// "grpc.lb.pick_result"}, "complete - dependent on child

						Value: 1, // one cache entry, figure out how big it is?
					},


				},
				// Temporality/IsMonotonic?
			},
		},
		{
			Name: "grpc.lb.rls.cache_size",
			Description: "EXPERIMENTAL. The current size of the RLS cache.",
			Unit:        "By",

			// In data (need to update RLS Server target)...is there a way to check labels conditionally?

			// One total data point right?
			Data: metricdata.Gauge[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					// Label:
					// []string{"grpc.target", "grpc.lb.rls.server_target", "grpc.lb.rls.instance_uuid"},
					{

						Value: 3, // What size is the cache entry? Just check emission?
					},
				},
			},

		}, // cache metrics for some, def for one with rls response...the other three are conditional...
	}

	//  metricdatatest.AssertHasAttributes()) { // has attributes is a subset, could scale down the full check and test the subset...

	metricdatatest.IgnoreExemplars() // "disables checking if exemplars are different"

	if !metricdatatest.AssertEqual(t, /*metricdata want, /*metricdata got) {

	}


	// If no need to ignore uuid (could just send this out for guidance)

	for _, metric := range wantMetrics { // test.wantMetrics...
		val, ok := gotMetrics[metric.Name] // poll or just read once? detemrinistic right because after rpc it will have emitted one...cache is gauge but already warmed up or not
		if !ok {
			t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
		}
		// Need to get this failing if introduce a bug...
		// Does this print diff?
		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
		}
	}

	// Move all the helpers to internal/testutils/rls or something, see if you can get failure ones working

}

*/

// Done, when I get back just make sure
// cache metrics work (ignore uuid, how to derive size?)

// Cleanup

// Switch his tests to use? Or send cleaned up e2e tests out for review...

func (s) TestRLSTargetPickMetric(t *testing.T) {

	// Same thing but set address through route lookup response...

	rlsServer, _ := rls.SetupFakeRLSServer(t, nil)

	rlsConfig := rls.BuildBasicRLSConfigWithChildPolicy(t, t.Name(), rlsServer.Address)
	backend := &stubserver.StubServer{
		EmptyCallF: func(ctx context.Context, empty *testpb.Empty) (*testpb.Empty, error) {
			return &testpb.Empty{}, nil
		},
	}
	if err := backend.StartServer(); err != nil {
		t.Fatalf("Failed to start backend: %v", err)
	}
	t.Logf("Started TestService backend at: %q", backend.Address)
	defer backend.Stop()

	rlsServer.SetResponseCallback(func(context.Context, *rlspb.RouteLookupRequest) *rls.RouteLookupResponse {
		return &rls.RouteLookupResponse{Resp: &rlspb.RouteLookupResponse{Targets: []string{backend.Address}}}
	})
	r := rls.StartManualResolverWithConfig(t, rlsConfig)
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	mo := opentelemetry.MetricsOptions{
		MeterProvider: provider,
		Metrics:       opentelemetry.DefaultMetrics().Add("grpc.lb.rls.cache_entries", "grpc.lb.rls.cache_size", "grpc.lb.rls.default_target_picks", "grpc.lb.rls.target_picks", "grpc.lb.rls.failed_picks"),
	}
	grpcTarget := r.Scheme() + ":///"
	cc, err := grpc.NewClient(grpcTarget, grpc.WithResolvers(r), grpc.WithTransportCredentials(insecure.NewCredentials()), opentelemetry.DialOption(opentelemetry.Options{MetricsOptions: mo}))
	if err != nil {
		t.Fatalf("Failed to dial local test server: %v", err)
	}
	defer cc.Close()

	wantMetrics := []metricdata.Metrics{
		{
			Name:        "grpc.lb.rls.target_picks",
			Description: "EXPERIMENTAL. Number of LB picks sent to each RLS target. Note that if the default target is also returned by the RLS server, RPCs sent to that target from the cache will be counted in this metric, not in grpc.rls.default_target_picks.",
			Unit:        "pick",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						// Labels: felt so good to fill these in lol...
						Attributes: attribute.NewSet(attribute.String("grpc.target", grpcTarget), attribute.String("grpc.lb.rls.server_target", rlsServer.Address), attribute.String("grpc.lb.rls.data_plane_target", backend.Address), attribute.String("grpc.lb.pick_result", "complete")), // this is constant for all but three seperate test cases anyway...
						Value:      1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},

		// Cache metrics...above should work irrespective of cache metrics...
	}
	client := testgrpc.NewTestServiceClient(cc)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	// *** This switches to wanting to hit a backend
	_, err = client.EmptyCall(ctx, &testpb.Empty{}) // only do what you need
	// Could queue...? Based on adaptive throttler...

	// Error recreating setup of RLS pick?
	// Where is it failing in this system even for throttled should emit failed so IDK...

	// print("err: ", err.Error(), "\n")
	if err != nil {
		t.Fatalf("client.EmptyCall failed with error: %v", err)
	}

	gotMetrics := metricsDataFromReader(ctx, reader)
	// ***

	// same assertion - helper?
	for _, metric := range wantMetrics {
		val, ok := gotMetrics[metric.Name] // poll or just read once? detemrinistic right because after rpc it will have emitted one...cache is gauge but already warmed up or not
		if !ok {
			t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
		}
		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) { // how to figure out the partitioning of label stuff...not for this one for cache check that separately?
			t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
		}
	}

	// Nondeterminism with respect to cache metrics uuid...ignore part?

} // send out these tests and then switch Easwar's test to use them once he approves it...

// ^^^ cache metrics and non deterministic labels,
// need to switch his tests to use moved helpers.
func (s) TestRLSDefaultTargetPickMetric(t *testing.T) {
	// Throttler might be needed to deterministically induce useDefaultPickIfPossible...
	// whereas below it will also be a failure but be different...
	// Not throttling seems to be fine here...

	// Start an RLS Server and set the throttler to always throttle requests...
	rlsServer, _ := rls.SetupFakeRLSServer(t, nil) // rewrite this or something perhaps?
	// overrideAdaptiveThrottler...
	// Build RLS service config with a default target.
	rlsConfig := rls.BuildBasicRLSConfigWithChildPolicy(t, t.Name(), rlsServer.Address)

	// Clean all this stuff up after done
	backend := &stubserver.StubServer{
		EmptyCallF: func(ctx context.Context, empty *testpb.Empty) (*testpb.Empty, error) {
			return &testpb.Empty{}, nil
		},
	}
	if err := backend.StartServer(); err != nil {
		t.Fatalf("Failed to start backend: %v", err)
	}
	t.Logf("Started TestService backend at: %q", backend.Address)
	defer backend.Stop()
	rlsConfig.RouteLookupConfig.DefaultTarget = backend.Address

	r := rls.StartManualResolverWithConfig(t, rlsConfig)
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	mo := opentelemetry.MetricsOptions{
		MeterProvider: provider,
		Metrics:       opentelemetry.DefaultMetrics().Add("grpc.lb.rls.cache_entries", "grpc.lb.rls.cache_size", "grpc.lb.rls.default_target_picks", "grpc.lb.rls.target_picks", "grpc.lb.rls.failed_picks"),
	}
	grpcTarget := r.Scheme() + ":///"
	cc, err := grpc.NewClient(grpcTarget, grpc.WithResolvers(r), grpc.WithTransportCredentials(insecure.NewCredentials()), opentelemetry.DialOption(opentelemetry.Options{MetricsOptions: mo}))
	if err != nil {
		t.Fatalf("Failed to dial local test server: %v", err)
	}
	defer cc.Close()

	wantMetrics := []metricdata.Metrics{
		{
			Name:        "grpc.lb.rls.default_target_picks",
			Description: "EXPERIMENTAL. Number of LB picks sent to the default target.",
			Unit:        "pick",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(attribute.String("grpc.target", grpcTarget), attribute.String("grpc.lb.rls.server_target", rlsServer.Address), attribute.String("grpc.lb.rls.data_plane_target", backend.Address), attribute.String("grpc.lb.pick_result", "complete")),
						Value:      1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		}, // Def gets emitted - uuid is only on cache so I think it's good - cache is just not set right...?
		// Curious - check if cache is set
		// only default target is set...

		// looks like it does get an rls response just ignores it...

		/*{ // how is there rls cache entry metric...
			Name:        "grpc.lb.rls.cache_entries",
			Description: "EXPERIMENTAL. Number of entries in the RLS cache.",
			Unit:        "entry",
			Data: metricdata.Gauge[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(attribute.String("grpc.target", grpcTarget), attribute.String("grpc.lb.rls.server_target", rlsServer.Address), attribute.String("grpc.lb.rls.instance_uuid", "some uuid")),
						Value:      1, // why is there a gauge...?
					},
				},
			},
		},*/ // oh fake rls server returns an empty response, important to check or implementation? Should emit for failing too no addresses...
		// ignore nondeterministic label
		{
			Name:        "grpc.lb.rls.cache_size",
			Description: "EXPERIMENTAL. The current size of the RLS cache.",
			Unit:        "By",
			Data: metricdata.Gauge[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(attribute.String("grpc.target", grpcTarget), attribute.String("grpc.lb.rls.server_target", rlsServer.Address), attribute.String("grpc.lb.rls.instance_uuid", "some uuid")),
						Value:      0, // yeah it's an empty cache entry...
					},
				},
			},
		}, // same empty cache (empty target list in rls response) causes failure, get the cache metrics working derive size deal with uuid nondeterminism and then done, cleanup and you'll be good
	}
	client := testgrpc.NewTestServiceClient(cc)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	_, err = client.EmptyCall(ctx, &testpb.Empty{})
	if err != nil {
		t.Fatalf("client.EmptyCall failed with error: %v", err)
	}

	gotMetrics := metricsDataFromReader(ctx, reader)

	// same assertion - helper?
	for _, metric := range wantMetrics {
		val, ok := gotMetrics[metric.Name] // no need to poll all the records happen sync right before...
		if !ok {
			t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
		}
		metricdatatest.IgnoreValue()                                                                                         // Does this ignore labels?
		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) { // how to figure out the partitioning of label stuff...not for this one for cache check that separately?
			t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
		}
	}
}

// Same setup as unit test write comments after figure out if t but I think setup is sufficiently different...
func (s) TestRLSFailedRPCMetric(t *testing.T) {

	// Start an RLS server and set the throttler to never throttle requests.
	rlsServer, _ := rls.SetupFakeRLSServer(t, nil) // rename to rlstestutils or something...

	// does the non overriding of the throttler cause any problems?

	// Build an RLS config without a default target.
	rlsConfig := rls.BuildBasicRLSConfigWithChildPolicy(t, t.Name(), rlsServer.Address) // need to switch his tests to use this too...
	// Register a manual resolver and push the RLS service config through it.
	r := rls.StartManualResolverWithConfig(t, rlsConfig)
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	mo := opentelemetry.MetricsOptions{
		MeterProvider: provider,
		Metrics:       opentelemetry.DefaultMetrics().Add("grpc.lb.rls.cache_entries", "grpc.lb.rls.cache_size", "grpc.lb.rls.default_target_picks", "grpc.lb.rls.target_picks", "grpc.lb.rls.failed_picks"),
	}
	grpcTarget := r.Scheme() + ":///"
	cc, err := grpc.NewClient(grpcTarget, grpc.WithResolvers(r), grpc.WithTransportCredentials(insecure.NewCredentials()), opentelemetry.DialOption(opentelemetry.Options{MetricsOptions: mo}))
	if err != nil {
		t.Fatalf("Failed to dial local test server: %v", err)
	}
	defer cc.Close()

	// Helper to create an RLS Config....struct that marshals into JSON
	// could use a different struct but this is fine, also I thinkkk I need
	// that full RLS Config with keybuilders etc...?

	// once you build out config build it out as the manual resolvers and deploy with service config

	// also stick a metrics recorder on there as well...

	// Easiest one to do/smoke test, just try this one out...
	wantMetrics := []metricdata.Metrics{
		{
			Name:        "grpc.lb.rls.failed_picks",
			Description: "EXPERIMENTAL. Number of LB picks failed due to either a failed RLS request or the RLS channel being throttled.",
			Unit:        "pick",
			// Labels:      []string{"grpc.target", "grpc.lb.rls.server_target"},
			// Labels are deterministic - could actually test this one out easiest smoke test
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						// Labels: felt so good to fill these in lol...
						Attributes: attribute.NewSet(attribute.String("grpc.target", grpcTarget), attribute.String("grpc.lb.rls.server_target", rlsServer.Address)), // this is constant for all but three seperate test cases anyway...
						Value:      1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		}, // Still need to figure out nondeterministic labels...
		// experiment but I think no cache metrics from this since cache isn't warmed up.....cache gets rls server target on an UpdateCCS...
	}

	// My guess is need to override default throttler...
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// Try default test timeout, but log what's going on with RPC's, just run this test

	// rls.MakeTestRPCAndVerifyError(ctx, t, cc, codes.Unavailable, errors.New("RLS response's target list does not contain any entries for key")) // this also passes a t...
	// Make an RPC, it should error no need to assert and then emit no need to verify error...
	client := testgrpc.NewTestServiceClient(cc)
	// name resolver producing zero addresses might not be an RLS failure, but a child failure that then gets converted...
	_, err = client.EmptyCall(ctx, &testpb.Empty{}) // only do what you need
	// Could queue...? Based on adaptive throttler...

	// Error recreating setup of RLS pick?
	// Where is it failing in this system even for throttled should emit failed so IDK...

	print("err: ", err.Error(), "\n") // grep this in codebase smh...
	if err == nil {                   // Honestly need to make sure this is an RLS error...
		t.Fatal("err is nil did not expect it")
	}

	// Need to read the gotMetrics below...
	gotMetrics := metricsDataFromReader(ctx, reader)

	// same assertion - helper?
	for _, metric := range wantMetrics {
		val, ok := gotMetrics[metric.Name]
		if !ok {
			t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
		}
		// Need to get this failing if introduce a bug...
		// Does this print diff?
		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) { // how to figure out the partitioning of label stuff...for cache turns out always emits cache...
			t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
		}
	}
}

// TestWRRMetrics tests the metrics emitted from the WRR LB Policy. It
// configures WRR as an endpoint picking policy through xDS on a ClientConn
// alongside an OpenTelemetry stats handler. It makes a few RPC's, and then
// sleeps for a bit to allow weight to expire. It then asserts OpenTelemetry
// metrics atoms are eventually present for all four WRR Metrics, alongside the
// correct target and locality label for each metric.
func (s) TestWRRMetrics(t *testing.T) {
	cmr := orca.NewServerMetricsRecorder().(orca.CallMetricsRecorder)
	backend1 := stubserver.StartTestService(t, &stubserver.StubServer{
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
	}, orca.CallMetricsServerOption(nil))
	port1 := itestutils.ParsePort(t, backend1.Address)
	defer backend1.Stop()

	cmr.SetQPS(10.0)
	cmr.SetApplicationUtilization(1.0)

	backend2 := stubserver.StartTestService(t, &stubserver.StubServer{
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
	}, orca.CallMetricsServerOption(nil))
	port2 := itestutils.ParsePort(t, backend2.Address)
	defer backend2.Stop()

	const serviceName = "my-service-client-side-xds"

	// Start an xDS management server.
	managementServer, nodeID, _, xdsResolver := setup.ManagementServerAndResolver(t)

	wrrConfig := &v3wrrlocalitypb.WrrLocality{
		EndpointPickingPolicy: &v3clusterpb.LoadBalancingPolicy{
			Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
				{
					TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
						TypedConfig: itestutils.MarshalAny(t, &v3clientsideweightedroundrobinpb.ClientSideWeightedRoundRobin{
							EnableOobLoadReport: &wrapperspb.BoolValue{
								Value: false,
							},
							// BlackoutPeriod long enough to cause load report
							// weight to trigger in the scope of test case.
							// WeightExpirationPeriod will cause the load report
							// weight for backend 1 to expire.
							BlackoutPeriod:          durationpb.New(5 * time.Millisecond),
							WeightExpirationPeriod:  durationpb.New(500 * time.Millisecond),
							WeightUpdatePeriod:      durationpb.New(time.Second),
							ErrorUtilizationPenalty: &wrapperspb.FloatValue{Value: 1},
						}),
					},
				},
			},
		},
	}

	routeConfigName := "route-" + serviceName
	clusterName := "cluster-" + serviceName
	endpointsName := "endpoints-" + serviceName
	resources := e2e.UpdateOptions{
		NodeID:    nodeID,
		Listeners: []*v3listenerpb.Listener{e2e.DefaultClientListener(serviceName, routeConfigName)},
		Routes:    []*v3routepb.RouteConfiguration{e2e.DefaultRouteConfig(routeConfigName, serviceName, clusterName)},
		Clusters:  []*v3clusterpb.Cluster{clusterWithLBConfiguration(t, clusterName, endpointsName, e2e.SecurityLevelNone, wrrConfig)},
		Endpoints: []*v3endpointpb.ClusterLoadAssignment{e2e.EndpointResourceWithOptions(e2e.EndpointOptions{
			ClusterName: endpointsName,
			Host:        "localhost",
			Localities: []e2e.LocalityOptions{
				{
					Backends: []e2e.BackendOptions{{Port: port1}, {Port: port2}},
					Weight:   1,
				},
			},
		})},
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))

	mo := opentelemetry.MetricsOptions{
		MeterProvider:  provider,
		Metrics:        opentelemetry.DefaultMetrics().Add("grpc.lb.wrr.rr_fallback", "grpc.lb.wrr.endpoint_weight_not_yet_usable", "grpc.lb.wrr.endpoint_weight_stale", "grpc.lb.wrr.endpoint_weights"),
		OptionalLabels: []string{"grpc.lb.locality"},
	}

	target := fmt.Sprintf("xds:///%s", serviceName) // Oh deploy with an xDS target, figures out WRR Config and logic from xDS Resources...CDS and gets chained lol these test helpers were already present...
	cc, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver), opentelemetry.DialOption(opentelemetry.Options{MetricsOptions: mo}))
	if err != nil {
		t.Fatalf("Failed to dial local test server: %v", err)
	}
	defer cc.Close()

	client := testgrpc.NewTestServiceClient(cc)

	// Make 100 RPC's. The two backends will send back load reports per call
	// giving the two SubChannels weights which will eventually expire. Two
	// backends needed as for only one backend, WRR does not recompute the
	// scheduler.
	receivedExpectedMetrics := grpcsync.NewEvent()
	go func() {
		for !receivedExpectedMetrics.HasFired() {
			client.EmptyCall(ctx, &testpb.Empty{})
			time.Sleep(2 * time.Millisecond)
		}
	}()

	targetAttr := attribute.String("grpc.target", target)
	localityAttr := attribute.String("grpc.lb.locality", `{"region":"region-1","zone":"zone-1","subZone":"subzone-1"}`)

	wantMetrics := []metricdata.Metrics{
		{
			Name:        "grpc.lb.wrr.rr_fallback",
			Description: "EXPERIMENTAL. Number of scheduler updates in which there were not enough endpoints with valid weight, which caused the WRR policy to fall back to RR behavior.",
			Unit:        "update",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(targetAttr, localityAttr),
						Value:      1, // value ignored
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},

		{
			Name:        "grpc.lb.wrr.endpoint_weight_not_yet_usable",
			Description: "EXPERIMENTAL. Number of endpoints from each scheduler update that don't yet have usable weight information (i.e., either the load report has not yet been received, or it is within the blackout period).",
			Unit:        "endpoint",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(targetAttr, localityAttr),
						Value:      1, // value ignored
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{
			Name:        "grpc.lb.wrr.endpoint_weights",
			Description: "EXPERIMENTAL. Weight of each endpoint, recorded on every scheduler update. Endpoints without usable weights will be recorded as weight 0.",
			Unit:        "endpoint",
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes: attribute.NewSet(targetAttr, localityAttr),
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
	}

	if err := pollForWantMetrics(ctx, t, reader, wantMetrics); err != nil {
		t.Fatal(err)
	}
	receivedExpectedMetrics.Fire()

	// Poll for 5 seconds for weight expiration metric. No more RPC's are being
	// made, so weight should expire on a subsequent scheduler update.
	eventuallyWantMetric := metricdata.Metrics{
		Name:        "grpc.lb.wrr.endpoint_weight_stale",
		Description: "EXPERIMENTAL. Number of endpoints from each scheduler update whose latest weight is older than the expiration period.",
		Unit:        "endpoint",
		Data: metricdata.Sum[int64]{
			DataPoints: []metricdata.DataPoint[int64]{
				{
					Attributes: attribute.NewSet(targetAttr, localityAttr),
					Value:      1, // value ignored
				},
			},
			Temporality: metricdata.CumulativeTemporality,
			IsMonotonic: true,
		},
	}

	if err := pollForWantMetrics(ctx, t, reader, []metricdata.Metrics{eventuallyWantMetric}); err != nil {
		t.Fatal(err)
	}
}

// pollForWantMetrics polls for the wantMetrics to show up on reader. Returns an
// error if metric is present but not equal to expected, or if the wantMetrics
// do not show up during the context timeout.
func pollForWantMetrics(ctx context.Context, t *testing.T, reader *metric.ManualReader, wantMetrics []metricdata.Metrics) error {
	for ; ctx.Err() == nil; <-time.After(time.Millisecond) {
		gotMetrics := metricsDataFromReader(ctx, reader)
		containsAllMetrics := true
		for _, metric := range wantMetrics {
			val, ok := gotMetrics[metric.Name]
			if !ok {
				containsAllMetrics = false
				break
			}
			if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreValue(), metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
				return fmt.Errorf("metrics data type not equal for metric: %v", metric.Name)
			}
		}
		if containsAllMetrics {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}

	return fmt.Errorf("error waiting for metrics %v: %v", wantMetrics, ctx.Err())
}
