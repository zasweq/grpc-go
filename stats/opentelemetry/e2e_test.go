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

	"github.com/google/go-cmp/cmp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

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

// Tasks
// Cleanup
// Go get the sdk


// go get at master to see if tests work
// then they need to perform a (stable?) release...but David mentioned it's a few months from being stable.
// so I can use it in my code

// It's stable wrt emitted metrics might change some names around so I'm good wrt writing tests



// maybe I need to update OTel Go to recent version to pull in reader/sdk

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
	reader := metric.NewManualReader()
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

	// Both these RPCs I know completed
	rm := &metricdata.ResourceMetrics{} // can I do this or just declare a pointer? I think this is fine allocates the memory?
	reader.Collect(ctx, rm)

	// Get one working first, then move on to the next
	/*if err := assertOnResourceMetrics2(rm); err != nil {
		t.Fatalf("resource metrics assertion failed with err %v", err)
	}*/

	mapToBuildOut/*ForFastAccess :) */ := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			mapToBuildOut[m.Name] = m
		}
	}

	wantMetrics := []metricdata.Metrics{
		{
			// Use this name as key into map
			Name: "grpc.client.attempt.started",
			Description: "The total number of RPC attempts started, including those that have not completed."/*description form registration here - does this also get overwritten?*/,
			Unit: "attempt", // do these get overwritten if set in the sdk?
			// Data: sum,/*metricdata.Sum{ // generics might require a higher version of go
			//DataPoints:
			//},*/
		},
	}

	for _, metric := range wantMetrics {
		val, ok := mapToBuildOut[metric.Name]
		if !ok {
			t.Fatalf("metric %v not present in recorded metrics", metric.Name)
		}
		if diff := cmp.Diff(val, metric); diff != "" { // What exactly to compare?
			t.Fatalf("unexpected metrics data (-got, +want): %v", diff)
		}
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

func assertOnResourceMetrics2(rm *metricdata.ResourceMetrics) error {
	// These come in a list, for it to have fast assertions make a map like OpenCensus e2e tests
	/*rm.ScopeMetrics // []ScopeMetrics
	for _, sm := range rm.ScopeMetrics {
		sm.Metrics // []Metrics
		for _, m := range sm.Metrics {
			m.Name
			m.Data
			m.Description // describes the metric - can assert on this
			m.Unit // only on counts
		}
	}*/
	// name -> metric with name, persist name twice for fast access
	mapToBuildOut/*ForFastAccess :) */ := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			mapToBuildOut[m.Name] = m
		}
	}

	// sum := metricdata.Sum{}
	// sum := metricdata.Sum[int64]{} // what happens if I send this to cmp diff
	// sum.DataPoints = wtf // can't find Data Points?
	/*stringMetricsName := "grpc.client.attempt.started"
	wantMetricData := metricdata.Metrics{
		Name: "grpc.client.attempt.started",
		Description: "The total number of RPC attempts started, including those that have not completed."/*description form registration here - does this also get overwritten?,
		Unit: "attempt", // do these get overwritten if set in the sdk?
		// Data: sum,/*metricdata.Sum{ // generics might require a higher version of go
			//DataPoints:
		//},
	}*/
	wantMetrics := []metricdata.Metrics{
		{
			// Use this name as key into map
			Name: "grpc.client.attempt.started",
			Description: "The total number of RPC attempts started, including those that have not completed."/*description form registration here - does this also get overwritten?*/,
			Unit: "attempt", // do these get overwritten if set in the sdk?
			// Data: sum,/*metricdata.Sum{ // generics might require a higher version of go
			//DataPoints:
			//},*/
		},
	}
	for _, metric := range wantMetrics {
		val, ok := mapToBuildOut[metric.Name]
		if !ok {
			t.Fatalf("metric %v not present in recorded metrics", metric.Name)
		}
		if diff := cmp.Diff(val, metric); diff != "" { // What exactly to compare?
			t.Fatalf("unexpected metrics data (-got, +want): %v", diff)
		}
	}

	/*val, ok := mapToBuildOut[stringMetricsName]
	if !ok {
		// pass this in or return error and log it in testing goroutine (do the latter)
		return fmt.Errorf("metric %v not present in recorded metrics", stringMetricsName)
	} // or could make this as part of assertion
	// string metrics name

	// Do I just cmp.Diff from val against metric want, I think just leave blank honestly for
	// stuff that only pertains to certain metrics. Do I need to define an Equal function for cmp.Diff to assert on (see OpenCensus e2e tests for how I do so)

	// cmp.Diff against the whole map or individual nodes?
	// I cmp.Diffed against whole map in OpenCensus
	if diff := cmp.Diff(val, wantMetricData); diff != "" { // What exactly to compare?
		return fmt.Errorf("unexpected metrics data (-got, +want): %v", diff)
	}*/
	return nil
}

func (s) TTestMetrics(t *testing.T) {
	/*tests := struct{
		name string

	}{

	}*/

	// unary
	// streaming

	wantMetrics := []metricdata.Metrics{
		{
			// Use this name as key into map
			Name: "grpc.client.attempt.started",
			Description: "The total number of RPC attempts started, including those that have not completed."/*description form registration here - does this also get overwritten?*/,
			Unit: "attempt", // do these get overwritten if set in the sdk?
			// Data: sum,/*metricdata.Sum{ // generics might require a higher version of go
			//DataPoints:
			//},*/
		},
	}

	// continue to build map to build out, use that instead.


}