/*
 *
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
 *
 */

package stats

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/internal/grpctest"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// TestPanic tests that registering two metrics with the same name across any
// type of metric triggers a panic.
func (s) TestPanic(t *testing.T) {
	snapshotMetricsRegistryForTesting(t)
	want := "metric simple counter already registered"
	defer func() {
		if r := recover(); !strings.Contains(fmt.Sprint(r), want) {
			t.Errorf("expected panic contains %q, got %q", want, r)
		}
	}()
	desc := MetricDescriptor{
		// Type is not expected to be set from the registerer, but meant to be
		// set by the metric registry.
		Name:        "simple counter",
		Description: "number of times recorded on tests",
		Unit:        "calls",
	}
	RegisterInt64Count(desc)
	RegisterInt64Gauge(desc)
}

// TestInstrumentRegistry tests the metric registry. It registers testing only
// metrics using the metric registry, and creates a fake metrics recorder which
// uses these metrics. Using the handles returned from the metric registry, this
// test records stats using the fake metrics recorder. Then, the test verifies
// the persisted metrics data in the metrics recorder is what is expected. Thus,
// this tests the interactions between the metrics recorder and the metrics
// registry.
func (s) TestMetricRegistry(t *testing.T) {
	snapshotMetricsRegistryForTesting(t)
	intCountHandle1 := RegisterInt64Count(MetricDescriptor{
		Name:           "simple counter",
		Description:    "sum of all emissions from tests",
		Unit:           "int",
		Labels:         []string{"int counter label"},
		OptionalLabels: []string{"int counter optional label"},
		Default:        false,
	})
	floatCountHandle1 := RegisterFloat64Count(MetricDescriptor{
		Name:           "float counter",
		Description:    "sum of all emissions from tests",
		Unit:           "float",
		Labels:         []string{"float counter label"},
		OptionalLabels: []string{"float counter optional label"},
		Default:        false,
	})
	intHistoHandle1 := RegisterInt64Histo(MetricDescriptor{
		Name:           "int histo",
		Description:    "sum of all emissions from tests",
		Unit:           "int",
		Labels:         []string{"int histo label"},
		OptionalLabels: []string{"int histo optional label"},
		Default:        false,
	})
	floatHistoHandle1 := RegisterFloat64Histo(MetricDescriptor{
		Name:           "float histo",
		Description:    "sum of all emissions from tests",
		Unit:           "float",
		Labels:         []string{"float histo label"},
		OptionalLabels: []string{"float histo optional label"},
		Default:        false,
	})
	intGaugeHandle1 := RegisterInt64Gauge(MetricDescriptor{
		Name:           "simple gauge",
		Description:    "the most recent int emitted by test",
		Unit:           "int",
		Labels:         []string{"int gauge label"},
		OptionalLabels: []string{"int gauge optional label"},
		Default:        false,
	})

	fmr := newFakeMetricsRecorder(t)

	intCountHandle1.Record(fmr, 1, []string{"some label value", "some optional label value"}...)
	// The Metric Descriptor in the handle should be able to identify the metric
	// information. This is the key passed to metrics recorder to identify
	// metric.
	if got := fmr.intValues[(*MetricDescriptor)(intCountHandle1)]; got != 1 {
		t.Fatalf("fmr.intValues[intCountHandle1.MetricDescriptor] got %v, want: %v", got, 1)
	}

	floatCountHandle1.Record(fmr, 1.2, []string{"some label value", "some optional label value"}...)
	if got := fmr.floatValues[(*MetricDescriptor)(floatCountHandle1)]; got != 1.2 {
		t.Fatalf("fmr.floatValues[floatCountHandle1.MetricDescriptor] got %v, want: %v", got, 1.2)
	}

	intHistoHandle1.Record(fmr, 3, []string{"some label value", "some optional label value"}...)
	if got := fmr.intValues[(*MetricDescriptor)(intHistoHandle1)]; got != 3 {
		t.Fatalf("fmr.intValues[intHistoHandle1.MetricDescriptor] got %v, want: %v", got, 3)
	}

	floatHistoHandle1.Record(fmr, 4.3, []string{"some label value", "some optional label value"}...)
	if got := fmr.floatValues[(*MetricDescriptor)(floatHistoHandle1)]; got != 4.3 {
		t.Fatalf("fmr.floatValues[floatHistoHandle1.MetricDescriptor] got %v, want: %v", got, 4.3)
	}

	intGaugeHandle1.Record(fmr, 7, []string{"some label value", "some optional label value"}...)
	if got := fmr.intValues[(*MetricDescriptor)(intGaugeHandle1)]; got != 7 {
		t.Fatalf("fmr.intValues[intGaugeHandle1.MetricDescriptor] got %v, want: %v", got, 7)
	}
}

// TestNumerousIntCounts tests numerous int count metrics registered onto the
// metric registry. A component (simulated by test) should be able to record on
// the different registered int count metrics.
func TestNumerousIntCounts(t *testing.T) {
	snapshotMetricsRegistryForTesting(t)
	intCountHandle1 := RegisterInt64Count(MetricDescriptor{
		Name:           "int counter",
		Description:    "sum of all emissions from tests",
		Unit:           "int",
		Labels:         []string{"int counter label"},
		OptionalLabels: []string{"int counter optional label"},
		Default:        false,
	})
	intCountHandle2 := RegisterInt64Count(MetricDescriptor{
		Name:           "int counter 2",
		Description:    "sum of all emissions from tests",
		Unit:           "int",
		Labels:         []string{"int counter label"},
		OptionalLabels: []string{"int counter optional label"},
		Default:        false,
	})
	intCountHandle3 := RegisterInt64Count(MetricDescriptor{
		Name:           "int counter 3",
		Description:    "sum of all emissions from tests",
		Unit:           "int",
		Labels:         []string{"int counter label"},
		OptionalLabels: []string{"int counter optional label"},
		Default:        false,
	})

	fmr := newFakeMetricsRecorder(t)

	intCountHandle1.Record(fmr, 1, []string{"some label value", "some optional label value"}...)
	got := []int64{fmr.intValues[(*MetricDescriptor)(intCountHandle1)], fmr.intValues[(*MetricDescriptor)(intCountHandle2)], fmr.intValues[(*MetricDescriptor)(intCountHandle3)]}
	want := []int64{1, 0, 0}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("fmr.intValues (-got, +want): %v", diff)
	}

	intCountHandle2.Record(fmr, 1, []string{"some label value", "some optional label value"}...)
	got = []int64{fmr.intValues[(*MetricDescriptor)(intCountHandle1)], fmr.intValues[(*MetricDescriptor)(intCountHandle2)], fmr.intValues[(*MetricDescriptor)(intCountHandle3)]}
	want = []int64{1, 1, 0}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("fmr.intValues (-got, +want): %v", diff)
	}

	intCountHandle3.Record(fmr, 1, []string{"some label value", "some optional label value"}...)
	got = []int64{fmr.intValues[(*MetricDescriptor)(intCountHandle1)], fmr.intValues[(*MetricDescriptor)(intCountHandle2)], fmr.intValues[(*MetricDescriptor)(intCountHandle3)]}
	want = []int64{1, 1, 1}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("fmr.intValues (-got, +want): %v", diff)
	}

	intCountHandle3.Record(fmr, 1, []string{"some label value", "some optional label value"}...)
	got = []int64{fmr.intValues[(*MetricDescriptor)(intCountHandle1)], fmr.intValues[(*MetricDescriptor)(intCountHandle2)], fmr.intValues[(*MetricDescriptor)(intCountHandle3)]}
	want = []int64{1, 1, 2}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Fatalf("fmr.intValues (-got, +want): %v", diff)
	}
}

type fakeMetricsRecorder struct {
	t *testing.T

	intValues   map[*MetricDescriptor]int64
	floatValues map[*MetricDescriptor]float64
}

// newFakeMetricsRecorder returns a fake metrics recorder based off the current
// state of global metric registry.
func newFakeMetricsRecorder(t *testing.T) *fakeMetricsRecorder {
	fmr := &fakeMetricsRecorder{
		t:           t,
		intValues:   make(map[*MetricDescriptor]int64),
		floatValues: make(map[*MetricDescriptor]float64),
	}

	for _, desc := range metricsRegistry { // This is no longer exported - should I make this a knob/configurable or something?
		switch desc.Type {
		case MetricTypeIntCount:
		case MetricTypeIntHisto:
		case MetricTypeIntGauge:
			fmr.intValues[desc] = 0
		case MetricTypeFloatCount:
		case MetricTypeFloatHisto:
			fmr.floatValues[desc] = 0
		}
	}
	return fmr
} // This takes a snapshot of global inst registry *at this components creation time*
// When you create in test you have a linking snapshot to a component...

// persistence of desc -> int/float seems pretty generalizable...

// Have two of these? Or just one for simplicity

// verifyLabels verifies that the labels received are of the expected length.
func verifyLabels(t *testing.T, labelsWant []string, optionalLabelsWant []string, labelsGot []string) {
	if len(labelsWant)+len(optionalLabelsWant) != len(labelsGot) {
		t.Fatalf("length of optional labels expected did not match got %v, want %v", len(labelsGot), len(labelsWant)+len(optionalLabelsWant))
	}
}

func (r *fakeMetricsRecorder) RecordInt64Count(handle *Int64CountHandle, incr int64, labels ...string) {
	verifyLabels(r.t, (*MetricDescriptor)(handle).Labels, (*MetricDescriptor)(handle).OptionalLabels, labels)
	r.intValues[(*MetricDescriptor)(handle)] += incr
}

func (r *fakeMetricsRecorder) RecordFloat64Count(handle *Float64CountHandle, incr float64, labels ...string) {
	verifyLabels(r.t, (*MetricDescriptor)(handle).Labels, (*MetricDescriptor)(handle).OptionalLabels, labels)
	r.floatValues[(*MetricDescriptor)(handle)] += incr
}

func (r *fakeMetricsRecorder) RecordInt64Histo(handle *Int64HistoHandle, incr int64, labels ...string) {
	verifyLabels(r.t, (*MetricDescriptor)(handle).Labels, (*MetricDescriptor)(handle).OptionalLabels, labels)
	r.intValues[(*MetricDescriptor)(handle)] += incr
}

func (r *fakeMetricsRecorder) RecordFloat64Histo(handle *Float64HistoHandle, incr float64, labels ...string) {
	verifyLabels(r.t, (*MetricDescriptor)(handle).Labels, (*MetricDescriptor)(handle).OptionalLabels, labels)
	r.floatValues[(*MetricDescriptor)(handle)] += incr
}

func (r *fakeMetricsRecorder) RecordInt64Gauge(handle *Int64GaugeHandle, incr int64, labels ...string) {
	verifyLabels(r.t, (*MetricDescriptor)(handle).Labels, (*MetricDescriptor)(handle).OptionalLabels, labels)
	r.intValues[(*MetricDescriptor)(handle)] += incr // just make this == and it'll be functionally equivalent...
}

// Write a balancer file that registers on instrument registry, records metrics on operations...?
// Do I need to get this to work? Deploying as top level balancer?
// RLS triggers by picker picks, dependent on target etc...

// WRR is scheduler update

// What about a true e2e test...?
// Test plumbing, needs to be on a channel...as metricsrecorder list is part of the channel, calls hit the one channel provides
// and hit all the OTel configures on channel

// so will need this deployment/setup infrastructure for e2e test...refactor it that way I guess...

// Deploy RLS/WRR/this balancer as top level - see test infra to see if there is
// already a way to do this...


// switch context to context.TODO don't take it as a component

// follow up with OTel time as to why they take a context in Go but not Java and C

// ignore merging map
// merge all the logic into an embedded struct, don't even need to declare it on client/server



// Doug said he's fine with my testing plan

// Global and Local dial option
// hits the two with the same metrics calls...

// So need to figure out API I want

// honestly same data structures idc about histos as much except
// need the dimension of key values


// counter - count for ** labels values **

// counter - given set of values, what value was recorded, basically just do a counter

// histo - given set of values - list of record calls for label values
// buckets - more opinionated about values receiving

// gauge - last thing...

// Also need to check based off the labels - Yijie did this by hashing a
// concatenation of k v pairs (what test labels emit) vs. the assertion needed to
// happen

// ***For a certain set of labels*** (Yijie represented with hash function)
// Count is 1, so coupled, so can just check number emitted

// Histo - persists all the most recent (deterministic?)

// Gauge - just the most recent as a gauge would...makes sense...

// When I get back figure out data structures/API's...does it need to be this heavyweight to test plumbing?


// The only thing that I will eventually record Histos on is endpoint weights...
// So I don't think I need to think about too much

// Also per label...test just verifies label...do I really need this for this PR though?

// fake metric recorder 1, fake metric recorder 2

// Verifies emissions upward...

// going to implement fake sh and mr anyway...might as well make the verifications useful?

// How will I do label key value look into map thingy?

// I will need to test the plumbing for 5? So might as well do it...
// RLS/WRR will need these ideals...

// What metric it actually is...and the values for certain key value pairs for those metrics...
// map[Metric]map[labels]->a certain value

// sum/gauge but for histos he kept all and verified on the full list

// I don't want to make histos as heavyweight check
// Just have it how it is now - but needs to scale up labels





// Given set of labels, what is the value?
