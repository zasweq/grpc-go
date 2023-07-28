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

// Package opentelemetry implements opencensus instrumentation code for gRPC-Go
// clients and servers.
package opentelemetry

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

var logger = grpclog.Component("opentelemetry-instrumentation")

var canonicalString = internal.CanonicalString.(func(codes.Code) string)

// what is the global default for MeterProvider? the no-op or the SDK

// I think we still want this to be dynamic...
var (
	joinDialOptions = internal.JoinDialOptions.(func(...grpc.DialOption) grpc.DialOption)
)

// make sure to mark as experimental, this is in flux.

// traceoptions and metricsOptions?

// takes this at registration time...not dynamic

// MetricsOptions are the metrics options for OpenTelemetry instrumentation.
type MetricsOptions struct {
	// MeterProvider is the MeterProvider instance that will be used for access
	// to Named Meter instances to instrument an application. Overwrites the
	// global default if set.

	// Intended to be set by the SDK implementation?

	MeterProvider metric.MeterProvider // already stable
	// EnabledMetrics are the advanced metrics that are enabled past the base
	// metrics.
	EnabledMetrics []string
	// DisabledMetrics are the metrics disabled that gRPC will not record.
	// gRPC OTel component (doesn't record) -> MeterProvider with the basics registered.
	DisabledMetrics []string
} // set this struct and call DialOption right?

func DialOption(mo MetricsOptions/*, to TraceOptions*/) grpc.DialOption {
	// both the interceptor (how to persist)?
	// and stats handler have to persist these options since they all work in conjunction...
	csh := &clientStatsHandler{mo: mo} // before creating interceptor (which records metrics) needs to build out metrics list

	// ohhh since interceptor is on stats handler good
	return joinDialOptions(grpc.WithChainUnaryInterceptor(csh.unaryInterceptor), grpc.WithStreamInterceptor(csh.streamInterceptor), grpc.WithStatsHandler(csh))
} // multiple by grpc.DialOption, grpc.DialOption

func (csh *clientStatsHandler) messAroundWithMetricsOptions() {
	csh.mo.EnabledMetrics // []string set at beginning - register more metrics...at what layer this or MeterProvider?
	csh.mo.DisabledMetrics // []string when to actually disable the metrics...
	// this is implemented by SDK or user can

	// sdk implements that
	csh.mo.MeterProvider // metric.MeterProvider
	csh.mo.MeterProvider.Meter(/*name string, meter option*/)
}

// preprocessing:
//        create call span - but only if traces aren't disabled...do I need to give an option disable traces
//
//

// postprocessing:
//        finish the call span
//        record call latency...

// what does the interceptor do...?
// top level span for sure
// top level metrics stuff like retry? server side doesn't have this concept right?
func (csh *clientStatsHandler) unaryInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	// add to ctx is one way for target
	cc.Target() // this comes in at the interceptor time, but only need to set once
	// if unset set and use that the rest of time, set at beginning of client conn and use that

	csh.mo.EnabledMetrics // how to use this to create advanced? when it does you just use it?
	csh.mo.DisabledMetrics // []string, make a set

	// same interface

	// how to interface with this to even record a metric?

	// if _, ok := csh.mo.DisabledMetricsMap[metric we know we're recording] {
	// }


	// same logic...as streaming

	// I just need top level span stuff...
	// metrics is in beta....

	// opencensus for both client and server side (server side does post processing with async callback)

	// grpc.client.call.duration (same as call latency)
	// this metric aims to measure the end-to-end time the gRPC
	// library takes to complete an RPC from the applications perspective

	// 1. create per call data needed for traces and metrics
	// call span

	// 2. do rpc
	err := invoker(ctx, method, req, reply, cc, opts...)

	// 3. post processing (callback in streaming flow...)

	return err
}

func (csh *clientStatsHandler) streamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	// same logic as unary

	// 1. create per call data needed for traces and metrics
	// call span, start time for duration of full call...
	// startTime := time.Now(), where this will be call duration

	// 2. do rpc
	s, err := streamer(ctx, desc, cc, method, opts...)
	if err != nil {
		return nil, err
	}
	return s, nil
	// 3. post processing (callback in streaming flow...)
}

func (csh *clientStatsHandler) perCallMetrics(err error, ctx context.Context, startTime time.Time, method string, target string) { // oh can't do inline for reusability...
	s := status.Convert(err)
	callLatency := float64(time.Since(startTime)) / float64(time.Millisecond)
	// method target and status:
	// method: method
	// target: cc.target
	// status: s

	// remake the variable here...




	if csh.registeredMetrics.clientCallDuration != nil {
		csh.registeredMetrics.clientCallDuration.Record(ctx, callLatency, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(method)), attribute.String("grpc.target", target), attribute.String("grpc.status", canonicalString(s.Code())))) // needs method target and status should I persist this?
	}
}



	// Same thing...Dial Option with everything configured

// metrics flow
// tracing flow


func ServerOption(mo MetricsOptions) grpc.ServerOption {
	return grpc.StatsHandler(&serverStatsHandler{mo: mo}) // why is this a pointer?
}


// In the top level context...

// opencensus top level span + timestamp (only call metric outside) we also will
// have retry metrics which need to persist a counter so add something to
// context at interceptor level, Doug mentioned because the Dial Option always
// has both components it's fine to assume need both to function properly, not
// an API within a component

type perCallInfo struct {
	// top level span

	target string

	// Eventually for retry metrics some sort of full time - Doug mentioned communication between these two components is handled by stats handler

	//
}

type perAttemptInfo struct { // created at beginning of stats handler tag, scoped to attempt, data needed per attempt and/attempt is handled

}

type clientStatsHandler struct {
	// what options/state needs to be persisted here?
	// to: opencensus is just trace sampler and whether you disable - same thing here?
	mo MetricsOptions // to avoid copy make these pointers/

	registeredMetrics registeredMetrics // pointer or not

	target string // this only needs to be set once
	// so either if target != "" set

	// also put target string in top level object

	// or just always set
}

// retry delay per call (A45)...through interceptor will be wrt the talking between interceptor and stats handler right...

// TagConn exists to satisfy stats.Handler.
func (csh *clientStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn exists to satisfy stats.Handler.
func (csh *clientStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// TagRPC implements per RPC attempt context management.
func (csh *clientStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	// doesn't look like csh takes any meausrements in Tag all in Handle since that gets events

	mi := &metricsInfo{
		startTime: time.Now(),
		method: info.FullMethodName,
	}

	// appends to OpenCensus tag metadata if set by application...do I want to
	// keep this feature for OTel...? Can this even be triggered in the OTel
	// flow?

	// creates an attempt span though...


	// puts a single thing in context with {mi, ti}
}

func (csh *clientStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	// gets metric info from tag rpc

	// csh.mo.MeterProvider // has access to this? to record measurements
	// and a list of disabled metrics and enabled metrics... (data passed because recordRPCData is on csh)

	// helper that switches on rpc stats type...does it populate with RPCInfo as well?
	csh.recordRPCData(ctx, rs, /*thing pulled from TagRPC...*/)

	// disable trace bool as well?



}

func removeLeadingSlash(mn string) string {
	return strings.TrimLeft(mn, "/")
}

// persist for the lifetime of HandleRPC
// TagRPC...aka the attempt life span
type metricsInfo struct { // rename? can I just reuse althoguh I think I want to keep orthogonal...
	// access these counts atomically for hedging in the future
	// number of messages sent from side (client || server)
	sentMsgs int64 // might not need this? for now
	// number of bytes sent (within each message) from side (client || server)
	sentBytes int64 // might not need this? for now
	// number of bytes after compression (within each message) from side (client || server)
	sentCompressedBytes int64 // I think compressed is all you need
	// number of messages received on side (client || server)
	recvMsgs int64 // might not need this?
	// number of bytes received (within each message) received on side (client
	// || server)
	recvBytes int64 // might not need this?
	// number of compressed bytes received (within each message) received on
	// side (client || server)
	recvCompressedBytes int64 // I think compressed is all you need
	// we only have a subset though, might not need sentBytes

	startTime time.Time
	method  string
} // target and authority tags I don't know if already have the plumbing to support...is this already passed in?

// talk to Yash about this stuff...

// do this for all basic...but also do this for Any additional (how to add that recording logic? like views?) and also
// disable (just don't add to map and if you don't get don't record
// in getter to record if in map add if not skip:
// if measure, ok := map[measurename]; ok {
//      record with tags
// }

// run after enabled disabled, run through these

func (csh *clientStatsHandler) constructMetricsForStatsHandler(mp metric.MeterProvider) {
	// mp.Meter() // persist all these measures in stats handler

	// One meter persisted with all counter?
	// map[metric name]counter?

	// Each telemetry component has it's own MeterProvider...and thus everything
	// can be scoped to csh's memory. The question is how far along do you
	// persist?
	// get a default bounds...OTel provides API later, so keep experimental
	m := csh.mo.MeterProvider.Meter("noop" /*what options do you provide, Attributes Type and Unit (/attempt is this just implied by dashboard? Do I need to explicitly set this?) are part of spec for instrument...*/) // The total number of RPC attempts started, including those that have not completed.
	// I think this is like a measure?
	// Float64Counter returns a new Float64Counter instrument identified by
	// name and configured with options. The instrument is used to
	// synchronously record increasing float64 measurements during a
	// computational operation.

	// one meter at instantiation
	// instruments the user specifies for that component

	// instrument...counter on an instrument
	/*
		grpc.client.attempt.started
		The total number of RPC attempts started, including those that have not completed.
		Attributes: grpc.method, grpc.target (handled with an add option)
		Type: Counter (count (/attempt) is this also an add option?)
		Unit: {attempt}
	*/
	// sdk you can provide explicit bounds (so o11y plugins can use), api still coming
	f64c, err := m.Int64Counter("grpc.client.attempt.started" /*<- correct string?*, what options do you provide here?*/, metric.WithUnit("attempt")) // over an attempt so do you have to provide this unit?
	if err != nil {
		// handle error - where should this codeblock be?
	}

	// clearly VVV recording does...
	// persist ^^^ in a map? How do instruments fit into this?

	// and then get map[measureName], Add to that?

	// assuming ctx propgates timeouts...

	// at least this is part of metric recording point
	f64c.Add(ctx, 1, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.target", "grpc target string here - target URI used when creating a gRPC Channel..."))) // Add records a change to the counter...
	// ||
	// Use the WithAttributeSet (or, if performance is not a concern, the WithAttributes) option to include measurement attributes.
	metric.WithAttributeSet(/*attribute set of method and target here...*/) // Set makes it more performant...


	/*
	grpc.client.attempt.sent_total_compressed_message_size
	Total bytes (compressed but not encrypted) sent across all request messages (metadata excluded) per RPC attempt; does not include grpc or transport framing bytes.
	Attributes: grpc.method, grpc.target, grpc.status
	Type: Histogram (Size Buckets) (Histogram with a parameter of buckets)
	*/
	i64his, err := m.Int64Histogram("grpc.client.attempt.duration"/*name, histogram option, I'm assuming you plumb in from htere*/, metric.Int64HistogramOption /*plug in a histogram option that specifies buckets - prob in a different package*/)
	if err != nil {
		// what to do, error out of the whole module?
	}
	/*
	var st string
		if e.Error != nil {
			s, _ := status.FromError(e.Error)
			st = canonicalString(s.Code())
		} else {
			st = "OK"
		}
	*/
	i64his.Record(ctx, /*compressed message bytes sent down*/, /*RecordOptions - def method target and status, should I write a helper for this?*/metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.target", "grpc target string here - target URI used when creating a gRPC Channel..."), attribute.String("grpc.status", st)))

	// if you store this as an interface, will need to typecast downward, not one it all implements

}

// set of names

// in init
// each metric defined in his spec
//        create a hardcoded measure of type based off his RFC definition

// rather than a map, Yash had hardcoded metrics with pointer determining whether set or not

type registeredMetrics struct { // nil or not nil means presence
	// "grpc.client.attempt.started"
	clientAttemptStarted metric.Int64Counter // no need to export since all happens internally
	// "grpc.client.attempt.duration"
	clientAttemptDuration metric.Float64Histogram
	// "grpc.client.attempt.sent_total_compressed_message_size"
	clientAttemptSentTotalCompressedMessageSize metric.Int64Histogram
	// "grpc.client.attempt.rcvd_total_compressed_message_size"
	clientAttemptRcvdTotalCompressedMessageSize metric.Int64Histogram

	// per call metrics - wait no that's at higher level, figure out how to plumb all these objects up there
	clientCallDuration metric.Float64Histogram

	// "grpc.server.call.started"
	serverCallStarted metric.Int64Counter // /call, needs method and authority
	// "grpc.server.call.sent_total_compressed_message_size"
	serverCallSentTotalCompressedMessageSize metric.Int64Histogram
	// "grpc.server.call.rcvd_total_compressed_message_size"
	serverCallRcvdTotalCompressedMessageSize metric.Int64Histogram
	// "grpc.server.call.duration"
	serverCallDuration metric.Float64Histogram

} // use this for both client and server side, so that way you can have metrics recording for both

// keep the flows separate for client and server, avoid the branch and pull
// authority from client headers



// make it required for Meter Provider, don't fallback to global (so we chose this vvv)

// not passing meter provider = fallback to no-op, since also record traces

// global one is settable by user...

// default to global if no meter provider set

// api (what's gated by presence checks) gets overwritten by sdk if saves

func (ssh *serverStatsHandler) buildMetricsDataStructuresAtInitTime(mo *MetricsOptions) {
	var setOfMetrics map[string]struct{}

	var registeredMetrics registeredMetrics

	// or pass in a meter
	meter := mo.MeterProvider.Meter("no-op namespace name? Prevent collisions?")
	// counter name - sdk takes precedence, then api (api here is defaults)
	if _, ok := setOfMetrics["grpc.server.call.started"]; ok {
		// grpc.client.attempt.started - Yash mentioned all this stuff is hardcoded based off metrics definitions...
		scs, err := meter.Int64Counter("grpc.server.call.started", metric.WithUnit("call") /*any other options?*/) // I think this handled with unit
		if err != nil {
			// handle error - should this fully error or continue onto the next metric...just not put it in the map, and future metrics recording points won't record
			// continue to next metric
			// log it didn't register - on what logger
		} else {
			// persist in map (as what)
			registeredMetrics.serverCallStarted = scs // will this cause some weird stuff with Garbage collector wrt pointer?
		}
	} // record on asc (by pulling out from persisting) in RPC Start on server side (is there a way to avoid this branch

	if _, ok := setOfMetrics["grpc.server.call.sent_total_compressed_message_size"]; ok {
		ss, err := meter.Int64Histogram("grpc.server.call.sent_total_compressed_message_size" /*option specifying size buckets here*/)
		if err != nil {
			// log it didn't register
		} else {
			registeredMetrics.serverCallSentTotalCompressedMessageSize = ss
		}
	} // record on scs in stats.End

	if _, ok := setOfMetrics["grpc.server.call.rcvd_total_compressed_message_size"]; ok {
		sr, err := meter.Int64Histogram("grpc.server.rcvd.sent_total_compressed_message_size" /*option specifying size buckets here*/)
		if err != nil {
			// log it didn't register
		} else {
			registeredMetrics.serverCallRcvdTotalCompressedMessageSize = sr
		}
	} // record on scs in stats.End

	if _, ok := setOfMetrics["grpc.server.call.duration"]; ok {
		// grpc.client.attempt.duration - e2e time it takes to complete an RPC attempt including time it takes to pick subchannel (do I need to plumb these description strings anywhere?)
		scd, err := meter.Float64Histogram("grpc.server.call.duration", metric.Float64HistogramOption()) // there's gotta be like with bounds (latency bucket) or something...also declare bounds above
		if err != nil {
			// handle error - should this fully error or continue onto the next metric...just not put it in the map, and future metrics recording points won't record
			// Yash mentioned just log failed registration - and agreed with behavior of continuing
		} else {
			registeredMetrics.serverCallDuration = scd
		}
	} // record on cad in stats.End
}

func (csh *clientStatsHandler) buildMetricsDataStructuresAtInitTime(mo *MetricsOptions) {
	// master set of metrics - metrics type?

	// []set of metrics - whether from his api or my api where you pass in directly and append
	var setOfMetrics map[string]struct{} // Yash had this passed in, provide default metrics to user, maybe I can just say register metrics...

	// build a map[metric name]interface{} where interface{} is arbitary counter type no use what Yash did
	// var mapOfMetrics map[string]interface{} // presence in this will be used for registration
	var registeredMetrics registeredMetrics // pointer or not?

	meter := mo.MeterProvider.Meter("no-op namespace name? Prevent collisions?"/*any options here?*/)

	if _, ok := setOfMetrics["grpc.client.attempt.started"]; ok {
		// grpc.client.attempt.started - Yash mentioned all this stuff is hardcoded based off metrics definitions...
		asc, err := meter.Int64Counter("grpc.client.attempt.started", metric.WithUnit("attempt") /*any other options?*/) // I think this handled with unit
		if err != nil {
			// handle error - should this fully error or continue onto the next metric...just not put it in the map, and future metrics recording points won't record
			// continue to next metric
			// log it didn't register
		} else {
			// persist in map (as what)
			registeredMetrics.clientAttemptStarted = asc // will this cause some weird stuff with Garbage collector wrt pointer?
		}
	} // record on asc (by pulling out from persisting) in RPC Start


	if _, ok := setOfMetrics["grpc.client.attempt.duration"]; ok {
		// grpc.client.attempt.duration - e2e time it takes to complete an RPC attempt including time it takes to pick subchannel (do I need to plumb these description strings anywhere?)
		cad, err := meter.Float64Histogram("grpc.client.attempt.duration", metric.Float64HistogramOption()) // there's gotta be like with bounds (latency bucket) or something...also declare bounds above
		if err != nil {
			// handle error - should this fully error or continue onto the next metric...just not put it in the map, and future metrics recording points won't record
			// Yash mentioned just log failed registration - and agreed with behavior of continuing
		} else {
			registeredMetrics.clientAttemptDuration = cad
		}
	} // record on cad in stats.End

	// histogram bounds are not part of their api yet
	metric.NewFloat64HistogramConfig()

	if _, ok := setOfMetrics["grpc.client.attempt.sent_total_compressed_message_size"]; ok {
		cas, err := meter.Int64Histogram("grpc.client.attempt.sent_total_compressed_message_size" /*option specifying size buckets here*/)
		if err != nil {
			// log it didn't register
		} else {
			registeredMetrics.clientAttemptSentTotalCompressedMessageSize = cas
		}
	} // record on cas in stats.End

	if _, ok := setOfMetrics["grpc.client.attempt.rcvd_total_compressed_message_size"]; ok {
		// gate based off set
		car, err := meter.Int64Histogram("grpc.client.attempt.rcvd_total_compressed_message_size" /*option specifying size buckets here*/)
		if err != nil {
			// log it didn't register
		} else {
			registeredMetrics.clientAttemptRcvdTotalCompressedMessageSize = car
		}
	} // record on cas in stats.End

	// counter name - sdk takes precedence, then api (api here is defaults)
	if _, ok := setOfMetrics["grpc.client.call.duration"]; ok {
		// grpc.client.attempt.started - Yash mentioned all this stuff is hardcoded based off metrics definitions...
		ccs, err := meter.Float64Histogram("grpc.client.call.duration", metric.WithUnit("call") /*any other options?*/) // I think this handled with unit
		if err != nil {
			// handle error - should this fully error or continue onto the next metric...just not put it in the map, and future metrics recording points won't record
			// continue to next metric
			// log it didn't register
		} else {
			// persist in map (as what)
			registeredMetrics.clientCallDuration = ccs // will this cause some weird stuff with Garbage collector wrt pointer?
		}
	}


	// counter name - sdk takes precedence, then api (api here is defaults)
	if _, ok := setOfMetrics["grpc.server.call.started"]; ok {
		// grpc.client.attempt.started - Yash mentioned all this stuff is hardcoded based off metrics definitions...
		scs, err := meter.Int64Counter("grpc.server.call.started", metric.WithUnit("call") /*any other options?*/) // I think this handled with unit
		if err != nil {
			// handle error - should this fully error or continue onto the next metric...just not put it in the map, and future metrics recording points won't record
			// continue to next metric
			// log it didn't register
		} else {
			// persist in map (as what)
			registeredMetrics.serverCallStarted = scs // will this cause some weird stuff with Garbage collector wrt pointer?
		}
	} // record on asc (by pulling out from persisting) in RPC Start on server side (is there a way to avoid this branch

	if _, ok := setOfMetrics["grpc.server.call.sent_total_compressed_message_size"]; ok {
		ss, err := meter.Int64Histogram("grpc.server.call.sent_total_compressed_message_size" /*option specifying size buckets here*/)
		if err != nil {
			// log it didn't register
		} else {
			registeredMetrics.serverCallSentTotalCompressedMessageSize = ss
		}
	} // record on scs in stats.End

	if _, ok := setOfMetrics["grpc.server.call.rcvd_total_compressed_message_size"]; ok {
		sr, err := meter.Int64Histogram("grpc.server.rcvd.sent_total_compressed_message_size" /*option specifying size buckets here*/)
		if err != nil {
			// log it didn't register
		} else {
			registeredMetrics.serverCallRcvdTotalCompressedMessageSize = sr
		}
	} // record on scs in stats.End

	if _, ok := setOfMetrics["grpc.server.call.duration"]; ok {
		// grpc.client.attempt.duration - e2e time it takes to complete an RPC attempt including time it takes to pick subchannel (do I need to plumb these description strings anywhere?)
		scd, err := meter.Float64Histogram("grpc.server.call.duration", metric.Float64HistogramOption()) // there's gotta be like with bounds (latency bucket) or something...also declare bounds above
		if err != nil {
			// handle error - should this fully error or continue onto the next metric...just not put it in the map, and future metrics recording points won't record
			// Yash mentioned just log failed registration - and agreed with behavior of continuing
		} else {
			registeredMetrics.serverCallDuration = scd
		}
	} // record on cad in stats.End

	// at this point extra metrics to register, still based on set regardless of how it's built.
	// structure api like you just pass in set
	csh.registeredMetrics = registeredMetrics

	// delete server stuff

}

// how does no-op get plumbed

// build out this map from MeterProvider...counters created check if there's a signelton

// meter from meter provider

// map[string name]interface

// can be used for presence
// if counter, ok := map[name]; ok {
//     typecast to expected interface...
//     record a measurement point
// }



// name of instrument needs to be unique

// and figure out scoping/recording

// MetricProvider global SDK
// Can annotate the global (i.e. add stuff to it) or put something completely else
// in there

// MetricProvider recording point...and what to persist and the interactions
// between this and others

// Persist a set of names to read from...set based off enabled + disabled etc.

// at each point if in set {
//           record (through something persisted/built out from Metric Provider)
// }



// Metrics needed to record (in first iteration):
// Client Side: (per attempt)

// Client Side: (per call)


// Server Side:



// creates a metric info object with start time and everything you need
// to record all the different measurements

func (csh *clientStatsHandler) recordRPCData(ctx context.Context, s stats.RPCStats, mi *metricsInfo) {
	// subset and the same stuff?
	switch st := s.(type) {
	case *stats.OutHeader, *stats.InTrailer, *stats.OutTrailer:

		// does authority come before begin...if not that's undefined

		// On server side - in header pull it out from there md

		// Headers and Trailers are not relevant to the measures, as the
		// measures concern number of messages and bytes for messages. This
		// aligns with flow control.
	case *stats.InHeader:
		st.Header /*"get this and persist :authority"...per call also can you use this to record*/
		st.Client // can branch
	case *stats.Begin: // records started RPCs
		// Same thing but talking to a different interface to record?
		// also gate with csh.DisableMetrics[name] where name corresponds to application
		// recordDataBegin(ctx, mi, st)

		// if client {
		//     ocstats.RecordWithOptions(ctx,

		/*
		OpenTelemetry has attributes similar to tags in OpenCensus, and the
		OpenCensus tag names already seem to match the OpenTelemetry spec -
		except for ‘_’ vs ‘.’ for namespaces. So we just replace the ‘_’ with
		‘.’. Note that the 'client' and 'server' distinction has also been
		removed since it does not add any benefit.

		Also two new attributes - target on client side and authority on server side (how do I know which metrics to add to is this apart of spec?)
		*/

		//			ocstats.WithTags(tag.Upsert(keyClientMethod, removeLeadingSlash(mi.method))),
		//			ocstats.WithMeasurements(clientStartedRPCs.M(1)))

		// what is the logical equivalent of ^^^ in OTel world? Assuming I've already registered...? <- need to figure out how to register too...
		// tag on method...same concept in OpenTelemetry?


		// which one of these operations do you make global?
		// opencensus records against global measures...
		// clientStartedRPCs is a global measure

		// if these are global, they're dependent on the passed in Meter Provider

		// In DialOption and you get the MeterProvider do you create globals from that...?

		// But you need more than one OpenCensus instrumentation code per channel,
		// each MeterProvider is going to have it's own exporter...
		// so do the measures need to be local to components?

		/*m := csh.mo.MeterProvider.Meter("grpc.client.attempt.started" /*what options do you provide, Attributes Type and Unit (/attempt is this just implied by dashboard? Do I need to explicitly set this?) are part of spec for instrument...) // The total number of RPC attempts started, including those that have not completed.
		// I think this is like a measure?
		// Float64Counter returns a new Float64Counter instrument identified by
		// name and configured with options. The instrument is used to
		// synchronously record increasing float64 measurements during a
		// computational operation.
		f64c, err := m.Float64Counter("grpc.client.attempt.started" /*<- correct string?*, what options do you provide here?)
		if err != nil {
			// handle error - where should this codeblock be? should this error continue or exit construction?
		}
		// assuming ctx propgates timeouts...
		f64c.Add(ctx, 1, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.target", "grpc target string here - target URI used when creating a gRPC Channel..."))) // Add records a change to the counter...

		// Use the WithAttributeSet (or, if performance is not a concern, the WithAttributes) option to include measurement attributes.
		metric.WithAttributeSet(/*attribute set of method and target here...)*/ // Set makes it more performant...

		// need to figure out how to set this valueto removeLeadingSlash(mi.method)
		// metric.WithAttributes(attribute.KeyValue{Key: "grpc.method", Value: attribute.Value{}}/*attribute key value of method and target here...*/) // plug this to the end of Add...
		// attribute.String("grpc.method", removeLeadingSlash(mi.method)) // same thing it seems

		// attribute.KeyValue{Key: "grpc.target", Value: attribute.Value{}/*plumb Target URI when creating gRPC Channel (do I need to add more data passed?)*/}
		// need to persist target somehow (new logic)...and method (already there)

		// attribute.String("grpc.target", "grpc target string here - target URI used when creating a gRPC Channel...")
		// OpenCensus:
		// Measures (i.e. a number you add 1 on)
		// Views on ^^^ can have more than one view on the same measure and do stuff like distribution and count...etc.
		if st.Client {
			// early return
			if csh.registeredMetrics.clientAttemptStarted != nil {
				csh.registeredMetrics.clientAttemptStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.target", "grpc target string here - target URI used when creating a gRPC Channel..."))) // Add records a change to the counter...
			}
			return
		}
		// else (implicit from early return)
		if csh.registeredMetrics.serverCallStarted != nil { // could read this into a local var for readability see what they say on CLs
			// persist method and authority and status from register or from interceptor is what doug said
			csh.registeredMetrics.serverCallStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("authority", /*authority is part of parsed target*/)))
		}

	case *stats.OutPayload: // the other events just record stuff for metrics info...scoped to per attempt
		// recordDataOutPayload(mi, st) // persists stuff in mi for recording at recordDataEnd

		// just record sentCompressedBytes, only one you need
		atomic.AddInt64(&mi.sentCompressedBytes, int64(st.CompressedLength)) // use these to record in Data End
	case *stats.InPayload:
		// recordDataInPayload(mi, st) // persists stuff in mi for recording at recordDataEnd
		atomic.AddInt64(&mi.recvCompressedBytes, int64(st.CompressedLength))
	case *stats.End:
		// recordDataEnd(ctx, mi, st) // records against globals or against component globals? I think for lifetime of component like RPCs started...for this stats handler component...might want to turn off metrics for a component?

		// so MeterProvider is clearly per opentelemetry (customize views means the views can't be global need to go through Meter Provider...)
		// looks like enable/disable metric are too


	default:
		// Shouldn't happen. gRPC calls into stats handler, and will never not
		// be one of the types above.
		logger.Errorf("Received unexpected stats type (%T) with data: %v", s, s)
	}
}

func (csh *clientStatsHandler) recordDataEnd(ctx context.Context, mi *metricsInfo, e *stats.End) {
	// latency bounds for distribution data (speced millisecond bounds) have
	// fractions, thus need a float.
	latency := float64(time.Since(mi.startTime)) / float64(time.Millisecond)
	var st string
	if e.Error != nil {
		s, _ := status.FromError(e.Error)
		st = canonicalString(s.Code())
	} else {
		st = "OK"
	}

	// if you're going to persist target persist from interceptor

	// oh wait this is the measurement point for a lotttt of metrics
	if e.Client { // is there a way to not have this branch for client/server?

		// attempts started already handled

		clientAttributeOption := metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.target", "target URI used when creating gRPC channel here"), attribute.String("grpc.status", st)) // honestly could make the three a variable also make all names consts at the start of file

		// 1. grpc.client.attempt.duration (latency plumb into measurement)
		if csh.registeredMetrics.clientAttemptDuration != nil { // could read into local var for readability
			csh.registeredMetrics.clientAttemptDuration.Record(ctx, latency, clientAttributeOption) // honestly could make the three a variable
		}

		// 2. grpc.client.attempt.sent_total_compressed_message_size
		// atomic.LoadInt64(&mi.sentCompressedBytes) // plumb this into measurement, why can this race? Hedging I think is what Doug said why you gate with an atomic
		if csh.registeredMetrics.clientAttemptSentTotalCompressedMessageSize != nil {
			csh.registeredMetrics.clientAttemptSentTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.sentCompressedBytes), clientAttributeOption /*<-method, target, status*/)
		}

		// 3. grpc.client.attempt.rcvd_total_compressed_message_size
		/*var i64his metric.Int64Histogram // how to plumb in bounds?
		i64his.Record(ctx, atomic.LoadInt64(&mi.recvCompressedBytes), /*RecordOptions - def method target and status, should I write a helper for this?metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.target", "grpc target string here - target URI used when creating a gRPC Channel..."), attribute.String("grpc.status", st)))
		atomic.LoadInt64(&mi.recvCompressedBytes)*/
		if csh.registeredMetrics.clientAttemptRcvdTotalCompressedMessageSize != nil {
			csh.registeredMetrics.clientAttemptRcvdTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.recvCompressedBytes), clientAttributeOption /*method, target, status*/)
		}


		// how to persist this in a map or something?

		// call duration happens in interceptor

		// early return...only handle client stuff
		return
	}
	// also make attributes a var, or all 3
	// record it for server

	if csh.registeredMetrics.serverCallSentTotalCompressedMessageSize != nil {
		csh.registeredMetrics.serverCallSentTotalCompressedMessageSize.Record(ctx, mi.sentCompressedBytes /*method authority and status*/)
	}

	if csh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize != nil {
		csh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize.Record(ctx, mi.recvCompressedBytes /*method authority and status*/)
	}

	if csh.registeredMetrics.serverCallDuration != nil {
		csh.registeredMetrics.serverCallDuration.Record(ctx, latency /*method authority and status*/)
	}
}

func (ssh *serverStatsHandler) recordRPCData(ctx context.Context, s stats.RPCStats, mi *metricsInfo) {
	switch st := s.(type) {
	case *stats.OutHeader, *stats.InTrailer, *stats.OutTrailer:
	case *stats.InHeader:
		st.Header /*"get this and persist :authority"...per call also can you use this to record*/
		st.Client // can branch
	case *stats.Begin:
		if ssh.registeredMetrics.serverCallStarted != nil { // could read this into a local var for readability see what they say on CLs
			// persist method and authority and status from register or from interceptor is what doug said
			ssh.registeredMetrics.serverCallStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("authority", /*persisted authority from InHeader...comes after*/)))
		}
		// or move this recording point to InHeader
	case *stats.OutPayload:
		atomic.AddInt64(&mi.sentCompressedBytes, int64(st.CompressedLength))
	case *stats.InPayload:
		atomic.AddInt64(&mi.recvCompressedBytes, int64(st.CompressedLength))
	default:
		// Shouldn't happen. gRPC calls into stats handler, and will never not
		// be one of the types above.
		logger.Errorf("Received unexpected stats type (%T) with data: %v", s, s)
	}
}

type serverStatsHandler struct {
	// to - opencensus is just trace sampler and whether you disable - same thing here?
	mo MetricsOptions

	registeredMetrics registeredMetrics // pointer or not
}

func (ssh *serverStatsHandler) recordDataEnd(ctx context.Context, mi *metricsInfo, e *stats.End) {
	// latency bounds for distribution data (speced millisecond bounds) have
	// fractions, thus need a float.
	latency := float64(time.Since(mi.startTime)) / float64(time.Millisecond)
	var st string
	if e.Error != nil {
		s, _ := status.FromError(e.Error)
		st = canonicalString(s.Code())
	} else {
		st = "OK"
	}

	serverAttributeOption := metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.authority", "persisted authority here"), attribute.String("grpc.status", st))

	// need to build registered metrics for both
	if ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize != nil {
		ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize.Record(ctx, mi.sentCompressedBytes, serverAttributeOption)
	}

	if ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize != nil {
		ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize.Record(ctx, mi.recvCompressedBytes, serverAttributeOption)
	}

	if ssh.registeredMetrics.serverCallDuration != nil {
		ssh.registeredMetrics.serverCallDuration.Record(ctx, latency, serverAttributeOption)
	}
}

// TagConn exists to satisfy stats.Handler.
func (ssh *serverStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn exists to satisfy stats.Handler.
func (ssh *serverStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// TagRPC implements per RPC context management.
func (ssh *serverStatsHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {

}

// HandleRPC implements per RPC tracing and stats implementation.
func (ssh *serverStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {

}

// Regardless of the actual API - (default check if disabled) || (check if extra enabled)

// or set received from user like opencensus (but could make above a set too)

// check set - for the name that the metric is with respect too
//		record corresponding metric

// overwrite the global? main thing that OpenTelemetry provides in SDK?
