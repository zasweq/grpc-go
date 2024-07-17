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
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/experimental/stats"
)

// Is this how I want to do it in RLS?
var intCountHandle *stats.Int64CountHandle /*= stats.Handler*/
var floatCountHandle *stats.Float64CountHandle
var intHistoHandle *stats.Int64HistoHandle
var floatHistoHandle *stats.Float64HistoHandle
var intGaugeHandle *stats.Int64GaugeHandle

// graceful switch top level balancer? yeah what is that for?

func init() {
	balancer.Register(recordingLoadBalancerBuilder{})

	// Register 5 types of instruments recorded on...
	// Maybe all when balancer is built?
	intCountHandle = stats.RegisterInt64Count()
	floatCountHandle = stats.RegisterFloat64Count()
	intHistoHandle = stats.RegisterInt64Histo()
	floatHistoHandle = stats.RegisterFloat64Histo()
	intGaugeHandle = stats.RegisterInt64Gauge()

	// yeah just record on these on build, I'm just making sure that
	// build has access to metrics recorder to persist around no crazy operations
	// and the metrics recorder is a list that forwards to all (write a comment about this?)

	// Don't need to add labels for now lightweight check, scale up label
	// verification (based off hash map...?) like Yijie

}

const recordingLoadBalancerName = "recording_load_balancer"

// Do I need to parse config? Is that optional?

type recordingLoadBalancerBuilder struct{}

func (recordingLoadBalancerBuilder) Name() string {
	return recordingLoadBalancerName
}

func (recordingLoadBalancerBuilder) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	// what to do with cc? does this even need to intercept?
	rlb := &recordingLoadBalancer{
		metricsRecorder: bOpts.MetricsRecorder,
	}
	// bOpts.MetricsRecorder // stats.MetricsRecorder
	intCountHandle.Record(bOpts.MetricsRecorder, 1) // send up minor types easy to verify..., label verification wrt system ends up later...
	floatCountHandle.Record(bOpts.MetricsRecorder, 2) // honestly just don't register labels...
	intHistoHandle.Record(bOpts.MetricsRecorder, 3)
	floatHistoHandle.Record(bOpts.MetricsRecorder, 4)
	intGaugeHandle.Record(bOpts.MetricsRecorder, 5)
	intGaugeHandle.Record(bOpts.MetricsRecorder, 7, "non-existent-label") // should get eaten by metrics recorder list and not end up in teh stats handler data...

	// embed pick first/balancer.Balancer

	return rlb // can this return pick first or something?
}

type recordingLoadBalancer struct { // internal because only used in this test...
	metricsRecorder stats.MetricsRecorder
	// embed balancer.Balancer/balancer.ClientConn?
} // embed a balancer.Balancer in it and defer to pick first...

// wrap pick first?


// Will need to deploy for e2e tests anyway...so this structure/flow is what I need to test WRR/RLS Metrics...
/*
func (b *recordingLoadBalancer) SomeOperation() { // or do this at build time so don't need to induce operation...need a knob for RLS/WRR to mess with operations/trigger operations there...
	b.metricsRecorder.RecordInt64Count()
	intCountHandle.Record(b.metricsRecorder, 1, /*some labels here...is it important to verify...labels are determined by what part of the system you're at...in xDS and RLS...)
	b.metricsRecorder.RecordFloat64Count()
	b.metricsRecorder.RecordInt64Histo() // thus need to register all of these oh wait you get a handle back and pass it the metrics recorder
	b.metricsRecorder.RecordFloat64Histo()
	b.metricsRecorder.RecordInt64Gauge()
}
*/

// What is the minimum required to get this LB working...
// as an actual balancer etc...
