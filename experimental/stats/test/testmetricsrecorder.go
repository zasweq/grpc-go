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
	"context"
	"github.com/google/go-cmp/cmp"
	estats "google.golang.org/grpc/experimental/stats"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/stats"
	"testing"
)

// test only...? how to not expose this to users...

// have this be seperate that'll make it easier, can refactor later...

// TestMetricsRecorder...
type TestMetricsRecorder struct {
	t *testing.T

	// 5 maps (with key values somehow accounted for?)
	intValues   map[*estats.MetricDescriptor]int64 // bucketed into two? maybe bucket by type...
	floatValues map[*estats.MetricDescriptor]float64
	// testing.T...what is this even used for? have assertions on this type

	// send something below or persist something above that is data +
	// labels...gets a desc (with labels key), incr data,

	// receive from channel, fail on testing.T


	intCountCh *testutils.Channel
	floatCountCh *testutils.Channel
	intHistoCh *testutils.Channel
	floatHistoCh *testutils.Channel
	intGaugeCh *testutils.Channel

}

// new which constructs based off state of metrics registry...
func NewTestMetricsRecorder(t *testing.T) *TestMetricsRecorder {
	tmr := &TestMetricsRecorder{
		t: t,
		intValues: make(map[*estats.MetricDescriptor]int64),
		floatValues: make(map[*estats.MetricDescriptor]float64),

		intCountCh: testutils.NewChannelWithSize(10), // or 1? Should just get 1 from test but WRR/RLS I don't know what triggers it...
		floatCountCh: testutils.NewChannelWithSize(10), // if I want one testutils.NewChannel()
		intHistoCh: testutils.NewChannelWithSize(10),
		floatHistoCh: testutils.NewChannelWithSize(10),
		intGaugeCh: testutils.NewChannelWithSize(10),
	}

	// for _, desc := range
	// estats.MetricDescriptor{}

	// so this fake one somehow needs to be configured with metrics names...
	var metrics []string
	for _, metric := range metrics {
		desc := estats.DescriptorForMetric(estats.Metric(metric))
		switch desc.Type {
		case estats.MetricTypeIntCount: // or can make this more granular by making the logic per...
		case estats.MetricTypeIntHisto:
		case estats.MetricTypeIntGauge:
			tmr.intValues[desc] = 0 // persist this still for basic case...
		case estats.MetricTypeFloatCount:
		case estats.MetricTypeFloatHisto:
			tmr.floatValues[desc] = 0
		} // this can import stats...stats doesn't need this...
	}
	return tmr
}

// record writes to map

// internal test util to not populate exported namespace?
type MetricsData struct { // metrics data received for all...
	// Needs to ID the handle...right
	Handle *estats.MetricDescriptor

	IntIncr int64 // Only set for corresponding type...
	FloatIncr float64

	LabelKeys []string // or make this one kv slice?
	LabelVals []string

	// Anything else?
} // Lower layer does label length check...

// could add verifications in future PR's which keep reading from these channels
// (one of the 5 specific for type) and expecting something...
func (r *TestMetricsRecorder) WaitForInt64Count(ctx context.Context, metricsDataWant MetricsData) { // Metrics recording points come sync? so do I even need to poll
	// Again can scale up these operations in the future
	got, err := r.intCountCh.Receive(ctx) // and then what to do with got?
	if err != nil {
		// Am I allowed to do this in a separate component?
		r.t.Fatalf("timeout waiting for int64 count")
	} // send it on this channel or persist it around to verify later...
	metricsDataGot := got.(MetricsData)
	/*
	if diff := cmp.Diff(gotAddrCount, wantAddrCount); diff != "" {
				logger.Infof("non-roundrobin, got address count in one iteration: %v, want: %v, Diff: %s", gotAddrCount, wantAddrCount, diff)
				continue
	*/
	if diff := cmp.Diff(metricsDataGot, metricsDataWant); diff != "" {
		r.t.Fatalf("int64count metricsData received unexpected value (-got, +want): %v", diff)
	}
}

// It's essentially like you persist it around in a buffer to be read later...rather than persist as a slice
// or w/e

// I don't think needs to do any metrics verifications...
func (r *TestMetricsRecorder) RecordInt64Count(handle *estats.Int64CountHandle, incr int64, labels ...string) {
	// r.intCountCh.Send(/*something with value type and key value of labels...I guess labels are something interesting to assert*/)

	// This persistence needs the k/v dimension though...I guess in future...for
	// certain labels if I want to add that dimension this is iterable and
	// malleable though...

	// probably need to export this
	r.intCountCh.Send(MetricsData{
		Handle: (*estats.MetricDescriptor)(handle),
		IntIncr: incr,

		LabelKeys: append(handle.Labels, handle.OptionalLabels...), // this verifies some part of instrument registry but do I want more?

		LabelVals: labels,

		// Anything else - if so can verify something from instrument
		// registration...maybe have the 5 registered instruments be consts do
		// it like inst registry tests eh...


	}) // or send context? This just sticks it on buffer right? Like testServer

	// trivial incr persistence, might want to combine hash with handle for label key
	// specificity, have to provide both handle and hash...



	r.intValues[(*estats.MetricDescriptor)(handle)] += incr // or in the future could scale up this persistence
}

func (r *TestMetricsRecorder) RecordFloat64Count(handle *estats.Float64CountHandle, incr float64, labels ...string) {
	r.floatCountCh.Send(MetricsData{
		Handle: (*estats.MetricDescriptor)(handle),
		FloatIncr: incr,

		LabelKeys: append(handle.Labels, handle.OptionalLabels...),

		LabelVals: labels,

		// Anything else - if so can verify something from instrument
		// registration...

	})

	// This persistence needs the k/v dimension though...I guess in future...
	r.floatValues[(*estats.MetricDescriptor)(handle)] += incr
}

func (r *TestMetricsRecorder) RecordInt64Histo(handle *estats.Int64HistoHandle, incr int64, labels ...string) {
	r.intHistoCh.Send(MetricsData{
		Handle: (*estats.MetricDescriptor)(handle),
		IntIncr: incr,

		LabelKeys: append(handle.Labels, handle.OptionalLabels...),

		LabelVals: labels,

		// Anything else - if so can verify something from instrument
		// registration...

	})

	// This persistence needs the k/v dimension though...I guess in future...
	r.intValues[(*estats.MetricDescriptor)(handle)] += incr
}

func (r *TestMetricsRecorder) RecordFloat64Histo(handle *estats.Float64HistoHandle, incr float64, labels ...string) {
	r.floatHistoCh.Send(MetricsData{
		Handle: (*estats.MetricDescriptor)(handle),
		FloatIncr: incr,

		LabelKeys: append(handle.Labels, handle.OptionalLabels...),

		LabelVals: labels,

		// Anything else - if so can verify something from instrument
		// registration...hardcode something interesting there?

	})
	// This persistence needs the k/v dimension though...I guess in future...
	r.floatValues[(*estats.MetricDescriptor)(handle)] += incr
}

func (r *TestMetricsRecorder) RecordInt64Gauge(handle *estats.Int64GaugeHandle, incr int64, labels ...string) {
	r.intGaugeCh.Send(MetricsData{
		Handle: (*estats.MetricDescriptor)(handle),
		IntIncr: incr,

		LabelKeys: append(handle.Labels, handle.OptionalLabels...),

		LabelVals: labels,

		// Anything else - if so can verify something from instrument
		// registration...

	})

	// This persistence needs the k/v dimension though...I guess in future...
	r.intValues[(*estats.MetricDescriptor)(handle)] = incr // have this be simply a check on most recent...
}

// To make it a stats.Handler:

func (r *TestMetricsRecorder) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	return ctx
}

func (r *TestMetricsRecorder) HandleRPC(context.Context, stats.RPCStats) {}

func (r *TestMetricsRecorder) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

func (r *TestMetricsRecorder) HandleConn(context.Context, stats.ConnStats) {}


// testmetricsrecorder just copy verbatim and export...figure out circular dependency issue? If the estats test doesn't take a dependency on this
// I think circular dependency problem goes away...

// and verify by reading map from test...Doug seemed fine with this...

// events that happen put it on buffered channel...or channel
// or callback like stub server?

// build full assertions on top of this...RLS/WRR


// Operations write to a channel and higher layers can read the channel as they want...

// Build on top of this, can assert like Yijie or not or keep it lightweight for the test

// 5 channels? Buffered channels?
// Sync point?

// Read off the 5 and make sure it hits at Dial time?

// What's the best thing to do here?




// buffered channel doesn't block and is a sink
// maybe do like test client with buffered channel of length 10...

// channels block, but do I have enough granular control of WRR/RLS to always have corresponding reads?

// testutils.Channel is a buffered sink that has extra operations wrapping it...




