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

package opentelemetry

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/experimental/stats" // estats as in other places?
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/grpctest"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric" // otelmetric as other places?
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

// TestMetricsRegistryMetrics tests the OpenTelemetry behavior with respect to registered metrics.


// TestMetricsRegistryMetrics tests the OpenTelemetry behavior with respect to
// registered metrics. It configures one of each kind of metric in the metrics
// registry but three int count metrics. All metrics are defaults outside of int
// count 2 and 3, and only int count 3 gets added to the default set.

// Optional labels are only configured on the float metrics. This test then
// makes measurements on those instruments, then tests the expected metrics
// emissions.

func (s) TestMetricsRegistryMetrics(t *testing.T) {
	internal.SnapshotMetricRegistryForTesting.(func (t *testing.T))(t)

	// Register instruments...could test defaults vs. not logic form this register call...
	intCountHandle1 := stats.RegisterInt64Count(stats.MetricDescriptor{
		Name: "int-counter-1",
		Description: "Sum of calls from test",
		Unit: "int",
		Labels: []string{"int counter 1 label key"},
		OptionalLabels: []string{"int counter 1 label key"},
		Default: true,
	}) // estats?

	// non default metric.
	// If not specified in OpenTelemetry constructor, this will become a no-op,
	// so measurements recorded on it won't show up in emitted metrics.
	intCountHandle2 := stats.RegisterInt64Count(stats.MetricDescriptor{
		Name: "int-counter-2",
		Description: "Sum of calls from test",
		Unit: "int",
		Labels: []string{"int counter 2 label key"},
		OptionalLabels: []string{"int counter 2 label key"},
		Default: false,
	})

	// Register another non default metric. This will get added to the default
	// metrics set in the OpenTelemetry constructor options, so metrics recorded
	// on this should show up in metrics emissions.

	// Test DefaultMetricsAdd a string so maybe a third intCountHandle
	intCountHandle3 := stats.RegisterInt64Count(stats.MetricDescriptor{
		Name: "int-counter-3",
		Description: "sum of calls from test",
		Unit: "int",
		Labels: []string{"int counter 3 label key"},
		OptionalLabels: []string{"int counter 3 optional label key"},
		Default: false,
	})
	// Register the other 4 types...test all emissions just to make sure plumbing works...test more than one label emission?
	floatCountHandle := stats.RegisterFloat64Count(stats.MetricDescriptor{
		Name: "float-counter",
		Description: "sum of calls from test",
		Unit: "float",
		Labels: []string{"float counter label key"},
		OptionalLabels: []string{"float counter optional label key"},
		Default: true,
	}) // labels provided should match up, since lower layer gives you that guarantee

	bounds := []float64{0, 5, 10}

	intHistoHandle := stats.RegisterInt64Histo(stats.MetricDescriptor{
		Name:           "int-histo",
		Description:    "histogram of call values from tests",
		Unit:           "int",
		Labels:         []string{"int histo label key"},
		OptionalLabels: []string{"int histo optional label key"},
		Default:        true,
		Bounds:         bounds,
	})
	floatHistoHandle := stats.RegisterFloat64Histo(stats.MetricDescriptor{
		Name:           "float-histo",
		Description:    "histogram of call values from tests",
		Unit:           "float",
		Labels:         []string{"float histo label key"},
		OptionalLabels: []string{"float histo optional label key"},
		Default:        true,
		Bounds:         bounds,
	})
	intGaugeHandle := stats.RegisterInt64Gauge(stats.MetricDescriptor{
		Name:           "simple-gauge",
		Description:    "the most recent int emitted by test",
		Unit:           "int",
		Labels:         []string{"int gauge label key"},
		OptionalLabels: []string{"int gauge optional label key"},
		Default:        true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))

	// This configures the defaults alongside int counter 3. All the instruments
	// registered except int counter 2 and 3 are default, so all measurements
	// recorded should show up in reader collected metrics except those for int
	// counter 2.

	// This also only toggles the float count and histo optional labels, so only
	// those should show up in metrics emissions. All the required labels should
	// show up in metrics emissions. (write comment somewhere about how labels
	// received have a guarantee here).


	// what about these keys can they have spaces? yes it seems...
	mo := MetricsOptions{
		Context: ctx, // Tests the context logic on metrics recording calls OTel rests as a seperate component that client conn and is independent so gets own context...
		Metrics: DefaultMetrics().Add(stats.Metric("int-counter-3")), // Does this work? Should I leave this required typecast (maybe provide example...)
		OptionalLabels: []string{"float counter optional label key", "float histo optional label key"}, // Setup some optional labels from tests...
		MeterProvider: provider, // This still needs a manual reader...and move to non per call test...
	}

	// Create an OTel plugin...use defaultMetrics() runtime, we'll see what Doug
	// says about leaving stuff unexported...
	// How to test client/server split - or say they hit the same helpers
	ssh := &serverStatsHandler{options: Options{MetricsOptions: mo}}
	ssh.initializeMetrics()

	// Don't have access to ssh in dial option, so just create one directly

	// When it hits make sure simulates layer below of eating labels...write
	// comment about this :)

	intCountHandle1.Record(ssh, 1, []string{"int counter 1 label value", "int counter 1 optional label value"}...)
	// Not part of metrics specified (not default), so this call shouldn't show up in emissions...
	intCountHandle2.Record(ssh, 2, []string{"int counter 2 label value", "int counter 2 optional label value"}...)
	// Part of metrics specified, so this call should show up in emissions...
	intCountHandle3.Record(ssh, 4, []string{"int counter 3 label value", "int counter 3 optional label value"}...) // record the same values...

	// These recording points should show up in emissions as they are defaults...

	// Test permutations of optional label values logic...configure only some, only those should show up...
	floatCountHandle.Record(ssh, 1.2, []string{"float counter label value", "float counter optional label value"}...)
	intHistoHandle.Record(ssh, 3, []string{"int histo label value", "int histo optional label value"}...)
	floatHistoHandle.Record(ssh, 4.3, []string{"float histo label value", "float histo optional label value"}...)
	intGaugeHandle.Record(ssh, 7, []string{"int gauge label value", "int gauge optional label value"}...) // size should match, below does this for us no need to verify...
	intGaugeHandle.Record(ssh, 8, []string{"int gauge label value", "int gauge optional label value"}...)

	// Emission is deterministic so bucket counts are also deteriministic...
	// I think need to test all 5 for just e2e plumbing (Done)



	// Yeah so label keys/vals unconditional...
	// optional label key/vals conditional...




	// check metrics atoms...probably define metrics atoms inline...can't use
	// mock stats handler here because this is the stats handler will learn how
	// to define metris atoms

	// Figure out full scenario first?

	rm := &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm)
	gotMetrics := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}

	// optional labels only for floats...

	// Metrics are stable from their perspective which is why this e2e test is
	// robust...
	wantMetrics := []metricdata.Metrics{ // Other dimension is optional labels turned on or not...figure out how to do partition...
		// Int count 1 and 3 (configured):
		{ // No attribute just buckets into 1 essentially right? yes it seems like
			Name: "int-counter-1",
			Description: "Sum of calls from test",
			Unit: "int",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(attribute.String("int counter 1 label key", "int counter 1 label value")), // What attributes do I attach here...label key values...optional labels can be tested by omission
						Value:      1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{
			Name: "int-counter-3",
			Description: "sum of calls from test",
			Unit: "int",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(attribute.String("int counter 3 label key", "int counter 3 label value")), // What attributes do I attach here...label key values...optional labels can be tested by omission
						Value:      4,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{
			Name: "float-counter",
			Description: "sum of calls from test",
			Unit: "float",
			Data: metricdata.Sum[float64]{
				DataPoints: []metricdata.DataPoint[float64]{
					{
						Attributes: attribute.NewSet(attribute.String("float counter label key", "float counter label value"), attribute.String("float counter optional label key", "float counter optional label value")), // What attributes do I attach here...label key values...optional labels can be tested by omission
						Value:      1.2,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		// bucket counts: 3 buckets with 1 0 0 - use different bounds for ints/floats though...
		{
			Name:           "int-histo",
			Description:    "histogram of call values from tests",
			Unit:           "int", // I think this is still the right unit just aggregated as a histo...
			// Define bucket counts too? argh should this just be default bounds? Looks like I may need to plumb bucket bounds through registry calls...
			Data: metricdata.Histogram[int64]{
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes:   attribute.NewSet(attribute.String("int histo label key", "int histo label value")),
						Count:        1,
						Bounds:       bounds,
						BucketCounts: []uint64{0, 1, 0, 0},
						Min:          metricdata.NewExtrema(int64(3)),
						Max:          metricdata.NewExtrema(int64(3)),
						Sum:          3,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name:           "float-histo",
			Description:    "histogram of call values from tests",
			Unit:           "float",
			// Define bucket counts too? argh
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes:   attribute.NewSet(attribute.String("float histo label key", "float histo label value"), attribute.String("float histo optional label key", "float histo optional label value")),
						Count:        1,
						Bounds:       bounds,
						BucketCounts: []uint64{0, 1, 0, 0},
						Min:          metricdata.NewExtrema(float64(4.3)),
						Max:          metricdata.NewExtrema(float64(4.3)),
						Sum:          4.3,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{ // What does a gauge emission even look like...I'll have to do this regardless just do it...
			Name:           "simple-gauge",
			Description:    "the most recent int emitted by test",
			Unit:           "int",
			// Record twice and make sure this is most recent...OTel concept but OTel is stable :).

			// 8 is most recent emission...
			Data: metricdata.Gauge[int64]{
				DataPoints: []metricdata.DataPoint[int64]{ // is this how gauges are represented?
					{
						Attributes: attribute.NewSet(attribute.String("int gauge label key", "int gauge label value")),
						Value:      8,
					},
				},
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


	// Assert int count isn't there, since loop is dependent on wantMetrics...
	// Behavior is not at all registered so no-op...do I need to check all 5
	// no-ops I don't think so...
	if _, ok := gotMetrics["int count 2"]; ok {
		t.Fatalf("Metric int count 2 present in recorded metrics, was not configured") // make these names consts? Doug wouldv'e said something on other PR if so...
	}

}
// Based on what Mark said...just testing plumbing essentially so I think I'm fine here don't need crazy emissions...

// Can't reuse these because these are defined in test body not RLS specific...



// metrics registry takes bounds (also allows you to define bounds in a metrics definition)

// If above unset, fall back to OTel defaults, (eventually exponential bounds)

// Orthogonal to OpenTelemetry taking precedence


/*
Actually, it was more like two precedences (for any stats handler):
1. Configured bounds (through metrics registry)/defined bounds
2. If above unset, fallback to OTel defaults, or when Exponential Bounds are supported Exponential Bounds

yup, this is exactly what i'm suggesting

You, 20 min
And then for OTel specifically there's another dimension of bounds being set in the meter provider which always takes precedence, but that's orthogonal to this 1 2 ordering for generic stats handlers
*/

// What happens if doesn't match up...

// so if set to nil, picks up otel defaults/exponential bounds

// document that in inst registry

// in otel read the field from desc for corresponding type of histo

// if nil otel defaults? Or does that happen already when you provide a nil bound?
// or if nil set to default otel bounds...

// how to represent the data in inst registry, and what should trigger fallback?


