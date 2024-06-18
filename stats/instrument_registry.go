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

import "log"

// Have the handles here and the global data structures
// OTel will create things based off this instrument registry...

// What data structures back this thing?

// OTel constructor reads these global data structures

// mention not thread safe (init has happens before so ok there)

// can't be consts - needs to be similar semantics to global LB Registry

// Big implementation question:
// 4 global things traced down through invariance or is the overall ordering importnat to keep?
// 4 globals lists down the whole chain...has the same underlying atomic unit...

// Balancer registry global vars:
/*
var (
	// m is a map from name to balancer builder.
	m = make(map[string]Builder)

	logger = grpclog.Component("balancer")
)
*/

// Balancer registry documentation for thread safety/init:

/*
// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe. If multiple Balancers are
// registered with the same name, the one registered last will take effect.
*/

// Will I even need this instType thing if 4 global lists (implied)

// Does this thing need a gauge? What do I need for WRR and Pick First?
// There's a gauge for pick first and rls metrics...

type instDesc struct { // labels are just []string
	// Do I need instrument type? what was the problem with 4 seperate let's try that
	name string
	desc string
	unit string
	labels []string // required labels for the instrument...
	optionalLabels []string // Do I even need to persist this or is locality passed up through API no I need it that is per call optional labels...

	def bool // whether the metric is on by default
}

var intCountInsts, floatCountInsts, intHistoInsts, floatHistoInsts []instDesc

// IntCountHandle is a typed handle for a int count instrument. This handle is
// passed at the recording point in order to know which instrument to record on.
type IntCountHandle struct {
	index int
}

// comment about thread safety here...

// Not a map to avoid string comps...

// registeredInsts are the registered instrument descriptor names.
var registeredInsts = make(map[string]bool)

// RegisterIntCount registers the int count instrument description onto the
// global registry. It returns a typed handle to use when recording data.
//
// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe. If multiple instruments are
// registered with the same name, this function will panic.
func RegisterIntCount(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) IntCountHandle { // does it still need this default thing?
	// what happens if name has already hit?
	// If multiple instrument descriptors are registered with the same name, it
	// is considered an error and should result in a panic.
	// panic at callsite?
	registerInst(name)

	/*

	When registering an instrument, a hint is provided as to whether stats
	plugins should consider that instrument on-by-default or off-by-default. The
	rationale being that component/plugin writers would have the best judgment
	on whether a metric should be enabled by default or not, especially if
	collecting the measurement is expensive.
	*/
	// def // bool - so what do I do with this?

	intCountInsts = append(intCountInsts, instDesc{
		name: name,
		desc: desc,
		unit: unit, // pass to counter, read in OTel constructor to create instruments
		labels: labels,
		optionalLabels: optionalLabels,
		def: def, // read at creation time, combine with logic of Metrics (unset get all defaults) set passed into OpenTelemetry
	})
	return IntCountHandle{ // pointer or not?
		index: len(intCountInsts) - 1,
	}
}

// FloatCountHandle is a typed handle for a float count instrument. This handle
// is passed at the recording point in order to know which instrument to record
// on.
type FloatCountHandle struct {
	index int
}

func registerInst(name string) {
	if registeredInsts[name] {
		log.Panicf("instrument %v already registered", name)
	}
	registeredInsts[name] = true
}

// RegisterFloatCount registers the float count instrument description onto the
// global registry. It returns a typed handle to use when recording data.
func RegisterFloatCount(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) FloatCountHandle {
	registerInst(name)
	floatCountInsts = append(floatCountInsts, instDesc{
		name: name,
		desc: desc,
		unit: unit, // pass to counter, read in OTel constructor to create instruments
		labels: labels,
		optionalLabels: optionalLabels,
		def: def, // read at creation time, combine with logic of Metrics (unset get all defaults) set passed into OpenTelemetry
	})
	return FloatCountHandle{ // pointer or not?
		index: len(intCountInsts) - 1,
	}
}

// IntHistoHandle is a typed handle for a int histogram instrument. This handle
// is passed at the recording point in order to know which instrument to record
// on.
type IntHistoHandle struct {
	index int
}

// RegisterIntHisto registers the int histogram instrument description onto the
// global registry. It returns a typed handle to use when recording data.
func RegisterIntHisto(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) IntHistoHandle {
	registerInst(name)
	intHistoInsts = append(intHistoInsts, instDesc{
		name: name,
		desc: desc,
		unit: unit, // pass to counter, read in OTel constructor to create instruments
		labels: labels,
		optionalLabels: optionalLabels,
		def: def, // read at creation time, combine with logic of Metrics (unset get all defaults) set passed into OpenTelemetry
	})
	return IntHistoHandle{ // pointer or not?
		index: len(intCountInsts) - 1,
	}
}

// FloatHistoHandle is a typed handle for a float histogram instrument. This
// handle is passed at the recording point in order to know which instrument to
// record on.
type FloatHistoHandle struct {
	index int
}

// RegisterFloatHisto registers the float histogram instrument description onto
// the global registry. It returns a typed handle to use when recording data.
func RegisterFloatHisto(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) FloatHistoHandle {
	registerInst(name)
	floatHistoInsts = append(floatHistoInsts, instDesc{
		name: name,
		desc: desc,
		unit: unit, // pass to counter, read in OTel constructor to create instruments
		labels: labels,
		optionalLabels: optionalLabels,
		def: def, // read at creation time, combine with logic of Metrics (unset get all defaults) set passed into OpenTelemetry
	})
	return FloatHistoHandle{ // pointer or not?
		index: len(intCountInsts) - 1,
	}
}

// Label represents a string attribute/label to attach to metrics.
type Label struct {
	// Key is the key of the label.
	Key string
	// Value is the value of the label
	Value string
}

