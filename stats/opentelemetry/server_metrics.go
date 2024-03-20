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
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

var (
// byte distribution with boundaries
// 0, 1024, 2048, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864, 268435456, 1073741824, 4294967296

// second distribution (for latency) with boundaries
// 0, 0.00001, 0.00005, 0.0001, 0.0003, 0.0006, 0.0008, 0.001, 0.002, 0.003, 0.004, 0.005, 0.006, 0.008, 0.01, 0.013, 0.016, 0.02, 0.025, 0.03, 0.04, 0.05, 0.065, 0.08, 0.1, 0.13, 0.16, 0.2, 0.25, 0.3, 0.4, 0.5, 0.65, 0.8, 1, 2, 5, 10, 20, 50, 100

// count distribution
// 0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536.

// Set in o11y plugins ^^^
)

type serverStatsHandler struct {
	mo MetricsOptions

	registeredMetrics registeredMetrics
}


func (ssh *serverStatsHandler) buildMetricsDataStructuresAtInitTime() {
	// Don't use no-op, just don't fill out any of the metrics, and if none of the
	// metrics are set then you just don't record.
	if ssh.mo.MeterProvider == nil {
		return
	}

	// what happens if meter is nil?
	meter := ssh.mo.MeterProvider.Meter("no-op namespace name? Prevent collisions?")
	if meter == nil {
		return
	}
	setOfMetrics := make(map[string]struct{}) // pre allocate length?
	for _, metric := range ssh.mo.Metrics {
		setOfMetrics[metric] = struct{}{}
	}

	registeredMetrics := registeredMetrics{}


	// counter name - sdk takes precedence, then api (api here is defaults overwritten by SDK)

	if _, ok := setOfMetrics["grpc.server.call.started"]; ok {
		scs, err := meter.Int64Counter("grpc.server.call.started", metric.WithUnit("call"), metric.WithDescription("The total number of RPCs started, including those that have not completed."))
		if err != nil {
			logger.Errorf("failed to register metric \"grpc.server.call.started\", will not record") // error or log?
		} else {
			registeredMetrics.serverCallStarted = scs // if this is nil then it just doesn't record - no harm done
		}
	}

	if _, ok := setOfMetrics["grpc.server.call.sent_total_compressed_message_size"]; ok {
		print("creating compressed message size")
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

// TagConn exists to satisfy stats.Handler.
func (ssh *serverStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn exists to satisfy stats.Handler.
func (ssh *serverStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// TagRPC implements per RPC context management.
func (ssh *serverStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	method := info.FullMethodName
	if ssh.mo.MethodAttributeFilter != nil {
		if !ssh.mo.MethodAttributeFilter(method) {
			method = "other"
		}
	}
	server := internal.ServerFromContext.(func(context.Context) *grpc.Server)(ctx)
	if server == nil { // Shouldn't happen, defensive programming.
		method = "other"
	} else {
		isRegisteredMethod := internal.IsRegisteredMethod.(func(*grpc.Server, string) bool)
		if !isRegisteredMethod(server, method) {
			method = "other"
		}
	}

	mi := &metricsInfo{
		startTime: time.Now(),
		method: method,
	}
	ri := &rpcInfo{
		mi: mi,
	}
	return setRPCInfo(ctx, ri)
}

// HandleRPC implements per RPC tracing and stats implementation.
func (ssh *serverStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	ri := getRPCInfo(ctx) // you do need this though and this is how OpenCensus does it...
	if ri == nil {
		// Shouldn't happen because TagRPC populates this information.
		print("ri == nil")
		return
	} // is it not getting the right data?
	ssh.processRPCData(ctx, rs, ri.mi)
}

func (ssh *serverStatsHandler) processRPCData(ctx context.Context, s stats.RPCStats, mi *metricsInfo) {
	switch st := s.(type) {
	case *stats.Begin, *stats.OutHeader, *stats.InTrailer:
		// Headers and Trailers are not relevant to the measures, as the
		// measures concern number of messages and bytes for messages. This
		// aligns with flow control.
	case *stats.OutTrailer:
		print("trailer received")
	case *stats.InHeader:
		/*authorityHeader := st.Header.Get(":authority")
		var authority string
		// "no eventual authority header is a valid rpc" - rare but it can happen, just log an empty string.
		if len(authorityHeader) == 0 {
			authority = ""
		} else {
			authority = authorityHeader[0]
		}
		mi.authority = authority // for metrics info scope it to the metrics info for efficiency purposes
		print("checking if server call started is nil")*/
		if ssh.registeredMetrics.serverCallStarted != nil { // could read this into a local var for readability see what they say on CLs
			print("recording server call started")
			ssh.registeredMetrics.serverCallStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method))/*, attribute.String("authority", authority/*persisted authority from InHeader...comes after or just move recording point to in header if in header is 1:1 for an RPC and is always called?*)*/))
		}
	case *stats.OutPayload:
		atomic.AddInt64(&mi.sentCompressedBytes, int64(st.CompressedLength))
	case *stats.InPayload:
		atomic.AddInt64(&mi.recvCompressedBytes, int64(st.CompressedLength))
	case *stats.End:
		ssh.processRPCEnd(ctx, mi, st)
	default:
		// Shouldn't happen. gRPC calls into stats handler, and will never not
		// be one of the types above.
		logger.Errorf("Received unexpected stats type (%T) with data: %v", s, s)
	}
}


func (ssh *serverStatsHandler) processRPCEnd(ctx context.Context, mi *metricsInfo, e *stats.End) {
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
	// if method in []string (persisted on the ssh) removeLeadingSlash(mi.method))
	// else generic?
	serverAttributeOption := metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), /*attribute.String("grpc.authority", mi.authority),*/ attribute.String("grpc.status", st))

	if ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize != nil {
		print("recording server side sent compressed message size ", atomic.LoadInt64(&mi.sentCompressedBytes))
		// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
		// called in a defer func server side - I think async because doesn't happen before RPC ends...event loops/processing
		ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.sentCompressedBytes), serverAttributeOption)
	}

	if ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize != nil {
		print("recording server side rcvd compressed message size ", atomic.LoadInt64(&mi.recvCompressedBytes))
		// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
		ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.recvCompressedBytes), serverAttributeOption)
	}

	if ssh.registeredMetrics.serverCallDuration != nil {
		// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
		ssh.registeredMetrics.serverCallDuration.Record(ctx, latency, serverAttributeOption) // happens before RPC ends so good to make assertions on it.
	}
}

// DefaultServerMetrics are the default? server metrics provided by this instrumentation code?
var DefaultServerMetrics = []string{
	"grpc.server.call.started",
	"grpc.server.call.sent_total_compressed_message_size",
	"grpc.server.call.rcvd_total_compressed_message_size",
	"grpc.server.call.duration",
}

/*
* Talk to Doug about authority thing and also []registered methods and passing it through call object in context scoped to call
* I don't know if client conn has access to []registered methods, if not need to also figure that out.

* Fix this bug of server side stats.End handling. It's being recorded on meter, but not plumbed through Collect()
* Make non deterministic assertions (I think part of same test code would be good)
* Cleanup PR
* Yashes design work
* Write design doc for this?

* Observability presentation
* Bootstrap generator testing
* Sell stock tmrw to avoid a debacle

* make sure canonical (dns) effective target on Target()...ParseTarget and find resolver default string + :// cc target. overwrite the old value
behavior change for target

method filtering i.e. registered or not

server side: through context - either server -> transport -> csh.  (or server -> csh) directly
for *server (would require callouts only in server). Because *server has access
to the registered methods. So server should own code
of determining it's a registered method or not.


client side: register when you wrap? Generated new foo client? call client conn - registering now - rpc method handler
pass dial option, interceptor pulls out of dial option (specifying RPCs came through stubs - bool stub vs. non stub (cardinality issue not issue) he likes it per call
*/


// Did anything change? Obv stats handler callouts,
// security vulnerability still there? Bounds still set by user right?
// also the labels to record changed but I think I've already taken that into account...

// go get the new module so maybe just do a whole new thing...with new otel dependency...
