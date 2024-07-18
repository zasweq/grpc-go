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


// I might want to keep this here...

// I think this component is fine as is...make it more nuanced?
/*
type FakeMetricsRecorder struct {
	t *testing.T

	IntValues   map[*MetricDescriptor]int64 // Should tests make assertions on these directly?
	FloatValues map[*MetricDescriptor]float64
} // is this a circular dependency? Oh access external package internal symbols through internal...

// NewFakeMetricsRecorder returns a fake metrics recorder based off the current
// state of global metric registry.
func NewFakeMetricsRecorder(t *testing.T) *FakeMetricsRecorder {
	fmr := &FakeMetricsRecorder{
		t:           t,
		IntValues:   make(map[*MetricDescriptor]int64),
		FloatValues: make(map[*MetricDescriptor]float64),
	}

	// Don't have access to the metrics registry - provide a knob/MetricDescriptor and get from metrics registry to build out
	// the values persisted here? Construct with some sort of knob?

	return fmr
}
// Verify labels happens at a layer below

// Circular dependency...
func (r *FakeMetricsRecorder) RecordInt64Count(handle *Int64CountHandle, incr int64, labels ...string) {
	verifyLabels(r.t, (*MetricDescriptor)(handle).Labels, (*MetricDescriptor)(handle).OptionalLabels, labels)
	// Any other behaviors needed? This mapping seems solid...
	r.intValues[(*MetricDescriptor)(handle)] += incr
}*/

// It's a data sink for RLS Metrics, is sum good enough? Older persistence?

// Or a new one for this test and new metrics?

// What data structures provide useful assertions/how to configure...

// Counters which build over time keep adding

// Histo nothing special OTel handles it

// Gauge is most recent...

