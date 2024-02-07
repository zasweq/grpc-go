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

package opentelemetry

import (
	"context"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

// distribution bounds...here or in other package
// We keep the same definitions for the histogram buckets as the ones in the OC
// spec.

// For buckets with sizes in bytes (‘By’) they have the following boundaries: 0,
// 1024, 2048, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864,
// 268435456, 1073741824, 4294967296. Called SizeBuckets.

// For buckets with latencies in seconds (‘s’) (float64 number) they have the
// following boundaries: 0, 0.00001, 0.00005, 0.0001, 0.0003, 0.0006, 0.0008,
// 0.001, 0.002, 0.003, 0.004, 0.005, 0.006, 0.008, 0.01, 0.013, 0.016, 0.02,
// 0.025, 0.03, 0.04, 0.05, 0.065, 0.08, 0.1, 0.13, 0.16, 0.2, 0.25, 0.3, 0.4,
// 0.5, 0.65, 0.8, 1, 2, 5, 10, 20, 50, 100. Called LatencyBuckets.

// Note that the OTel API does not currently provide the ability to add in
// boundaries to the instrument, but the new iteration on the API makes it
// possible to give ’advice’ on the boundaries.

// Advice in API, needs to be set at implementation.


type clientStatsHandler struct {
	mo MetricsOptions

	registeredMetrics registeredMetrics
}


func (csh *clientStatsHandler) buildMetricsDataStructuresAtInitTime() {
	// Don't use no-op, just don't fill out any of the metrics, and if none of the
	// metrics are set then you just don't record.
	if csh.mo.MeterProvider == nil {
		return
	}

	meter := csh.mo.MeterProvider.Meter("no-op namespace name? Prevent collisions?"/*any options here? I don't thinkkk so...*/)
	if meter == nil {
		return
	}

	setOfMetrics := make(map[string]struct{}) // pre allocate length?
	for _, metric := range csh.mo.Metrics {
		setOfMetrics[metric] = struct{}{}
	}

	registeredMetrics := registeredMetrics{}


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
			registeredMetrics.clientAttemptRcvdTotalCompressedMessageSize = car // labels injected into otel instantiation at otel build time for labels. For each label wanted.
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
// "Change the generated code" - regenerate the proto files (and upgrade gRPC version). Do I need to change proto compiler?
// bool or all registered services

// when Invoke and by in large once this interceptor hits, all it gets is a method string.

// however, this string passed down also needs to be bucketed into (part of
// registered methods vs. not part of registered methods)

func (csh *clientStatsHandler) unaryInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	ci := &callInfo{ // I don't think we need this server side...client side concepts. Server data scoped to context is implicitly call. Maybe call it client call info?
		target: cc.Target(),
	} // put inside top level call context, read target in non locking way write timestamp with a context held in attempt layer
	// how does this with work with context keys (one key value pair *per context*)

	// for opts in call options
	//   if call options contains is registered
	//   set a bit scoped to the call that records the method name...otherwise records as other...scale up call option and use this for all metrics

	// Implementations should provide the option to override this behavior to allow recording generic method names as well.

	// so yeah can use that for generic method names, proxy sets this option (without generated stub...)

	// "To prevent this, unregistered/generic method names should by default be
	// reported with "other" value instead. Implementations should provide the
	// option to override this behavior to allow recording generic method names
	// as well."

	// so yeah scoped to metrics module only...yeah scoped to metrics attribute
	// only so this seems right


	ctx = setCallInfo(ctx, ci)

	// Specific context a packages key


	// so each new context can have *one* value for it
	// different attmepts will have diferent contexts so different attempt info
	// one key (from package) value for all derived contexts
	// so read that value with same key point to same memory, exactly what you want set target once and set top level timestamp for hedging
	// need a lock for hedging, without hedging seems like you won't talk to Doug about it...I think he'll require a lock grab or atomic load or something
	// I think hedging is all to the same target though. It's the race between events and RPC end not all hedging.

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
	// write top level object (for retry metrics)
	// toplevelobject.Time (needs to be protected with a mutex)

	// record timestamp at end of attempt
	// if set at start of next attempt (inside the attempt) writes to top level call object.
	ci := &callInfo{
		target: cc.Target(),
	}
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

func (csh *clientStatsHandler) perCallMetrics(ctx context.Context, err error, startTime time.Time, method string, target string) {
	s := status.Convert(err)
	callLatency := float64(time.Since(startTime)) / float64(time.Millisecond)
	if csh.registeredMetrics.clientCallDuration != nil {
		csh.registeredMetrics.clientCallDuration.Record(ctx, callLatency, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(method)), attribute.String("grpc.target", target), attribute.String("grpc.status", canonicalString(s.Code())))) // needs method target and status should I persist this?
	}
}

// TagConn exists to satisfy stats.Handler.
func (csh *clientStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn exists to satisfy stats.Handler.
func (csh *clientStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// TagRPC implements per RPC attempt context management.
func (csh *clientStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	mi := &metricsInfo{ // populates information about RPC start.
		startTime: time.Now(),
		method: info.FullMethodName,
	}
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

	csh.processRPCEvent(ctx, rs, ri.mi) // maybe change these method name plumbing.
}

func (csh *clientStatsHandler) processRPCEvent(ctx context.Context, s stats.RPCStats, mi *metricsInfo) {
	switch st := s.(type) {
	case *stats.InHeader, *stats.OutHeader, *stats.InTrailer, *stats.OutTrailer:
	case *stats.Begin:
		ci := getCallInfo(ctx)
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
		csh.processRPCEnd(ctx, mi, st)
	default:
		// Shouldn't happen. gRPC calls into stats handler, and will never not
		// be one of the types above.
		logger.Errorf("Received unexpected stats type (%T) with data: %v", s, s)
	}
}

func (csh *clientStatsHandler) processRPCEnd(ctx context.Context, mi *metricsInfo, e *stats.End) {
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
		// record unconditionally
		csh.registeredMetrics.clientAttemptSentTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.sentCompressedBytes), clientAttributeOption)
	}

	if csh.registeredMetrics.clientAttemptRcvdTotalCompressedMessageSize != nil {
		csh.registeredMetrics.clientAttemptRcvdTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.recvCompressedBytes), clientAttributeOption)
	}
} // also need extra labels configured at instantation and logic for trying to pull out of call info for non started rpc metrics on the call

// DefaultClientMetrics are the default? client metrics provided by this instrumentation code?
var DefaultClientMetrics = []string{
	"grpc.client.attempt.started",
	"grpc.client.attempt.duration",
	"grpc.client.attempt.sent_total_compressed_message_size",
	"grpc.client.attempt.rcvd_total_compressed_message_size",
	"grpc.client.call.duration",
}

// get it to the point where I can go run it and see metrics emissions - maybe
// rebasing will have fixed bug of not seeing last 3/ new otel mod

// peer.Peer add (triage if Java/c++ does it like this)
// or stick it in our context


// start pulling these into different files and cleaning up*** :)
// I'll have to go get...should I just rebase >>>
// is registered method server side is merged...I think I can test that now...

// I will have to update go.mods in this case...last three weren't getting hit
