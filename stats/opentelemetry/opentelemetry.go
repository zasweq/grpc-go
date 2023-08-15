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
	// to Named Meter instances to instrument an application. If unset, no
	// metrics will be supported. The implementation knobs of this field take
	// precedence over the API calls from the field in this component.
	MeterProvider metric.MeterProvider // To enable metrics collection, set a meter provider. If unset, no metrics will be recorded. Merge this in with ^^^.

	// This is api ^^^, my api will just be a []string that defaults are exposed to users and then you can set or not...

	// Export Default Metrics...take this and build a set and you have that set part of my algorithm

	// Metrics are the metrics to instrument. Will turn on the corresponding
	// metric supported by this component if applicable.
	Metrics []string // export the default names like opencensus


}

func DialOption(mo MetricsOptions) grpc.DialOption {
	csh := &clientStatsHandler{mo: mo}
	csh.buildMetricsDataStructuresAtInitTime() // perhaps make this a constructor...that links the two together
	return joinDialOptions(grpc.WithChainUnaryInterceptor(csh.unaryInterceptor), grpc.WithStreamInterceptor(csh.streamInterceptor), grpc.WithStatsHandler(csh))
} // multiple components by grpc.DialOption, grpc.DialOption, need to add a unit test


// what does the interceptor do...?
// top level span for sure
// top level metrics stuff like retry? server side doesn't have this concept right?
func (csh *clientStatsHandler) unaryInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	// cc.Target() // this comes in at the interceptor time, but only need to set once
	// if unset set and use that the rest of time, set at beginning of client conn and use that

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

	// for target attribute:

	ci := &callInfo{ // I don't think we need this server side...client side concepts
		target: cc.Target(),
	} // put inside top level call context, read target in non locking way write timestamp with a context held in attempt layer
	// how does this with work with context keys
	ctx = setCallInfo(ctx, ci)

	// Specific context a packages key


	// so each new context can have *one* value for it
	// different attmepts will have diferent contexts so different attempt info
	// one key (from package) value for all derived contexts
	// so read that value with same key point to same memory, exactly what you want set target once and set top level timestamp for hedging
	// need a lock for hedging, without hedging seems like you won't talk to Doug about it...I think he'll require a lock grab or atomic load or something

	// so create getters and setters and I think that works due to colliding key...





	// 1. create per call data needed for traces and metrics
	// call span
	startTime := time.Now() // these are local variables/closures...should I merge this with top level call object or are these timestamps orthogonal?

	// 2. do rpc
	err := invoker(ctx, method, req, reply, cc, opts...)

	// 3. post processing (callback in streaming flow...)
	csh.perCallMetrics(ctx, err, startTime, method, cc.Target())
	return err
}

func (csh *clientStatsHandler) streamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	cc.Target() // create top level object, is mutable over the call lifespan as per Java

	// write top level object
	// toplevelobject.Time (needs to be protected with a mutex)

	// record timestamp at end of attempt
	// if set at start of next attempt (inside the attempt) writes to top level call object.
	// so read this out for target string set once and read no lock since already set, can't write to stats handlers memory because data accesses are slow...
	ci := &callInfo{
		target: cc.Target(),
	} // put inside top level call context, read target in non locking way write timestamp with a context held in attempt layer
	ctx = setCallInfo(ctx, ci)
	startTime := time.Now()
	callback := func(err error) {
		csh.perCallMetrics(ctx, err, startTime, method, cc.Target())
	}
	opts = append([]grpc.CallOption{grpc.OnFinish(callback)}, opts...)
	s, err := streamer(ctx, desc, cc, method, opts...)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (csh *clientStatsHandler) perCallMetrics(ctx context.Context, err error, startTime time.Time, method string, target string) { // oh can't do inline for reusability...
	s := status.Convert(err)
	callLatency := float64(time.Since(startTime)) / float64(time.Millisecond)
	if csh.registeredMetrics.clientCallDuration != nil {
		csh.registeredMetrics.clientCallDuration.Record(ctx, callLatency, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(method)), attribute.String("grpc.target", target), attribute.String("grpc.status", canonicalString(s.Code())))) // needs method target and status should I persist this?
	}
}


func ServerOption(mo MetricsOptions) grpc.ServerOption {
	ssh := &serverStatsHandler{mo: mo}
	ssh.buildMetricsDataStructuresAtInitTime() // or make it as part of new?...make one constructor
	return grpc.StatsHandler(&serverStatsHandler{mo: mo})
}


// In the top level context...per call level stuff

// opencensus top level span + timestamp (only call metric outside) we also will
// have retry metrics which need to persist a counter so add something to
// context at interceptor level, Doug mentioned because the Dial Option always
// has both components it's fine to assume need both to function properly, not
// an API within a component

type callInfo struct { // mutable, will be written to for timestamps for time without attempts...
	// top level span

	target string

	// Eventually for retry metrics some sort of full time - Doug mentioned communication between these two components is handled by stats handler

	//
}

type callInfoKey struct {}

func setCallInfo(ctx context.Context, ci *callInfo) context.Context {
	return context.WithValue(ctx, callInfoKey{}, ci)
}

// getCallInfo returns the callInfo stored in the context, or nil
// if there isn't one.
func getCallInfo(ctx context.Context) *callInfo { // if this errors in attempt component error out right?
	ci, _ := ctx.Value(callInfoKey{}).(*callInfo)
	return ci
}

// rename metrics info to this?
type attemptInfo struct { // created at beginning of stats handler tag, scoped to attempt, data needed per attempt and/attempt is handled
	// I think this is mi and ti
}

type clientStatsHandler struct {
	// what options/state needs to be persisted here?
	// to: opencensus is just trace sampler and whether you disable - same thing here?
	mo MetricsOptions // to avoid copy make these pointers/

	registeredMetrics *registeredMetrics // pointer or not

	target string // this only needs to be set once - put it in call info

}

// retry delay per call (A45)...through interceptor will be wrt the talking between interceptor and stats handler right...
type rpcInfo struct {
	mi *metricsInfo
}

type rpcInfoKey struct{}

func setRPCInfo(ctx context.Context, ri *rpcInfo) context.Context {
	return context.WithValue(ctx, rpcInfoKey{}, ri)
}

// getRPCInfo returns the rpcInfo stored in the context, or nil
// if there isn't one.
func getRPCInfo(ctx context.Context) *rpcInfo {
	ri, _ := ctx.Value(rpcInfoKey{}).(*rpcInfo)
	return ri
}

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
	// flow? Yash says wait to keep it compatible cross language

	// creates an attempt span though...
	ri := &rpcInfo{
		mi: mi,
	}
	return setRPCInfo(ctx, ri)
}

func (csh *clientStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	ri := getRPCInfo(ctx)
	if ri == nil {
		// Shouldn't happen because TagRPC populates this information.
		return
	}

	// gets metric info from tag rpc

	// csh.mo.MeterProvider // has access to this? to record measurements
	// and a list of disabled metrics and enabled metrics... (data passed because recordRPCData is on csh)

	// helper that switches on rpc stats type...does it populate with RPCInfo as well?
	csh.recordRPCData(ctx, rs, ri.mi)

	// disable trace bool as well?



}

func removeLeadingSlash(mn string) string {
	return strings.TrimLeft(mn, "/")
}

// info about attempt life span client side, call life span server side.
type metricsInfo struct {
	// access these counts atomically for hedging in the future:
	// number of bytes after compression (within each message) from side (client || server)
	sentCompressedBytes int64
	// number of compressed bytes received (within each message) received on
	// side (client || server)
	recvCompressedBytes int64

	startTime time.Time
	method  string
	authority string
}

type registeredMetrics struct { // nil or not nil means presence
	// "grpc.client.attempt.started"
	clientAttemptStarted metric.Int64Counter
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

func (ssh *serverStatsHandler) buildMetricsDataStructuresAtInitTime() {
	// Don't use no-op, just don't fill out any of the metrics, and if none of the
	// metrics are set then you just don't record.
	if ssh.mo.MeterProvider == nil {
		return
	}

	var setOfMetrics map[string]struct{} // pre allocate? is that really more efficient?
	for _, metric := range ssh.mo.Metrics {
		setOfMetrics[metric] = struct{}{}
	}

	registeredMetrics := &registeredMetrics{}
	// what happens if meter is nil?
	meter := ssh.mo.MeterProvider.Meter("no-op namespace name? Prevent collisions?")
	// counter name - sdk takes precedence, then api (api here is defaults overwritten by SDK)

	if _, ok := setOfMetrics["grpc.server.call.started"]; ok {
		scs, err := meter.Int64Counter("grpc.server.call.started", metric.WithUnit("call"), metric.WithDescription("The total number of RPCs started, including those that have not completed."))
		if err != nil {
			logger.Errorf("failed to register metric \"grpc.server.call.started\", will not record") // error or log?
		} else {
			registeredMetrics.serverCallStarted = scs
		}
	}

	if _, ok := setOfMetrics["grpc.server.call.sent_total_compressed_message_size"]; ok {
		ss, err := meter.Int64Histogram("grpc.server.call.sent_total_compressed_message_size", metric.WithDescription("Total bytes (compressed but not encrypted) sent across all response messages (metadata excluded) per RPC; does not include grpc or transport framing bytes."))
		if err != nil {
			logger.Errorf("failed to register metric \"grpc.server.call.sent_total_compressed_message_size\", will not record") // error or log?
		} else {
			registeredMetrics.serverCallSentTotalCompressedMessageSize = ss
		}
	}

	if _, ok := setOfMetrics["grpc.server.call.rcvd_total_compressed_message_size"]; ok {
		sr, err := meter.Int64Histogram("grpc.server.rcvd.sent_total_compressed_message_size", metric.WithDescription("Total bytes (compressed but not encrypted) received across all request messages (metadata excluded) per RPC; does not include grpc or transport framing bytes."))
		if err != nil {
			logger.Errorf("failed to register metric \"grpc.server.rcvd.sent_total_compressed_message_size\", will not record") // error or log?
		} else {
			registeredMetrics.serverCallRcvdTotalCompressedMessageSize = sr
		}
	}

	if _, ok := setOfMetrics["grpc.server.call.duration"]; ok {
		scd, err := meter.Float64Histogram("grpc.server.call.duration", metric.WithDescription("\nThis metric aims to measure the end2end time an RPC takes from the server transport’s (HTTP2/ inproc / cronet) perspective.")) // there's gotta be like with bounds (latency bucket) or something...also declare bounds above only settable at sdk level not api level which wins out anyway precedence wise anyway...otherwise default bounds
		if err != nil {
			logger.Errorf("failed to register metric \"grpc.server.call.duration\", will not record") // error or log?
		} else {
			registeredMetrics.serverCallDuration = scd
		}
	}

	ssh.registeredMetrics = registeredMetrics
}

func (csh *clientStatsHandler) buildMetricsDataStructuresAtInitTime() {
	// Don't use no-op, just don't fill out any of the metrics, and if none of the
	// metrics are set then you just don't record.
	if csh.mo.MeterProvider == nil {
		return
	}

	// []set of metrics - whether from his api or my api where you pass in directly and append
	// var setOfMetrics map[string]struct{} // Yash had this passed in, provide default metrics to user, maybe I can just say register metrics...
	var setOfMetrics map[string]struct{} // pre allocate?
	for _, metric := range csh.mo.Metrics {
		setOfMetrics[metric] = struct{}{}
	}

	registeredMetrics := &registeredMetrics{}

	meter := csh.mo.MeterProvider.Meter("no-op namespace name? Prevent collisions?"/*any options here? I don't thinkkk so...*/)

	if _, ok := setOfMetrics["grpc.client.attempt.started"]; ok {
		asc, err := meter.Int64Counter("grpc.client.attempt.started", metric.WithUnit("attempt"), metric.WithDescription("The total number of RPC attempts started, including those that have not completed."))
		if err != nil { // or should this trigger error and exit? but should be best effort?
			logger.Errorf("failed to register metric \"grpc.client.attempt.started\", will not record") // error or log?
		} else {
			registeredMetrics.clientAttemptStarted = asc
		}
	}

	if _, ok := setOfMetrics["grpc.client.attempt.duration"]; ok {
		cad, err := meter.Float64Histogram("grpc.client.attempt.duration", metric.WithDescription("End-to-end time taken to complete an RPC attempt including the time it takes to pick a subchannel."))
		if err != nil {
			logger.Errorf("failed to register metric \"grpc.client.attempt.started\", will not record") // error or log
		} else {
			registeredMetrics.clientAttemptDuration = cad
		}
	}

	// what kind of implementation do you use for the overwriting metric in tests? how do I make tests interesting

	// histogram bounds are not part of their api yet - but caller can set document this and sdk over these api calls precedence wise somewhere in this file

	if _, ok := setOfMetrics["grpc.client.attempt.sent_total_compressed_message_size"]; ok {
		cas, err := meter.Int64Histogram("grpc.client.attempt.sent_total_compressed_message_size", metric.WithDescription("Total bytes (compressed but not encrypted) sent across all request messages (metadata excluded) per RPC attempt; does not include grpc or transport framing bytes."))
		if err != nil {
			logger.Errorf("failed to register metric \"grpc.client.attempt.sent_total_compressed_message_size\", will not record") // error or log?
		} else {
			registeredMetrics.clientAttemptSentTotalCompressedMessageSize = cas
		}
	}

	if _, ok := setOfMetrics["grpc.client.attempt.rcvd_total_compressed_message_size"]; ok {
		car, err := meter.Int64Histogram("grpc.client.attempt.rcvd_total_compressed_message_size", metric.WithDescription("Total bytes (compressed but not encrypted) received across all response messages (metadata excluded) per RPC attempt; does not include grpc or transport framing bytes."))
		if err != nil {
			logger.Errorf("failed to register metric \"grpc.client.rcvd.sent_total_compressed_message_size\", will not record") // error or log?
		} else {
			registeredMetrics.clientAttemptRcvdTotalCompressedMessageSize = car
		}
	}

	// counter name - sdk takes precedence, then api (api here is defaults)
	if _, ok := setOfMetrics["grpc.client.call.duration"]; ok {
		ccs, err := meter.Float64Histogram("grpc.client.call.duration", metric.WithUnit("call"), metric.WithDescription("This metric aims to measure the end-to-end time the gRPC library takes to complete an RPC from the application’s perspective."))
		if err != nil {
			logger.Errorf("failed to register metric \"grpc.client.call.duration\", will not record") // error or log?
		} else {
			registeredMetrics.clientCallDuration = ccs
		}
	}
	csh.registeredMetrics = registeredMetrics
}

func (csh *clientStatsHandler) recordRPCData(ctx context.Context, s stats.RPCStats, mi *metricsInfo) {
	switch st := s.(type) {
	case *stats.InHeader, *stats.OutHeader, *stats.InTrailer, *stats.OutTrailer:
	case *stats.Begin:
		// read target from ctx - no lock - only sets before


		// either write target into mi or read it later again...I think the
		// former although that write and read could be racy for hedging. So this is faster operation wise.

		ci := getCallInfo(ctx) // easier to do two look ups
		if ci == nil {
			// Shouldn't happen, set by interceptor, defensive programming. Log it won't record?
			return
		}

		if csh.registeredMetrics.clientAttemptStarted != nil {
			csh.registeredMetrics.clientAttemptStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.target", ci.target))) // Add records a change to the counter...attributeset for efficiency
		}
	case *stats.OutPayload:
		atomic.AddInt64(&mi.sentCompressedBytes, int64(st.CompressedLength))
	case *stats.InPayload:
		atomic.AddInt64(&mi.recvCompressedBytes, int64(st.CompressedLength))
	case *stats.End:
		csh.recordDataEnd(ctx, mi, st)
	default:
		// Shouldn't happen. gRPC calls into stats handler, and will never not
		// be one of the types above.
		logger.Errorf("Received unexpected stats type (%T) with data: %v", s, s)
	}
}


// if authority isn't present fallback to host, if neither fallback to empty string


// top level and call level talk through context, record time no active attempt so has to fit in

func (csh *clientStatsHandler) recordDataEnd(ctx context.Context, mi *metricsInfo, e *stats.End) {
	ci := getCallInfo(ctx)
	if ci == nil {
		// Shouldn't happen, set by interceptor, defensive programming. Log it won't record?
		return
	}
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

	clientAttributeOption := metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.target", ci.target), attribute.String("grpc.status", st))
	if csh.registeredMetrics.clientAttemptDuration != nil { // could read into local var for readability
		csh.registeredMetrics.clientAttemptDuration.Record(ctx, latency, clientAttributeOption)
	}

	if csh.registeredMetrics.clientAttemptSentTotalCompressedMessageSize != nil {
		csh.registeredMetrics.clientAttemptSentTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.sentCompressedBytes), clientAttributeOption)
	}

	if csh.registeredMetrics.clientAttemptRcvdTotalCompressedMessageSize != nil {
		csh.registeredMetrics.clientAttemptRcvdTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.recvCompressedBytes), clientAttributeOption)
	}
}

// list of what to talk to Doug about:
// tag thingy that we had in OpenCensus but Yash doesn't want which arbitrarily propagates tags
// set by application across the network on the wire

// target and authority client (how I have top level info) and server side (use
// ch to record for efficiency) and efficiency

func (ssh *serverStatsHandler) recordRPCData(ctx context.Context, s stats.RPCStats, mi *metricsInfo) {
	switch st := s.(type) {
	case *stats.Begin, *stats.OutHeader, *stats.InTrailer, *stats.OutTrailer:
		// Headers and Trailers are not relevant to the measures, as the
		// measures concern number of messages and bytes for messages. This
		// aligns with flow control.
	// Should I use in header client side server call started?
	case *stats.InHeader:
		authorityHeader := st.Header.Get(":authority")
		var authority string
		// "no eventual authority header is a valid rpc" - rare but it can happen, just log an empty string.
		if len(authorityHeader) == 0 {
			authority = "" // leave the authority blank or just leave the tag off?
		} else {
			authority = authorityHeader[0]
		}
		mi.authority = authority

		// InHeader comes in 1:1 with an rpc server side, since it's required to start an RPC.
		// Eric said comes in almost at same time as start but make sure.
		if ssh.registeredMetrics.serverCallStarted != nil { // could read this into a local var for readability see what they say on CLs
			ssh.registeredMetrics.serverCallStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("authority", authority/*persisted authority from InHeader...comes after or just move recording point to in header if in header is 1:1 for an RPC and is always called?*/)))
		}
	case *stats.OutPayload:
		atomic.AddInt64(&mi.sentCompressedBytes, int64(st.CompressedLength))
	case *stats.InPayload:
		atomic.AddInt64(&mi.recvCompressedBytes, int64(st.CompressedLength))
	case *stats.End:
		ssh.recordDataEnd(ctx, mi, st)
	default:
		// Shouldn't happen. gRPC calls into stats handler, and will never not
		// be one of the types above.
		logger.Errorf("Received unexpected stats type (%T) with data: %v", s, s)
	}
}

type serverStatsHandler struct {
	mo MetricsOptions

	registeredMetrics *registeredMetrics // I don't even know
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

	serverAttributeOption := metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.authority", mi.authority), attribute.String("grpc.status", st))

	if ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize != nil {
		ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize.Record(ctx, mi.sentCompressedBytes, serverAttributeOption)
	}

	if ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize != nil {
		ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize.Record(ctx, mi.recvCompressedBytes, serverAttributeOption)
	}

	if ssh.registeredMetrics.serverCallDuration != nil {
		ssh.registeredMetrics.serverCallDuration.Record(ctx, latency, serverAttributeOption) // happens before RPC ends so good to make assertions on it.
	}
}

// TagConn exists to satisfy stats.Handler.
func (ssh *serverStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn exists to satisfy stats.Handler.
func (ssh *serverStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// TagRPC implements per RPC context management.
func (ssh *serverStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	mi := &metricsInfo{
		startTime: time.Now(),
		method: info.FullMethodName,
	}
	ri := &rpcInfo{
		mi: mi,
	}
	return setRPCInfo(ctx, ri)
}

// HandleRPC implements per RPC tracing and stats implementation.
func (ssh *serverStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	ri := getRPCInfo(ctx)
	if ri == nil {
		// Shouldn't happen because TagRPC populates this information.
		return
	}
	ssh.recordRPCData(ctx, rs, ri.mi)
}

// DefaultClientViews is the set of client views which are considered the
// minimum required to monitor client side performance.
/*var DefaultClientViews = []*view.View{
	ClientSentBytesPerRPCView,
	ClientReceivedBytesPerRPCView,
	ClientRoundtripLatencyView,
	ClientCompletedRPCsView,
	ClientStartedRPCsView,
}*/

// DefaultClientMetrics are the client metrics...
var DefaultClientMetrics = []string{
	"grpc.client.attempt.started",
	"grpc.client.attempt.duration",
	"grpc.client.attempt.sent_total_compressed_message_size",
	"grpc.client.attempt.rcvd_total_compressed_message_size",
	"grpc.client.call.duration",
}

// DefaultServerMetrics are the client metrics...
var DefaultServerMetrics = []string{
	"grpc.server.call.started",
	"grpc.server.call.sent_total_compressed_message_size",
	"grpc.server.call.rcvd_total_compressed_message_size",
	"grpc.server.call.duration",
}

// These metrics are used to register metrics

// start pulling these into different files and cleaning up
