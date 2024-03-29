/*
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
 */

package opentelemetry

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/metric/metricdata/metricdatatest"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/stubserver"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

var defaultTestTimeout = 5 * time.Second

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// waitForServerCompletedRPCs waits until the unary and streaming stats.End
// calls are finished processing. It does this by waiting for the expected
// metric triggered by stats.End to appear through the passed in metrics reader.
func waitForServerCompletedRPCs(ctx context.Context, reader metric.Reader, wantMetric metricdata.Metrics, t *testing.T) (map[string]metricdata.Metrics, error) {
	for ; ctx.Err() == nil; <-time.After(time.Millisecond) {
		rm := &metricdata.ResourceMetrics{}
		reader.Collect(ctx, rm)
		gotMetrics := map[string]metricdata.Metrics{}
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				gotMetrics[m.Name] = m
			}
		}
		val, ok := gotMetrics[wantMetric.Name]
		if !ok {
			continue
		}
		// their package has good assertions on their data types.
		// use their assertions, only on subset we want and ignore fields we don't want
		if !metricdatatest.AssertEqual(t, wantMetric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			continue
		} // coment what this actually asserts on? the assertions do work though
		return gotMetrics, nil
	}
	return nil, fmt.Errorf("error waiting for metric %v: %v", wantMetric, ctx.Err())
}

// return reader and also stub server...
// setup does and returns...
func setup(t *testing.T, tafOn bool, maf func(string) bool) (*metric.ManualReader, *stubserver.StubServer) { // also return a cleanup? user can do it
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(
		metric.WithReader(reader),
	)
	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{Payload: &testpb.Payload{
				Body: make([]byte, 10000),
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
	var taf func(string) bool
	if tafOn {
		taf = func(str string) bool {
			if str == ss.Target {
				return false
			}
			return true
		}
	}
	if err := ss.Start([]grpc.ServerOption{ServerOption(MetricsOptions{
		MeterProvider: provider,
		Metrics:       DefaultServerMetrics,
		TargetAttributeFilter: taf,
		MethodAttributeFilter: maf,
	})}, DialOption(MetricsOptions{
		MeterProvider: provider,
		Metrics:       DefaultClientMetrics,
		TargetAttributeFilter: taf,
		MethodAttributeFilter: maf,
	})); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	return reader, ss
}

// TestMethodTargetAttributeFilter tests the method and target attribute filter.
// The method and target filter set should bucket the grpc.method/grpc.target
// attribute into "other" if filter specifies.
func (s) TestMethodTargetAttributeFilter(t *testing.T) {
	maf := func(str string) bool {
		if str == "/grpc.testing.TestService/UnaryCall" {
			return false
		}
		// Will allow duplex/any other type of RPC.
		return true
	}
	// pull out setup into a helper
	reader, ss := setup(t, true, maf)
	defer ss.Stop()

	// make a single RPC (unary rpc), and filter out the target and method
	// that would correspond.
	// on a basic metric
	// client metric started (and client end metrics) have method and target
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
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
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}
	rm := &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm)

	// "other" for when method and target attribute filter specify too.
	wantMetrics := []metricdata.Metrics{
		{
			Name: "grpc.client.attempt.started",
			Description: "The total number of RPC attempts started, including those that have not completed.",
			Unit: "attempt",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(attribute.String("grpc.method", "other"), attribute.String("grpc.target", "other")),
						Value: 1, // if you make more than one unary rpc this shoulddd be 2
					},
					{
						Attributes: attribute.NewSet(attribute.String("grpc.method", "grpc.testing.TestService/FullDuplexCall"), attribute.String("grpc.target", "other")),
						Value: 1, // if you make more than one streaming rpc this shoulddd be 2
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		// could do another metric for good measure...
	} // is it eventually consistent on the started? does it not block right on the started?
	// no need to sync anything because started happens sync? if async need to
	// poll until it happens, sync point in main testing goroutine.
	gotMetrics := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}

	for _, metric := range wantMetrics {
		val, ok := gotMetrics[metric.Name]
		if !ok {
			t.Fatalf("metric %v not present in recorded metrics", metric.Name)
		}
		// their package has good assertions on their data types.
		// use their assertions, only on subset we want and ignore fields we don't want
		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			t.Fatalf("metrics data type not equal for metric: %v", metric.Name)
		}
	}

}

// equality seems to work...

// pull out into setup?

// TestStaticMethod tests the method filtration client and server side. Methods
// that are static/registered client and server side should show up as an
// attribute in metrics, and methods that aren't (such as generic methods)
// shouldn't show up.
/*
func (s) TestStaticMethod(t *testing.T) {
	reader, ss := setup(t, false, nil)
	defer ss.Stop()

	// full server option for grpc.UnknownServiceHandler plumb this in
	// can I invoke into an unknown? at least hit started
	// merge this with assertion of second?

	//
	grpc.StreamHandler()
	// poll for all metrics that come from End
	// also loop through under 5s for the duration...maybe second pass lightweight

	/*
	FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
				for {
					_, err := stream.Recv()
					if err == io.EOF {
						return nil
					}
				}
			},


	// invoke client side for "unknown" method - if unset here will get an unimplemented back but will still get a
	// RPC, as stats.InHeader comes before

	streamHandler := func (srv any, stream grpc.ServerStream) { // where does it get callout - unregistered doesn't it come out from transport?
		// doesn't do anything with it - this is orthogonal to started though right started comes before
	}
	// same context you pass into generated stubs...
	grpc.UnknownServiceHandler()
	// unimplemented_method_name should become "other"

	/*
	te.unknownHandler = func(srv any, stream grpc.ServerStream) error {
			method, ok = grpc.MethodFromServerStream(stream)
			return nil
	}


	ss.CC.Invoke(ctx, "unimplemented_method_name", nil, nil, nil)

	// if can add handlers to server, can also do this as part of my second
	// RPC's in full test below...


	// what to expose server side? UnknownServiceHandler...

	// server option, make client and server option configurable, switch setup below to use that too

	// invoke and start an RPC Client Side that hits the UnknownServiceHandler started server side?



	// irrespective of how I plumb "genericmethodhandler",
	// I'll have to plumb the dial and server option of otel in,
	// can I just use mock?

	// Figure out a way to trigger a method inside vs. outside
	// the buckets that are acceptable.

	// How to set a generic method client side? Can I plumb in/control method header?

	// How to set a generic method server side? Can I plumb in/control method header?

	// All I need here is normal metrics stuff wanted but with the attribute for method bucked into "other".

	// Below tests that :method is ok and doesn't get bucketed into "other".
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(
		metric.WithReader(reader),
	)

	// cc.Invoke string for method

	// register a handler for it
	// or unregistered method handler grpc.UnregisteredMethodHandler (path for Invoke)
	// examples maybe for server side...

	// Can I use unregistered method handler in this case? Or does it not need to go through stub server...

	// setup with


	// a single unary RPC for client and server

	// declare the want metric as
	// started as "other" on client and server

	// OpenTelemetry should bucket unregistered methods into "other".
	otherMethodAttr := attribute.String("grpc.method", "other")
	targetAttr := attribute.String("grpc.target", ss.Target) // can I use ss here? can I plumb unregistered method handler through stub server?
	// statusAttr := attribute.String("grpc.status", "OK")
	wantMetrics := []metricdata.Metrics{
		{
			Name: "grpc.client.attempt.started",
			Description: "The total number of RPC attempts started, including those that have not completed.",
			Unit: "attempt",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(otherMethodAttr, targetAttr),
						Value: 1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{
			Name: "grpc.server.call.started",
			Description: "The total number of RPCs started, including those that have not completed.",
			Unit: "call",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(otherMethodAttr),
						Value: 1,
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
			t.Fatalf("metric %v not present in recorded metrics", metric.Name)
		}
		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			t.Fatalf("metrics data type not equal for metric: %v", metric.Name)
		} // what does assert equal actually check...this is what they use...where to make this comment
	}
} // lighter weight test maybe? server side already have tested distinction?
// client is isStaticMethod call option (comes from generated code)
// server side is to read isStaticMethod call option...
 */


// getting one other for unary and streaming vvv (does it need to match with stub server? need to register? seems wrong?)
func assertDataPointWithinFiveSeconds(metric metricdata.Metrics) error {
	histo, ok := metric.Data.(metricdata.Histogram[float64])
	if !ok {
		return fmt.Errorf("metric data is not histogram")
	}
	for _, dataPoint := range histo.DataPoints {
		// 0 1 5
		var boundWithFive int
		for i, bucket := range dataPoint.Bounds {
			if bucket >= 5 {
				boundWithFive = i
			}
		}
		foundPoint := false
		for i, bucket := range dataPoint.BucketCounts {
			if i >= boundWithFive {
				return fmt.Errorf("data point not found in bucket <=5 seconds")
			}
			if bucket == 1 {
				foundPoint = true
				break
			}
		}
		if !foundPoint {
			return fmt.Errorf("no data point found for metric")
		}
	}
	return nil
}

// TestAllMetricsOneFunction tests emitted metrics from OpenTelemetry
// instrumentation component. It then configures a system with a gRPC Client and
// gRPC server with the OpenTelemetry Dial and Server Option configured
// specifying all the metrics provided by this package, and makes a Unary RPC
// and a Streaming RPC. These two RPCs should cause certain recording for each
// registered metric observed through a Manual Metrics Reader on the provided
// OpenTelemetry SDK's Meter Provider.
func (s) TestAllMetricsOneFunction(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(
		metric.WithReader(reader),
	)

	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			return &testpb.SimpleResponse{Payload: &testpb.Payload{
				Body: make([]byte, 10000),
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
	if err := ss.Start([]grpc.ServerOption{ServerOption(MetricsOptions{
		MeterProvider: provider,
		Metrics:       DefaultServerMetrics,
	})}, DialOption(MetricsOptions{
		MeterProvider: provider,
		Metrics:       DefaultClientMetrics,
	})); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
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
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}

	rm := &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm)

	gotMetrics := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}

	unaryMethodAttr := attribute.String("grpc.method", "grpc.testing.TestService/UnaryCall") // could pull name into var but I think this is only place you use it
	duplexMethodAttr := attribute.String("grpc.method", "grpc.testing.TestService/FullDuplexCall")

	targetAttr := attribute.String("grpc.target", ss.Target)
	statusAttr := attribute.String("grpc.status", "OK")

	// Can either declare all inline or declare as 4 variables (the four possible combinations (or append it)
	attribute.NewSet(unaryMethodAttr, targetAttr) // Do I need to throw this in based off 6 ^^^^ or could just make 4 of these
	attribute.NewSet(duplexMethodAttr, targetAttr) // can't reuse these actually can but I think better to declare inline...how to make sure these are correct? needs to go through cmp.Diff
	// client started RPCs tags ^^^

	attribute.NewSet(unaryMethodAttr, targetAttr, statusAttr) // statusOKAttr
	attribute.NewSet(duplexMethodAttr, targetAttr, statusAttr)
	// client histogram tags ^^^

	attribute.NewSet(unaryMethodAttr)
	attribute.NewSet(duplexMethodAttr)
	// server started RPCs tags ^^^

	attribute.NewSet(unaryMethodAttr, statusAttr)
	attribute.NewSet(unaryMethodAttr, statusAttr)
	// server histogram tags ^^^

	wantMetrics := []metricdata.Metrics{
		{
			Name: "grpc.client.attempt.started",
			Description: "The total number of RPC attempts started, including those that have not completed.",
			Unit: "attempt", // do these get overwritten if set in the sdk?
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr),
						Value: 1, // if you make more than one unary rpc this shoulddd be 2 - could add to test below
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr, targetAttr),
						Value: 1, // if you make more than one streaming rpc this shoulddd be 2
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{
			Name:        "grpc.client.attempt.duration",
			Description: "End-to-end time taken to complete an RPC attempt including the time it takes to pick a subchannel.",
			Unit: "s",
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr, statusAttr),
						Count: 1,
						Bounds: DefaultLatencyBounds,
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr, targetAttr, statusAttr),
						Count: 1,
						Bounds: DefaultLatencyBounds,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name: "grpc.client.attempt.sent_total_compressed_message_size",
			Description: "Total bytes (compressed but not encrypted) sent across all request messages (metadata excluded) per RPC attempt; does not include grpc or transport framing bytes.",
			Unit: "By",
			Data: metricdata.Histogram[int64]{
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr, statusAttr),
						// ignore start time/endtime (does this happen as part of equal?)
						Count: 1, // if you make more than one streaming rpc call this should be 2
						// much others, fill out and see if you want it

						// how do bounds work and how is the count linked to bounds?
						// default bounds due to api call, as no views set on the default meter provider passed in.

						Bounds: DefaultSizeBounds,
						BucketCounts: []uint64{0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(57)),
						Max: metricdata.NewExtrema(int64(57)),
						Sum: 57,
					},
					// No data sent or received on stream so nothing recorded here...maybe add a send/recv msg on stream and see what happens
					{
						Attributes: attribute.NewSet(duplexMethodAttr, targetAttr, statusAttr),
						// ignore start time/endtime
						Count: 1, // if you make more than one streaming rpc call this should be 2

						// how do bounds work and how is the count linked to bounds?
						// default bounds due to api call, as no views set on the default meter provider passed in.
						Bounds: DefaultSizeBounds, // is this milliseconds? 5000 won't be enough or is it < 5 seconds. Should user set these bounds?
						BucketCounts: []uint64{0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(0)),
						Max: metricdata.NewExtrema(int64(0)),
						Sum: 0,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name: "grpc.client.attempt.rcvd_total_compressed_message_size",
			Description: "Total bytes (compressed but not encrypted) received across all response messages (metadata excluded) per RPC attempt; does not include grpc or transport framing bytes.",
			Unit: "By",
			Data: metricdata.Histogram[int64]{
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr, statusAttr),
						Count: 1, // if you make more than one streaming rpc call this should be 2
						Bounds: DefaultSizeBounds,
						BucketCounts: []uint64{0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(57)),
						Max: metricdata.NewExtrema(int64(57)),
						Sum: 57,
					},
					// No data sent or received on stream so nothing recorded here...maybe add a send/recv msg on stream and see what happens
					{
						Attributes: attribute.NewSet(duplexMethodAttr, targetAttr, statusAttr),
						// ignore start time/endtime
						Count: 1, // if you make more than one streaming rpc call this should be 2

						Bounds: DefaultSizeBounds,
						BucketCounts: []uint64{0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(0)),
						Max: metricdata.NewExtrema(int64(0)),
						Sum: 0,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			}, // these are same values receives server side for these types of metrics too...(also don't build on go 1.17 due to generics)
		},
		{
			Name:        "grpc.client.call.duration",
			Description: "This metric aims to measure the end-to-end time the gRPC library takes to complete an RPC from the application’s perspective.",
			Unit: "s",
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr, statusAttr),
						Count: 1,
						Bounds: DefaultLatencyBounds,
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr, targetAttr, statusAttr),
						Count: 1,
						Bounds: DefaultLatencyBounds,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name: "grpc.server.call.started",
			Description: "The total number of RPCs started, including those that have not completed.",
			Unit: "call",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr),
						Value: 1,
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr),
						Value: 1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{
			Name: "grpc.server.call.sent_total_compressed_message_size",
			Unit: "By",
			Description: "Total bytes (compressed but not encrypted) sent across all response messages (metadata excluded) per RPC; does not include grpc or transport framing bytes.",
			Data: metricdata.Histogram[int64]{
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, statusAttr),
						Count: 1,
						Bounds: DefaultSizeBounds, // just assert < 5000
						BucketCounts: []uint64{0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(57)),
						Max: metricdata.NewExtrema(int64(57)),
						Sum: 57,
					},
					// No data sent or received on stream so nothing recorded here...maybe add a send/recv msg on stream and see what happens
					{
						Attributes: attribute.NewSet(duplexMethodAttr, statusAttr),
						Count: 1,
						Bounds: DefaultSizeBounds,
						BucketCounts: []uint64{0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(0)),
						Max: metricdata.NewExtrema(int64(0)),
						Sum: 0,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name: "grpc.server.call.rcvd_total_compressed_message_size",
			Unit: "By",
			Description: "Total bytes (compressed but not encrypted) received across all request messages (metadata excluded) per RPC; does not include grpc or transport framing bytes.",
			Data: metricdata.Histogram[int64]{
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, statusAttr),
						Count: 1,
						Bounds: DefaultSizeBounds,
						BucketCounts: []uint64{0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(57)),
						Max: metricdata.NewExtrema(int64(57)),
						Sum: 57,
					},
					// No data sent or received on stream so nothing recorded here...maybe add a send/recv msg on stream and see what happens
					{
						Attributes: attribute.NewSet(duplexMethodAttr, statusAttr),
						// ignore start time/endtime
						Count: 1, // if you make more than one streaming rpc call this should be 2
						// much others, fill out and see if you want it

						// how do bounds work and how is the count linked to bounds?
						// default bounds due to api call, as no views set on the default meter provider passed in.

						Bounds: DefaultSizeBounds,
						BucketCounts: []uint64{0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(0)),
						Max: metricdata.NewExtrema(int64(0)),
						Sum: 0,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name:        "grpc.server.call.duration",
			Description: "This metric aims to measure the end2end time an RPC takes from the server transport’s (HTTP2/ inproc) perspective.",
			Unit: "s",
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, statusAttr),
						Count: 1,
						Bounds: DefaultLatencyBounds,
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr, statusAttr),
						Count: 1,
						Bounds: DefaultLatencyBounds,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
	}

	for _, metric := range wantMetrics {
		if metric.Name == "grpc.server.call.sent_total_compressed_message_size" || metric.Name == "grpc.server.call.rcvd_total_compressed_message_size" {
			// Sync the metric reader to see the event because stats.End is
			// handled async server side. Thus, poll until it shows up. Once
			// this first server side metric triggered by stats.End shows up,
			// all the rest will be synced and ready to go.
			if gotMetrics, err = waitForServerCompletedRPCs(ctx, reader, metric, t); err != nil { // I still think you need this. Wait technically need a sync point for all.
				t.Fatalf("error waiting for sent total compressed message size for metric: %v", metric.Name)
			} // you need to wait for all of these actually.
			continue
		}
		// if metric.Name == one of the three duration metrics
		//        loop through bounds and make sure all < 5 seconds buckets... (maybe helper)
		//        either ignore the buckets field and manually check and send to assert equal, or just compare bounds
		metricdatatest.IgnoreValue() // ignore the value, and then manually assert on it

		// If one of the duration metrics, ignore the bucket counts, and make
		// sure it falls within a count < 5 seconds (maximum duration of test
		// due to context).

		val, ok := gotMetrics[metric.Name]
		if !ok {
			t.Fatalf("metric %v not present in recorded metrics", metric.Name)
		}
		if metric.Name == "grpc.client.attempt.duration" || metric.Name == "grpc.client.call.duration" || metric.Name == "grpc.server.call.duration" {
			if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars(), metricdatatest.IgnoreValue()) {
				t.Fatalf("metrics data type not equal for metric: %v", metric.Name)
			}
			if err := assertDataPointWithinFiveSeconds(val); err != nil {
				t.Fatalf("Data point not within five seconds for metric %v: %v", metric.Name, err)
			}
			continue
		}

		// their package has good assertions on their data types.
		// use their assertions, only on subset we want and ignore fields we don't want
		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			t.Fatalf("metrics data type not equal for metric: %v", metric.Name)
		} // what does assert equal actually check...this is what they use...
	}

	// two data points under 5 seconds...

	// Make another unary and streaming call. This should should up in the
	// metrics. (copy over once cleaned up and double it).


	/*if _, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{Payload: &testpb.Payload{
		Body: make([]byte, 10000),
	}}, grpc.UseCompressor(gzip.Name)); err != nil { // deterministic compression from OpenCensus test...still need it because one of main metrics in OTel is compressed metrics
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}
	stream, err = ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend()
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}*/

	// after below need to add default bounds then poll for all 3 ends, then add loop for < 5s (first task blocks this)

	// started is sync, so no need to poll

	// This Invoke doesn't pass the StaticMethodCallOption. Thus, it should
	// become "other" on client side metrics. Since it is also not registered on
	// the server either, it should also become "other" on the server.
	err = ss.CC.Invoke(ctx, "/grpc.testing.TestService/UnregisteredCall", nil, nil, []grpc.CallOption{}...) // handle the error or just use it?
	t.Log(err)


	rm = &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm)
	gotMetrics = map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}
	otherMethodAttr := attribute.String("grpc.method", "other")
	wantMetrics = []metricdata.Metrics{
		{
			Name: "grpc.client.attempt.started",
			Description: "The total number of RPC attempts started, including those that have not completed.",
			Unit: "attempt",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr),
						Value: 1, // if you make more than one unary rpc this shoulddd be 2 - could add to test below
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr, targetAttr),
						Value: 1, // if you make more than one streaming rpc this shoulddd be 2
					},
					{
						Attributes: attribute.NewSet(otherMethodAttr, targetAttr),
						Value: 1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{
			Name: "grpc.server.call.started",
			Description: "The total number of RPCs started, including those that have not completed.",
			Unit: "call",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr),
						Value: 1,
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr),
						Value: 1,
					},
					{
						Attributes: attribute.NewSet(otherMethodAttr),
						Value: 1,
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
			t.Fatalf("metric %v not present in recorded metrics", metric.Name)
		}
		// their package has good assertions on their data types.
		// use their assertions, only on subset we want and ignore fields we don't want
		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			t.Fatalf("metrics data type not equal for metric: %v", metric.Name)
		}
	}
}

// cleanup, answer any open questions such as test multiple RPC's?
// make vet happy, if happy then leave todo
