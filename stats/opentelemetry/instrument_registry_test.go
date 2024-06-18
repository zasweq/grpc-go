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

package opentelemetry

import "testing"

func (s) TestInstrumentRegistry(t *testing.T) {
	// Unit test this handle thing...

	// Ask Yash wtf do I even test here?

	intCountHandle := RegisterIntCount() // These API Calls how do I even test them? Downstream effect is global

	floatCountHandle := RegisterFloatCount()

	intHistoHandle := RegisterIntHisto()

	floatHistoHandle := RegisterFloatHisto() // how do I test this - also comes after init

	// Error condition: panic - how do I test that (could return to plumbing back an error...)
}

func (s) TestE2E(t *testing.T) {
	// Register an instrument
	intCountHandle := RegisterIntCount("instrument name", "instrument description", "RPCs", []string{"label"}, []string{"optional label"}, true) // how does this merge with defualt set, and does it ever "eat" label emissions?

	// and then take a handle and record on it...

	// but the only thing that uses these globals is real OpenTelemetry stats handler
	// Is this "fake stats handlers"
	intCountInsts // []instDesc - this is what I need to read to test, what should my fake stats handler do?


	// Expect metrics emissions
}

// "Register fake instruments and fake stats handlers"

// MetricsRecorder stats handler - can verify

// how to test metrics recorder list and how to typecast? Give a list of questions to ask Doug...

type fakeMetricsRecorder struct {
	// Data structures to verify properties of instrument registry...
	intCountInsts, floatCountInsts, intHistoInsts, floatHistoInsts []instDesc
}

// properties of instrument registry to test:
// 1. handle returned is correct
// 2. it correctly persists the data downward

// newFakeMetricsRecorder...creates based off instrument registry.
func newFakeMetricsRecorder() *fakeMetricsRecorder {
	intCountInsts // []instDesc - read this and do something with it

	for _, inst := range intCountInsts {
		// I think if you just persist this around

		inst. // what to do with this? Do it as OTel does it? Trace it down to something verifiable
	}

	floatCountInsts // []floatDesc - read this and do something with it (probably need to keep ordering to verify properties)
	intHistoInsts
	floatHistoInsts
	return &fakeMetricsRecorder{
		intCountInsts: intCountInsts, // copy, this is a pointer to same so if something else modifies this might not be right...
		floatCountInsts: floatCountInsts,
		intHistoInsts: intHistoInsts,
		floatHistoInsts: floatHistoInsts,
	}
}

// call into API's with handle to verify something
func (r *fakeMetricsRecorder) RecordIntCount(handle IntCountHandle, labels []Label, optionalLabels []Label, incr int64) {

	// and read the inst descs from the handle passed this correctly tests...

	// how do I verify
	r // some type of data in this to declare instruments
	r.intCountInsts[handle.index] // verify this is what is expected...does this verify labels?

	// should I do something with these labels/optionalLabels passed in, this will show me how this layer will eventually work...
}

func (r *fakeMetricsRecorder) RecordFloatCount(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {



}

func (r *fakeMetricsRecorder) RecordIntHisto(handle IntHistoHandle, labels []Label, optionalLabels []Label, incr int64) {}

func (r *fakeMetricsRecorder) RecordFloatHisto(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {}

// Two more layers to figure out how to unit test - the OpenTelemetry Metric Recorder API (which uses instrument registry0

// and once I add that metrics recorder list thing, that layer...
// does that layer eat the optional labels/labels if doesn't match up or does it need to assume a component will
// attach correct labels...
