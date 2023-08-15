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
	"go.opencensus.io/metric/metricdata"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/internal/stubserver"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"testing"

	"google.golang.org/grpc/internal/grpctest"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/aggregation"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// same structure? client and server side with instrumentation turned on?
// verify exporter?


// need to verify can plumb more than one instrumentation component into same ClientConn



func (s) TestMessAround(t *testing.T) {
	// fake exporter I think is way to go
	/*
	// David mentioned that using SDK/default global is similar to using
	// OpenCensus originally...
	sdktrace.NewTracerProvider(
			// batcher on the exporter
			// exp = sdktrace.SpanExporter...do I even need this NewTraceExporter
			// sdktrace.WithBatcher(exporter) <- configure exporter through passed in trace options?
			// and resource
			)
	*/

	// trace.Tracer is a TracerProvider.Tracer() <- Tracer provider comes from SDK and you register a SpanExporter

	// call Start() on trace.Tracer

	// pass the provider as a metrics option, the provider has the exporter, put
	// a fake exporter in and I think it will do it that way. ping Yash on how
	// he tested the exporting part?

}

// similar flow for metrics?
func (s) TestMetrics(t *testing.T) {

	// Meter Provider - either set views or not set views, gets the default
	// []string metrics - creates the measures on the meter

	// Metrics Reader(exporter (with set aggregation)) - Yash just used this to read data, but can also mock exporter, but then you'd have to check what happens to aggregation...

	// exporter set or not
	provider := metric.NewMeterProvider( // go get this...
		metric.WithReader(exporter), // is this a metric reader - yes it is! this is the complicated piece that observes metrics, part of Provider? No created before and specified in it's constructor...

		metric.WithView(metric.NewView(
			// instrument -> view
			// if specify an instrument in API, will create a default view, unless set by this
			metric.Instrument{ // Yash let it create default views
				Name:  "custom_histogram",
				Scope: instrumentation.Scope{Name: meterName},
			},
			metric.Stream{
				Name: "bar",
				// default bounds? I think Yash just let it create default
				Aggregation: aggregation.ExplicitBucketHistogram{ // this sets the histogram bounds, which overwrites what you set in OTelinstrumentation library
					Boundaries: []float64{64, 128, 256, 512, 1024, 2048, 4096},
				},
			},
			)),
		)


	// I think what I decided what that the reader read all the stuff from the provider and it worked black box wise...

	// It already does it, but Yash needed to override to get it working because of Abstract class

	metric.WithReader(exporter) // this implements right thing
	// need a ref to exporter, pass it rm to populate
	// make assertions on the rm

}

// []scope metrics
// []metrics - aggregations created by meter
// Metrics {
// name, description, unit, data
// }


// Yash mocked reader - all measurements that are recorded, then sent to exporter
// but just made measurements on reader, stuck in data structure and that's what he wanted

// Collect() - called by exporters and also testing code, collects any opentelemetry metrics recorded
// testing code, this
// exporters get this data and send it somewhere
// just call it yourself essentially (like ExportView/Span)


// "the user can also configure that component" - so Yash mock of MetricObserver is correct

type mockReader struct {
	// sdk.Reader - would intercept collect, but need a pluggable interface
	// test cllient conn implements all the methods

	// Observe package somehow (either reuse what they have or write your own)
	metrics []metricsdk.Metric // make assertions on this
}

// maybe sdk hasn't done a release yet
func (mr *mockReader) Collect(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	// Look at tests that OTel wrote
	// either wrap the Collect call() and intercept rm
	// Or write my own that calls into pipeline from instrument.Record() -> MetricProducer



	// Load the persisted producer (I think this is 1:1 with MeterProvider)
	// Pass rm into the producer

	// produceHolder is used as an atomic.Value to wrap non concrete producer type

	// ph.produce(ctx, rm) (load ph atomically) is this the same thing as producer?

	// MeterProvider (records measures) -> Observer on that (can I just mock Meter Provider)
	// what actually happens to these instruments? where does the data lay

	// instrument create a default view...where does this data go?

	// need to observe it with produce() -> is this only their default sdk?
	// does the api calls do it? will it produce? Write my own

	// MeterProvider -> MetricObserver (the interface between these two how are they linked)?




	// this triggers exporter to read all the metrics needed

	// do the same thing here for verification purposes

	// see the same code....populate mp the same way or just use it fill out data and make assertions on that?

	// persist some metrics state that's ok to make assertions on
	// What part of metricdata.ResourceMetrics is important to persist here for verificaiton

	// Yash used default views from OTel package
	// inherinted from metric readefr - abstract except collect

	// everything else a no-op
	// the only functionality he had was the observing of the top level (copied over) so I think manual reader . collect should work
	// reuse the produce helper the same

	// resource metrics.Scope.metrics description unit etc. should work
	// the system should still be same - client or server with a OpenTel dial option pluged in

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
		/*meter provider with functionality of actually taking measurements (Yash did it through abstract class)*/
		Metrics: DefaultServerMetrics,
	})}, DialOption(MetricsOptions{
		/*meter provider with functionality of actually taking measurements (Yash did it through abstract class)*/
		Metrics: DefaultClientMetrics,
	})); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	defer ss.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// Make two RPC's, a unary RPC and a streaming RPC. These should cause
	// certain metrics to be emitted.
	if _, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{Payload: &testpb.Payload{
		Body: make([]byte, 10000),
	}}, grpc.UseCompressor(gzip.Name)); err != nil { // deterministic compression from OpenCensus...
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

	// What part of metrics data do you need to make assertions on?

	/*
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

	// same shit server side

	type Metrics struct {
	    Name        string
	    Description string
	    Unit        string
	    Data        Aggregation
	}
	*/

}

func (mr *mockReader) ForceFlush(context.Context) error { return nil } // I call stuff on the reader right

func (mr *mockReader) Shutdown(context.Context) error { return nil } // I call stuff on this right so can leave null

// Tasks
// Cleanup
// Go get the sdk


// maybe I need to update OTel Go to recent version to pull in reader/sdk

func (s) TestAllMetricsOneFunction(t *testing.T) {
	/*
	the example uses otelprom, which embeds a reader so I can collect. Is there
	something that doesn't tie it to prometheus?
	exporter, err := otelprom.New()
		if err != nil {
			log.Fatal(err)
		}

	// embeds a metric.NewManualReader...use this and hold ref to it. I think this is what I need
	reader := metric.NewManualReader(cfg.manualReaderOptions()...)
	*/
	provider := metric.NewMeterProvider( // go get this...
		metric.WithReader(reader), // is this a metric reader - yes it is! this is the complicated piece that observes metrics, part of Provider? No created before and specified in it's constructor...

		metric.WithView(metric.NewView(
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
			},
		)),
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
		Metrics: DefaultServerMetrics,
	})}, DialOption(MetricsOptions{
		MeterProvider: provider, // these don't collide
		Metrics: DefaultClientMetrics,
	})); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	defer ss.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// Make two RPC's, a unary RPC and a streaming RPC. These should cause
	// certain metrics to be emitted.
	if _, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{Payload: &testpb.Payload{
		Body: make([]byte, 10000),
	}}, grpc.UseCompressor(gzip.Name)); err != nil { // deterministic compression from OpenCensus...
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



	/*
	after reader plumbed ^^^
	rm := &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm) // black box - already implemented, plumbs collected data into rm anyway, make assertions on that

	pass rm to assertions or do it inline, based off:

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

		// same shit server side
	*/
}
