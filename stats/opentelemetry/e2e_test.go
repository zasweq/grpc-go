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
	"google.golang.org/grpc/experimental/stats"
	"google.golang.org/grpc/internal/stats/instrumentregistry"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/stubserver"
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
	provider := metric.NewMeterProvider(
		metric.WithReader(reader),
	)
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
			Metrics:               opentelemetry.DefaultMetrics,
			MethodAttributeFilter: methodAttributeFilter,
		}})}, opentelemetry.DialOption(opentelemetry.Options{
		MetricsOptions: opentelemetry.MetricsOptions{
			MeterProvider: provider,
			Metrics:       opentelemetry.DefaultMetrics,
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
func (s) TestAllMetricsOneFunction(t *testing.T) { // Switch to All Per Call Metrics?
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

func (s) TestDefaultMetrics(t *testing.T) {
	// register default metric

	// unset gets default metric...

	// and record on it...test below can make default

	// otherwise need to explicitly specify and scale up...

	// adding or removing to default?
}

// document required to call clear for every test that reigstered with inst
// registry Doug might want to set to before? return snapshot or something
// (mentorship)

func (s) TestNonPerCallMetrics(t *testing.T) {
	// Create new instruments which can be verified on (look at inst. registry tests for inspiration as to what can verify?)
	intCountHandle := instrumentregistry.RegisterInt64Count("some counter", "Number of calls from test", "calls", []string{"some label key"}, []string{"some optional label key"}, true) // basic count...make this a helper nah it's fine


	// Same options as usual? Manual everything...

	// Create OTel - will create instruments based on ^^^
	opentelemetry.DialOption() // create it here - doesn't need per call callouts since will call below...
	// Above returns something that contains 3 under the hood, could also just reach into ssh directly...

	// same options as passed above...

	// pass it here, just use this directly...
	ssh := &serverStatsHandler{options: o} // doesn't have access to this - so don't make e2e test?
	ssh.initializeMetrics() // move to OTel package if want to access this symbol...




	// this returns a dial option with 3 (interceptor, interceptor, stats
	// handler) <- need a ref to stats handler, how to typecast downward?

	// in client conn this will be able to typecast downward because of sh global + local...
	// convert otel somehow to vvv (not a helper to look into it)
	var mr stats.MetricsRecorder

	mr.RecordIntCount(intCountHandle, []stats.Label{{Key: "some label key", Value: "some label val"}}, []stats.Label{{Key: "some optional label key", Value: "some optional label val"}}, 1) // labels get eaten a a lower level, so this level is assumed to get the right labels...


	// Pass sh downward in a list like I had?



	// Typecast this down to MetricsRecorder()...will teach me how to typecast this in client and server


	// record non per call metrics on OTel...



	// Do I need to poll? Is it eventually consistent or can I just check I
	// think latter since record doesn't come in goroutine just happens sync
	// above...

	// on manual reader, check like metrics atoms below, need to make the
	// metrics atoms reusable/extensible

}

// Maybe just treat this like a smoke test, and leave full plumbing/specifics to RLS/Pick First Metrics or something like that:
// 1. Default Metrics Logic
// 2. Maybe record one of each metric and expect it (merge with defaults somehow)...
// including gauges which will be just the most recent recording point...



// I think this is same thing but metrics set isn't moved yet...

func (s) CreateMetricsAtoms() {
	// What is an atom corresponding to non per call metric...

	// See per call for examples of counts/gauges

	// after recording expect atoms...should this helper take interesting options...what are options to plumb through


	// reuse for rls and pick first tests, but that might be best defined by inlining the expectations...


	// Concretely here's what one looks like:
	/*
	{
				Name:        "grpc.client.call.duration",
				Description: "Time taken by gRPC to complete an RPC from application's perspective.",
				Unit:        "s",
				Data: metricdata.Histogram[float64]{
					DataPoints: []metricdata.HistogramDataPoint[float64]{
						{
							Attributes: attribute.NewSet(methodAttr, targetAttr, statusAttr),
							Count:      1,
							Bounds:     DefaultLatencyBounds,
						},
					},
					Temporality: metricdata.CumulativeTemporality,
				},
			},
	*/

	metricdata.Metrics{
		Name: "some counter",
		Description: "Number of calls from test",
		Unit: "calls",
		Data: metricdata.Sum[int64]{
			DataPoints: []metricdata.DataPoint[int64]{
				{
					// Key value pairs pass keys to inst registry...
					Attributes: attribute.NewSet(unaryMethodAttr), // attibutes come from labels so arbitrary
					Value:      1,
				},
				{
					Attributes: attribute.NewSet(duplexMethodAttr), // attibutes come from labels so arbitrary
					Value:      1,
				},
			},
			Temporality: metricdata.CumulativeTemporality,
			IsMonotonic: true,
		},
	}


	// Gauge is just current value?
}

// Could I merge stuff with the testutils?


