package grpcapi

// global instrument registry:

// IntCountHandle is a typed handle for a int count instrument. This handle is
// passed at the recording point in order to know which instrument to record on.
type IntCountHandle struct {
	index int
}

// RegisterIntCount registers the int count instrument description onto the
// global registry. It returns a typed handle to use when recording data.
func RegisterIntCount(name string, desc string, unit string, labels []string, optionalLabels []string) IntCountHandle {}

// FloatCountHandle is a typed handle for a float count instrument. This handle
// is passed at the recording point in order to know which instrument to record
// on.
type FloatCountHandle struct {
	index int
}

// RegisterFloatCount registers the float count instrument description onto the
// global registry. It returns a typed handle to use when recording data.
func RegisterFloatCount(name string, desc string, unit string, labels []string, optionalLabels []string) FloatCountHandle {}

// IntHistoHandle is a typed handle for a int histogram instrument. This handle
// is passed at the recording point in order to know which instrument to record
// on.
type IntHistoHandle struct {
	index int
}

// RegisterIntHisto registers the int histogram instrument description onto the
// global registry. It returns a typed handle to use when recording data.
func RegisterIntHisto(name string, desc string, unit string, labels []string, optionalLabels []string) IntHistoHandle {}

// FloatHistoHandle is a typed handle for a float histogram instrument. This
// handle is passed at the recording point in order to know which instrument to
// record on.
type FloatHistoHandle struct {
	index int
}

// RegisterFloatHisto registers the float histogram instrument description onto
// the global registry. It returns a typed handle to use when recording data.
func RegisterFloatHisto(name string, desc string, unit string, labels []string, optionalLabels []string) FloatHistoHandle {}

// RegisterMetricsRecorder registers the MetricsRecorder onto the global
// registry.
func RegisterMetricsRecorder(mr *MetricsRecorder) {}

// GetMetricsRecordersForTarget returns the metrics recorders registered in the global
// registry that apply to the target.
func GetMetricsRecordersForTarget(target string) MetricsRecorders {}

// same bucket as A66 (static metrics + globally registered)
// also plugin/optional label plugin filter goes through same thing
// because needed for csm as well

// MetricsRecorderOpts are the options used to configure a MetricsRecorder.
type MetricsRecorderOpts struct {
	// MeterProvider is the MeterProvider instance that will be used for access
	// to Named Meter instances to instrument an application. To enable metrics
	// collection, set a meter provider. If unset, no metrics will be recorded.
	// Any implementation knobs (i.e. views, bounds) set in the passed in object
	// take precedence over the API calls from the interface in this component
	// (i.e. it will create default views for unset views).
	MeterProvider metric.MeterProvider
	// Metrics are the metrics to instrument. Will turn on the corresponding
	// metric if also in global registry.
	// api for enable/disable...instead of this
	// Metrics []string // allocates enough space from global instruments registry regardless, keeps the properties...
	// MetricsToEnable are the metrics to enable alongside the metrics which are
	// by default turned on. These strings alongside the defaults will be
	// configured if the metric if also present in global registry.
	MetricsToEnable []string // map maybe to avoid string lookups form global registry
	// MetricsToDisable are the metrics to disable of the metrics which are
	// default turned on.
	MetricsToDisable []string

	// TargetScope is a callback that passes in the target string and returns
	// whether a target is applicable for a MetricsRecorder. If unset,
	// will never match to a target.
	TargetScope func(string) bool // noop if used in open source
	// TargetAttributeFilter is a callback that takes the target string and
	// returns a bool representing whether to use target as a label value or use
	// the string "other". If unset, will use the target string as is.
	TargetAttributeFilter func(string) bool


	// *** Cleanup
	// will eventually need to persist this map from name to index want
	// read the optional labels/instrument name from index, look into map below
	// handle1: (1) 2 (3) 4 (5)
	// handle2: 6 (7) 8 (9) 10

	// example usage of mapping above with the single optional label...mention will eventually make extensible

	// PluginOption
	PluginOption func(index int) []string // rename? eventually emit labels for an arbitrary index
}

// mention extensible and map it to structure above, want this to work in future with multiple optional labels
func ExamplePluginOption(index int) []string {
	// use index to look into global registry
	// if name := globalregistry[index].name; name == "metricWithOptionalLabel" {
	//       return []string{label}
	// }


	// eventually
	// map[string][]string (built from instruments registry/choosing optional labels you want from instrument registry)
}
// ***



// NewGlobalMetricsRecorder returns a global metrics recorder constructed with
// the provided options.
func NewGlobalMetricsRecorder(opts MetricsRecorderOpts) *MetricsRecorder {}

// MetricsRecorder in an object used to record non per call metrics. At
// creation time, it creates instruments based off options provided alongside
// the global instruments registry. It records data points on these instruments
// based off handles provided by the global registry.

// get this out by at latest Wed so people can review and add comments...

type MetricsRecorder struct {} // not implementable by users?

// Target returns whether this global metrics recorder is applicable for the
// passed in target.
func (mr *MetricsRecorder) Target(target string) bool {}

// Label represents a string attribute/label to attach to metrics.
type Label struct {
	// Key is the key of the label.
	Key string
	// Value is the value of the label
	Value string
}

// RecordIntCount records the Int Count measurement? for the instrument
// corresponding to the handle.
func (mr *MetricsRecorder) RecordIntCount(handle IntCountHandle, labels []Label, optionalLabels []Label, incr int64) {}

// RecordFloatCount records the Float Count measurement for the instrument
// corresponding to the handle.
func (mr *MetricsRecorder) RecordFloatCount(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {}

// RecordIntHisto records the Int Histogram measurement for the instrument
// corresponding to the handle.
func (mr *MetricsRecorder) RecordIntHisto(handle IntHistoHandle, labels []Label, optionalLabels []Label, incr int64) {}

// RecordFloatHisto records the Float Histogram measurement for the instrument
// corresponding to the handle.
func (mr *MetricsRecorder) RecordFloatHisto(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {}

// MetricsRecorders are all of the MetricsRecorders corresponding to a certain
// target. Measurements recorded on this object will be forwarded to all of the
// MetricsRecorders present.
type MetricsRecorders struct{}

// RecordIntCount records the Int Count measurement on all the global metrics
// recorders present.
func (gmr *MetricsRecorders) RecordIntCount(handle IntCountHandle, labels []Label, optionalLabels []Label, incr int64) {}

// RecordFloatCount records the Float Count measurement on all the global
// metrics recorders present.
func (gmr *MetricsRecorders) RecordFloatCount(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {}

// RecordIntHisto records the Int Histogram measurement on all the global
// metrics recorders present.
func (gmr *MetricsRecorders) RecordIntHisto(handle IntHistoHandle, labels []Label, optionalLabels []Label, incr int64) {}

// RecordFloatHisto records the Float Histogram measurement on all the global
// metrics recorders present.
func (gmr *MetricsRecorders) RecordFloatHisto(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {}


// usage in WRR
func init() { // optional labels?
	intCountHandle := RegisterIntCount("")
}

type wrr struct {
	mrs MetricsRecorders
}

func objectCreation() {
	// take target - walk through reasoning of using at this moment instead of after dial (and that dial changes)
	mrs := GetMetricsRecordersForTarget( /*target passed in in build options*/)
}

func (wrr *wrr) recording() {
	var intCountHandle IntCountHandle
	wrr.mrs.RecordIntCount(intCountHandle, Label{Key: /*same label as passed above...*/}, nil, 1) // provides type safety...
}




// for presentation call out type safety, call into []gmr (also call out it's a different object)
// also the fact we choose to go seperate object rather than mess with exisiting stats handler...
// Talking points
// WRR/xDS Client talk to []MetricsRecorders
// look into global registry at balancer creation time (other option is after Dial (where target can change)), canonical target
// is already present in balancer, at xDS Client time when switched to per target xDS Client is when I can look at it...

