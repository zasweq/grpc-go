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

// also need to get generated code (regenerate.sh is all I think) working



// waitForServerCompletedRPCs waits until the unary and streaming stats.End
// calls are finished processing (from the want metrics passed in)
func waitForServerCompletedRPCs(ctx context.Context, provider *metric.MeterProvider, reader metric.Reader, wantMetric metricdata.Metrics, t *testing.T) (map[string]metricdata.Metrics, error) {
	// poll seen metrics. The row length should be 2.
	for ; ctx.Err() == nil; <-time.After(time.Millisecond) { // I do need this sync point, but it's just not showing up in the metrics readers map. and I don't set any default views. I also see it happen, but it doesn't work
		// poll until two rows found with the distinct names...
		// provider.ForceFlush(ctx) // even flushing doesn't work, it doesn't get otel data...
		// 5 maximum?
		rm := &metricdata.ResourceMetrics{} // can I do this or just declare a pointer? I think this is fine allocates the memory?
		reader.Collect(ctx, rm)
		newMapToBuildOut /*ForFastAccess :) */ := map[string]metricdata.Metrics{}
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				print("new map name: ", m.Name, "\n")
				newMapToBuildOut[m.Name] = m
			}
		}
		print("length of map to build out: ", len(newMapToBuildOut), "\n")
		val, ok := newMapToBuildOut[wantMetric.Name]
		if !ok {
			print(wantMetric.Name," not found in new map\n")
			continue
		}
		// their package has good assertions on their data types.
		// use their assertions, only on subset we want and ignore fields we don't want
		if !metricdatatest.AssertEqual(t, wantMetric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			continue
		}
		return newMapToBuildOut, nil
	}
	return nil, fmt.Errorf("error waiting for metric %v: %v", wantMetric, ctx.Err())
}

// return reader and also stub server...
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

func (s) TestMethodTargetAttributeFilter(t *testing.T) { // so this works...so equality checks work!
	// get this working...
	maf := func(str string) bool {
		if str == "/grpc.testing.TestService/UnaryCall" {
			return false
		}
		// will allow duplex...do I need to declare all metrics wanted for this to work?
		return true
	}
	// pull out setup into a helper
	reader, ss := setup(t, true, maf)
	defer ss.Stop()

	// make a single RPC (unary rpc), and filter out the target and method
	// that would correspond.
	// on a basic metric
	// client metric started (and client end metrics) have method and target
	// assert the same as map below except with "other" for method and target

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if _, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{Payload: &testpb.Payload{
		Body: make([]byte, 10000),
	}}, grpc.UseCompressor(gzip.Name)); err != nil { // deterministic compression from OpenCensus test...still need it because one of main metrics in OTel is compressed metrics
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
	rm := &metricdata.ResourceMetrics{} // can I do this or just declare a pointer? I think this is fine allocates the memory?
	reader.Collect(ctx, rm)

	wantMetrics := []metricdata.Metrics{
		{
			// Use this name as key into map
			Name: "grpc.client.attempt.started",
			Description: "The total number of RPC attempts started, including those that have not completed.",
			Unit: "attempt", // do these get overwritten if set in the sdk?
			// Data: sum,/*metricdata.Sum{ // generics might require a higher version of go
			//DataPoints:
			//},
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
	mapToBuildOut /*ForFastAccess :) */ := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			mapToBuildOut[m.Name] = m
		}
	}




	// could do a second measure for good reason...
	for _, metric := range wantMetrics {
		val, ok := mapToBuildOut[metric.Name]
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

func (s) TestBasicBehaviors(t *testing.T) {
	// lighter weight tests here for the specific behaviors want to test

	// some contenders:
	// canonical target client (need client) and server? side (or let unit tests logically handle this) ***

	// Problem: start an rpc for method not registered
	// attempt to start for method not_registered, can see in client start?



	// static method vs. not client side (didn't unit test generated protos, so
	// this behavior test needs to make it's way here however I want to test
	// it)...

	// server side read pointer to server, don't need a distinction already unit
	// test this, but perhaps add distinction. Esp if it's easy to add
	// distinction for above ^^^.

	// this will allow you to pinpoint a specific behavior, (I think below was
	// passing if you remove the last 3?)


	// on client (and server since server registers stubs which get read)

	// test recording method for unary/streaming and method for other...
	// client and server side will be the same bucket in vs. out, test both sides

	// how do I trigger stats callouts with unregistered "generic" methods (why
	// we expose attribute filters for this case) in a test...manually trigger
	// generic rpc call and record started before rpc end/status?

}

// getting one other for unary and streaming vvv (does it need to match with stub server? need to register? seems wrong?)

// TestAllMetricsOneFunction tests emitted metrics from gRPC. It then configures
// a system with a gRPC Client and gRPC server with the OpenTelemetry Dial and
// Server Option configured specifying all the metrics provided by this package,
// and makes a Unary RPC and a Streaming RPC. These two RPCs should cause
// certain recording for each registered metric observed through a Manual
// Metrics Reader on the SDK's Meter Provider.
func (s) TestAllMetricsOneFunction(t *testing.T) {
	/*
		// embeds a metric.NewManualReader...use this and hold ref to it. I think this is what I need
		reader := metric.NewManualReader(cfg.manualReaderOptions()...) // do I want any options?
	*/
	// subsequent reads don't see the new metric
	reader := metric.NewManualReader() // testMetricReader
	// I think both NewMeterProvider and NewManualReader call come out of the metrics sdk...
	provider := metric.NewMeterProvider( // go get this...is this function call actually available and this creates a meter provider?
		metric.WithReader(reader), // is this a metric reader - yes it is! this is the complicated piece that observes metrics, part of Provider? No created before and specified in it's constructor...

		/*metric.WithView(metric.NewView(
		// I think the only thing that setting this would cause is the
		// bounds to not be default, but as below can use default bounds I
		// think deterministically.

		// instrument -> view
		// if specify an instrument in API, will create a default view, unless set by this
		metric.Instrument{ // Yash let it create default views
			Name:  "custom_histogram",
			Scope: instrumentation.Scope{Name: meterName},
		},
		metric.Stream{
			Name: "bar",
			// default bounds? I think Yash just let it create default.
			// if default bounds are deterministic can also make
			// assertions on that
			Aggregation: aggregation.ExplicitBucketHistogram{ // this sets the histogram bounds, which overwrites what you set in OTelinstrumentation library
				Boundaries: []float64{64, 128, 256, 512, 1024, 2048, 4096},
			},
		},)))*/
	)

	// has all client + server working
	// the last three that do get pinged and recorded what happens to them? size 57 with the right

	// where is it stored in this provider, can the reader read it?
	// 12345 <- stored somewhere, the reader can consistent read it after polls
	// I can observe changes to this 12345, but I can't see 678 at all
	// why no new metrics?

	// (what happens if I add data with new RPCs (could also help with
	// triggering outside generic) for the working 5, is that also observed?)

	// 678 <- where does this information go in OTel? if this data is persisted, why is it not observed in the manual reader

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
	// certain metrics to be emitted, which will be observed by the Metric Reader.
	if _, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{Payload: &testpb.Payload{
		Body: make([]byte, 10000),
	}}, grpc.UseCompressor(gzip.Name)); err != nil { // deterministic compression from OpenCensus test...still need it because one of main metrics in OTel is compressed metrics
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

	// Both these RPCs I know completed
	rm := &metricdata.ResourceMetrics{} // can I do this or just declare a pointer? I think this is fine allocates the memory?
	reader.Collect(ctx, rm)

	// Get one working first, then move on to the next
	/*if err := assertOnResourceMetrics2(rm); err != nil {
		t.Fatalf("resource metrics assertion failed with err %v", err)
	}*/
	// Server Side stats.End call happens asynchronously for both Unary and
	// Streaming calls with respect to the RPC returning client side. Thus, add
	// a sync point for metrics triggered by this call.
	/*if err := waitForServerCompletedRPCs(ctx); err != nil {
		t.Fatal(err)
	}*/

	mapToBuildOut /*ForFastAccess :) */ := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			mapToBuildOut[m.Name] = m
		}
	}

	// lighter way of testing "other"...

	// need to test:
	// a. TargetAttribute/MethodAttribute filter in the top level
	// b. registered method client side vs. not buckets (how to test these - see unit tests?)
	//    registered method server side vs. not buckets (how to test this - would need to mock an unregistered method...)
	// c. canonical target for those that don't become "other" (can test both same target (already tested below))
	// and canonical - but that would need to still pass Dial while also getting prepended



	unaryMethodAttr := attribute.String("grpc.method", "grpc.testing.TestService/UnaryCall") // could pul into var but I think this is only place you use it
	duplexMethodAttr := attribute.String("grpc.method", "grpc.testing.TestService/FullDuplexCall")
	// These tags are used for every single metric ^^^

	// Target is what you dial with
	targetAttr := attribute.String("grpc.target", ss.Target) // this read comes after the write right (I think ss.Start writes target)? Yes target gets populated in ss.Start in start server from start client resolver I think

	// status - for all the histogram ones (think about future metrics)
	statusAttr := attribute.String("grpc.status", "OK") // Same as OpenCensus :)

	// no more need for authority attribute

	// I think this needs to be passed into the attributes. Yes part of data points and so are a lot of others...a lot of
	// other things

	// manually verify these attributes, setting up sceanrios under test will be hard...

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

	// design doc and presentation... lol should I move it...

	// pull in OTel master

	attribute.NewSet(unaryMethodAttr, statusAttr)
	attribute.NewSet(unaryMethodAttr, statusAttr) // I could test other tag dimensions but this is what opencensus did and I think method dimension is all you need to test
	// server histogram tags ^^^

	// These assertions are orthogonal to the changes Yash makes except without authority.
	// Method header should stay the same.

	// This all was working outside the three in server defer, I also have no idea how to test generics

	wantMetrics := []metricdata.Metrics{
		{
			// Use this name as key into map
			Name: "grpc.client.attempt.started",
			Description: "The total number of RPC attempts started, including those that have not completed.",
			Unit: "attempt", // do these get overwritten if set in the sdk?
			// Data: sum,/*metricdata.Sum{ // generics might require a higher version of go
			//DataPoints:
			//},
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr),
						Value: 1, // if you make more than one unary rpc this shoulddd be 2
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
		},*/ // seems like there's more values...
		{ // seems to be deterministic...
			Name: "grpc.client.attempt.sent_total_compressed_message_size",
			Description: "Total bytes (compressed but not encrypted) sent across all request messages (metadata excluded) per RPC attempt; does not include grpc or transport framing bytes.",
			Unit: "By",
			Data: metricdata.Histogram[int64]{ // should be deterministic for their assertions
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr, statusAttr),
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
						// ignore start time/endtime
						Count: 1, // if you make more than one streaming rpc call this should be 2
						// much others, fill out and see if you want it

						// how do bounds work and how is the count linked to bounds?
						// default bounds due to api call, as no views set on the default meter provider passed in.

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
		// ^^^ these all work


		// vvv this doesn't

		{ // seems to be deterministic...
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
		{ // seems to be deterministic...
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
		// note that must come first in the map. Needs to be first one.
		if metric.Name == "grpc.server.call.sent_total_compressed_message_size" { // or have this be the first
			// sync the metric reader to see the event because stats.End is handled async server side.
			// Thus, poll until it shows up. Once this first server side metric shows up,
			// all the rest will be synced and ready to go. Thus, update the map accordingly.
			// or don't persist state over time, but then would need to pass a want.
			if mapToBuildOut, err = waitForServerCompletedRPCs(ctx, provider, reader, metric, t); err != nil { // I still think you need this.
				t.Fatalf("error waiting for sent total compressed message size for metric: %v", metric.Name)
			}
		}
		val, ok := mapToBuildOut[metric.Name]
		if !ok {
			t.Fatalf("metric %v not present in recorded metrics", metric.Name)
		}
		// their package has good assertions on their data types.
		// use their assertions, only on subset we want and ignore fields we don't want
		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			t.Fatalf("metrics data type not equal for metric: %v", metric.Name)
		} // e2e_test.go:720: metric grpc.server.call.sent_total_compressed_message_size not present in recorded metrics
		// and if you poll never records either


		// We use cmp.Diff, so maybe just use this
		/*if diff := cmp.Diff(val, metric, cmpopts.IgnoreUnexported(metricdata.DataPoint[int64]{}, metricdata.HistogramDataPoint[float64]{}, attribute.KeyValue{})); diff != "" { // What exactly to compare?
			t.Fatalf("unexpected metrics data (-got, +want): %v", diff)
		}*/
	}






	// It's observing the new grpc.client.attempt.started
	// extra point for unary and streaming call, but not the three ends

	// after vvv, uncomment the async above ^^^ if you want to run it there
	// this after also isn't being observed.
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
	}
	// time.Sleep(5 * time.Second)
	rm = &metricdata.ResourceMetrics{} // can I do this or just declare a pointer? I think this is fine allocates the memory?
	reader.Collect(ctx, rm)
	mapToBuildOut /*ForFastAccess :)  = map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			mapToBuildOut[m.Name] = m
		}
	}

	for _, metric := range wantMetrics {
		// note that must come first in the map. Needs to be first one.
		if metric.Name == "grpc.server.call.sent_total_compressed_message_size" { // or have this be the first
			// sync the metric reader to see the event because stats.End is handled async server side.
			// Thus, poll until it shows up. Once this first server side metric shows up,
			// all the rest will be synced and ready to go. Thus, update the map accordingly.
			// or don't persist state over time, but then would need to pass a want.
			if mapToBuildOut, err = waitForServerCompletedRPCs(ctx, provider, reader, metric, t); err != nil {
				t.Fatalf("error waiting for sent total compressed message size for metric: %v", metric.Name)
			}
		}
		val, ok := mapToBuildOut[metric.Name]
		if !ok {
			t.Fatalf("metric %v not present in recorded metrics", metric.Name)
		}
		// their package has good assertions on their data types.
		// use their assertions, only on subset we want and ignore fields we don't want
		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			t.Fatalf("metrics data type not equal for metric: %v", metric.Name)
		} // e2e_test.go:720: metric grpc.server.call.sent_total_compressed_message_size not present in recorded metrics
		// and if you poll never records either


		// We use cmp.Diff, so maybe just use this
		/*if diff := cmp.Diff(val, metric, cmpopts.IgnoreUnexported(metricdata.DataPoint[int64]{}, metricdata.HistogramDataPoint[float64]{}, attribute.KeyValue{})); diff != "" { // What exactly to compare?
			t.Fatalf("unexpected metrics data (-got, +want): %v", diff)
		}
	}*/






	// SEE HOW OPENTELEMETRY TESTS IT - YASH DOES SAME THING
	// sum metric data - this should be for the first want sum
	/*metricdata.Sum[int64]{
		DataPoints: []metricdata.DataPoint[int64]{
			{
				Attributes: attribute.NewSet(unaryMethodAttr, targetAttr),
				Value: 1, // if you make more than one unary rpc this shoulddd be 2
			},
			{
				Attributes: attribute.NewSet(duplexMethodAttr, targetAttr),
				Value: 1, // if you make more than one streaming rpc this shoulddd be 2
			},
		},
		Temporality: "CumulativeTemporality",
		IsMonotonic: true,
	}*/

	// how to get generics working and how to actually run it?
	/*metricdata.Histogram[float64]{
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
			},

		},
		IsMonotonic: true,
	}*/
}

/*
	-       Data: metricdata.Sum[int64]{
	        -               DataPoints: []metricdata.DataPoint[int64]{
	        -                       {
	        -                               Attributes: attribute.Set{...}, // important, the key sort of
	        -                               StartTime:  s"2023-08-22 17:15:06.546491 -0400"..., // ignore this - non deterministic
	        -                               Time:       s"2023-08-22 17:15:06.551407 -0400"..., // ignore this - non deterministic
	        -                               Value:      1, // important, tied to expected attribute, figure out how to declare attribute
	        -                       },
	        -                       {
	        -                               Attributes: attribute.Set{...},
	        -                               StartTime:  s"2023-08-22 17:15:06.546491 -0400"...,
	        -                               Time:       s"2023-08-22 17:15:06.551407 -0400"...,
	        -                               Value:      1,
	        -                       },
	        -               },
	        -               Temporality: s"CumulativeTemporality",
	        -               IsMonotonic: true,
	        -       },



	        -       Data: metricdata.Histogram[float64]{
	        -               DataPoints: []metricdata.HistogramDataPoint[float64]{
	        -                       {
	        -                               Attributes: attribute.Set{...}, // how does this relate to histogram? Is it the same thing keyed on count?
	        -                               StartTime:  s"2023-08-22 18:00:27.92402 -0400 "...,
	        -                               Time:       s"2023-08-22 18:00:27.930055 -0400"...,
	        -                               Count:      1, // tied to attribute like count calls?
	        -                               ...
	        -                       },
	        -                       {
	        -                               Attributes: attribute.Set{...}, // how does this relate to histogram? Is it the same thing keyed on count?
	        -                               StartTime:  s"2023-08-22 18:00:27.92402 -0400 "...,
	        -                               Time:       s"2023-08-22 18:00:27.930055 -0400"...,
	        -                               Count:      1,
	        -                               ...
	        -                       },
	        -               },
	        -               Temporality: s"CumulativeTemporality",
	        -       },

*/

// pointer?

/*
	// HistogramDataPoint is a single histogram data point in a timeseries.
	type HistogramDataPoint[N int64 | float64] struct {
		// Attributes is the set of key value pairs that uniquely identify the
		// timeseries.
		Attributes attribute.Set // how to declare this?
		// StartTime is when the timeseries was started.
		StartTime time.Time // ignore
		// Time is the time when the timeseries was recorded.
		Time time.Time // ignore

		// Seems ike it's all present?
		// Count is the number of updates this histogram has been calculated with.
		Count uint64
		// Bounds are the upper bounds of the buckets of the histogram. Because the
		// last boundary is +infinity this one is implied.
		Bounds []float64
		// BucketCounts is the count of each of the buckets.
		BucketCounts []uint64

		// Min is the minimum value recorded. (optional)
		Min Extrema[N]
		// Max is the maximum value recorded. (optional)
		Max Extrema[N]
		// Sum is the sum of the values recorded.
		Sum N

		// Exemplars is the sampled Exemplars collected during the timeseries.
		Exemplars []Exemplar[N] `json:",omitempty"`
	}
*/
// wrt what we want like context timeouts I think I need this
/*func (s metricdata.Sum) Equal(s2 metricdata.Sum) {
	s.DataPoints // []of DataPoint below, cmp.Diff on the []
	s.IsMonotonic // bool,
	s.Temporality // check this too
}

func (h metricdata.Histogram) Equal(h2 metricdata.Histogram) {
	// Are these [] ordering of data points deterministic? (like tags in OC. If non deterministic sort into determinism)
	h.DataPoints // I think since everything is primitives but data points, just cmp.Equal on whole thing and overwrite Equal on special types that need overwriting like these data points
	h.Temporality
}*/

// at the highest level, can use there's
/*
	func (dp metricdata.DataPoint[N]) Equal(dp2 metricdata.DataPoint[N]) bool {
	/*dp.Attributes // how to compare, this is a Set{} down the hierarchy has methods *on certain types* maybe try and find something they expose for equality comparisons?
	// ignore timestamps for start and time recorded (I think ignoring requires special equals)
	dp.Value // assert this is the same - either an int64 or float64...so just normal equality
	// Ignore exemplars - holds trace/span id. maybe useful for linking traces and spans?

	// for the sake of running it to see if it works - comment out top level
	return dp.Value == dp2.Value // does this just work wrt generics?

} // only started rpc int64 counts, so I think this is all you need

// "consider using a custom Comparer"

// cannot handle unexported field at {metricdata.Metrics}.Data.(metricdata.Sum[int64]).DataPoints[1].Attributes.equivalent:

// Also problem of equality on generics
func (hdp metricdata.HistogramDataPoint[N]) Equal(hdp2 metricdata.HistogramDataPoint[N]) bool {
	// how to do equality on generics?
	/*hdp.Attributes // how to compare this? This is an attribute.Set.
	attribute.Set
	// unexported fields to make assertions on
	// however, provides methods on certain types you can use as getters to get certain fields
	// ignore time

	// Count is the number of updates this histogram has been calculated with. -
	// what does this even mean? I think just ignore.

	hdp.Count // assert on this, how does it relate to bounds
	hdp.Bounds // I think the same default? bounds for every histogram metric
	hdp.BucketCounts // I think that it places counts within buckets, loop over bottom five seconds and assert data point in it. (Within 5s context bounds)

	// ignore hdp.Min
	// ignore hdp.Max

	// ignore Sum? Or assert 1?
	return hdp.Sum == hdp2.Sum

	// Exemplars? I don't even know what this is. Ignore?
}*/

// ^^^ duped vvv because have both type of metrics
/*func (hdp metricdata.HistogramDataPoint[N]) Equal(hdp2 metricdata.HistogramDataPoint[N]) bool {
	// how to do equality on generics?
	/*hdp.Attributes // how to compare this? This is an attribute.Set.
	attribute.Set
	// unexported fields to make assertions on
	// however, provides methods on certain types you can use as getters to get certain fields
	// ignore time

	// Count is the number of updates this histogram has been calculated with. -
	// what does this even mean? I think just ignore.

	hdp.Count // assert on this, how does it relate to bounds
	hdp.Bounds // I think the same default? bounds for every histogram metric
	hdp.BucketCounts // I think that it places counts within buckets, loop over bottom five seconds and assert data point in it. (Within 5s context bounds)

	// ignore hdp.Min
	// ignore hdp.Max

	// ignore Sum? Or assert 1?
	return hdp.Sum == hdp2.Sum

	// Exemplars? I don't even know what this is. Ignore?
}*/



// can either reuse their equality method or use this one...
// Yash said account for readability
/*func (hdp metricdata.HistogramDataPoint) Equal(hdp2 metricdata.HistogramDataPoint) {
	metricdata.HistogramDataPoint
}*/

/*
	after reader plumbed ^^^
	rm := &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm) // black box - already implemented, plumbs collected data into rm anyway, make assertions on that

	pass rm to assertion helper or do it inline, based off:

	// What part of metrics data do you need to make assertions on?

	// Metrics is a collection of one or more aggregated timeseries from an Instrument.
		type Metrics struct {
			// Name is the name of the Instrument that created this data.
			Name string
			// Description is the description of the Instrument, which can be used in documentation.
			Description string
			// Unit is the unit in which the Instrument reports.
			Unit string
			// Data is the aggregated data from an Instrument.
			Data Aggregation
		}

	One Unary one Stream:

		{
		grpc.client.attempt.started
		"The total number of RPC attempts started, including those that have not completed."
		"attempt"
		Data - Aggregation (2 as count) not a histogram right?
		}

		{
		grpc.client.attempt.duration
		"End-to-end time taken to complete an RPC attempt including the time it takes to pick a subchannel."
		no unit provided - ""?
		Data - either default histogram buckets if use default view (what Yash did - is there a default for floats) or float histogram with buckets I sent - OC I just fored over all buckets < 5 seconds and asserted two data points fell there
		}

		{
		grpc.client.attempt.sent_total_compressed_message_size
		"Total bytes (compressed but not encrypted) sent across all request messages (metadata excluded) per RPC attempt; does not include grpc or transport framing bytes."
		no unit provided - ""?
		Data - either default histogram buckets or not, but shoulddd be determinisitic
		}

		{
		"grpc.client.attempt.rcvd_total_compressed_message_size"
		"Total bytes (compressed but not encrypted) received across all response messages (metadata excluded) per RPC attempt; does not include grpc or transport framing bytes."
		no unit provided - ""?
		Data - either default histogram buckets or not, but shoulddd be determinisitic
		}

		{
		"grpc.client.call.duration"
		"This metric aims to measure the end-to-end time the gRPC library takes to complete an RPC from the application’s perspective."
		no unit provided - ""?
		Data - either default histogram buckets if use default view (what Yash did - is there a default for floats) or float histogram with buckets I sent - OC I just fored over all buckets < 5 seconds and asserted two data points fell there
		}

		// same thing for server side metrics
*/


/*
I rebase it off master...going to use generics
Figure out a way to express this in my code, maybe go2?

-       Data: metricdata.Sum[int64]{
        -               DataPoints: []metricdata.DataPoint[int64]{
        -                       {
        -                               Attributes: attribute.Set{...},
        -                               StartTime:  s"2023-08-22 17:15:06.546491 -0400"...,
        -                               Time:       s"2023-08-22 17:15:06.551407 -0400"...,
        -                               Value:      1,
        -                       },
        -                       {
        -                               Attributes: attribute.Set{...},
        -                               StartTime:  s"2023-08-22 17:15:06.546491 -0400"...,
        -                               Time:       s"2023-08-22 17:15:06.551407 -0400"...,
        -                               Value:      1,
        -                       },
        -               },
        -               Temporality: s"CumulativeTemporality",
        -               IsMonotonic: true,
        -       },
*/

// Pass Equal with ignoring timestamp and exemplars (I might need to ignore after)

// does this for the metrics type - do this for presence check

// then use their branching logic for sum data points and histogram data points (and with histogram data points you need
// to fill out all of the expected - what are default bounds play with it to find out since they append reasons

/*
Remove the authority tag, can scale up



Caps the state space possible of tags...over 2000 stops emitting metrics
unregistered method - switch to "generic"

if registered use it

confirm if go clients methods registered or not. Doug's convinced server has it client does it.

proxy is a client (can also emit more than one), if client doesn't have it need to add it


fill out api
*/

// generics should work not in go2 file you can still define it and compiler
// will stick work just won't have syntax highlighting...

// why doesn't the three metrics hit, I see them being recorded and I poll
// but doesn't show up here

// generics work here, testing generics should work here...
// verify exact emissions

// got all the recent depencies

// need to add other test (from not stub)
// and also registered method test (this can be assertion of call option)

// also add target attribute (I think already part of these e2e assertions)

// why doesn't it trace through, is it a snapshot because the metrics happen at
// other places right? might be a bug in their code since I depend on them...
