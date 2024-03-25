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

type serverStatsHandler struct {
	mo MetricsOptions

	registeredMetrics registeredMetrics
}


func (ssh *serverStatsHandler) initializeMetrics() {
	// Don't use no-op, just don't fill out any of the metrics, and if none of the
	// metrics are set then you just don't record.
	if ssh.mo.MeterProvider == nil {
		return
	}

	// what happens if meter is nil?
	meter := ssh.mo.MeterProvider.Meter("no-op")
	if meter == nil {
		return
	}
	setOfMetrics := ssh.mo.Metrics.metrics

	registeredMetrics := registeredMetrics{}


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
		print("creating compressed message size")
		ss, err := meter.Int64Histogram("grpc.server.call.sent_total_compressed_message_size", metric.WithUnit("By"), metric.WithDescription("Total bytes (compressed but not encrypted) sent across all response messages (metadata excluded) per RPC; does not include grpc or transport framing bytes."))
		if err != nil {
			logger.Errorf("failed to register metric \"grpc.server.call.sent_total_compressed_message_size\", will not record") // error or log?
		} else {
			registeredMetrics.serverCallSentTotalCompressedMessageSize = ss
		}
	}

	if _, ok := setOfMetrics["grpc.server.call.rcvd_total_compressed_message_size"]; ok {
		sr, err := meter.Int64Histogram("grpc.server.call.rcvd_total_compressed_message_size", metric.WithUnit("By"), metric.WithDescription("Total bytes (compressed but not encrypted) received across all request messages (metadata excluded) per RPC; does not include grpc or transport framing bytes."))
		if err != nil {
			logger.Errorf("failed to register metric \"grpc.server.rcvd.sent_total_compressed_message_size\", will not record") // error or log?
		} else {
			registeredMetrics.serverCallRcvdTotalCompressedMessageSize = sr
		}
	}

	if _, ok := setOfMetrics["grpc.server.call.duration"]; ok {
		scd, err := meter.Float64Histogram("grpc.server.call.duration", metric.WithUnit("s"), metric.WithDescription("This metric aims to measure the end2end time an RPC takes from the server transport’s (HTTP2/ inproc / cronet) perspective.")) // there's gotta be like with bounds (latency bucket) or something...also declare bounds above only settable at sdk level not api level which wins out anyway precedence wise anyway...otherwise default bounds
		if err != nil {
			logger.Errorf("failed to register metric \"grpc.server.call.duration\", will not record") // error or log?
		} else {
			registeredMetrics.serverCallDuration = scd
		}
	}

	print("writing to registered metrics")
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
	ri := getRPCInfo(ctx)
	if ri == nil {
		// Shouldn't happen because TagRPC populates this information.
		print("ri == nil")
		return
	} // is it not getting the right data?
	ssh.processRPCData(ctx, rs, ri.mi)
}

func (ssh *serverStatsHandler) processRPCData(ctx context.Context, s stats.RPCStats, mi *metricsInfo) {
	switch st := s.(type) {
	case *stats.Begin, *stats.OutHeader, *stats.InTrailer/*, *stats.OutTrailer*/:
		// Headers and Trailers are not relevant to the measures, as the
		// measures concern number of messages and bytes for messages. This
		// aligns with flow control.
	case *stats.OutTrailer:
		print("trailer received")
		/*latency := float64(time.Since(mi.startTime)) / float64(time.Millisecond)
		sta := "OK" // it's the same server goroutine, just async with the test. Even sleeping doesn't do anything
		serverAttributeOption := metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.status", sta))

		// 57 and 0, is 0 correct here? yeah makes sense no message sent on stream, as client side
		if ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize != nil {
			// I see it being recorded, but it doesn't make it's way to metric reader...why? all the other metrics work
			print("recording server side sent compressed message size: ", atomic.LoadInt64(&mi.sentCompressedBytes), "\n") // calls out with End, but doesn't actually record...
			// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
			// called in a defer func server side - I think async because doesn't happen before RPC ends...event loops/processing...but should def eventually hit just defered is it even async? put end on the server transport
			ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.sentCompressedBytes), serverAttributeOption)
		}

		if ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize != nil {
			print("recording server side rcvd compressed message size: ", atomic.LoadInt64(&mi.recvCompressedBytes), "\n")
			// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
			ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.recvCompressedBytes), serverAttributeOption)
		} // cleanup of meter provider before these three? noop perhaps?

		if ssh.registeredMetrics.serverCallDuration != nil {
			print("recording server side call duration: ", latency, "\n")
			// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
			ssh.registeredMetrics.serverCallDuration.Record(ctx, latency, serverAttributeOption) // happens before RPC ends so good to make assertions on it.
		}*/
	case *stats.InHeader:
		if ssh.registeredMetrics.serverCallStarted != nil { // could read this into a local var for readability see what they say on CLs
			print("recording server call started")
			ssh.registeredMetrics.serverCallStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method))))
		}

		/*serverAttributeOption := metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)))

		// records these three data point, does not pick up on data point below, whether unique or addition to histogram

		// 57 and 0, is 0 correct here? yeah makes sense no message sent on stream, as client side
		if ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize != nil {
			// I see it being recorded, but it doesn't make it's way to metric reader...why? all the other metrics work
			print("recording server side sent compressed message size ", atomic.LoadInt64(&mi.sentCompressedBytes)) // calls out with End, but doesn't actually record...
			// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
			// called in a defer func server side - I think async because doesn't happen before RPC ends...event loops/processing...but should def eventually hit just defered is it even async? put end on the server transport
			ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.sentCompressedBytes), serverAttributeOption)
		}

		if ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize != nil {
			print("recording server side rcvd compressed message size ", atomic.LoadInt64(&mi.recvCompressedBytes))
			// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
			ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.recvCompressedBytes), serverAttributeOption)
		} // cleanup of meter provider before these three? noop perhaps?

		if ssh.registeredMetrics.serverCallDuration != nil {
			print("recording server side call duration ", 1)
			// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
			ssh.registeredMetrics.serverCallDuration.Record(ctx, 1, serverAttributeOption) // happens before RPC ends so good to make assertions on it.
		} // shows up in this. if this hits, how many show up*/

	case *stats.OutPayload:
		atomic.AddInt64(&mi.sentCompressedBytes, int64(st.CompressedLength))
	case *stats.InPayload:
		atomic.AddInt64(&mi.recvCompressedBytes, int64(st.CompressedLength))
	case *stats.End: // how are none of these getting observed? even after sleep so not that
		print("received stats.End, calling processRPCEnd") // even if it hits here, doesn't record

		// extra, see if this hits nope
		/*if ssh.registeredMetrics.serverCallStarted != nil { // could read this into a local var for readability see what they say on CLs
			print("recording server call started in End")
			ssh.registeredMetrics.serverCallStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method))))
		}*/
		// extra, see if this hits

		/*latency := float64(time.Since(mi.startTime)) / float64(time.Millisecond)
		var sta string
		if st.Error != nil {
			s, _ := status.FromError(st.Error)
			sta = canonicalString(s.Code())
		} else {
			sta = "OK"
		}
		serverAttributeOption := metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.status", sta))

		// 57 and 0, is 0 correct here? yeah makes sense no message sent on stream, as client side
		if ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize != nil {
			// I see it being recorded, but it doesn't make it's way to metric reader...why? all the other metrics work
			print("recording server side sent compressed message size ", atomic.LoadInt64(&mi.sentCompressedBytes)) // calls out with End, but doesn't actually record...
			// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
			// called in a defer func server side - I think async because doesn't happen before RPC ends...event loops/processing...but should def eventually hit just defered is it even async? put end on the server transport
			ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.sentCompressedBytes), serverAttributeOption)
		}

		if ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize != nil {
			print("recording server side rcvd compressed message size ", atomic.LoadInt64(&mi.recvCompressedBytes))
			// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
			ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.recvCompressedBytes), serverAttributeOption)
		} // cleanup of meter provider before these three? noop perhaps?

		if ssh.registeredMetrics.serverCallDuration != nil {
			print("recording server side call duration ", latency)
			// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
			ssh.registeredMetrics.serverCallDuration.Record(ctx, latency, serverAttributeOption) // happens before RPC ends so good to make assertions on it.
		}*/

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
	} // it's the same server goroutine, just async with the test. Even sleeping doesn't do anything
	serverAttributeOption := metric.WithAttributes(attribute.String("grpc.method", removeLeadingSlash(mi.method)), attribute.String("grpc.status", st))

	// 57 and 0, is 0 correct here? yeah makes sense no message sent on stream, as client side
	if ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize != nil {
		// I see it being recorded, but it doesn't make it's way to metric reader...why? all the other metrics work
		print("recording server side sent compressed message size: ", atomic.LoadInt64(&mi.sentCompressedBytes), "\n") // calls out with End, but doesn't actually record...
		// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
		// called in a defer func server side - I think async because doesn't happen before RPC ends...event loops/processing...but should def eventually hit just defered is it even async? put end on the server transport
		ssh.registeredMetrics.serverCallSentTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.sentCompressedBytes), serverAttributeOption)
	}

	if ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize != nil {
		print("recording server side rcvd compressed message size: ", atomic.LoadInt64(&mi.recvCompressedBytes), "\n")
		// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
		ssh.registeredMetrics.serverCallRcvdTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.recvCompressedBytes), serverAttributeOption)
	} // cleanup of meter provider before these three? noop perhaps?

	if ssh.registeredMetrics.serverCallDuration != nil {
		print("recording server side call duration: ", latency, "\n")
		// this is being called, but it's not showing up in the recording point of Collect. Somewhere it's lost.
		ssh.registeredMetrics.serverCallDuration.Record(ctx, latency, serverAttributeOption) // happens before RPC ends so good to make assertions on it.
	}
}

// DefaultServerMetrics are the default server metrics provided by this module.
var DefaultServerMetrics = Metrics{
	metrics: map[string]bool{
		"grpc.server.call.started": true,
		"grpc.server.call.sent_total_compressed_message_size": true,
		"grpc.server.call.rcvd_total_compressed_message_size": true,
		"grpc.server.call.duration": true,
	},
}