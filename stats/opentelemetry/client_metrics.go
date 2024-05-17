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
	istats "google.golang.org/grpc/internal/stats"
	"google.golang.org/grpc/metadata"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type clientStatsHandler struct {
	o Options

	clientMetrics clientMetrics
}

func (csh *clientStatsHandler) initializeMetrics() {
	// Will set no metrics to record, logically making this stats handler a
	// no-op.
	if csh.o.MetricsOptions.MeterProvider == nil {
		return
	}

	meter := csh.o.MetricsOptions.MeterProvider.Meter("grpc-go " + grpc.Version)
	if meter == nil {
		return
	}

	setOfMetrics := csh.o.MetricsOptions.Metrics.metrics

	csh.clientMetrics.attemptStarted = createInt64Counter(setOfMetrics, "grpc.client.attempt.started", meter, metric.WithUnit("attempt"), metric.WithDescription("Number of client call attempts started."))
	csh.clientMetrics.attemptDuration = createFloat64Histogram(setOfMetrics, "grpc.client.attempt.duration", meter, metric.WithUnit("s"), metric.WithDescription("End-to-end time taken to complete a client call attempt."), metric.WithExplicitBucketBoundaries(DefaultLatencyBounds...))
	csh.clientMetrics.attemptSentTotalCompressedMessageSize = createInt64Histogram(setOfMetrics, "grpc.client.attempt.sent_total_compressed_message_size", meter, metric.WithUnit("By"), metric.WithDescription("Compressed message bytes sent per client call attempt."), metric.WithExplicitBucketBoundaries(DefaultSizeBounds...))
	csh.clientMetrics.attemptRcvdTotalCompressedMessageSize = createInt64Histogram(setOfMetrics, "grpc.client.attempt.rcvd_total_compressed_message_size", meter, metric.WithUnit("By"), metric.WithDescription("Compressed message bytes received per call attempt."), metric.WithExplicitBucketBoundaries(DefaultSizeBounds...))
	csh.clientMetrics.callDuration = createFloat64Histogram(setOfMetrics, "grpc.client.call.duration", meter, metric.WithUnit("s"), metric.WithDescription("Time taken by gRPC to complete an RPC from application's perspective."), metric.WithExplicitBucketBoundaries(DefaultLatencyBounds...))
}

func (csh *clientStatsHandler) unaryInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	ci := &callInfo{
		target: csh.determineTarget(cc),
		method: csh.determineMethod(method, opts...),
	}
	ctx = setCallInfo(ctx, ci)

	if csh.o.MetricsOptions.pluginOption != nil {
		md := csh.o.MetricsOptions.pluginOption.GetMetadata()
		val := md.Get("x-envoy-peer-metadata")
		if len(val) == 1 {
			ctx = metadata.AppendToOutgoingContext(ctx, metadataExchangeKey, val[0])
		}
	}

	startTime := time.Now()
	err := invoker(ctx, method, req, reply, cc, opts...)
	csh.perCallMetrics(ctx, err, startTime, ci)
	return err
}

// determineTarget determines the target to record attributes with. This will be
// "other" if target filter is set and specifies, the target name as is
// otherwise.
func (csh *clientStatsHandler) determineTarget(cc *grpc.ClientConn) string {
	target := cc.CanonicalTarget()
	if f := csh.o.MetricsOptions.TargetAttributeFilter; f != nil && !f(target) {
		target = "other"
	}
	return target
}

// determineMethod determines the method to record attributes with. This will be
// "other" if StaticMethod isn't specified or if method filter is set and
// specifies, the method name as is otherwise.
func (csh *clientStatsHandler) determineMethod(method string, opts ...grpc.CallOption) string {
	for _, opt := range opts {
		if _, ok := opt.(grpc.StaticMethodCallOption); ok {
			return removeLeadingSlash(method)
		}
	}
	return "other"
}

func (csh *clientStatsHandler) streamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	ci := &callInfo{
		target: csh.determineTarget(cc),
		method: csh.determineMethod(method, opts...),
	}
	ctx = setCallInfo(ctx, ci)

	if csh.o.MetricsOptions.pluginOption != nil { // from incoming context right for GetLabels? well e2e test and find out :)
		md := csh.o.MetricsOptions.pluginOption.GetMetadata() // metadata.MD
		val := md.Get("x-envoy-peer-metadata")
		if len(val) == 1 {
			ctx = metadata.AppendToOutgoingContext(ctx, metadataExchangeKey, val[0])
		}
	}

	startTime := time.Now()

	callback := func(err error) {
		csh.perCallMetrics(ctx, err, startTime, ci)
	}
	opts = append([]grpc.CallOption{grpc.OnFinish(callback)}, opts...)
	return streamer(ctx, desc, cc, method, opts...)
}

func (csh *clientStatsHandler) perCallMetrics(ctx context.Context, err error, startTime time.Time, ci *callInfo) {
	s := status.Convert(err)
	callLatency := float64(time.Since(startTime)) / float64(time.Second)
	csh.clientMetrics.callDuration.Record(ctx, callLatency, metric.WithAttributes(attribute.String("grpc.method", ci.method), attribute.String("grpc.target", ci.target), attribute.String("grpc.status", canonicalString(s.Code()))))
}

// TagConn exists to satisfy stats.Handler.
func (csh *clientStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn exists to satisfy stats.Handler.
func (csh *clientStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// TagRPC implements per RPC attempt context management.
func (csh *clientStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	labels := &istats.Labels{
		TelemetryLabels: make(map[string]string),
	}
	mi := &metricsInfo{ // populates information about RPC start.
		startTime: time.Now(),
		xDSLabels: labels.TelemetryLabels, // holds a ref to map, make sure this doesn't race (and works, just test it :D)
	}
	ri := &rpcInfo{
		mi: mi,
	}
	ctx = istats.SetLabels(ctx, labels) // ctx passed is immutable, however cluster_impl writes to the map of Telemetry Labels on the heap.
	return setRPCInfo(ctx, ri)
}

func (csh *clientStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	ri := getRPCInfo(ctx)
	if ri == nil {
		logger.Error("ctx passed into client side stats handler metrics event handling has no client attempt data present")
		return
	}
	csh.processRPCEvent(ctx, rs, ri.mi)
}

func (csh *clientStatsHandler) processRPCEvent(ctx context.Context, s stats.RPCStats, mi *metricsInfo) {
	switch st := s.(type) {
	case *stats.Begin:
		ci := getCallInfo(ctx)
		if ci == nil {
			logger.Error("ctx passed into client side stats handler metrics event handling has no metrics data present")
			return
		}

		csh.clientMetrics.attemptStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("grpc.method", ci.method), attribute.String("grpc.target", ci.target)))
	case *stats.OutPayload:
		atomic.AddInt64(&mi.sentCompressedBytes, int64(st.CompressedLength))
	case *stats.InPayload:
		atomic.AddInt64(&mi.recvCompressedBytes, int64(st.CompressedLength))
	case *stats.End:
		csh.processRPCEnd(ctx, mi, st)
	case *stats.InHeader:
		// Also delete target filtration...maybe same PR?
		if !mi.labelsReceived && csh.o.MetricsOptions.pluginOption != nil {
			mi.labels = csh.o.MetricsOptions.pluginOption.GetLabels(st.Header, mi.xDSLabels) // way to get xDS Labels set in Tag(), so I'm good here...
			mi.labelsReceived = true
		}
	case *stats.InTrailer: // only variable is header and trailer can pull out into helper
		if !mi.labelsReceived && csh.o.MetricsOptions.pluginOption != nil {
			mi.labels = csh.o.MetricsOptions.pluginOption.GetLabels(st.Trailer, mi.xDSLabels) // this read should just...work right (since set in picker update (pass around a heap pointer)
			mi.labelsReceived = true
		}
	default:
	}
}

func (csh *clientStatsHandler) processRPCEnd(ctx context.Context, mi *metricsInfo, e *stats.End) {
	ci := getCallInfo(ctx)
	if ci == nil {
		logger.Error("ctx passed into client side stats handler metrics event handling has no metrics data present")
		return
	}
	latency := float64(time.Since(mi.startTime)) / float64(time.Second)
	st := "OK"
	if e.Error != nil {
		s, _ := status.FromError(e.Error)
		st = canonicalString(s.Code())
	}

	attributes := []attribute.KeyValue{
		attribute.String("grpc.method", ci.method),
		attribute.String("grpc.target", ci.target),
		attribute.String("grpc.status", st),
	}

	for k, v := range mi.labels {
		attributes = append(attributes, attribute.String(k, v))
	}

	clientAttributeOption := metric.WithAttributes(attributes...)
	csh.clientMetrics.attemptDuration.Record(ctx, latency, clientAttributeOption)
	csh.clientMetrics.attemptSentTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.sentCompressedBytes), clientAttributeOption)
	csh.clientMetrics.attemptRcvdTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&mi.recvCompressedBytes), clientAttributeOption)
}

const (
	// ClientAttemptStarted is the number of client call attempts started.
	ClientAttemptStarted Metric = "grpc.client.attempt.started"
	// ClientAttemptDuration is the end-to-end time taken to complete a client
	// call attempt.
	ClientAttemptDuration Metric = "grpc.client.attempt.duration"
	// ClientAttemptSentCompressedTotalMessageSize is the compressed message
	// bytes sent per client call attempt.
	ClientAttemptSentCompressedTotalMessageSize Metric = "grpc.client.attempt.sent_total_compressed_message_size"
	// ClientAttemptRcvdCompressedTotalMessageSize is the compressed message
	// bytes received per call attempt.
	ClientAttemptRcvdCompressedTotalMessageSize Metric = "grpc.client.attempt.rcvd_total_compressed_message_size"
	// ClientCallDuration is the time taken by gRPC to complete an RPC from
	// application's perspective.
	ClientCallDuration Metric = "grpc.client.call.duration"
)
