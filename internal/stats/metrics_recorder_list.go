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

package stats

import (
	estats "google.golang.org/grpc/experimental/stats"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/stats"
)

// Import as istats?

var logger = grpclog.Component("metrics-recorder-list") // oh if I move whole test to internal including test utils won't populate external namespace...

// MetricRecorderList forwards...


// Eats and logs if labels don't match up...?
type MetricsRecorderList struct {
	// metricsRecorders are the metrics recorders this list will forward to.
	metricsRecorders []estats.MetricsRecorder // estats?
}

// NewMetricsRecorderList creates a new metric recorder list with all the stats
// handlers provided which implement the MetricsRecorder interface...

// Talk about functional no-op?
func NewMetricsRecorderList(shs []stats.Handler) *MetricsRecorderList {
	print("In new metrics recorder list")
	var mrs []estats.MetricsRecorder

	for _, sh := range shs {
		if mr, ok := sh.(estats.MetricsRecorder); ok {
			mrs = append(mrs, mr) // Does this operation actually wokr?
		}
	}
	print("new metrics recorder list, len of mrs: ", len(mrs)) // correct length...just doesn't hit record

	return &MetricsRecorderList{
		metricsRecorders: mrs, // if this length is 0, this is a functional no-op
	}
}

func verifyLabels() { // I like this as a function call still rather than length check? Honestly could just length check

}

// New interface (see estats.MetricsRecorder api) and also
// it gets labels/optional labels from what is provided, and just does a length check
func (l *MetricsRecorderList) RecordInt64Count(handle *estats.Int64CountHandle, incr int64, labels ...string) {
	print("in metric recorder list record int64 count")
	/*handle // *estats.Int64CountHandle, I don't need to address the registry this has labels/optional labels...
	handle.Labels // []string
	handle.OptionalLabels // []string*/

	// labels... []string? Keep same API as previous...and then put this in cc/server...
	// Inline check rather than passing to a helper?
	if got, want := len(handle.Labels) + len(handle.OptionalLabels), len(labels); got != want { // so var args are a slice...
		// fmt.Errorf("length of labels passed incorrect got: %v, want: %v",  len(optionalLabelsGot), len(optionalLabelsWant))
		// logger? an error/warning with got/expected length...
		logger.Infof("length of labels passed to RecordInt64Count incorrect got: %v, want: %v", got, want)
	} // Assert on a property of this error string in tests? when I "eat" call or just use fact it didn't record...

	for _, metricRecorder := range l.metricsRecorders { // so metric recorder passed to it in build is nil...
		print("in metric recorder list metric recorder iteration")
		metricRecorder.RecordInt64Count(handle, incr, labels...)
	}
} // Honestly this is orthogonal to the PR in flight...I think I can just treat this as separate...

func (l *MetricsRecorderList) RecordFloat64Count(handle *estats.Float64CountHandle, incr float64, labels ...string) {
	if got, want := len(handle.Labels) + len(handle.OptionalLabels), len(labels); got != want { // so var args are a slice...
		// fmt.Errorf("length of labels passed incorrect got: %v, want: %v",  len(optionalLabelsGot), len(optionalLabelsWant))
		// logger? an error/warning with got/expected length...
		// I think just a grpc logger as per Doug's comment...
		logger.Infof("length of labels passed to RecordFloat64Count incorrect got: %v, want: %v", got, want)
	}

	for _, metricRecorder := range l.metricsRecorders {
		metricRecorder.RecordFloat64Count(handle, incr, labels...) // same pointer, typecast the pointer as equivalent, I copied then pointed to new copy...
	}
}

func (l *MetricsRecorderList) RecordInt64Histo(handle *estats.Int64HistoHandle, incr int64, labels ...string) {
	if got, want := len(handle.Labels) + len(handle.OptionalLabels), len(labels); got != want { // so var args are a slice...
		// fmt.Errorf("length of labels passed incorrect got: %v, want: %v",  len(optionalLabelsGot), len(optionalLabelsWant))
		// logger? an error/warning with got/expected length...
		logger.Infof("length of labels passed to RecordInt64Histo incorrect got: %v, want: %v", got, want)
	}

	for _, metricRecorder := range l.metricsRecorders {
		metricRecorder.RecordInt64Histo(handle, incr, labels...)
	}
} // do argument names need to match up to implement an interface?

func (l *MetricsRecorderList) RecordFloat64Histo(handle *estats.Float64HistoHandle, incr float64, labels ...string) {
	if got, want := len(handle.Labels) + len(handle.OptionalLabels), len(labels); got != want { // so var args are a slice...
		// fmt.Errorf("length of labels passed incorrect got: %v, want: %v",  len(optionalLabelsGot), len(optionalLabelsWant))
		// logger? an error/warning with got/expected length...
		logger.Infof("length of labels passed to RecordFloat64Histo incorrect got: %v, want: %v", got, want)
	}

	for _, metricRecorder := range l.metricsRecorders {
		metricRecorder.RecordFloat64Histo(handle, incr, labels...)
	}
}

func (l *MetricsRecorderList) RecordInt64Gauge(handle *estats.Int64GaugeHandle, incr int64, labels ...string) {
	if got, want := len(handle.Labels) + len(handle.OptionalLabels), len(labels); got != want { // so var args are a slice...
		// fmt.Errorf("length of labels passed incorrect got: %v, want: %v",  len(optionalLabelsGot), len(optionalLabelsWant))
		// logger? an error/warning with got/expected length...
		logger.Infof("length of labels passed to RecordInt64Gauge incorrect got: %v, want: %v", got, want)
	}

	for _, metricRecorder := range l.metricsRecorders {
		metricRecorder.RecordInt64Gauge(handle, incr, labels...)
	}
}



// Will I need to reuse some of these fake stats handlers/components for my e2e
// tests with respect to wrr/rls? I guess try and figure that out...draw it out?

