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
func setup(t *testing.T, tafOn bool, maf func(string) bool) (*metric.ManualReader /*or generic reader*/, *stubserver.StubServer) { // also return a cleanup?
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
		// did this ever pass the other one's equality or was I just looking at presence?
		// I see method: "other" and target: "whatever:///" and a single data point...
	}

}

// equality seems to work...

// pull out into setup?

// TestStaticMethod tests the method filtration client and server side. Methods
// that are static/registered client and server side should show up as an
// attribute in metrics, and methods that aren't (such as generic methods)
// shouldn't show up.
func (s) TestStaticMethod(t *testing.T) {
	// Figure out a way to trigger a method inside vs. outside
	// the buckets that are acceptable.

	// How to set a generic method client side? Can I plumb in/control method header?

	// How to set a generic method server side? Can I plumb in/control method header?

} // lighter weight test maybe? server side already have tested distinction?
// client is isStaticMethod call option (comes from generated code)
// server side is to read isStaticMethod call option...

// go get 1.24 (and delete my changes...)

// getting one other for unary and streaming vvv (does it need to match with stub server? need to register? seems wrong?)

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
		/*{
			Name:        "grpc.client.attempt.duration",
			Description: "End-to-end time taken to complete an RPC attempt including the time it takes to pick a subchannel.",
			// Unit: , // ignore this, since it isn't present. Yeah empty string so just don't set it
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr, statusAttr),
						// ignore start time/endtime
						Count: 1, // if you make more than one unary rpc call this should be 2
						// much others, fill out and see if you want it

						// how do bounds work and how is the count linked to bounds?
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr, targetAttr, statusAttr),
						// ignore start time/endtime
						Count: 1, // if you make more than one streaming rpc call this should be 2
						// much others, fill out and see if you want it

						// how do bounds work and how is the count linked to bounds?
						// default bounds due to api call, as no views set on the default meter provider passed in.

						Bounds: []float64{0, 5, 10, 25, 50, 75, 100, 250,
							500, 750, 1000, 2500, 5000, 7500, 10000},
						BucketCounts: []uint64{0x0, 0x1, // why is this deterministic? wtf? doesn't seem to align with min and max
							0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.Extrema[float64]{value:0.142856, valid:true}, // these are non deterministic. Should I do this in a seperate assertion? Like OpenCensus, needs to assert within certain bounds.
						Max: metricdata.Extrema[float64]{value:0.142856, valid:true},
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},*/ // seems like there's more values...need to test these somehow (buckets below 5?)
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

						Bounds: []float64{0, 5, 10, 25, 50, 75, 100, 250,
							500, 750, 1000, 2500, 5000, 7500, 10000},
						BucketCounts: []uint64{0x0, 0x0,
							0x0, 0x0, 0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
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
						Bounds: []float64{0, 5, 10, 25, 50, 75, 100, 250,
							500, 750, 1000, 2500, 5000, 7500, 10000},
						BucketCounts: []uint64{0x1, 0x0, // why is this deterministic? wtf? doesn't seem to align with min and max
							0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(0)), // these are non deterministic. Should I do this in a seperate assertion? Like OpenCensus, needs to assert within certain bounds.
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
			Data: metricdata.Histogram[int64]{ // should be deterministic for their assertions
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr, statusAttr),
						Count: 1, // if you make more than one streaming rpc call this should be 2

						// how do bounds work and how is the count linked to bounds?
						// default bounds due to api call, as no views set on the default meter provider passed in. Should I use a variable for default bounds?

						Bounds: []float64{0, 5, 10, 25, 50, 75, 100, 250,
							500, 750, 1000, 2500, 5000, 7500, 10000},
						BucketCounts: []uint64{0x0, 0x0, // why is this deterministic? wtf? doesn't seem to align with min and max
							0x0, 0x0, 0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						// Yup body with 10k both ways so this is how it works...
						Min: metricdata.NewExtrema(int64(57)), // these are non deterministic. Should I do this in a seperate assertion? Like OpenCensus, needs to assert within certain bounds.
						Max: metricdata.NewExtrema(int64(57)), // oh right it receives bytes from the server...the exact same though? Maybe
						Sum: 57, // this seems wrong (although deterministic, and could very well fall under bounds of OpenCensus tests)
					},
					// No data sent or received on stream so nothing recorded here...maybe add a send/recv msg on stream and see what happens
					{
						Attributes: attribute.NewSet(duplexMethodAttr, targetAttr, statusAttr),
						// ignore start time/endtime
						Count: 1, // if you make more than one streaming rpc call this should be 2
						// much others, fill out and see if you want it

						// how do bounds work and how is the count linked to bounds?
						// default bounds due to api call, as no views set on the default meter provider passed in.

						Bounds: []float64{0, 5, 10, 25, 50, 75, 100, 250,
							500, 750, 1000, 2500, 5000, 7500, 10000},
						BucketCounts: []uint64{0x1, 0x0, // why is this deterministic? wtf? doesn't seem to align with min and max
							0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(0)), // these are non deterministic. Should I do this in a seperate assertion? Like OpenCensus, needs to assert within certain bounds.
						Max: metricdata.NewExtrema(int64(0)),
						Sum: 0,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			}, // these are same values receives server side for these types of metrics too...(also don't build on go 1.17 due to generics)
		},
		/*{
			// grpc client call duration, same issue as client call duration typecast down and make manual assertions it's within 5 seconds
		},*/
		{
			// Use this name as key into map
			Name: "grpc.server.call.started",
			Description: "The total number of RPCs started, including those that have not completed.",
			Unit: "call", // do these get overwritten if set in the sdk?
			// Data: sum,/*metricdata.Sum{ // generics might require a higher version of go
			//DataPoints:
			//},
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr), // delete authority anyway
						Value: 1, // if you make more than one unary rpc this shoulddd be 2
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr),
						Value: 1, // if you make more than one streaming rpc this shoulddd be 2
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{ // Upgrade version...
			Name: "grpc.server.call.sent_total_compressed_message_size",
			Unit: "By",
			Description: "Total bytes (compressed but not encrypted) sent across all response messages (metadata excluded) per RPC; does not include grpc or transport framing bytes.",
			Data: metricdata.Histogram[int64]{ // should be deterministic for their assertions
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, statusAttr),
						// ignore start time/endtime
						Count: 1, // if you make more than one streaming rpc call this should be 2
						// much others, fill out and see if you want it

						// how do bounds work and how is the count linked to bounds?
						// default bounds due to api call, as no views set on the default meter provider passed in.

						Bounds: []float64{0, 5, 10, 25, 50, 75, 100, 250,
							500, 750, 1000, 2500, 5000, 7500, 10000},
						BucketCounts: []uint64{0x0, 0x0,
							0x0, 0x0, 0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(57)),
						Max: metricdata.NewExtrema(int64(57)),
						Sum: 57,
					},
					// No data sent or received on stream so nothing recorded here...maybe add a send/recv msg on stream and see what happens
					{
						Attributes: attribute.NewSet(duplexMethodAttr, statusAttr),
						// ignore start time/endtime does this happen in there assertion?
						Count: 1, // if you make more than one streaming rpc call this should be 2
						// much others, fill out and see if you want it

						// how do bounds work and how is the count linked to bounds?
						// default bounds due to api call, as no views set on the default meter provider passed in.

						Bounds: []float64{0, 5, 10, 25, 50, 75, 100, 250,
							500, 750, 1000, 2500, 5000, 7500, 10000},
						BucketCounts: []uint64{0x1, 0x0, // why is this deterministic? wtf? doesn't seem to align with min and max
							0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(0)), // these are non deterministic. Should I do this in a seperate assertion? Like OpenCensus, needs to assert within certain bounds.
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
			Data: metricdata.Histogram[int64]{ // should be deterministic for their assertions
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, statusAttr),
						// ignore start time/endtime
						Count: 1, // if you make more than one streaming rpc call this should be 2
						// much others, fill out and see if you want it

						// how do bounds work and how is the count linked to bounds?
						// default bounds due to api call, as no views set on the default meter provider passed in.

						Bounds: []float64{0, 5, 10, 25, 50, 75, 100, 250,
							500, 750, 1000, 2500, 5000, 7500, 10000},
						BucketCounts: []uint64{0x0, 0x0, // why is this deterministic? wtf? doesn't seem to align with min and max
							0x0, 0x0, 0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(57)), // these are non deterministic. Should I do this in a seperate assertion? Like OpenCensus, needs to assert within certain bounds.
						Max: metricdata.NewExtrema(int64(57)),
						Sum: 57, // this seems wrong (although deterministic, and could very well fall under bounds of OpenCensus tests)
					},
					// No data sent or received on stream so nothing recorded here...maybe add a send/recv msg on stream and see what happens
					{
						Attributes: attribute.NewSet(duplexMethodAttr, statusAttr),
						// ignore start time/endtime
						Count: 1, // if you make more than one streaming rpc call this should be 2
						// much others, fill out and see if you want it

						// how do bounds work and how is the count linked to bounds?
						// default bounds due to api call, as no views set on the default meter provider passed in.

						Bounds: []float64{0, 5, 10, 25, 50, 75, 100, 250,
							500, 750, 1000, 2500, 5000, 7500, 10000},
						BucketCounts: []uint64{0x1, 0x0, // why is this deterministic? wtf? doesn't seem to align with min and max
							0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min: metricdata.NewExtrema(int64(0)), // these are non deterministic. Should I do this in a seperate assertion? Like OpenCensus, needs to assert within certain bounds.
						Max: metricdata.NewExtrema(int64(0)),
						Sum: 0,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},

		// Duration same issue as client two durations, figure out tmrw. still need to figure out
		// duration.

		// I think for durations - it's a histogram - should take place within 5
		// seconds (according to default bounds, which are stable I guess).


	}

	for _, metric := range wantMetrics {
		if metric.Name == "grpc.server.call.sent_total_compressed_message_size" {
			// Sync the metric reader to see the event because stats.End is
			// handled async server side. Thus, poll until it shows up. Once
			// this first server side metric triggered by stats.End shows up,
			// all the rest will be synced and ready to go.
			if gotMetrics, err = waitForServerCompletedRPCs(ctx, reader, metric, t); err != nil { // I still think you need this. Wait technically need a sync point for all.
				t.Fatalf("error waiting for sent total compressed message size for metric: %v", metric.Name)
			}
		}

		

		val, ok := gotMetrics[metric.Name]
		if !ok {
			t.Fatalf("metric %v not present in recorded metrics", metric.Name)
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
	if _, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{Payload: &testpb.Payload{
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
	}
	rm = &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm)
	gotMetrics = map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}

	for _, metric := range wantMetrics {
		if metric.Name == "grpc.server.call.sent_total_compressed_message_size" {
			// Sync the metric reader to see the event because stats.End is
			// handled async server side. Thus, poll until it shows up. Once
			// this first server side metric triggered by stats.End shows up,
			// all the rest will be synced and ready to go.
			if gotMetrics, err = waitForServerCompletedRPCs(ctx, reader, metric, t); err != nil { // I still think you need this. Wait technically need a sync point for all.
				t.Fatalf("error waiting for sent total compressed message size for metric: %v", metric.Name)
			}
		}
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
