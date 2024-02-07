package grpc

import "go.opentelemetry.io/otel/metric"

var (
	// 4 global instrument slices here...or is there more?
)

// metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(method)), attribute.String("grpc.target", target), attribute.String("grpc.status", canonicalString(s.Code()))

// these are string attributes...do we really want to plumb this through whole graph?

// 4 wrapper types here - type safety, wrap index

// do these wrapper types need to do anything else?

func RegisterIntCounter(name string, description string, unit string) int {

}

func RegisterFloatCounter()



// or interface
// []interface, interface
// call into

// struct enum {
//     type
// } *

//



// can have seperate instrument apis

// [] enumm:

// type type enum (int method)


type something struct { // can be a pointer to this and just read it
	// type - needs to match up with recording point
	// pass an index
}

// three components:
// instrument registry api (Ergonomic)

// gmr "global metrics recorder"

// how wrr will use - builder for instruments, builder for intermediate []gmr

// gmr construction:
// for enum := range enum {
//    typecast - create instrument
// }

// how would instrument be stored?
// struct {
//    instrument type 1
//    instrument type 2
// }

// func (gmr *globalMetricsRecorderList) (enumm) {
//     gmr.createdMetrics[enumm.intIndex].type == enum.typee // type saftey?
//     switch enum type
//
// }


// func (gmr *globalMetricsRecorderList) record(instrument type)
// {
//        pass handle to gmr
// }

// ^^^ this api needs a type

// wrr -

// int metric
// float metric



// orrr have seperate type flows for each instrument type...

// handle returned is opaque type that can be anything...








// round 3:


// global instruments registry

type instrumentType int

const ( // is it just these types? will need to scale all of this up if I add a new histogram type
	intCount instrumentType = iota
	floatCount
	intHisto
	floatHisto
)

type instDesc struct {
	insType instrumentType
	name string
	desc string
	unit string
	labels []string // required labels for the instrument - not per stats handler just continually reading the global registry...
	optionalLabels []string // optional labels for this instrument...
}

var (
	sliceOfInstruments []instDesc
)

type IntCountHandle struct {
	index int
}

// must be called at init time...
func RegisterIntCount(name string, desc string, unit string, labels []string, optionalLabels []string) IntCountHandle { // return one pointer?
	sliceOfInstruments = append(sliceOfInstruments, instDesc{
		insType: intCount,
		name: name,
		desc: desc,
		unit: unit,
		labels: labels,
		optionalLabels: optionalLabels,
	})
	return IntCountHandle{
		index: len(sliceOfInstruments) - 1,
	}
}

type FloatCountHandle struct { // type safety by calling into []gmr with this...
	index int
}

func RegisterFloatCount(name string, desc string, unit string, labels []string, optionalLabels []string) FloatCountHandle {
	sliceOfInstruments = append(sliceOfInstruments, instDesc{
		insType: floatCount,
		name: name,
		desc: desc,
		unit: unit,
		labels: labels,
		optionalLabels: optionalLabels,
	})
	return FloatCountHandle{
		index: len(sliceOfInstruments) - 1,
	}
}

type IntHistoHandle struct {
	index int
}

func RegisterIntHisto(name string, desc string, unit string, labels []string, optionalLabels []string) IntHistoHandle {
	sliceOfInstruments = append(sliceOfInstruments, instDesc{
		insType: intHisto,
		name: name,
		desc: desc,
		unit: unit,
		labels: labels,
		optionalLabels: optionalLabels,
	})
	return IntHistoHandle{
		index: len(sliceOfInstruments) - 1,
	}
}

type FloatHistoHandle struct {
	index int
}

func RegisterFloatHisto(name string, desc string, unit string, labels []string, optionalLabels []string) FloatHistoHandle {
	sliceOfInstruments = append(sliceOfInstruments, instDesc{
		insType: floatHisto,
		name: name,
		desc: desc,
		unit: unit,
		labels: labels,
		optionalLabels: optionalLabels,
	})
	return FloatHistoHandle{
		index: len(sliceOfInstruments) - 1,
	}
} // everything it needs to create instruments



// global stats handler registry

// persist []gmrs,

// pointer
func RegisterGMR(gmr *globalMetricsRecorder) {
	// does above already have target knowledge in it?
}

func GetGMRsForTarget(target string) globalMetricsRecorders { // do I want this to be a pointer?
	// []gmr
	// for each global gmrs
	//  if target append

	return globalMetricsRecorders{
		gmrs: /*[]gmr*/,
	}

}

// real labels plumb it's way down unconditionally - contract that WRR needs to
// call it with correct number...

// optional labels persisted in global registry...

// callout into this

// should I use int or positive only?
func optionalLabels(index int/*already has type safety, just an index here*/, /*I don't think I need actual measurement...*/) []string { // keep ordering down stack....
	// use index to read...
	// has optional labels from global - how does this scale?
}

// this is essentially a bit that turns on the single optional label...similar api but experimental...

// this and original meter provider need target filtration

// needs otel meter provider - maybe have this live, also takes in []registeredMetrics (from user)
type gmrOpts struct {
	MeterProvider metric.MeterProvider
	// Metrics are the metrics to instrument. Will turn on the corresponding
	// metric if also in global registry.
	Metrics []string // allocates enough space from global instruments registry regardless, keeps the properties...
	// TargetScope is a callback that passes in the target string and returns
	// whether a target is applicable for a globalMetricsRecorder. If unset,
	// will never match to a target.
	TargetScope func(string) bool
	// TargetAttributeFilter is a callback that takes the target string and
	// returns a bool representing whether to use target as a label value or use
	// the string "other". If unset, will use the target string as is.
	TargetAttributeFilter func(string) bool
}

func NewGlobalMetricsRecorder(opts gmrOpts) *globalMetricsRecorder2 {
	// construct instruments based off global + metrics (see otel code for guidance)
	// Uses MeterProvider and Metrics

	return &globalMetricsRecorder2{
		targetScope: opts.TargetScope,
		targetAttributeFilter: opts.TargetAttributeFilter,
	}
}

// also on this object some metrics are on by default

// add 3 apis
// or a go specific way of scaling up and down set
// enable/disable
// []string for both? how would it match up? if not in slice just ignore? or ignore useless?
// disableAll too ? why would someone want to disable all?
// return a signal that represents whether metric enabled/disabled...nah than you can't figure out what part of slice

// will document that default on

func (gmr *globalMetricsRecorder2) DisableAllMetrics() {

}

func (gmr *globalMetricsRecorder2) EnableMetrics(metrics []string) { // past the default - map? I think slice is fine

}

func (gmr *globalMetricsRecorder2) DisableMetrics(metrics []string) {

}


// same plugin options as otel? does it need to be on the same type
// need meter provider and registered metrics for it to record correclty
type globalMetricsRecorder2 struct {
	// two options:
	instruments []interface{} // get type safety from type of index returned
	// map
	instruments2 map[/*x bit pointer to something...*/]interface{} // or have numerous slices/maps
	instruments3 map[int]interface{} // how much slower is this really?

	// scope and target filter part of build options
	targetScope func(string) bool

	// this also needs to get added to otel api vvv
	targetAttributeFilter func(string) bool // on recording point when you precompute labels once you use this and it works...
	// methodAttributeFilter func(string) bool
}

// Target returns whether this global metrics recorder is applicable for the
// passed in target.
func (gmr *globalMetricsRecorder2) Target(target string) bool {
	if gmr.targetScope == nil { // is this a valid function pointer?
		return false
	}
	return gmr.targetScope(target)
	// virtual bool IsEnabledForTarget(absl::string_view target);

	//  how will this determine? One target or numerous? constructor for targets I think is plural...no one used o11y lol

	// one stats plugin might be interested in multiple targets, or would want
	// to observe targets that follow a specific format

}

// is this ergonomic, can change over time...instrument registry persist vector of strings, wrr provides value
type label struct {
	key string
	value string
}

func (gmr *globalMetricsRecorder2) RecordIntCount(handle IntCountHandle, labels []label, optionalLabels []label, incr int64) { // or does it always just iterate 1? no any increment
	// typecast downward
	gmr.instruments[handle.index]/*otel.IntCountInstrument*/

	//  log an error (shouldn't happen as type safety check already)
	// record on instrument using incr and labels
}

func (gmr *globalMetricsRecorder2) RecordFloatCount(handle FloatCountHandle, labels []label, optionalLabels []label, incr float64) {
	// typecast downward
	gmr.instruments[handle.index]/*otel.FloatCountInstrument*/

	//  log an error (shouldn't happen)
	// record on instrument using incr and labels
}

func (gmr *globalMetricsRecorder2) RecordIntHisto(handle IntHistoHandle, labels []label, optionalLabels []label, incr int64) { // or does it always just iterate 1? no any increment
	// typecast downward
	gmr.instruments[handle.index]/*otel.IntHistoInstrument*/

	//  log an error (shouldn't happen)
	// record on instrument using incr and labels
}

func (gmr *globalMetricsRecorder2) RecordFloatHisto(handle FloatCountHandle, labels []label, optionalLabels []label, incr float64) {
	// typecast downward
	gmr.instruments[handle.index]/*otel.FloatHistoInstrument*/

	//  log an error (shouldn't happen)
	// record on instrument using incr and labels
}

// []gmr
type globalMetricsRecorders struct { // parsed from global registry into target
	gmrs []globalMetricsRecorder2
}

// compile time type safety on these...
func (gmr *globalMetricsRecorders) RecordIntCount(handle IntCountHandle, labels []label, optionalLabels []label, incr int64/*type1*/) {
	// for each gmr
	for _, gm := range gmr.gmrs { // can I make the [] a type?
		gm.RecordIntCount(handle, labels, optionalLabels, incr)
	}
	//    forward
}

func (gmr *globalMetricsRecorders) RecordFloatCount(handle FloatCountHandle, labels []label, optionalLabels []label, incr float64) {
	for _, gm := range gmr.gmrs {
		gm.RecordFloatCount(handle, labels, optionalLabels, incr)
	}
}

func (gmr *globalMetricsRecorders) RecordIntHisto(handle IntHistoHandle, labels []label, optionalLabels []label, incr int64) {
	for _, gm := range gmr.gmrs {
		gm.RecordIntHisto(handle, labels, optionalLabels, incr)
	}
}

func (gmr *globalMetricsRecorder) RecordFloatHisto(handle FloatHistoHandle, labels []label, optionalLabels []label, incr float64) {
	for _, gm := range gmr.gmrs {
		gm.RecordFloatHisto(handle, labels, optionalLabels, incr)
	}
}
