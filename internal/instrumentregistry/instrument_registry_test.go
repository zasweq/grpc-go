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

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// "Register fake instruments and fake stats handlers"

// Give a list of questions to ask Doug...
// and also plumbing for this non per call metrics things wrt interfaces...

// fakeMetricsRecorder...uses inst registry? From the instruments in the
// instrument registry, it allocates data that will record measurements passed
// to this recorder from a test.

// Do not construct directly; use newMetricsRecorder() instead.
type fakeMetricsRecorder struct {
	intCountInsts, floatCountInsts, intHistoInsts, floatHistoInsts []InstrumentDescriptor // verify something from this?

	// "So our fake stats plugin contains a data structure that shows the
	// current values of the metrics, and that's what we use for testing" -
	// current values of the metrics

	// The instruments registered are real, not fake.  It's just that they are
	// registered only within the tests, not in production code.

	// persists the total of the measurement points at each index.

	int64counts []int64
	float64counts []float64

	// floatCounts?

	// how to specify bounds? we don't really need bounds
	// data structure for int/float histo here
	// persists the most recent of measurement points at each index
	int64histos []int64
	float64histos []float64
	int64gauge []int64
}

// TestPanic tests that registering two instruments with the same name across
// any type of instrument triggers a panic.
func (s) TestPanic(t *testing.T) {
	RegisterInt64Count("simple counter", "number of times recorded on tests", "calls", nil, nil, false)
	RegisterInt64Gauge("simple counter", "number of times recorded on tests", "calls", nil, nil, false)
	want := "instrument simple counter already registered"
	if r := recover(); r != want {
		t.Errorf("expected panic %q, got %q", want, r)
	} // compare with any?
}

// one edge case is probably numerous counts

// 1 2 3 4, 11 2 33 4 <- don't cause collisions and handle correctly propagates through system

// TestInstrumentRegistry...
func (s) TestInstrumentRegistry(t *testing.T) {
	// Java checks it at runtime. Throw an exception if labels don't match. Logic fail fast.
	intCountHandle1 := RegisterInt64Count("simple counter", "number of times recorded on tests", "calls", nil, nil, false) // have numerous?
	intCountHandle2 := RegisterInt64Count("simple counter 2", "number of times recorded on tests", "calls", nil, nil, false) // store names around


	// intCountHandle provides scope for the new metric...nothing
	// global recording point if string
	// but handle gives only a component access to record a metric...links arbitraryness/globalness to this
	// otherwise would string compare against a whole map

	// things derive in the system from this int

	// it also allows passing something back, it can be a string under the hood
	// 1000's of metrics to avoid string comparisons
	// don't need to grep for it

	// I could test labels and optional labels to make sure that works?
	intGaugeHandle := RegisterInt64Gauge("simple gauge", "the most recent int emitted by test", "int", nil, nil, false)



	fmr := newFakeMetricsRecorder() // talk about do not construct directly


	// Record metrics for this int count thing...

	// just do once per
	fmr.RecordIntCount(intCountHandle1, nil, nil, 1) // labels and optional labels would be done in the stats handler...this doesn't need to look at it, just incr one and ignore inc for each record call?

	intCountsWant := []int64{1, 0} // this
	// "cpo.GetLabels returned unexpected value (-got, +want): %v", diff
	if diff := cmp.Diff(fmr.int64counts, intCountsWant); diff != "" { // could expose an api that reads fmr.int64 counts...
		t.Fatalf("fmr.int64counts (-got, +want): %v", diff)
	} // Get this working?

	// with numerous 1 2
	fmr.RecordIntCount(intCountHandle2, nil, nil, 1)

	// start with 1 1 2 1
	// then record on 1
	// should be 1 2 2 1
	intCountsWant = []int64{1, 1}
	if diff := cmp.Diff(fmr.int64counts, intCountsWant); diff != "" {
		t.Fatalf("fmr.int64counts (-got, +want): %v", diff)
	}
	fmr.RecordIntCount(intCountHandle1, nil, nil, 1)
	intCountsWant = []int64{2, 1}
	if diff := cmp.Diff(fmr.int64counts, intCountsWant); diff != "" {
		t.Fatalf("fmr.int64counts (-got, +want): %v", diff)
	} // If I get this working can...make sure it compiles
	fmr.RecordIntCount(intCountHandle2, nil, nil, 1)
	intCountsWant = []int64{2, 2}
	if diff := cmp.Diff(fmr.int64counts, intCountsWant); diff != "" {
		t.Fatalf("fmr.int64counts (-got, +want): %v", diff)
	}


	fmr.RecordIntHisto() // and register and figure out data structure

	fmr.RecordFloatHisto() // and register and figure out data structure...




	fmr.RecordIntGauge(intGaugeHandle, nil, nil, 1)
	intGaugeWant := []int64{1}
	if diff := cmp.Diff(fmr.int64gauge, intGaugeWant); diff != "" {
		t.Fatalf("fmr.int64counts (-got, +want): %v", diff)
	}

	fmr.RecordIntGauge(intGaugeHandle, nil, nil, 2) // should override, not add
	intGaugeWant = []int64{2}
	if diff := cmp.Diff(fmr.int64gauge, intGaugeWant); diff != "" {
		t.Fatalf("fmr.int64counts (-got, +want): %v", diff)
	}

	// Should histos build out bucket counts or is this handled by OTel...
	// test most recent emission? What data structure same thing?
	// Should I have a float count?

	RegisterFloat64Count() // Don't really need this...
} // pull this into numerous tests?

// properties of instrument registry to test:
// 1. handle returned is correct
// 2. it correctly persists the data downward

// newFakeMetricsRecorder...creates based off instrument registry.
func newFakeMetricsRecorder() *fakeMetricsRecorder {
	Int64CountInsts // []instDesc - read this and do something with it

	for _, inst := range Int64CountInsts { // in OTel: internal.IntCountInsts
		// I think if you just persist this around

		inst. // what to do with this? Do it as OTel does it? Trace it down to something verifiable
	}

	FloatCountInsts // []floatDesc - read this and do something with it (probably need to keep ordering to verify properties)
	Int64HistoInsts
	Float64HistoInsts

	fmr := &fakeMetricsRecorder{}
	// store inst to verify?
	// use the registry to create something

	// "So our fake stats plugin contains a data structure that shows the
	// current values of the metrics, and that's what we use for testing" -
	// current values of the metrics

	// need more than one to test invariant properties

	fmr.int64counts = make([]int64, len(Int64CountInsts))

	for _, inst := range Int64CountInsts {
		// Store the data structure with same invariants
		// as instrument registry...
		// []CurrentValueOfMetric...use index to look into this...
		// Should I store the full array? Then would need to typecast the storage too?

		// interactions between the two...store info about the metric or some other property of inst registry...

		// Data structure that represents an int count...just an int64 right...
		// fmr.int64counts = append(fmr.int64counts, 0) // allocate same amount of space

	} // stored in meter for OTel?
	// or allocate ehre?



	// At creation time OTel uses this to create meters with the same data structures,
	// then receives measurements for those meters from API's below...

	// OTel instruments that record the stats data passed through API, that's
	// how it links...

	// this needs to also create a data structure that when metric data comes it
	// it can record on...then verify the data structures (analogous to OTel instruments)
	// as you provide it metric data...


	return &fakeMetricsRecorder{ // takes a "snapshot" at this objects creation time like a picker a synchronous snapshot of async system
		intCountInsts:   Int64CountInsts, // copy, this is a pointer to same so if something else modifies this might not be right...
		floatCountInsts: FloatCountInsts,
		intHistoInsts:   Int64HistoInsts,
		floatHistoInsts: Float64HistoInsts,
	}
}

// The instruments registered are real, not fake.  It's just that they are
// registered only within the tests, not in production code.

// What edge cases of the inst registry occur that I can verify?

// registering across API's triggers a panic?

//


// call into API's with handle to verify something
func (r *fakeMetricsRecorder) RecordIntCount(handle Int64CountHandle, labels []Label, optionalLabels []Label, incr int64) {

	// and read the inst descs from the handle passed this correctly tests...

	r.intCountInsts[handle.index] // verify this is what is expected...does this verify labels?

	// should I do something with these labels/optionalLabels passed in, this will show me how this layer will eventually work...

	inst := r.intCountInsts[handle.index]
	// Compare inst to a got?
	inst // compare all persisted data to all



	// check if out of index, if so fail test?
	r.int64counts[handle.index] += incr // assert on this +=
}



func (r *fakeMetricsRecorder) RecordFloatCount(handle Float64CountHandle, labels []Label, optionalLabels []Label, incr float64) { // Let's just do this?
	r.float64counts[handle.index] += incr
} // carry down properties of instrument registry, so compare got vs. want, also what should I do with other parts of API

func (r *fakeMetricsRecorder) RecordIntHisto(handle Int64HistoHandle, labels []Label, optionalLabels []Label, incr int64) {
	r.int64histos[handle.index] = incr
}


func (r *fakeMetricsRecorder) RecordFloatHisto(handle Float64CountHandle, labels []Label, optionalLabels []Label, incr float64) {
	r.float64histos[handle.index] = incr
}

func (r *fakeMetricsRecorder) RecordIntGauge(handle Int64GaugeHandle, labels []Label, optionalLabels []Label, incr int64) {
	// check if out of index, if so fail test? Could hold onto t in r
	r.int64gauge[handle.index] = incr
}

// Two more layers to figure out how to unit test - the OpenTelemetry Metric Recorder API (which uses instrument registry0

// and once I add that metrics recorder list thing, that layer...
// does that layer eat the optional labels/labels if doesn't match up or does it need to assume a component will
// attach correct labels...

// How do I test usage in OTel
// and []MetricsRecorder (which will likely eat emissions)


