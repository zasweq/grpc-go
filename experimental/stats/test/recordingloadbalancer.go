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
	"google.golang.org/grpc/balancer/pickfirst"
	"google.golang.org/grpc/experimental/stats"
)

// Is this how I want to do it in RLS?
// var intCountHandle *stats.Int64CountHandle /*= stats.Handler*/
/*var floatCountHandle *stats.Float64CountHandle
var intHistoHandle *stats.Int64HistoHandle
var floatHistoHandle *stats.Float64HistoHandle
var intGaugeHandle *stats.Int64GaugeHandle*/

var ( // Can declare vars as such...
	intCountHandle = stats.RegisterInt64Count(stats.MetricDescriptor{
		Name:           "simple counter",
		Description:    "sum of all emissions from tests",
		Unit:           "int",
		Labels:         []string{"int counter label"},
		OptionalLabels: []string{"int counter optional label"},
		Default:        false, // This is an OTel specific concept, could drop and make it a seperate part of API...
	})
	floatCountHandle = stats.RegisterFloat64Count(stats.MetricDescriptor{
		Name:           "float counter",
		Description:    "sum of all emissions from tests",
		Unit:           "float",
		Labels:         []string{"float counter label"},
		OptionalLabels: []string{"float counter optional label"},
		Default:        false,
	})
	intHistoHandle = stats.RegisterInt64Histo(stats.MetricDescriptor{
		Name:           "int histo",
		Description:    "sum of all emissions from tests",
		Unit:           "int",
		Labels:         []string{"int histo label"},
		OptionalLabels: []string{"int histo optional label"},
		Default:        false,
	})
	floatHistoHandle = stats.RegisterFloat64Histo(stats.MetricDescriptor{
		Name:           "float histo",
		Description:    "sum of all emissions from tests",
		Unit:           "float",
		Labels:         []string{"float histo label"},
		OptionalLabels: []string{"float histo optional label"},
		Default:        false,
	})
	intGaugeHandle = stats.RegisterInt64Gauge(stats.MetricDescriptor{
		Name:           "simple gauge",
		Description:    "the most recent int emitted by test",
		Unit:           "int",
		Labels:         []string{"int gauge label"},
		OptionalLabels: []string{"int gauge optional label"},
		Default:        false,
	})
)

// graceful switch top level balancer? yeah what is that for?

func init() {
	balancer.Register(recordingLoadBalancerBuilder{})

	// Register 5 types of instruments recorded on...
	// Maybe all when balancer is built? yeah

	// Make these desc consts?, or do vars things that are imported initialize
	// first...or else nothing would work

	/*intCountHandle = stats.RegisterInt64Count() // Register with anything interesting here? Figure out how underneath layer will work with assertions/channel sends callback based? See stub server...?
	floatCountHandle = stats.RegisterFloat64Count()
	intHistoHandle = stats.RegisterInt64Histo()
	floatHistoHandle = stats.RegisterFloat64Histo()
	intGaugeHandle = stats.RegisterInt64Gauge()*/

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
	/*rlb := &recordingLoadBalancer{
		metricsRecorder: bOpts.MetricsRecorder, // I don't even think you need this persisted...
	}*/
	// bOpts.MetricsRecorder // stats.MetricsRecorder
	intCountHandle.Record(bOpts.MetricsRecorder, 1, "int counter label val", "int counter optional label val") // send up minor types easy to verify..., label verification wrt system ends up later...
	floatCountHandle.Record(bOpts.MetricsRecorder, 2, "float counter label val", "float counter optional label val") // honestly just don't register labels...
	intHistoHandle.Record(bOpts.MetricsRecorder, 3, "int histo label val", "int histo optional label val")
	floatHistoHandle.Record(bOpts.MetricsRecorder, 4, "float histo label val", "float histo optional label val")
	intGaugeHandle.Record(bOpts.MetricsRecorder, 5, "int gauge label val", "int gauge optional label val")
	intGaugeHandle.Record(bOpts.MetricsRecorder, 7, "non-existent-label") // should get eaten by metrics recorder list and not end up in teh stats handler data...

	// last part is fine
	// the labels emitted will be asserted on...so needs to match up/be interesting...
	// all it needs to match up is length...optional labels are unconditionally implemented and filtered at OTel level...


	// Yeah I guess checks the labels plumbing if you do have key values...will this panic somehow?

	// balancer.Get(pickfirst.Name).Build(cc, bOpts) // pass build options down, copy, this cc makes it skip this layer...

	return &recordingLoadBalancer{
		Balancer: balancer.Get(pickfirst.Name).Build(cc, bOpts),
	}
}

type recordingLoadBalancer struct { // internal because only used in this test...
	// metricsRecorder stats.MetricsRecorder
	// embed balancer.Balancer/balancer.ClientConn?
	balancer.Balancer
}


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

// embed pick first, I guess give it cc which skips this layer wrapping cc...

// I think this is the minimum required, this should just work (need to cleanup...)

