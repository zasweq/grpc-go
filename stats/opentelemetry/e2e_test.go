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


// Tasks
// Cleanup
// Go get the sdk


// maybe I need to update OTel Go to recent version to pull in reader/sdk

// TestAllMetricsOneFunction tests emitted metrics from gRPC. It then configures
// a system with a gRPC Client and gRPC server with the OpenTelemetry Dial and
// Server Option configured specifying all the metrics provided by this package,
// and makes a Unary RPC and a Streaming RPC. These two RPCs should cause
// certain recording for each registered metric observed through a Manual
// Metrics Reader on the Meter Provider.
func (s) TestAllMetricsOneFunction(t *testing.T) {
	/*
	// embeds a metric.NewManualReader...use this and hold ref to it. I think this is what I need
	reader := metric.NewManualReader(cfg.manualReaderOptions()...) // do I want any options?
	*/
	// I think both NewMeterProvider and NewManualReader call come out of the metrics sdk...
	provider := metric.NewMeterProvider( // go get this...is this function call actually available and this creates a meter provider?
		metric.WithReader(reader), // is this a metric reader - yes it is! this is the complicated piece that observes metrics, part of Provider? No created before and specified in it's constructor...

		metric.WithView(metric.NewView(
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
		MeterProvider: provider,
		Metrics: DefaultClientMetrics,
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
}
