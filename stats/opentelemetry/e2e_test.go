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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/stubserver"
	itestutils "google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/stats/opentelemetry"
	"google.golang.org/grpc/stats/opentelemetry/internal/testutils"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/metric/metricdata/metricdatatest"
)

var defaultTestTimeout = 5 * time.Second

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// setup creates a stub server with OpenTelemetry component configured on client
// and server side. It returns a reader for metrics emitted from OpenTelemetry
// component and the server.
func setup(t *testing.T, methodAttributeFilter func(string) bool) (*metric.ManualReader, *stubserver.StubServer) {
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
	reader, ss := setup(t, maf)
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
	reader, ss := setup(t, nil)
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

// deploy custom lb to get locality

// For RLS use deployment infra with RLS top level balancer just to get it to work,
// Plug an OTel plugin there for the base case...
// rls target and grpc target (and pick result)
// so I don't think needs to be in xDS Tree top level for both corner cases
// and full plumbing...

// gets balancer/addresses through xDS...

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


// TestWRRMetrics tests the metrics emitted from the WRR LB Policy. It
// configures WRR as an endpoint picking policy through xDS on a ClientConn
// alongside an OpenTelemetry stats handler. It makes a few RPC's, and then
// asserts OpenTelemetry metrics atoms are present for all four WRR Metrics,
// alongside the correct target and locality label.

// so need same setup with ClientConn/fully working server? yes except server
// has load reports...

// the RPC's work as written so I think this is fine...

func (s) TestWRRMetrics(t *testing.T) {
	// If need shared symbols move to internal/testutils/opentelemetry...

	// orca mr and also how to setup a service...

	backend1 := stubserver.StartTestService(t, nil)
	// backend1.S.RegisterService() // happen before serve is start, register an orca service...
	port1 := itestutils.ParsePort(t, backend1.Address)
	defer backend1.Stop()
	backend2 := stubserver.StartTestService(t, nil)
	port2 := itestutils.ParsePort(t, backend2.Address)
	defer backend2.Stop()
	backend3 := stubserver.StartTestService(t, nil)
	port3 := itestutils.ParsePort(t, backend3.Address)
	defer backend3.Stop()

	// One backend, so will fallback to rr.
	// Wait but three would work too...yeah it says it's recording...

	const serviceName = "my-service-client-side-xds"

	// Do I need 3 or will 1 or 2 work wrt metrics I want...

	// what buckets do the histos pick up

	// Don't need an address distribution count...just make the RPC's


	// When I get back - make this helper shared...

	// Start an xDS management server.
	managementServer, nodeID, _, xdsResolver := e2e.SetupManagementServerAndResolver(t) // move to testutils...

	wrrConfig := &v3wrrlocalitypb.WrrLocality{
		EndpointPickingPolicy: &v3clusterpb.LoadBalancingPolicy{
			Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
				{
					TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
						TypedConfig: itestutils.MarshalAny(t, &v3clientsideweightedroundrobinpb.ClientSideWeightedRoundRobin{
							EnableOobLoadReport: &wrapperspb.BoolValue{
								Value: false,
							},
							// BlackoutPeriod long enough to cause load report weights to
							// trigger in the scope of test case, but no load reports
							// configured anyway.
							BlackoutPeriod:          durationpb.New(10 * time.Second), // 0
							WeightExpirationPeriod:  durationpb.New(10 * time.Second), // fractional...time.Sleep to trigger it...
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
		Clusters:  []*v3clusterpb.Cluster{clusterWithLBConfiguration(t, clusterName, endpointsName, e2e.SecurityLevelNone, wrrConfig)}, // inline these symbols perhaps...these are the two resource helpers defined above in custom lb test...
		Endpoints: []*v3endpointpb.ClusterLoadAssignment{e2e.EndpointResourceWithOptions(e2e.EndpointOptions{
			ClusterName: endpointsName,
			Host:        "localhost",
			Localities: []e2e.LocalityOptions{ // maybe just have one priority? what's the locality name?
				{
					Backends: []e2e.BackendOptions{{Port: port1}, {Port: port2}, {Port: port3}},
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
		MeterProvider:         provider,
		Metrics:               opentelemetry.DefaultMetrics().Add("grpc.lb.wrr.rr_fallback", "grpc.lb.wrr.endpoint_weight_not_yet_usable", "grpc.lb.wrr.endpoint_weight_stale", "grpc.lb.wrr.endpoint_weights") /*add the 4 wrr ones or figure out how to start from empty and scale up...*/,
		OptionalLabels: []string{"grpc.lb.locality"},
	}

	// it happens sync when it's built right? yeah just first scheduler update...
	target := fmt.Sprintf("xds:///%s", serviceName) // use this for label emission expectations...what is the locality? I've already derived this...
	cc, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(xdsResolver), opentelemetry.DialOption(opentelemetry.Options{MetricsOptions: mo}))
	if err != nil {
		t.Fatalf("Failed to dial local test server: %v", err)
	}
	defer cc.Close()

	client := testgrpc.NewTestServiceClient(cc)

	// It must get addresses from xDS tree...oh triggers it by putting addresses in EDS...

	// make a few RPC's...wrap this symbol with helper for distribution check...
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil { // does it matter...options or what happens...
		t.Fatalf("EmptyCall() = %v, want <nil>", err)
	}
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil { // does it matter...options or what happens...
		t.Fatalf("EmptyCall() = %v, want <nil>", err)
	}
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}); err != nil { // does it matter...options or what happens...
		t.Fatalf("EmptyCall() = %v, want <nil>", err)
	}
	// This works so backend plumbing works as usual...
	// just need to scale up orca...


	// WRR should function as normal...


	// time.Sleeps to trigger scheduler update/expiration...


	// poll for all 4 or just endpoint weight expires...



	// light assertions here on the emitted WRR Metrics...
	rm := &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm)
	gotMetrics := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}

	// and then attrs defined inline here or something...

	// no need to poll since gotMetrics emits as a result of WRR getting scheduler...

	// where do Addrs/connecting SCs get set?
	// ah cool no need to setup weights...since just defaults to rr...
	targetAttr := attribute.String("grpc.target", target)
	localityAttr := attribute.String("grpc.lb.locality", `{"region":"region-1","zone":"zone-1","subZone":"subzone-1"}`) // yeah derived in telemetry labels test...


	// wantMetrics but do a presence check (ignore using their library...)

	wantMetrics := []metricdata.Metrics{
		{
			Name: "grpc.lb.wrr.rr_fallback",
			Description: "EXPERIMENTAL. Number of scheduler updates in which there were not enough endpoints with valid weight, which caused the WRR policy to fall back to RR behavior.",
			Unit: "update",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(targetAttr, localityAttr),
						Value:      1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},

		// configure ORCA Service, and have it emit metrics for each one...

		{
			Name: "grpc.lb.wrr.endpoint_weight_not_yet_usable",
			Description: "EXPERIMENTAL. Number of endpoints from each scheduler update that don't yet have usable weight information (i.e., either the load report has not yet been received, or it is within the blackout period).",
			Unit: "endpoint",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(targetAttr, localityAttr),
						Value:      1, // value ignored...it is some value though...
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},

		// sleep and then poll for the metrics to expire...

		/*{
			Name: "grpc.lb.wrr.endpoint_weight_stale",
			Description: "EXPERIMENTAL. Number of endpoints from each scheduler update whose latest weight is older than the expiration period.",
			Unit: "endpoint",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(targetAttr, localityAttr),
						Value:      1, // value ignored...it is some value though...
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},*/
		{
			Name: "grpc.lb.wrr.endpoint_weights",
			Description: "EXPERIMENTAL. Weight of each endpoint, recorded on every scheduler update. Endpoints without usable weights will be recorded as weight 0.",
			Unit: "endpoint",
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes: attribute.NewSet(targetAttr, localityAttr),
						// Value:      1, // value ignored...it is some value though...
						// Bounds: , // what bounds did I pass up for this, I added the option DefaultBounds but does this ignore? What are the default bounds?
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
	}

	for _, metric := range wantMetrics {
		val, ok := gotMetrics[metric.Name]
		if !ok {
			t.Fatalf("Metric %v not present in recorded metrics", metric.Name)
		}
		// ignores non deterministic values...
		// I think labels are tested here though...target and locality seems like ignore value still checks labels but make sure...
		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreValue(),  metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			t.Fatalf("Metrics data type not equal for metric: %v", metric.Name)
		}
	}
}
