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
	estats "google.golang.org/grpc/experimental/stats"
	einstrumentregistry "google.golang.org/grpc/experimental/stats/instrumentregistry"
	"google.golang.org/grpc/internal/stats/instrumentregistry"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	otelattribute "go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

type serverStatsHandler struct {
	options Options

	serverMetrics serverMetrics
}

func (h *serverStatsHandler) initializeMetrics() {
	// Will set no metrics to record, logically making this stats handler a
	// no-op.
	if h.options.MetricsOptions.MeterProvider == nil {
		return
	}

	meter := h.options.MetricsOptions.MeterProvider.Meter("grpc-go", otelmetric.WithInstrumentationVersion(grpc.Version))
	if meter == nil {
		return
	}
	metrics := h.options.MetricsOptions.Metrics
	if metrics == nil {
		metrics = DefaultMetrics // switch this to a runtime call - alongside thing exposed to users...
	}

	h.serverMetrics.callStarted = createInt64Counter(metrics.metrics, "grpc.server.call.started", meter, otelmetric.WithUnit("call"), otelmetric.WithDescription("Number of server calls started."))
	h.serverMetrics.callSentTotalCompressedMessageSize = createInt64Histogram(metrics.metrics, "grpc.server.call.sent_total_compressed_message_size", meter, otelmetric.WithUnit("By"), otelmetric.WithDescription("Compressed message bytes sent per server call."), otelmetric.WithExplicitBucketBoundaries(DefaultSizeBounds...))
	h.serverMetrics.callRcvdTotalCompressedMessageSize = createInt64Histogram(metrics.metrics, "grpc.server.call.rcvd_total_compressed_message_size", meter, otelmetric.WithUnit("By"), otelmetric.WithDescription("Compressed message bytes received per server call."), otelmetric.WithExplicitBucketBoundaries(DefaultSizeBounds...))
	h.serverMetrics.callDuration = createFloat64Histogram(metrics.metrics, "grpc.server.call.duration", meter, otelmetric.WithUnit("s"), otelmetric.WithDescription("End-to-end time taken to complete a call from server transport's perspective."), otelmetric.WithExplicitBucketBoundaries(DefaultLatencyBounds...))

	// Non per call metrics:
	for _, inst := range instrumentregistry.Int64CountInsts {
		ic := createInt64Counter(metrics.metrics, stats.Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.serverMetrics.intCounts = append(h.serverMetrics.intCounts, ic)
	}
	for _, inst := range instrumentregistry.Float64CountInsts {
		fc := createFloat64Counter(metrics.metrics, stats.Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.serverMetrics.floatCounts = append(h.serverMetrics.floatCounts, fc)
	}
	for _, inst := range instrumentregistry.Int64HistoInsts {
		ih := createInt64Histogram(metrics.metrics, stats.Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.serverMetrics.intHistos = append(h.serverMetrics.intHistos, ih)
	}
	for _, inst := range instrumentregistry.Float64HistoInsts {
		fh := createFloat64Histogram(metrics.metrics, stats.Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.serverMetrics.floatHistos = append(h.serverMetrics.floatHistos, fh)
	}
	for _, inst := range instrumentregistry.Int64GaugeInsts {
		ig := createInt64Gauge(metrics.metrics, stats.Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.serverMetrics.intGauges = append(h.serverMetrics.intGauges, ig)
	}
}

// on the stats handler not the interceptor - built in component of server too
// (top level component that all components exist under)

func (h *serverStatsHandler) RecordIntCount(handle einstrumentregistry.Int64CountHandle, labels []estats.Label, optionalLabels []estats.Label, incr int64) {
	ao := createAttributeOptionFromLabels(labels, optionalLabels)
	h.serverMetrics.intCounts[handle.Index].Add(context.Background()/*switch to background from client/server*/, incr, ao)
}

func (h *serverStatsHandler) RecordFloatCount(handle einstrumentregistry.Float64CountHandle, labels []estats.Label, optionalLabels []estats.Label, incr float64) {
	ao := createAttributeOptionFromLabels(labels, optionalLabels)
	h.serverMetrics.floatCounts[handle.Index].Add(context.Background() /*switch to background from client/server*/, incr, ao)
}

func (h *serverStatsHandler) RecordIntHisto(handle einstrumentregistry.Int64HistoHandle, labels []estats.Label, optionalLabels []estats.Label, incr int64) {
	ao := createAttributeOptionFromLabels(labels, optionalLabels)

	h.serverMetrics.intHistos[handle.Index].Record(context.Background()/*switch to background from client/server*/, incr, ao)
}

func (h *serverStatsHandler) RecordFloatHisto(handle einstrumentregistry.Float64CountHandle, labels []estats.Label, optionalLabels []estats.Label, incr float64) {
	ao := createAttributeOptionFromLabels(labels, optionalLabels)

	h.serverMetrics.floatHistos[handle.Index].Record(context.Background()/*switch to background from client/server*/, incr, ao) // store contexts through tree, shouldn't require context when there's no context for certain operations...this will "break" go style guide but Doug is ok with that :)
}

func (h *serverStatsHandler) RecordIntGauge(handle einstrumentregistry.Int64GaugeHandle, labels []estats.Label, optionalLabels []estats.Label, incr int64) {
	ao := createAttributeOptionFromLabels(labels, optionalLabels)

	h.serverMetrics.intGauges[handle.Index].Record(context.Background()/*switch to background from client/server*/, incr, ao)
}


// attachLabelsTransportStream intercepts SetHeader and SendHeader calls of the
// underlying ServerTransportStream to attach metadataExchangeLabels.
type attachLabelsTransportStream struct {
	grpc.ServerTransportStream

	attachedLabels         atomic.Bool
	metadataExchangeLabels metadata.MD
}

func (s *attachLabelsTransportStream) SetHeader(md metadata.MD) error {
	if !s.attachedLabels.Swap(true) {
		s.ServerTransportStream.SetHeader(s.metadataExchangeLabels)
	}
	return s.ServerTransportStream.SetHeader(md)
}

func (s *attachLabelsTransportStream) SendHeader(md metadata.MD) error {
	if !s.attachedLabels.Swap(true) {
		s.ServerTransportStream.SetHeader(s.metadataExchangeLabels)
	}

	return s.ServerTransportStream.SendHeader(md)
}

func (h *serverStatsHandler) unaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	var metadataExchangeLabels metadata.MD
	if h.options.MetricsOptions.pluginOption != nil {
		metadataExchangeLabels = h.options.MetricsOptions.pluginOption.GetMetadata()
	}

	sts := grpc.ServerTransportStreamFromContext(ctx)

	alts := &attachLabelsTransportStream{
		ServerTransportStream:  sts,
		metadataExchangeLabels: metadataExchangeLabels,
	}
	ctx = grpc.NewContextWithServerTransportStream(ctx, alts)

	any, err := handler(ctx, req)
	if err != nil { // maybe trailers-only if headers haven't already been sent
		if !alts.attachedLabels.Swap(true) {
			alts.SetTrailer(alts.metadataExchangeLabels)
		}
	} else { // headers will be written; a message was sent
		if !alts.attachedLabels.Swap(true) {
			alts.SetHeader(alts.metadataExchangeLabels)
		}
	}

	return any, err
}

// attachLabelsStream embeds a grpc.ServerStream, and intercepts the
// SetHeader/SendHeader/SendMsg/SendTrailer call to attach metadata exchange
// labels.
type attachLabelsStream struct {
	grpc.ServerStream

	attachedLabels         atomic.Bool
	metadataExchangeLabels metadata.MD
}

func (s *attachLabelsStream) SetHeader(md metadata.MD) error {
	if !s.attachedLabels.Swap(true) {
		s.ServerStream.SetHeader(s.metadataExchangeLabels)
	}

	return s.ServerStream.SetHeader(md)
}

func (s *attachLabelsStream) SendHeader(md metadata.MD) error {
	if !s.attachedLabels.Swap(true) {
		s.ServerStream.SetHeader(s.metadataExchangeLabels)
	}

	return s.ServerStream.SendHeader(md)
}

func (s *attachLabelsStream) SendMsg(m any) error {
	if !s.attachedLabels.Swap(true) {
		s.ServerStream.SetHeader(s.metadataExchangeLabels)
	}
	return s.ServerStream.SendMsg(m)
}

func (h *serverStatsHandler) streamInterceptor(srv any, ss grpc.ServerStream, ssi *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	var metadataExchangeLabels metadata.MD
	if h.options.MetricsOptions.pluginOption != nil {
		metadataExchangeLabels = h.options.MetricsOptions.pluginOption.GetMetadata()
	}
	als := &attachLabelsStream{
		ServerStream:           ss,
		metadataExchangeLabels: metadataExchangeLabels,
	}
	err := handler(srv, als)

	// Add metadata exchange labels to trailers if never sent in headers,
	// irrespective of whether or not RPC failed.
	if !als.attachedLabels.Load() {
		als.SetTrailer(als.metadataExchangeLabels)
	}
	return err
}

// TagConn exists to satisfy stats.Handler.
func (h *serverStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn exists to satisfy stats.Handler.
func (h *serverStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// TagRPC implements per RPC context management.
func (h *serverStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	method := info.FullMethodName
	if h.options.MetricsOptions.MethodAttributeFilter != nil {
		if !h.options.MetricsOptions.MethodAttributeFilter(method) {
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

	ai := &attemptInfo{
		startTime: time.Now(),
		method:    removeLeadingSlash(method),
	}
	ri := &rpcInfo{
		ai: ai,
	}
	return setRPCInfo(ctx, ri)
}

// HandleRPC implements per RPC tracing and stats implementation.
func (h *serverStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	ri := getRPCInfo(ctx)
	if ri == nil {
		logger.Error("ctx passed into server side stats handler metrics event handling has no server call data present")
		return
	}
	h.processRPCData(ctx, rs, ri.ai)
}

func (h *serverStatsHandler) processRPCData(ctx context.Context, s stats.RPCStats, ai *attemptInfo) {
	switch st := s.(type) {
	case *stats.InHeader:
		if ai.pluginOptionLabels == nil && h.options.MetricsOptions.pluginOption != nil {
			labels := h.options.MetricsOptions.pluginOption.GetLabels(st.Header)
			if labels == nil {
				labels = map[string]string{} // Shouldn't return a nil map. Make it empty if so to ignore future Get Calls for this Attempt.
			}
			ai.pluginOptionLabels = labels
		}
		h.serverMetrics.callStarted.Add(ctx, 1, otelmetric.WithAttributes(otelattribute.String("grpc.method", ai.method)))
	case *stats.OutPayload:
		atomic.AddInt64(&ai.sentCompressedBytes, int64(st.CompressedLength))
	case *stats.InPayload:
		atomic.AddInt64(&ai.recvCompressedBytes, int64(st.CompressedLength))
	case *stats.End:
		h.processRPCEnd(ctx, ai, st)
	default:
	}
}

func (h *serverStatsHandler) processRPCEnd(ctx context.Context, ai *attemptInfo, e *stats.End) {
	latency := float64(time.Since(ai.startTime)) / float64(time.Second)
	st := "OK"
	if e.Error != nil {
		s, _ := status.FromError(e.Error)
		st = canonicalString(s.Code())
	}
	attributes := []otelattribute.KeyValue{
		otelattribute.String("grpc.method", ai.method),
		otelattribute.String("grpc.status", st),
	}
	for k, v := range ai.pluginOptionLabels {
		attributes = append(attributes, otelattribute.String(k, v))
	}

	serverAttributeOption := otelmetric.WithAttributes(attributes...)
	h.serverMetrics.callDuration.Record(ctx, latency, serverAttributeOption)
	h.serverMetrics.callSentTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&ai.sentCompressedBytes), serverAttributeOption)
	h.serverMetrics.callRcvdTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&ai.recvCompressedBytes), serverAttributeOption)
}

const (
	// ServerCallStarted is the number of server calls started.
	ServerCallStarted stats.Metric = "grpc.server.call.started"
	// ServerCallSentCompressedTotalMessageSize is the compressed message bytes
	// sent per server call.
	ServerCallSentCompressedTotalMessageSize stats.Metric = "grpc.server.call.sent_total_compressed_message_size"
	// ServerCallRcvdCompressedTotalMessageSize is the compressed message bytes
	// received per server call.
	ServerCallRcvdCompressedTotalMessageSize stats.Metric = "grpc.server.call.rcvd_total_compressed_message_size"
	// ServerCallDuration is the end-to-end time taken to complete a call from
	// server transport's perspective.
	ServerCallDuration stats.Metric = "grpc.server.call.duration"
)
