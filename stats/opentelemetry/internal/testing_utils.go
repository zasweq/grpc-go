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


package internal

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/metric/metricdata/metricdatatest"
)

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
		if !metricdatatest.AssertEqual(t, wantMetric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			continue
		}
		return gotMetrics, nil
	}
	return nil, fmt.Errorf("error waiting for metric %v: %v", wantMetric, ctx.Err())
}

// assertDataPointWithinFiveSeconds asserts the metric passed in contains
// a histogram with dataPoints that fall within buckets that are <=5.
func assertDataPointWithinFiveSeconds(metric metricdata.Metrics) error {
	histo, ok := metric.Data.(metricdata.Histogram[float64])
	if !ok {
		return fmt.Errorf("metric data is not histogram")
	}
	for _, dataPoint := range histo.DataPoints {
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

// Helpers below...
// MetricDataOptions are the options used to configure the metricData emissions
// of expected metrics data from NewMetricData. (rename function? this feels
// like the different config state spaces for xDS haha).
type MetricDataOptions struct {
	// CSMLabels are the csm labels to attach to metrics which receive csm
	// labels (all A66 expect client call and started RPC's client and server
	// side).
	CSMLabels []attribute.KeyValue
	// Target is the target of the client and server.
	Target string // need to set this based on variable stub server - return target? and set in function body dynamically
	// UnaryMessageSent is whether a message was sent for the unary RPC or not.
	// This unary message is assumed to be 10000 bytes and the RPC is assumed to
	// have a gzip compressor call option set. This assumes both client and peer
	// sent a message.
	UnaryMessageSent bool
	// StreamingMessageSent is whether a message was sent for the streaming RPC
	// or not. This unary message is assumed to be 10000 bytes and the RPC is
	// assumed to have a gzip compressor call option set. This assumes both
	// client and peer sent a message.
	StreamingMessageSent bool
}

// MetricData returns a metricsDataSlice for A66 metrics for client and server
// with a unary RPC and streaming RPC with certain compression and message flow
// sent. If csmAttributes is set to true, the corresponding CSM Metrics (not
// client side call metrics, or started on client and server side).
func MetricData(options MetricDataOptions) []metricdata.Metrics {
	// unaryMessageSent and streamingMessageSent likely just affect compressed bytes on the wire


	unaryMethodAttr := attribute.String("grpc.method", "grpc.testing.TestService/UnaryCall")
	duplexMethodAttr := attribute.String("grpc.method", "grpc.testing.TestService/FullDuplexCall")

	targetAttr := attribute.String("grpc.target", options.Target) // target is variable - construct dynamically
	statusAttr := attribute.String("grpc.status", "OK")

	// Started don't need it luckily so just build out - other ones are fixed
	unaryMethodClientSideEnd := []attribute.KeyValue{
		unaryMethodAttr,
		targetAttr,
		statusAttr,
	}
	streamingMethodClientSideEnd := []attribute.KeyValue{
		duplexMethodAttr,
		targetAttr,
		statusAttr,
	}
	unaryMethodServerSideEnd := []attribute.KeyValue{
		unaryMethodAttr,
		targetAttr,
	}

	streamingMethodServerSideEnd := []attribute.KeyValue{
		duplexMethodAttr,
		targetAttr,
	}

	unaryMethodClientSideEnd = append(unaryMethodClientSideEnd, options.CSMLabels...)
	streamingMethodClientSideEnd = append(streamingMethodClientSideEnd, options.CSMLabels...)
	unaryMethodServerSideEnd = append(unaryMethodServerSideEnd, options.CSMLabels...)
	streamingMethodServerSideEnd = append(streamingMethodServerSideEnd, options.CSMLabels...)
	var unaryCompressedBytesSentRecv int64
	if options.UnaryMessageSent {
		unaryCompressedBytesSentRecv = 57 // Fixed 10000 bytes with gzip assumption.
	}

	var streamingCompressedBytesSentRecv int64
	if options.StreamingMessageSent {
		streamingCompressedBytesSentRecv = 57 // Fixed 10000 bytes with gzip assumption.
	}

	return []metricdata.Metrics{
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
						Value:      1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{
			Name:        "grpc.client.attempt.duration",
			Description: "End-to-end time taken to complete a client call attempt.",
			Unit:        "s",
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes: attribute.NewSet(unaryMethodClientSideEnd...),
						Count:      1,
						Bounds:     DefaultLatencyBounds,
					},
					{
						Attributes: attribute.NewSet(streamingMethodClientSideEnd...),
						Count:      1,
						Bounds:     DefaultLatencyBounds,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name:        "grpc.client.attempt.sent_total_compressed_message_size",
			Description: "Compressed message bytes sent per client call attempt.",
			Unit:        "By",
			Data: metricdata.Histogram[int64]{
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes:   attribute.NewSet(unaryMethodClientSideEnd...),
						Count:        1,
						Bounds:       DefaultSizeBounds,
						BucketCounts: []uint64{0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min:          metricdata.NewExtrema(int64(57)),
						Max:          metricdata.NewExtrema(int64(57)),
						Sum:          unaryCompressedBytesSentRecv,
					},
					{
						Attributes:   attribute.NewSet(streamingMethodClientSideEnd...),
						Count:        1,
						Bounds:       DefaultSizeBounds,
						BucketCounts: []uint64{0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min:          metricdata.NewExtrema(int64(0)),
						Max:          metricdata.NewExtrema(int64(0)),
						Sum:          streamingCompressedBytesSentRecv,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name:        "grpc.client.attempt.rcvd_total_compressed_message_size",
			Description: "Compressed message bytes received per call attempt.",
			Unit:        "By",
			Data: metricdata.Histogram[int64]{
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes:   attribute.NewSet(unaryMethodClientSideEnd...),
						Count:        1,
						Bounds:       DefaultSizeBounds,
						BucketCounts: []uint64{0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min:          metricdata.NewExtrema(int64(57)),
						Max:          metricdata.NewExtrema(int64(57)),
						Sum:          unaryCompressedBytesSentRecv,
					},
					{
						Attributes:   attribute.NewSet(streamingMethodClientSideEnd...),
						Count:        1,
						Bounds:       DefaultSizeBounds,
						BucketCounts: []uint64{0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min:          metricdata.NewExtrema(int64(0)),
						Max:          metricdata.NewExtrema(int64(0)),
						Sum:          streamingCompressedBytesSentRecv,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name:        "grpc.client.call.duration",
			Description: "Time taken by gRPC to complete an RPC from application's perspective.",
			Unit:        "s",
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr, statusAttr),
						Count:      1,
						Bounds:     DefaultLatencyBounds,
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr, targetAttr, statusAttr),
						Count:      1,
						Bounds:     DefaultLatencyBounds,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
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
						Value:      1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{ // switch otel e2e to use this and see if it works, other thing is to mock env
			Name:        "grpc.server.call.sent_total_compressed_message_size",
			Unit:        "By",
			Description: "Compressed message bytes sent per server call.",
			Data: metricdata.Histogram[int64]{
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes:   attribute.NewSet(unaryMethodServerSideEnd...),
						Count:        1,
						Bounds:       DefaultSizeBounds,
						BucketCounts: []uint64{0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min:          metricdata.NewExtrema(int64(57)),
						Max:          metricdata.NewExtrema(int64(57)),
						Sum:          unaryCompressedBytesSentRecv,
					},
					{
						Attributes:   attribute.NewSet(streamingMethodServerSideEnd...),
						Count:        1,
						Bounds:       DefaultSizeBounds,
						BucketCounts: []uint64{0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min:          metricdata.NewExtrema(int64(0)),
						Max:          metricdata.NewExtrema(int64(0)),
						Sum:          streamingCompressedBytesSentRecv,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name:        "grpc.server.call.rcvd_total_compressed_message_size",
			Unit:        "By",
			Description: "Compressed message bytes received per server call.",
			Data: metricdata.Histogram[int64]{
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes:   attribute.NewSet(unaryMethodServerSideEnd...),
						Count:        1,
						Bounds:       DefaultSizeBounds,
						BucketCounts: []uint64{0x0, 0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min:          metricdata.NewExtrema(int64(57)),
						Max:          metricdata.NewExtrema(int64(57)),
						Sum:          unaryCompressedBytesSentRecv,
					},
					{
						Attributes:   attribute.NewSet(streamingMethodServerSideEnd...),
						Count:        1,
						Bounds:       DefaultSizeBounds,
						BucketCounts: []uint64{0x1, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
						Min:          metricdata.NewExtrema(int64(0)),
						Max:          metricdata.NewExtrema(int64(0)),
						Sum:          streamingCompressedBytesSentRecv,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name:        "grpc.server.call.duration",
			Description: "End-to-end time taken to complete a call from server transport's perspective.",
			Unit:        "s",
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes: attribute.NewSet(unaryMethodServerSideEnd...),
						Count:      1,
						Bounds:     DefaultLatencyBounds,
					},
					{
						Attributes: attribute.NewSet(streamingMethodServerSideEnd...),
						Count:      1,
						Bounds:     DefaultLatencyBounds,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
	}
}

// CompareGotWantMetrics asserts wantMetrics are what we expect...
func CompareGotWantMetrics(ctx context.Context, t *testing.T, mr *metric.ManualReader, gotMetrics map[string]metricdata.Metrics, wantMetrics []metricdata.Metrics) { // return an error instead of t...
	for _, metric := range wantMetrics {
		if metric.Name == "grpc.server.call.sent_total_compressed_message_size" || metric.Name == "grpc.server.call.rcvd_total_compressed_message_size" {
			// Sync the metric reader to see the event because stats.End is
			// handled async server side. Thus, poll until metrics created from
			// stats.End show up.
			var err error
			if gotMetrics, err = waitForServerCompletedRPCs(ctx, mr, metric, t); err != nil { // move to shared helper
				t.Fatalf("error waiting for sent total compressed message size for metric %v: %v", metric.Name, err)
			}
			continue
		}

		// If one of the duration metrics, ignore the bucket counts, and make
		// sure it count falls within a bucket <= 5 seconds (maximum duration of
		// test due to context).
		val, ok := gotMetrics[metric.Name]
		if !ok {
			t.Fatalf("metric %v not present in recorded metrics", metric.Name)
		}
		if metric.Name == "grpc.client.attempt.duration" || metric.Name == "grpc.client.call.duration" || metric.Name == "grpc.server.call.duration" {
			if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars(), metricdatatest.IgnoreValue()) {
				t.Fatalf("metrics data type not equal for metric: %v", metric.Name)
			}
			if err := assertDataPointWithinFiveSeconds(val); err != nil { // move to shared helper...
				t.Fatalf("Data point not within five seconds for metric %v: %v", metric.Name, err)
			}
			continue
		}

		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			t.Fatalf("metrics data type not equal for metric: %v", metric.Name)
		}
	}
}
