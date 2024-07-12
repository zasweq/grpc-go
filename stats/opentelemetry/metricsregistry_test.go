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
	"go.opentelemetry.io/otel/attribute"
	"testing"
	"time"

	"google.golang.org/grpc/experimental/stats"
	"google.golang.org/grpc/internal/grpctest"

	"go.opentelemetry.io/otel/sdk/metric" // otelmetric as other places?
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

var defaultTestTimeout = 5 * time.Second

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
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

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	reader := metric.NewManualReader() // Don't need stub server anymore since this component records...oh yeah optional label part of test...
	provider := metric.NewMeterProvider(metric.WithReader(reader))

	// and add that to default set other 4 have test for plumbing...create top
	// level comment that describes sccenario...or maybe wait until after it
	// works...

	mo := MetricsOptions{
		Context: context.Background(), // Should this have timeout might fail vet
		Metrics: DefaultMetrics().Add(stats.Metric("int counter 3")), // Does this work?
		OptionalLabels: []string{}, // Setup some optional labels from tests...
		MeterProvider: provider, // This still needs a manual reader...and move to non per call test...
	}

	// Create an OTel plugin...use defaultMetrics() runtime, we'll see what Doug
	// says about leaving stuff unexported...
	// How to test client/server split - or say they hit the same helpers
	ssh := &serverStatsHandler{options: Options{MetricsOptions: mo}}
	ssh.initializeMetrics() // need to do context.Background() dance too, nah set in options...initalize reads the global registry...



	// Don't have access to ssh in dial option, so just create one directly

	// Pass that OTel plugin to handle...

	// When it hits make sure simulates layer below of eating labels...

	intCountHandle1.Record(ssh, 1, []string{"int counter 1 label value", "int counter 1 optional label value"}...)
	// Not part of metrics specified (not default), so this call shouldn't show up in emissions...
	intCountHandle2.Record(ssh, 2, []string{"int counter 2 label value", "int counter 2 optional label value"}...)
	// Part of metrics specified, so this call should show up in emissions...
	intCountHandle3.Record(ssh, 4, []string{"int counter 3 label value", "int counter 3 optional label value"}...) // record the same values...

	// These recording points should show up in emissions as they are defaults...

	// Test permutations of optional label values logic...configure only some, only those should show up...
	floatCountHandle1.Record(ssh, 1.2, []string{"float counter label value", "float counter optional label value"}...) // these labels should be specific, make specific
	intHistoHandle1.Record(ssh, 3, []string{"int histo label value", "int histo optional label value"}...)
	floatHistoHandle1.Record(ssh, 4.3, []string{"float histo label value", "float histo optional label value"}...)
	intGaugeHandle1.Record(ssh, 7, []string{"int histo label value", "int histo optional label value"}...) // size should match, below does this for us no need to verify...
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

	// Figure out full sceanrio first?

	rm := &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm) // Same context you provide to OTel? Yeah scope it with a default test timeout tests that record flow too...
	gotMetrics := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}

	// Metrics are stable from their perspective which is why this e2e test is
	// robust...
	wantMetrics := []metricdata.Metrics{ // Other dimension is optional labels turned on or not...figure out how to do partition...
		// Int count 1 and 3 (configured):
		{ // No attribute just buckets into 1 essentially right?
			Name: "int counter 1",
			Description: "Sum of calls from test",
			Unit: "int",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr), // What attributes do I attach here...label key values...optional labels can be tested by omission
						Value:      1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{
			Name: "int counter 3",
			Description: "Sum of calls from test",
			Unit: "int",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr), // What attributes do I attach here...label key values...optional labels can be tested by omission
						Value:      4,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
		{ // Drop the numbers...
			Name: "float counter 1",
			Description: "Sum of calls from test",
			Unit: "float",
			Data: metricdata.Sum[float64]{
				DataPoints: []metricdata.DataPoint[float64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr), // What attributes do I attach here...label key values...optional labels can be tested by omission
						Value:      1.2,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		},
	}

} // I think this full test is fine, maybe hard to get to compile but I think we'll be good...


// What are input variables?
// 5 different types such as count, histo, and gauge


func (s) CreateMetricsAtoms() { // What knobs, what do I pass back or just do this inline...

} // Based on what Mark said...just testing plumbing essentially so I think I'm fine here...

