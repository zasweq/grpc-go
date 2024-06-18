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

package instrumentregistry

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

// where are these labels and optional labels eaten? That templating thing is way to complicated
// I'll just check in metrics recorder (plus we're not as crazy about performance)

// InstrumentDescriptor is a data of a registered instrument (metric).
type InstrumentDescriptor struct { // labels are just []string
	// Do I need instrument type? what was the problem with 4 seperate let's try that
	// Name is the name of this metric.
	Name           string
	// Description is the description of this metric.
	Description    string
	// Unit is the unit of this metric.
	Unit           string
	// Labels are the label keys for this metric.
	Labels         []string // required Labels for the instrument...
	// OptionalLabels are the optional label keys for this
	// metric.
	OptionalLabels []string // Do I even need to persist this or is locality passed up through API no I need it that is per call optional Labels...
	// Default is whether this metric is on by default.
	Default bool // whether the metric is on by default
}

// I think using 4 is reasonable...

// Int64CountInsts...
var Int64CountInsts []InstrumentDescriptor
// FloatCountInsts...
var FloatCountInsts []InstrumentDescriptor // not needed for now but Yash thinks will be needed in future
// Int64HistoInsts...
var Int64HistoInsts []InstrumentDescriptor
// Float64HistoInsts...
var Float64HistoInsts []InstrumentDescriptor
// Int64GaugeInsts...
var Int64GaugeInsts []InstrumentDescriptor // could move these declarations closer...

// Int64CountHandle is a typed handle for a int count instrument. This handle is
// passed at the recording point in order to know which instrument to record on.
type Int64CountHandle struct {
	index int
}

// comment about thread safety here...

// Not a map to avoid string comps...

// registeredInsts are the registered instrument descriptor names.
var registeredInsts = make(map[string]bool)

// If default, set this.

// DefaultNonPerCallMetrics (merge if user doesn't set)
var DefaultNonPerCallMetrics = make(map[string]string)

// This has no access to default metrics,
// build out own and expect users to call into

// OTel can read this global too

// RegisterInt64Count registers the int count instrument description onto the
// global registry. It returns a typed handle to use when recording data.
//
// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe. If multiple instruments are
// registered with the same name, this function will panic.
func RegisterInt64Count(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) Int64CountHandle {
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

	Int64CountInsts = append(Int64CountInsts, InstrumentDescriptor{
		Name:           name,
		Description:    desc,
		Unit:           unit,
		Labels:         labels,
		OptionalLabels: optionalLabels,
		Default:        def, // read at creation time, combine with logic of Metrics (unset get all defaults) set passed into OpenTelemetry
	})
	return Int64CountHandle{ // pointer or not?
		index: len(Int64CountInsts) - 1,
	}
}

// Float64CountHandle is a typed handle for a float count instrument. This handle
// is passed at the recording point in order to know which instrument to record
// on.
type Float64CountHandle struct {
	index int
}

func registerInst(name string) {
	if registeredInsts[name] {
		log.Panicf("instrument %v already registered", name)
	}
	registeredInsts[name] = true
}

// RegisterFloat64Count registers the float count instrument description onto the
// global registry. It returns a typed handle to use when recording data.
//
// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe. If multiple instruments are
// registered with the same name, this function will panic.
func RegisterFloat64Count(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) Float64CountHandle {
	registerInst(name)
	FloatCountInsts = append(FloatCountInsts, InstrumentDescriptor{
		Name:           name,
		Description:    desc,
		Unit:           unit,
		Labels:         labels,
		OptionalLabels: optionalLabels,
		Default:        def, // read at creation time, combine with logic of Metrics (unset get all defaults) set passed into OpenTelemetry
	})
	return Float64CountHandle{ // pointer or not?
		index: len(FloatCountInsts) - 1,
	}
}

// Int64HistoHandle is a typed handle for a int histogram instrument. This handle
// is passed at the recording point in order to know which instrument to record
// on.
type Int64HistoHandle struct {
	index int
}

// RegisterInt64Histo registers the int histogram instrument description onto the
// global registry. It returns a typed handle to use when recording data.
//
// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe. If multiple instruments are
// registered with the same name, this function will panic.
func RegisterInt64Histo(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) Int64HistoHandle {
	registerInst(name)
	Int64HistoInsts = append(Int64HistoInsts, InstrumentDescriptor{
		Name:           name,
		Description:    desc,
		Unit:           unit,
		Labels:         labels,
		OptionalLabels: optionalLabels,
		Default:        def, // read at creation time, combine with logic of Metrics (unset get all defaults) set passed into OpenTelemetry
	})
	return Int64HistoHandle{ // pointer or not? it's one int I don't think it matters...
		index: len(Int64HistoInsts) - 1,
	}
}

// Float64HistoHandle is a typed handle for a float histogram instrument. This
// handle is passed at the recording point in order to know which instrument to
// record on.
type Float64HistoHandle struct {
	index int
}

// RegisterFloat64Histo registers the float histogram instrument description
// onto the global registry. It returns a typed handle to use when recording
// data.
//
// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe. If multiple instruments are
// registered with the same name, this function will panic.
func RegisterFloat64Histo(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) Float64HistoHandle {
	registerInst(name)
	Float64HistoInsts = append(Float64HistoInsts, InstrumentDescriptor{
		Name:           name,
		Description:    desc,
		Unit:           unit,
		Labels:         labels,
		OptionalLabels: optionalLabels,
		Default:        def, // read at creation time, combine with logic of Metrics (unset get all defaults) set passed into OpenTelemetry
	})
	return Float64HistoHandle{ // pointer or not?
		index: len(Float64HistoInsts) - 1,
	}
}

// Label represents a string attribute/label to attach to metrics.
type Label struct {
	// Key is the key of the label.
	Key string
	// Value is the value of the label
	Value string
}

// Should I call this int64 like c instead of int? int64 count histo etc.
// even uint64...what exact type does otel create?
// double
type Int64GaugeHandle struct {
	index int
}

// RegisterInt64Gauge registers the int gauge instrument description onto the
// global registry. It returns a typed handle to use when recording data.
//
// NOTE: this function must only be called during initialization time (i.e. in
// an init() function), and is not thread-safe. If multiple instruments are
// registered with the same name, this function will panic.
func RegisterInt64Gauge(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) Int64GaugeHandle {
	registerInst(name)
	Int64GaugeInsts = append(Int64GaugeInsts, InstrumentDescriptor{
		Name:           name,
		Description:    desc,
		Unit:           unit,
		Labels:         labels,
		OptionalLabels: optionalLabels,
		Default:        def, // read at creation time, combine with logic of Metrics (unset get all defaults) set passed into OpenTelemetry
	})
	return Int64GaugeHandle{
		index: len(Int64GaugeInsts) - 1,
	}
} // This needs a new helper in OTel...





// where to put metrics recorder, that needs to read inst description and if labels don't match up
// eat call and error....
