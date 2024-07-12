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
			Metrics:               opentelemetry.DefaultPerCallMetrics,
			MethodAttributeFilter: methodAttributeFilter,
		}})}, opentelemetry.DialOption(opentelemetry.Options{
		MetricsOptions: opentelemetry.MetricsOptions{
			MeterProvider: provider,
			Metrics:       opentelemetry.DefaultPerCallMetrics,
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

// Same thing right pass a handle except method is not on handle so you pass the
// OTel thing you create...

func (s) TestMetricsRegistryMetrics(t *testing.T) {
	// Internal linkage to that clear helper thingy...
	// internal.ClearMetricsRegistryForTesting.(t)

	// Only difference is don't start from fresh slate, start from an instrument
	// registry with stuff already in it, but test should be robust enough where
	// that doesn't affect it...



	// Register instruments...could test defaults vs. not logic form this register call...
	intCountHandle1 := stats.RegisterInt64Count(stats.MetricDescriptor{
		Name: "int counter 1",
		Description: "Sum of calls from test",
		Unit: "int",
		Labels: []string{"int counter 1 key"}, // These show up in emissions...so test this...
		OptionalLabels: []string{"int counter 1 label key"},
		Default: true,
	}) // estats?

	// non default metric.
	// If not specified in OpenTelemetry constructor, this will become a no-op,
	// so measurements recorded on it won't show up in emitted metrics.
	intCountHandle2 := stats.RegisterInt64Count(stats.MetricDescriptor{
		Name: "int counter 2",
		Description: "Sum of calls from test",
		Unit: "int",
		Labels: []string{"int counter 2 key"}, // These show up in emissions...so test this...
		OptionalLabels: []string{"int counter 2 label key"},
		Default: false,
	})

	// Register another non default metric. This will get added to the default
	// metrics set in the OpenTelemetry constructor options, so metrics recorded
	// on this should show up in metrics emissions.

	// Test DefaultMetricsAdd a string so maybe a third intCountHandle
	intCountHandle3 := stats.RegisterInt64Count(stats.MetricDescriptor{
		Name: "int counter 3",
		Description: "Sum of calls from test",
		Unit: "int",
		Labels: []string{"int counter 3 key"}, // These show up in emissions...so test this...
		OptionalLabels: []string{"int counter 3 label key"},
		Default: false,
	})
	// Register the other 4 types...test all emissions just to make sure plumbing works...test more than one label emission?
	floatCountHandle1 := stats.RegisterFloat64Count(stats.MetricDescriptor{
		Name: "float counter 1",
		Description: "Sum of calls from test",
		Unit: "float",
		Labels: []string{"float counter key"}, // There's a lot of knobs for emissions, I think just hardcode one per test....
		OptionalLabels: []string{"float counter label key"},
		Default: true,
	})
	intHistoHandle1 := stats.RegisterInt64Histo(stats.MetricDescriptor{
		Name:           "int histo",
		Description:    "sum of all emissions from tests",
		Unit:           "int",
		Labels:         []string{"int histo label"},
		OptionalLabels: []string{"int histo optional label"},
		Default:        false,
	})
	floatHistoHandle1 := stats.RegisterFloat64Histo(stats.MetricDescriptor{
		Name:           "float histo",
		Description:    "sum of all emissions from tests",
		Unit:           "float",
		Labels:         []string{"float histo label"},
		OptionalLabels: []string{"float histo optional label"},
		Default:        false,
	})
	intGaugeHandle1 := stats.RegisterInt64Gauge(stats.MetricDescriptor{
		Name:           "simple gauge",
		Description:    "the most recent int emitted by test",
		Unit:           "int",
		Labels:         []string{"int gauge label"},
		OptionalLabels: []string{"int gauge optional label"},
		Default:        false,
	})

	// Oh and takes pointers now...

	// and add that to default set other 4 have test for plumbing...create top
	// level comment that describes sccenario...or maybe wait until after it
	// works...

	opts := opentelemetry.MetricsOptions{
		Context: context.Background(), // Should this have timeout might fail vet
		Metrics: opentelemetry.DefaultMetrics().Add(stats.Metric("int counter 3")),
		OptionalLabels: []string{}, // Setup some optional labels from tests...
	}

	// Create an OTel plugin...use defaultMetrics() runtime, we'll see what Doug
	// says about leaving stuff unexported...
	// How to test client/server split - or say they hit the same helpers
	ssh := &serverStatsHandler{options: opts}
	ssh.initializeMetrics() // need to do context.Background() dance too, nah set in options...initalize reads the global registry...



	// Don't have access to ssh in dial option, so just create one directly

	// Pass that OTel plugin to handle...

	// When it hits make sure simulates layer below of eating labels...

	intCountHandle1.Record(fmr, 1, []string{"int counter 1 label value", "int counter 1 optional label value"}...)
	// Not part of metrics specified (not default), so this call shouldn't show up in emissions...
	intCountHandle2.Record(fmr, 2, []string{"int counter 2 label value", "int counter 2 optional label value"}...)
	// Part of metrics specified, so this call should show up in emissions...
	intCountHandle3.Record(fmr, 4, []string{"int counter 3 label value", "int counter 3 optional label value"}...) // record the same values...

	// These recording points should show up in emissions as they are defaults...

	// Test permutations of optional label values logic...configure only some, only those should show up...
	floatCountHandle1.Record(fmr, 1.2, []string{"float counter label value", "float counter optional label value"}...) // these labels should be specific, make specific
	intHistoHandle1.Record(fmr, 3, []string{"int histo label value", "int histo optional label value"}...)
	floatHistoHandle1.Record(fmr, 4.3, []string{"float histo label value", "float histo optional label value"}...)
	intGaugeHandle1.Record(fmr, 7, []string{"int histo label value", "int histo optional label value"}...) // size should match, below does this for us no need to verify...
	// In metrics registry test: (label values get tested in emission...)

	// other 4:
	// float count handle is 1.2
	// int histo handle is 3
	// float histo handle is 4.3
	// int gauge handle is 7
	// buckets and also label keys and values get attached...I could also squash and rebase onto instrument registry...
	// for the getters and the typecasts of handle (just do the typecasts in this layer I guess)

	// Yeah so label keys/vals unconditional...
	// optional label key/vals conditional...




	// check metrics atoms...probably define metrics atoms inline...can't use
	// mock stats handler here because this is the stats handler will learn how
	// to define metris atoms

}

// What are input variables?
// 5 different types such as count, histo, and gauge


func (s) CreateMetricsAtoms() { // What knobs, what do I pass back or just do this inline...

}


// Mark:

// mock test - hits stats handler
// e2e test - real otel emissions, so keep this layer metrics emissions light and just test plumbing...
// what unit tests are worth testing here? DefaultMetrics test? Or incorporate that into e2e
// Try to record and expect no metrics atom because becomes a noop



// I think handles changed and getter changed so will have to base off passed in user set for iteration (noops becomes !- nil checks does this merge with per call well)
// If want to do it the other way expose keys (set already built out or a slice) for no-op way...can talk to Doug about this...

// Tests are orthogonal so will always be applicable


