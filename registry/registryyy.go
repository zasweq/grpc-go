package registry

import (
	"go.opentelemetry.io/otel/attribute"
	grpc "google.golang.org/grpc"
	"google.golang.org/grpc/stats/opencensus"
)

// global instrument registry:

// IntCountHandle is a typed handle for a int count instrument. This handle is
// passed at the recording point in order to know which instrument to record on.
type IntCountHandle struct {
	index int
}

// RegisterIntCount registers the int count instrument description onto the
// global registry. It returns a typed handle to use when recording data.
func RegisterIntCount(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) IntCountHandle {}

// FloatCountHandle is a typed handle for a float count instrument. This handle
// is passed at the recording point in order to know which instrument to record
// on.
type FloatCountHandle struct {
	index int
}

// RegisterFloatCount registers the float count instrument description onto the
// global registry. It returns a typed handle to use when recording data.
func RegisterFloatCount(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) FloatCountHandle {}

// IntHistoHandle is a typed handle for a int histogram instrument. This handle
// is passed at the recording point in order to know which instrument to record
// on.
type IntHistoHandle struct {
	index int
}

// RegisterIntHisto registers the int histogram instrument description onto the
// global registry. It returns a typed handle to use when recording data.
func RegisterIntHisto(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) IntHistoHandle {}

// FloatHistoHandle is a typed handle for a float histogram instrument. This
// handle is passed at the recording point in order to know which instrument to
// record on.
type FloatHistoHandle struct {
	index int
}

// RegisterFloatHisto registers the float histogram instrument description onto
// the global registry. It returns a typed handle to use when recording data.
func RegisterFloatHisto(name string, desc string, unit string, labels []string, optionalLabels []string, def bool) FloatHistoHandle {}

// Label represents a string attribute/label to attach to metrics.
type Label struct {
	// Key is the key of the label.
	Key string
	// Value is the value of the label
	Value string
}

// litmus test is assumption

// []stats.Handler ->
// one dial option that does both
// Dial Option for both or one dial option for each returned as a join
// {stats.Handler, MetricsRecorder}
// also what to persist in channel (both)
// stats.Handler interface and typecast
// type {stats handler}, type metrics recorder, embed both in one, put sh in metrics recorder
// future compatibility...how to configure and what to persist?
// same thing for DialOption, also what in Channel


// global instrument registry is the same...

type MetricsRecorder interface { // pluggable - OTel not per call but can switch later...black box, define interface I want
	Target(string) bool // forwards to underlying OTel
	// RecordIntCount
	RecordIntCount(IntCountHandle, []Label, []Label, int64)

	RecordFloatCount(FloatCountHandle, []Label, []Label, float64)

	RecordIntHisto(IntHistoHandle, []Label, []Label, int64)

	RecordFloatHisto(FloatCountHandle, []Label, []Label, float64)
} // The OTel stats plugin implements this no questions asked

// Interceptor on the object...
// csh.interceptor
// Stats Handler (csh), on this object this will implement it

// So the OTel object? yes
// &clientStatsHandler implements these methods concretely

// how do I plumb the object into

// grpc.WithStatsHandler is a stats.Handler
// can I typecast the interface down?

// also OTel api returns a joined dial option
// with interceptors and stats handlers

// return both?

// another dimension
// start it at the top level - two separate dial options with two separate types...

// or typecast it down at somepoint in the system - this determines what gets returned from otel new
// if any of the []sh does implement npc interface use that to record...

// pass it down regardless...
// ask Yash what's going on

// if you do persist a list of []sh
// expose that through balancer.ClientConn

// do this typecast down in WRR after typecasting it down...




// Extra OTel plugin options
type OTelPluginOpts struct { // extra on top of ones already there...
	// MeterProvider is the MeterProvider instance that will be used for access
	// to Named Meter instances to instrument an application. To enable metrics
	// collection, set a meter provider. If unset, no metrics will be recorded.
	// Any implementation knobs (i.e. views, bounds) set in the passed in object
	// take precedence over the API calls from the interface in this component
	// (i.e. it will create default views for unset views).
	MeterProvider metric.MeterProvider
	// MetricsToEnable are the metrics to enable alongside the metrics which are
	// by default turned on. These strings alongside the defaults will be
	// configured if the metric if also present in global registry.
	MetricsToEnable []string // is this for both per call and non per call I think so because wants to be seamless
	// MetricsToDisable are the metrics to disable of the metrics which are
	// default turned on.
	MetricsToDisable []string
	// set a bool for disable?
	// method/target attribute filter already there

	// Attributes are constant attributes applied to every recorded metric.
	Attributes []attribute.KeyValue // otel.attribute

	// OptionalLabels are the optional labels to record on this plugin.
	OptionalLabels []string

	// TargetAttributeFilter is a callback that takes the target string and
	// returns a bool representing whether to use target as a label value or use
	// the string "other". If unset, will use the target string as is.
	TargetAttributeFilter func(string) bool // target scope no longer needed since same functionality from per channel dial option
	// MethodAttributeFilter is a callback that takes the method string and
	// returns a bool representing whether to use method as a label value or use
	// the string "other". If unset, will use the method string as is. This is
	// used only for generic methods, and not registered methods.
	MethodAttributeFilter func(string) bool
}

// will create a [] of all the arbitrary ones alongside the fixed

// extra otel methods
// RecordIntCount records the Int Count measurement? for the instrument
// corresponding to the handle.
func (ot *OpenTelemetryPlugin) RecordIntCount(handle IntCountHandle, labels []Label, optionalLabels []Label, incr int64) {}

// RecordFloatCount records the Float Count measurement for the instrument
// corresponding to the handle.
func (ot *OpenTelemetryPlugin) RecordFloatCount(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {}

// RecordIntHisto records the Int Histogram measurement for the instrument
// corresponding to the handle.
func (ot *OpenTelemetryPlugin) RecordIntHisto(handle IntHistoHandle, labels []Label, optionalLabels []Label, incr int64) {}

// RecordFloatHisto records the Float Histogram measurement for the instrument
// corresponding to the handle.
func (ot *OpenTelemetryPlugin) RecordFloatHisto(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {}

// []OTel - forwards to all otel instances underlying

// StatsPlugins are all of the StatsPlugins corresponding to a certain
// target. Measurements recorded on this object will be forwarded to all of the
// StatsPlugins present.
type StatsPlugins struct{} // (built out at Dial time from global Dial Options and per channel Dial Options and passed to load balancer)

// RecordIntCount records the Int Count measurement on all the stats plugins
// present.
func (sps *StatsPlugins) RecordIntCount(handle IntCountHandle, labels []Label, optionalLabels []Label, incr int64) {}

// RecordFloatCount records the Float Count measurement on all the stats plugins
// present.
func (sps *StatsPlugins) RecordFloatCount(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {}

// RecordIntHisto records the Int Histogram measurement on all the the stats
// plugins present.
func (sps *StatsPlugins) RecordIntHisto(handle IntHistoHandle, labels []Label, optionalLabels []Label, incr int64) {}

// RecordFloatHisto records the Float Histogram measurement on all the the stats
// plugins present.
func (sps *StatsPlugins) RecordFloatHisto(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {}

var rrFallbackHandle IntCountHandle

func init() {
	rrFallbackHandle = RegisterIntCount("grpc.wrr.rr_fallback", "Number of scheduler updates in which there were not enough endpoints with valid weight, which caused the WRR policy to fall back to RR behavior.", "updates",[]string{"grpc.target"}, []string{"grpc.locality"}, true)
}

type WeightedRoundRobin struct {
	sps StatsPlugins
}

func (wrr *WeightedRoundRobin) recordingPoint() {
	wrr.sps.RecordIntCount(rrFallbackHandle, []Label{{Key: "grpc.target", Value: "canonicalized_target"}}, []Label{{Key: "grpc.locality", Value: "locality"}}, 1)
}

func main() {
	// set it with certain opts here...
	opts := OTelPluginOpts{ // switch this to full options...
		MeterProvider: /*meter provider here...*/,
		// how to convey optional labels to add (also account for csm plugin option)
		// in an efficient data structure

		// I feel like either []string
		// or a set of string
	}

	// this gives you unary + streaming interceptors and a stats handler, do I
	// want this to be all 3 + non per call (how to plumb a non per call into
	// this system?)?

	// Dial option for both? Can you typecast underlying type?
	ocdo := opencensus.DialOption(opts) // should this get it out of the box I think soooo if configured with other stats handler just don't use that if it doesn't implement non per call interface
	grpc.Dial("target here...", ocdo)
	internal.SetGlobalDialOption(ocdo) // global dial option is only internal for now...will need to expose this
} // or go through this operation vvv ()

// another thing Yash wants is a function, does both out of the box so backwards compatible
// function
//     register client
//     register server
// because some metrics might be across both/orthogonal to what side so get everything out of the box in one operation...



// combine the two after dial here
func (cc) Combine() {
	// for over normal opts
	// for over global, append to shs (this is how it already works right)

	// persists a []stats.Handler rn, send that around
	// question is how to merge this seperate concept of []nonpercall metrics in.
	// non per call type under the hood on the otel struct
	// can I access underlyingtype.(NonPerCallMetrics interface) to do it this way?


	// holds onto both...configure seperatly? how to plumb this extra dimension knob in?

	// how to get it to client/WRR is the other question...
}

// []percall and non per call, pass this around to the
// WRR and xDS Client components that need this...
// still need to decide whether helper on ClientConn or pass it down through options...

// xDS Client uses first

// can give a light rough draft to Doug with the three code snippets desired above...
