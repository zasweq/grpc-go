/*
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
 */

package opentelemetry

import (
	"context"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	otelinternal "google.golang.org/grpc/stats/opentelemetry/internal"
	"google.golang.org/grpc/status"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type serverStatsHandler struct {
	o Options // I could put it in this thing, then users can't set it, the call into csm/ exposed to users sets this...

	pluginOption otelinternal.PluginOption

	serverMetrics serverMetrics
}

// Just like I added join dial options on client side, join server options for all this to work...
// wrap a transport stream or something...

/*
so in the interceptor, if the handler returns and headers haven't been set/sent
and no message has been sent, SetTrailer()
*/

// need to join the interceptors with stats handlers...
func (ssh *serverStatsHandler) unaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	// Somehow wrap the unary interceptor stream...I think the same object because same logic on operations...

	// where is the transport stream? in info? in handler? same mechanics wrt operations?

	grpc.ServerTransportStream()

	grpc.NewContextWithServerTransportStream()

}

// so a no-op interceptor if not CSM...how to make this determination bit *in OTel*?

// attachLabelsStream wraps around the embedded grpc.ServerStream, and intercepts
// the SetHeader/SendHeader/SendMsg/SendTrailer call to attach metadata exchange labels.
type attachLabelsStream struct {
	grpc.ServerStream
	// grpc.ServerTransportStream // has no SendMsg, I guess happens in the operation of headers...I need to do the underlying plugin option first looks like this might not be solution

	// Defaults to false...
	attachedLabels         atomic.Bool // accessed atomically (or could I use atomic bool type?)
	metadataExchangeLabels metadata.MD // written to when unary/streaming create this persisted from constructor?
} // can I trace sync. requirements to what needs bool write or not?

func (ald *attachLabelsStream) SetHeader(md metadata.MD) error { // you could get two that call this...
	// just append, it can't call itself, I think append just once, if another
	// comes in sets both so doesn't matter ordering... one of them that will
	// eventually get merged will happen, other operations can't come in concurrently
	// anyway so you're good here...can't have two merged
	if sentMD := ald.attachedLabels.Swap(true); !sentMD { // Should this be renamed "attachedMD"?
		val := ald.metadataExchangeLabels.Get("x-envoy-peer-metadata")
		md.Append("x-envoy-peer-metadata", val...)
	}


	return ald.SetHeader(md)
} // Figure out how to merge with outgoing context...

// I think I just append to md, how do I test different scenarios wrt this trailers only logic have the stream server side need to do something with it...

// could return just the string from plugin option...

// pointer, append and create a new one?
func (ald *attachLabelsStream) SendHeader(md metadata.MD) error {
	if attachedLabels := ald.attachedLabels.Swap(true); !attachedLabels { // I think this is fien no need to set header in this case...
		val := ald.metadataExchangeLabels.Get("x-envoy-peer-metadata")
		md.Append("x-envoy-peer-metadata", val...)
	}

	return ald.ServerStream.SendHeader(md) // does the synchronous guarantees get lost somewhere here?
} // error in certain orderings, a state space of validity

// write bool before any operations? race conditions...?
func (ald *attachLabelsStream) SendMsg(m any) error {
	if attachedLabels := ald.attachedLabels.Swap(true); !attachedLabels {
		ald.ServerStream.SetHeader(ald.metadataExchangeLabels)
	}
	ald.ServerStream.SendMsg(m)
}

func newAttachMDStream(ss grpc.ServerStream, md metadata.MD) *attachLabelsStream { // I think this is all the logic, now to figure out how to get a ref and also...do it for unary? some transport stream?
	return &attachLabelsStream{
		ServerStream: ss,
		// Maybe it doesn't take it, returns a new md object which gets merged with md from application
		metadataExchangeLabels: md/*csm plugin.NewLabelsMD()*/, // should these getters be global?, how do I get this ref?
	}
}

func (ssh *serverStatsHandler) streamInterceptor(srv any, ss grpc.ServerStream, ssi *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	// Do all operations needed while also wrapping the stream...
	amd := newAttachMDStream(ss, /*csm plugin.NewLabelsMD()*/)
	err := handler(srv, amd)
	if err != nil {
		// logger("RPC failed with error: %v", err)
	}
	// I attach it irrespective of failing RPC...if it fails before hitting stream interceptor that's fine, so this needs to do it for any error...which I think this is...
	// amd.sentMD // atomcially load this...
	// should this also be conditional on err != nil...I don't think so
	if !amd.attachedLabels.Load() { // handler responds, headers/messages haven't been set/sent...setTrailer...
		/*
			"the trailers aren't sent though
			they're sent when the handler returns"
		*/
		amd.SetTrailer(amd.metadataExchangeLabels) // will get written to *after* this handler returns...
	}
	return err
}

func (ssh *serverStatsHandler) initializeMetrics() {
	// Will set no metrics to record, logically making this stats handler a
	// no-op.
	if ssh.o.MetricsOptions.MeterProvider == nil {
		return
	}

	meter := ssh.o.MetricsOptions.MeterProvider.Meter("grpc-go " + grpc.Version)
	if meter == nil {
		return
	}
	setOfMetrics := ssh.o.MetricsOptions.Metrics.metrics

	ssh.serverMetrics.callStarted = createInt64Counter(setOfMetrics, "grpc.server.call.started", meter, metric.WithUnit("call"), metric.WithDescription("Number of server calls started."))
	ssh.serverMetrics.callSentTotalCompressedMessageSize = createInt64Histogram(setOfMetrics, "grpc.server.call.sent_total_compressed_message_size", meter, metric.WithUnit("By"), metric.WithDescription("Compressed message bytes sent per server call."), metric.WithExplicitBucketBoundaries(DefaultSizeBounds...))
	ssh.serverMetrics.callRcvdTotalCompressedMessageSize = createInt64Histogram(setOfMetrics, "grpc.server.call.rcvd_total_compressed_message_size", meter, metric.WithUnit("By"), metric.WithDescription("Compressed message bytes received per server call."), metric.WithExplicitBucketBoundaries(DefaultSizeBounds...))
	ssh.serverMetrics.callDuration = createFloat64Histogram(setOfMetrics, "grpc.server.call.duration", meter, metric.WithUnit("s"), metric.WithDescription("End-to-end time taken to complete a call from server transport's perspective."), metric.WithExplicitBucketBoundaries(DefaultLatencyBounds...))
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
	if ssh.o.MetricsOptions.MethodAttributeFilter != nil {
		if !ssh.o.MetricsOptions.MethodAttributeFilter(method) {
			method = "other"
		}
	}
	server := internal.ServerFromContext.(func(context.Context) *grpc.Server)(ctx)
	if server == nil { // Shouldn't happen, defensive programming.
		logger.Error("ctx passed into server side stats handler has no grpc server ref")
		method = "other"
	} else {
		isRegisteredMethod := internal.IsRegisteredMethod.(func(*grpc.Server, string) bool)
		if !isRegisteredMethod(server, method) {
			method = "other"
		}
	}

	mi := &metricsInfo{
		startTime: time.Now(),
		method:    removeLeadingSlash(method),
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
		logger.Error("ctx passed into server side stats handler metrics event handling has no server call data present")
		return
	}
	ssh.processRPCData(ctx, rs, ri.mi)
}

// How to configure this? (i.e. plumb the plugin option)

func (ssh *serverStatsHandler) processRPCData(ctx context.Context, s stats.RPCStats, mi *metricsInfo) {
	switch st := s.(type) {
	case *stats.InHeader:
		ssh.serverMetrics.callStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("grpc.method", mi.method)))
	// Only read the first headers?
		if labelsReceived := mi.labelsReceived.Swap(true); !labelsReceived { // this might not even need to be atomic...ins and outs, wb for hedging?
			mi.labels = ssh.pluginOption.GetLabels(st.Header, nil) // no xDS Labels server side
		}
	case *stats.OutPayload:
		atomic.AddInt64(&mi.sentCompressedBytes, int64(st.CompressedLength))
	case *stats.InPayload:
		atomic.AddInt64(&mi.recvCompressedBytes, int64(st.CompressedLength))
	case *stats.End:
		ssh.processRPCEnd(ctx, mi, st)
	default:
	}
}

func (ssh *serverStatsHandler) processRPCEnd(ctx context.Context, mi *metricsInfo, e *stats.End) {
	latency := float64(time.Since(mi.startTime)) / float64(time.Second)
	st := "OK"
	if e.Error != nil {
		s, _ := status.FromError(e.Error)
		st = canonicalString(s.Code())
	}
	attributes := []attribute.KeyValue{
		attribute.String("grpc.method", mi.method),
		attribute.String("grpc.status", st),
	}
	for k, v := range mi.labels { // How do I even test this?
		attributes = append(attributes, attribute.String(k, v))
	}
	serverAttributeOption := metric.WithAttributes(attributes...)
	ssh.serverMetrics.callDuration.Record(ctx, latency, serverAttributeOption)
	ssh.serverMetrics.callSentTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.sentCompressedBytes), serverAttributeOption)
	ssh.serverMetrics.callRcvdTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.recvCompressedBytes), serverAttributeOption)
} // wrapping stream server side...once you mechanically wrap from client it's same operations...
// testing, and also how to configure it

// csm: configureOTElWithCSM(OTelOptions) {
//     wrap options by writing to unexported field? Or does it need to export to this package just an internal interface?
// }


const (
	// ServerCallStarted is the number of server calls started.
	ServerCallStarted Metric = "grpc.server.call.started"
	// ServerCallSentCompressedTotalMessageSize is the compressed message bytes
	// sent per server call.
	ServerCallSentCompressedTotalMessageSize Metric = "grpc.server.call.sent_total_compressed_message_size"
	// ServerCallRcvdCompressedTotalMessageSize is the compressed message bytes
	// received per server call.
	ServerCallRcvdCompressedTotalMessageSize Metric = "grpc.server.call.rcvd_total_compressed_message_size"
	// ServerCallDuration is the end-to-end time taken to complete a call from
	// server transport's perspective.
	ServerCallDuration Metric = "grpc.server.call.duration"
)
