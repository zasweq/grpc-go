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

// At Dial Time - look through all global and local stats handlers typecast to
// this, and put in a MetricRecorder that forwards to all under the hood

type MetricsRecorder interface { // OTel will implement this extra API

	// RecordIntCount
	RecordIntCount(IntCountHandle, []Label, []Label, int64)

	RecordFloatCount(FloatCountHandle, []Label, []Label, float64)

	RecordIntHisto(IntHistoHandle, []Label, []Label, int64)

	RecordFloatHisto(FloatCountHandle, []Label, []Label, float64)

	RecordIntGauge(IntGaugeHandle, []Label, []Label, int64)
} // defined in internal? "duck typing"

// ClientConn uses this so export?

type MetricsRecorderList struct {
	// holds onto a list of stats plugins - OTel at creation time
	// determines if it implements NonPerCall, and if it does forward?
	// What layer do I put this logic at?

	// global + local to stats plugin at Channel creation time...
	metricsRecorders []MetricsRecorder

}
// Make a list of these to discuss with Doug:
// 1. Dial Option with new interface, otel set as both stats handler and metrics recorder?

// 2. Or typecast all the stats handlers to this and pass it down to here in the client?

// 3. Or typecast here?

// should I do something like this? What do I take, and then typecast down...
// does this give access to the methods underlying this handler thing?
func NewMetricsRecorderList(sps []Handler) *MetricsRecorderList { // what exactly do I pass in here/how do I do this typecast at the snapshot point
	// yeah what interface do I pass down
} // and then how do I pass this to LB's (Eric and Doug mentioned exported function on ClientConn with embedding or something)

func (l *MetricsRecorderList) RecordIntCount(handle IntCountHandle, labels []Label, optionalLabels []Label, incr int64) { // Do instrument registry first?
	// just forward to all?
	for _, metricRecorder := range l.metricsRecorders {
		metricRecorder.RecordIntCount(handle, labels, optionalLabels, incr) // no need for lazy init premature optimization (can't I do a map for recording point? loses index but string is fine? talk to Doug about this?)
	}
}

func (l *MetricsRecorderList) RecordFloatCount(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {
	// in all instruments: (Yash does verify at this layer)

	// check if labels/optional labels provided match, if not error/warning log
	for _, metricRecorder := range l.metricsRecorders {
		metricRecorder.RecordFloatCount(handle, labels, optionalLabels, incr) // no need for lazy init premature optimization (can't I do a map for recording point? loses index but string is fine? talk to Doug about this?)
	}
}

func (l *MetricsRecorderList) RecordIntHisto(handle IntHistoHandle, labels []Label, optionalLabels []Label, incr int64) {
	// in all instruments: (Yash does verify at this layer)

	// check if labels/optional labels provided match, if not error/warning log
	for _, metricRecorder := range l.metricsRecorders {
		metricRecorder.RecordIntHisto(handle, labels, optionalLabels, incr) // no need for lazy init premature optimization (can't I do a map for recording point? loses index but string is fine? talk to Doug about this?)
	}
}

func (l *MetricsRecorderList) RecordFloatHisto(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {
	// in all instruments: (Yash does verify at this layer)

	// check if labels/optional labels provided match, if not error/warning log

	for _, metricRecorder := range l.metricsRecorders {
		metricRecorder.RecordFloatHisto(handle, labels, optionalLabels, incr) // no need for lazy init premature optimization (can't I do a map for recording point? loses index but string is fine? talk to Doug about this?)
	}
} // and then persist a single MetricsRecorder and pass that to LB Policies?


// instead of index pass the string name of instrument lol

// OTel extra methods of these ^^^

// Do OTel creation, this layer, then how do I test?

// Or instrument registry + otel creation

// Then Channel creating this metrics recorder list +
// usage of MetricsRecords

