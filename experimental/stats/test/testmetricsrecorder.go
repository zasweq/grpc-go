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

package test

import (
	"google.golang.org/grpc/experimental/stats"
	"testing"
)

// have this be seperate that'll make it easier, can refactor later...

// TestMetricsRecorder...
type TestMetricsRecorder struct {
	// 5 maps (with key values somehow accounted for?)
	intValues   map[*stats.MetricDescriptor]int64 // bucketed into two? maybe bucket by type...
	floatValues map[*stats.MetricDescriptor]float64
	// testing.T
}

// new which constructs based off state of metrics registry...
func NewTestMetricsRecorder(t *testing.T) *TestMetricsRecorder {
	tmr := &TestMetricsRecorder{
		t: t,
		intValues: make(map[*stats.MetricDescriptor]int64),
		floatValues: make(map[*stats.MetricDescriptor]float64),
	}

	// for _, desc := range
	stats.MetricDescriptor{}

	// so this fake one somehow needs to be configured with metrics names...
	var metrics []string
	for _, metric := range metrics {
		desc := stats.DescriptorForMetric(stats.Metric(metric))
		switch desc.Type {
		case stats.MetricTypeIntCount: // or can make this more granular by making the logic per...
		case stats.MetricTypeIntHisto:
		case stats.MetricTypeIntGauge:
			tmr.intValues[desc] = 0
		case stats.MetricTypeFloatCount:
		case stats.MetricTypeFloatHisto:
			tmr.floatValues[desc] = 0
		} // this can import stats...stats doesn't need this...
	}

}

// record writes to map


// I don't think needs to do any metrics verifications...
func (r *TestMetricsRecorder) RecordInt64Count(handle *stats.Int64CountHandle, incr int64, labels ...string) {
	// This persistence needs the k/v dimension though...I guess in future...
	r.intValues[(*stats.MetricDescriptor)(handle)] += incr
}

func (r *TestMetricsRecorder) RecordFloat64Count(handle *stats.Float64CountHandle, incr float64, labels ...string) {
	// This persistence needs the k/v dimension though...I guess in future...
	r.floatValues[(*stats.MetricDescriptor)(handle)] += incr
}

func (r *TestMetricsRecorder) RecordInt64Histo(handle *stats.Int64HistoHandle, incr int64, labels ...string) {
	// This persistence needs the k/v dimension though...I guess in future...
	r.intValues[(*stats.MetricDescriptor)(handle)] += incr
}

func (r *TestMetricsRecorder) RecordFloat64Histo(handle *stats.Float64HistoHandle, incr float64, labels ...string) {
	// This persistence needs the k/v dimension though...I guess in future...
	r.floatValues[(*stats.MetricDescriptor)(handle)] += incr
}

func (r *TestMetricsRecorder) RecordInt64Gauge(handle *stats.Int64GaugeHandle, incr int64, labels ...string) {
	// This persistence needs the k/v dimension though...I guess in future...
	r.intValues[(*stats.MetricDescriptor)(handle)] = incr // have this be simply a check on most recent...
}

// testmetricsrecorder just copy verbatim and export...figure out circular dependency issue?

// and verify by reading map from test...Doug seemed fine with this...
