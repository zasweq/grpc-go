/*
 * Copyright 2023 gRPC authors.
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

package grpc

import "google.golang.org/grpc/stats"

// balancer registry example:
/*
var (
	// m is a map from name to balancer builder.
	m = make(map[string]Builder)

	logger = grpclog.Component("balancer")
)
*/

// Probably have this live in a registry package - like balancer/
// registry
var (
	// sync needed? No written to at init time...
	// exporter right?
	counters []counterDescriptor // pointer or not?
	histograms []histogramDescriptor // pointer or not
)

// global what

// persist a type in these descriptors
// orrrr persist a count type1 type2 histogram type1 type 2 (I prefer generics)

// float counter so this all needs to be generic

// counterDescriptor is a descriptor of a counter instrument.
type counterDescriptor struct {
	name string
	description string
	unit string
	// another thing - type string
} // how to plumb?

// record(delta (generic), ...) - count

// record(histogram point(generic, ...)

// Are these OTel specific? balancer registry is global map?

// histogramDescriptor is a descriptor of a histogram instrument.
type histogramDescriptor struct { // why the distinction between this and instrument?
	name string
	description string
	unit string
	// another thing - type string
} // the type needs to be exported right?


// distinction between global count and global histogram descriptors...

// another dimension to the OTel API

// register interface - public so exported...Capital == public in c++

/*
// Register registers the balancer builder to the balancer map. b.Name
// (lowercased) will be used as the name registered with this builder.  If the
// Builder implements ConfigParser, ParseConfig will be called when new service
// configs are received by the resolver, and the result will be provided to the
// Balancer in UpdateClientConnState.
//
// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe. If multiple Balancers are
// registered with the same name, the one registered last will take effect.
func Register(b Builder) {
*/

// returns an int to index global registry - does go need this?

// mention api for either both or one

// init has ordering...
func RegisterCounter(name string, description string, unit string) int { // document only called at init time and maybe on slice too?
	// append to global slice and return an index?
	counters = append(counters, counterDescriptor{ // accessed multiple times per RPC. Pointer?
		name: name,
		description: description,
		unit: unit,
	}) // do I need to log something?
	// faster to persist an int or just take length?
	return len(counters) - 1 // he will comment if it's slower
}

// target -> []gmr which gets called into us api

// returns an index to global slice? How to keep track and count?

func RegisterHistogram(name string, description string, unit string) int { // bounds come from OTel...so need a distinction
	// append to global slice and return an index?
	histograms = append(histograms, histogramDescriptor{
		name: name,
		description: description,
		unit: unit,
	}) // you can append to nil
	return len(histograms) - 1
}

// returns an index to global slice? How to keep track and count?

// why distinction? can names collide

// next layers are the global stats handler registry...record API that records

// and

// extended optional interface...to not break stats handler API

// sh layer (not global - I think global calls it)
type instrumentRecording interface {
	// yash
	// returns instrument return value of OTel instrument type (this would rest in OTel package to take dependency)
	// getter of instrument (do it at global? sh layer? configure OTel per channel?)
	// will be called by the global stats handler registry
	getCounter(name string, description string, unit string) // why do you need all 3


	// or just record the metric within the component

	incrementCounter(name string) // are counters by 1, if so don't need an int


}

// persist something globally? target information encoded *in stats handler*?
// api for registering against global registry here...
func Register(sh *stats.Handler) { // return an error - document only called at init time...
	// append to global registry - will be searched and target encoded in the stats handler registry...
}

// target -> []stats handlers

// or have []gmr be a gmr itself, delegates to all it holds

// global which reads above
type globalStatsPluginRegistry struct { // top level var?

	// for the key, do you need the full index descriptor?

	// record counter - either calls above directly and records it in sh or gets an instrument to register
	// RecordCounter(/*c passes in an index you can use*/, /*labels*/, /*optional labels*/) // don't you just name?
	// doesn't need to be fully foolproof

	// record histogram - function on gspr
	// RecordHistogram(/*c passes in an index you can use*/, /*labels?*/, /*optional labels*/) // pass in labels? what is needed?


} // the recording API here is fixed

type aggregateGlobalMetricsRecorder struct { // aggregate global metrics recorder
	// for each over this
	gmrs []globalMetricsRecorder // emitted from target


}

type globalMetricsRecorder struct {

	target string
}

// RecordCounter
// and RecordHistogram - need to plumb a generic into this...needs to match up

// do I want this int index to be unsigned? Does it matter that much?
// record plugins on this type
func (gmr *globalMetricsRecorder) RecordCounter(index int, /*labels*/, /*optional labels*/, /*emitted count by?*/) { // I think I want it on this type...

	// steps get index that is allocated - returns an instrument with a certain type - need this call to match up...
	// methods on the instrument type
	// instrument[Generic Type]

	// generic recording point - needs to match up with instrument generic type above, so this doesn't work...

}

// two pieces of information - index into gmr's allocated instruments, and generic type it can be called with
// how to make sure generic type matches up?

// what is the data point passed to a histogram?
func (gmr *globalMetricsRecorder) RecordHistogram(index int, /*what is recording point of histogram?*/, /*labels*/, /*optional labels*/) {

}

func (gmr *globalMetricsRecorder) targetApplicable(target string) bool {
	return gmr.target == target
}

var (
	globalMetricsRecorders []globalMetricsRecorder // pointer?
)

// persist something in registry?

// some sort of concept of list of sh
func (gspr *globalStatsPluginRegistry) GetStatsHandlerListForTarget(target string) []globalMetricsRecorder { // is this the right thing to return?
	var ret []globalMetricsRecorder
	// for sh := global registry {
	for _, gmr := range globalMetricsRecorders {
		if gmr.targetApplicable(target) { // optional interface so idk how to typecast this...do I return the interface that has both? or just scale up the struct no need for second interface?
			ret = append(ret, gmr)
		}
	}
	//     if target
	// }
	return ret // figure out types here, sh or extra
}

// interface {}

// struct {
// sh
// extra interface
// }

// only register globally...
// user uses global registry for non per call

// different dial option entirely if we go that route

// explicit non per call sh

// other sh should maybe be CallTracer

// channel initialization figures out
// either pass around shs from channel or
// plumb target around - balancer has access to target (canonical)

// single that fans out to many (non per call stats handler)

// global metrics recorder...
// make it global - call from WRR with target perhaps, no change in API

// stats.MetricsRecorder (metrics that aren't per call)

// pointer to tuple (maybe in a list), vs. index
// integer offset is faster if that's how we index stats plugin object
// know how it's going to be used

// rough API by mid Feb, can make changes later...tbd

// Discussion:


// wrap returned instruments/register instruments with type

// type IntCountHandle struct {
//     index int
// }

// persist in different type global? slices for each type in all...

// registerIntCount(tuple) IntCountHandle {

// how will index be allocated in stats object - arbitrary types...






// gmr has same exposure
// options{
//   meterprovider
//   metricsToRecord []string
// }



// gmr -
// recordCountInt // same as below? indexes array of instruments...and records itself
// recordCountFloat
// recordHistoInt
// recordHistoFloat


// global registry...
// []gmrs -> partioned by target into vvv

// []gmr - partioned from target

// recordCountInt(IntCountHandle (index + type), labels, count(typed to int))
// recordCountFloat(IntFloatHandle (index + type), labels, count(typed to float))
// recordHistoInt(IntHistoHandle (index + type), labels, int histo point)
// recordHistoFloat(FloatHistoHandle (index + type), labels, float histo point))

// high level - specific types...

// option was either holding onto specific instruments in wrr or stats plugin object
// instruments would scale it up, don't need handle since already partioned

// way I see it is this:
// global lists for all 4 types
// []intcounts
// []floatcounts
// []inthistos
// []float64histos

// in gmr builder: lives in same package as global instruments registry
// type gmr struct {
//     intCounts []otel.intcount // looked into by index in handle type
//     floatCounts []otel.floatCounts
//     intHistos []otel.intHisto
//     floatHistos []otel.floatHisto
// }

// in gmr builder (also takes a list from user representing metrics to record)

// for intcounts
//     otel.createIntCount(3 pieces of data from tuple)

// for floatcounts
//     otel.createFloatCount(3 pieces of data from tuple)

// for inthistos
//     otel.createIntHisto(3 pieces of data from tuple)

// for float64histos
//     otel.createFloatHisto(3 pieces of data from tuple)
