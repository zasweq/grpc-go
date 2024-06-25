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

package instrumentregistry

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/internal/grpctest"
)

// keep the same API as metrics recorder although doesn't explicitly need to implement it, won't be defined in this PR

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// TestPanic tests that registering two instruments with the same name across
// any type of instrument triggers a panic.
func (s) TestPanic(t *testing.T) {
	defer ClearInstrumentRegistryForTesting()
	// this needs to be called in a defer
	want := "instrument simple counter already registered"
	defer func() {
		if r := recover(); r == nil { // typecast this to string?
			t.Errorf("expected panic %q, got %q", want, r)
		} // compare with any?
	}()
	RegisterInt64Count("simple counter", "number of times recorded on tests", "calls", nil, nil, false)
	RegisterInt64Gauge("simple counter", "number of times recorded on tests", "calls", nil, nil, false)
}

// fakeMetricsRecorder...uses inst registry? From the instruments in the
// instrument registry, it allocates data that will record measurements passed
// to this recorder from a test.

// Do not construct directly; use newFakeMetricsRecorder() instead.
type fakeMetricsRecorder struct {
	intCountInsts, floatCountInsts, intHistoInsts, floatHistoInsts []InstrumentDescriptor // verify something from this?

	t *testing.T

	// with Labels to verify fully? yes, I could also assert on the full data structures...see above
	int64counts []int64WithLabels
	float64counts []float64WithLabels
	int64histos []int64WithLabels
	float64histos []float64WithLabels
	int64gauges []int64WithLabels
}

func newFakeMetricsRecorder(t *testing.T) *fakeMetricsRecorder {
	// persist all information or is this fine?
	fmr := &fakeMetricsRecorder{t: t}
	for _, inst := range Int64CountInsts {
		fmr.int64counts = append(fmr.int64counts, int64WithLabels{
			labels: inst.Labels,
			optionalLabels: inst.OptionalLabels,
		})
	}
	for _, inst := range Float64CountInsts {
		fmr.float64counts = append(fmr.float64counts, float64WithLabels{
			labels: inst.Labels,
			optionalLabels: inst.OptionalLabels,
		})
	}
	for _, inst := range Int64HistoInsts {
		fmr.int64histos = append(fmr.int64histos, int64WithLabels{
			labels: inst.Labels,
			optionalLabels: inst.OptionalLabels,
		})
	}
	for _, inst := range Float64HistoInsts {
		fmr.float64histos = append(fmr.float64histos, float64WithLabels{
			labels: inst.Labels,
			optionalLabels: inst.OptionalLabels,
		})
	}
	for _, inst := range Int64GaugeInsts {
		fmr.int64gauges = append(fmr.int64gauges, int64WithLabels{
			labels: inst.Labels,
			optionalLabels: inst.OptionalLabels,
		})
	}
	return fmr // or declare locals
}

// 1 2 3 4, 11 2 33 4 <- don't cause collisions and handle correctly propagates through system

// TestInstrumentRegistry tests the instrument registry. It registers testing
// only instruments using the instrument registry, and creates a fake metrics
// recorder which uses these instruments. Using the handles returned from the
// instrument registry, this test records stats using the fake metrics recorder.
// Then, the test verifies the persisted metrics data in the metrics recorder is
// what is expected. Thus, this tests the interactions between the metrics
// recorder and the instruments registry.
func (s) TestInstrumentRegistry(t *testing.T) {
	defer ClearInstrumentRegistryForTesting()
	// All types or just one?
	// future metrics recorder API - test optional labels work too how I will verify later (move helper to shared place)

	// could assert on default and only construct on defaults let's wait until Doug's comment
	intCountHandle1 := RegisterInt64Count("int counter", "number of times recorded on tests", "calls", []string{"int counter label"}, []string{"int counter optional label"}, false)
	/*intCountHandle2 := */RegisterInt64Count("int counter 2", "number of times recorded on tests", "calls", []string{"int counter 2 label"}, []string{"int counter 2 optional label"}, false)

	floatCountHandle1 := RegisterFloat64Count("float counter", "number of times recorded on tests", "calls", []string{"float counter label"}, []string{"float counter optional label"}, false)
	intHistoHandle1 := RegisterInt64Histo("int histo", "", "calls", []string{"int histo label"}, []string{"int histo optional label"}, false)
	floatHistoHandle1 := RegisterFloat64Histo("float histo", "", "calls", []string{"float histo label"}, []string{"float histo optional label"}, false)
	// I could test labels and optional labels to make sure that works?
	intGaugeHandle1 := RegisterInt64Gauge("simple gauge", "the most recent int emitted by test", "int", []string{"int gauge label"}, []string{"int gauge optional label"}, false)


	// any cross product issues here?
	fmr := newFakeMetricsRecorder(t)
	// Should I require both labels in prod? Always emit in tests?
	fmr.RecordIntCount(intCountHandle1, []Label{{Key: "int counter label", Value: "some value"}}, []Label{{Key: "int counter optional label", Value: "some value"}}, 1)

	// wrong labels get eaten?
	// verify {1, 0} equivalent
	// intCountsWant := []int64{1, 0} // persist optional labels but can check all others too...
	intWithLabelsWant := []int64WithLabels{
		{
			value: 1,
			labels: []string{"int counter label"},
			optionalLabels: []string{"int counter optional label"},
		},
		{
			value: 0,
			labels: []string{"int counter 2 label"},
			optionalLabels: []string{"int counter 2 optional label"},
		},
	}
	if diff := cmp.Diff(fmr.int64counts, intWithLabelsWant, cmp.AllowUnexported(int64WithLabels{})); diff != "" {
		t.Fatalf("fmr.int64counts (-got, +want): %v", diff)
	}

	fmr.RecordFloatCount(floatCountHandle1, []Label{{Key: "float counter label", Value: "some value"}}, []Label{{Key: "float counter optional label", Value: "some value"}}, 1.2)
	// assert 1.2...
	// fmr.float64counts
	floatWithLabelsWant := []float64WithLabels{
		{
			value: 1.2,
			labels: []string{"float counter label"},
			optionalLabels: []string{"float counter optional label"},
		},
	}
	if diff := cmp.Diff(fmr.float64counts, floatWithLabelsWant, cmp.AllowUnexported(float64WithLabels{})); diff != "" {
		t.Fatalf("fmr.float64counts (-got, +want): %v", diff)
	}

	fmr.RecordIntHisto(intHistoHandle1, []Label{{Key: "int histo label", Value: "some value"}}, []Label{{Key: "int histo optional label", Value: "some value"}}, 3)
	intHistoWithLabelsWant := []int64WithLabels{
		{
			value: 3,
			labels: []string{"int histo label"},
			optionalLabels: []string{"int histo optional label"},
		},
	}
	if diff := cmp.Diff(fmr.int64histos, intHistoWithLabelsWant, cmp.AllowUnexported(int64WithLabels{})); diff != "" {
		t.Fatalf("fmr.int64histos (-got, +want): %v", diff)
	}

	fmr.RecordFloatHisto(floatHistoHandle1, []Label{{Key: "float histo label", Value: "some value"}}, []Label{{Key: "float histo optional label", Value: "some value"}}, 4)
	floatHistoWithLabelsWant := []float64WithLabels{
		{
			value: 4,
			labels: []string{"float histo label"},
			optionalLabels: []string{"float histo optional label"},
		},
	}
	if diff := cmp.Diff(fmr.float64histos, floatHistoWithLabelsWant, cmp.AllowUnexported(float64WithLabels{})); diff != "" {
		t.Fatalf("fmr.float64histos (-got, +want): %v", diff)
	}

	fmr.RecordIntGauge(intGaugeHandle1, []Label{{Key: "int gauge label", Value: "some value"}}, []Label{{Key: "int gauge optional label", Value: "some value"}}, 7)
	intGaugeWithLabelsWant := []int64WithLabels{
		{
			value: 7,
			labels: []string{"int gauge label"},
			optionalLabels: []string{"int gauge optional label"},
		},
	}
	if diff := cmp.Diff(fmr.int64gauges, intGaugeWithLabelsWant, cmp.AllowUnexported(int64WithLabels{})); diff != "" {
		t.Fatalf("fmr.int64gauges (-got, +want): %v", diff)
	}


	// maybe have this robust 11111
} // what happens in the failure case

// panic tested above, I wonder if Doug will think of any other edge cases to test...
// and the next one failure case don't test programmer error test panic

func (s) TestNumerousIntCounts(t *testing.T) {
	defer ClearInstrumentRegistryForTesting()
	intCountHandle1 := RegisterInt64Count("int counter", "number of times recorded on tests", "calls", []string{"int counter label"}, []string{"int counter optional label"}, false)
	intCountHandle2 := RegisterInt64Count("int counter 2", "number of times recorded on tests", "calls", []string{"int counter 2 label"}, []string{"int counter 2 optional label"}, false)
	intCountHandle3 := RegisterInt64Count("int counter 3", "number of times recorded on tests", "calls", []string{"int counter 3 label"}, []string{"int counter 3 optional label"}, false)

	fmr := newFakeMetricsRecorder(t)

	// 100
	fmr.RecordIntCount(intCountHandle1, []Label{{Key: "int counter label", Value: "some value"}}, []Label{{Key: "int counter optional label", Value: "some value"}}, 1)
	intWithLabelsWant := []int64WithLabels{
		{
			value: 1,
			labels: []string{"int counter label"},
			optionalLabels: []string{"int counter optional label"},
		},
		{
			value: 0,
			labels: []string{"int counter 2 label"},
			optionalLabels: []string{"int counter 2 optional label"},
		},
		{
			value: 0,
			labels: []string{"int counter 3 label"},
			optionalLabels: []string{"int counter 3 optional label"},
		},
	}
	if diff := cmp.Diff(fmr.int64counts, intWithLabelsWant, cmp.AllowUnexported(int64WithLabels{})); diff != "" {
		t.Fatalf("fmr.int64counts (-got, +want): %v", diff)
	}

	// 110
	fmr.RecordIntCount(intCountHandle2, []Label{{Key: "int counter 2 label", Value: "some value"}}, []Label{{Key: "int counter 2 optional label", Value: "some value"}}, 1)
	intWithLabelsWant = []int64WithLabels{
		{
			value: 1,
			labels: []string{"int counter label"},
			optionalLabels: []string{"int counter optional label"},
		},
		{
			value: 1,
			labels: []string{"int counter 2 label"},
			optionalLabels: []string{"int counter 2 optional label"},
		},
		{
			value: 0,
			labels: []string{"int counter 3 label"},
			optionalLabels: []string{"int counter 3 optional label"},
		},
	}
	if diff := cmp.Diff(fmr.int64counts, intWithLabelsWant, cmp.AllowUnexported(int64WithLabels{})); diff != "" {
		t.Fatalf("fmr.int64counts (-got, +want): %v", diff)
	}

	// 111
	fmr.RecordIntCount(intCountHandle3, []Label{{Key: "int counter 3 label", Value: "some value"}}, []Label{{Key: "int counter 3 optional label", Value: "some value"}}, 1)
	intWithLabelsWant = []int64WithLabels{
		{
			value: 1,
			labels: []string{"int counter label"},
			optionalLabels: []string{"int counter optional label"},
		},
		{
			value: 1,
			labels: []string{"int counter 2 label"},
			optionalLabels: []string{"int counter 2 optional label"},
		},
		{
			value: 1,
			labels: []string{"int counter 3 label"},
			optionalLabels: []string{"int counter 3 optional label"},
		},
	}
	if diff := cmp.Diff(fmr.int64counts, intWithLabelsWant, cmp.AllowUnexported(int64WithLabels{})); diff != "" {
		t.Fatalf("fmr.int64counts (-got, +want): %v", diff)
	}

	// 112
	fmr.RecordIntCount(intCountHandle3, []Label{{Key: "int counter 3 label", Value: "some value"}}, []Label{{Key: "int counter 3 optional label", Value: "some value"}}, 1)
	intWithLabelsWant = []int64WithLabels{
		{
			value: 1,
			labels: []string{"int counter label"},
			optionalLabels: []string{"int counter optional label"},
		},
		{
			value: 1,
			labels: []string{"int counter 2 label"},
			optionalLabels: []string{"int counter 2 optional label"},
		},
		{
			value: 2,
			labels: []string{"int counter 3 label"},
			optionalLabels: []string{"int counter 3 optional label"},
		},
	}
	if diff := cmp.Diff(fmr.int64counts, intWithLabelsWant, cmp.AllowUnexported(int64WithLabels{})); diff != "" {
		t.Fatalf("fmr.int64counts (-got, +want): %v", diff)
	}


	// ...
}

// first one in arg list so you're good here...
func verifyLabels(t *testing.T, labelsWant []string, optionalLabelsWant []string, labelsGot []Label, optionalLabelsGot []Label) /*what should it return?*/ {
	for i, label := range labelsWant {
		if labelsGot[i].Key != label {
			t.Fatalf("label key at position %v got %v, want %v", i, labelsGot[i].Key, label)
		}
	}
	if len(labelsWant) != len(labelsGot) {
		t.Fatalf("length of labels expected did not match got %v, want %v", len(labelsGot), len(optionalLabelsWant))
	}

	for i, label := range optionalLabelsWant {
		if optionalLabelsGot[i].Key != label {
			t.Fatalf("optional label key at position %v got %v, want %v", i, optionalLabelsGot[i].Key, label)
		}
	}
	if len(optionalLabelsWant) != len(optionalLabelsGot) {
		t.Fatalf("length of optional labels expected did not match got %v, want %v", len(optionalLabelsGot), len(optionalLabelsWant))
	}
}

func (r *fakeMetricsRecorder) RecordIntCount(handle Int64CountHandle, labels []Label, optionalLabels []Label, incr int64) {
	// Do optional labels as well?
	ic := r.int64counts[handle.Index]

	// the labels should positionally map too

	// Labels should match up too
	/*for i, label := range ic.labels {
		if labels[i].Key != label {
			r.t.Fatalf("label key at position %v got %v, want %v", i, labels[i].Key, label)
		}
	}
	if len(ic.labels) != len(labels) {
		r.t.Fatalf("length of labels expected did not match got %v, want %v", len(labels), len(ic.labels))
	}

	for i, label := range ic.optionalLabels {
		if optionalLabels[i].Key != label {
			r.t.Fatalf("optional label key at position %v got %v, want %v", i, optionalLabels[i].Key, label)
		}
	}
	if len(ic.optionalLabels) != len(optionalLabels) {
		r.t.Fatalf("length of optional labels expected did not match got %v, want %v", len(optionalLabels), len(ic.optionalLabels))
	}*/
	// Could refactor this to take labels/optionalLabels from each helper. How to do this cleanly?
	verifyLabels(r.t, ic.labels, ic.optionalLabels, labels, optionalLabels) // what to return? or just fail on t?



	// Locality already have access to right? in xDS Balancer need to plumb this up

	// Does this write to stack?

	print("incrementing by: ", incr)

	// check if out of index, if so fail test?
	r.int64counts[handle.Index].value += incr // assert on this +=
}

func (r *fakeMetricsRecorder) RecordFloatCount(handle Float64CountHandle, labels []Label, optionalLabels []Label, incr float64) { // Let's just do this?
	fc := r.float64counts[handle.Index]
	verifyLabels(r.t, fc.labels, fc.optionalLabels, labels, optionalLabels)
	r.float64counts[handle.Index].value += incr
} // carry down properties of instrument registry, so compare got vs. want, also what should I do with other parts of API

func (r *fakeMetricsRecorder) RecordIntHisto(handle Int64HistoHandle, labels []Label, optionalLabels []Label, incr int64) {
	ih := r.int64histos[handle.Index]
	verifyLabels(r.t, ih.labels, ih.optionalLabels, labels, optionalLabels)
	r.int64histos[handle.Index].value = incr
}

func (r *fakeMetricsRecorder) RecordFloatHisto(handle Float64HistoHandle, labels []Label, optionalLabels []Label, incr float64) {
	fh := r.float64histos[handle.Index]
	verifyLabels(r.t, fh.labels, fh.optionalLabels, labels, optionalLabels)
	r.float64histos[handle.Index].value = incr
}

func (r *fakeMetricsRecorder) RecordIntGauge(handle Int64GaugeHandle, labels []Label, optionalLabels []Label, incr int64) {
	ig := r.int64gauges[handle.Index]
	verifyLabels(r.t, ig.labels, ig.optionalLabels, labels, optionalLabels)
	// check if out of index, if so fail test? Could hold onto t in r
	r.int64gauges[handle.Index].value = incr
}

type int64WithLabels struct {
	value int64
	labels []string
	optionalLabels []string
}

type float64WithLabels struct {
	value float64
	labels []string
	optionalLabels []string
}
