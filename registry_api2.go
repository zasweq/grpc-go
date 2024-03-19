package grpcapi2

// Label represents a string attribute/label to attach to metrics.
type Label struct {
	// Key is the key of the label.
	Key string
	// Value is the value of the label
	Value string
}

// global instrument registry is the same...

type MetricsRecorder interface { // pluggable - OTel not per call but can switch later...black box, define interface I want
	Target(string) bool // forwards to underlying OTel
	// RecordIntCount
	RecordIntCount(IntCountHandle, []Label, []Label, int64)

	RecordFloatCount(FloatCountHandle, []Label, []Label, float64)

	RecordIntHisto(IntHistoHandle, []Label, []Label, int64)

	RecordFloatHisto(FloatCountHandle, []Label, []Label, float64)
}

// metricsrecorderopts switch to stats handler api


// aggregate list of metrics recorder...same interface that
// just delegates/forwards...just forwards upward
// aggregate object passed from client conn that implements this interface ^^^

// switch the metric recorder opts to stats handlers

// and [] delegates to all...
// maybe the concrete type definition, which implements interface

// MetricsRecorder implemented by OTel and the object passed around...

// either pass the object thingy through a scaled up balancer.ClientConn (should
// make a lowercase to force embed)

// or as part of build options
// but it does hold onto
// wrr {
//    []nonPerCallMetric
// }


