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
	istats "google.golang.org/grpc/internal/stats"
	"google.golang.org/grpc/internal/stats/instrumentregistry"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	estats "google.golang.org/grpc/experimental/stats"
	einstrumentregistry "google.golang.org/grpc/experimental/stats/instrumentregistry"

	otelattribute "go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

type clientStatsHandler struct {
	options Options

	clientMetrics clientMetrics
}

// This PR to finish I think needs background context
// from client and server...
// pass that in options, store it around in client/server stats handler...

// creation of things built on for both sides, which forwards to underlying,
// pass in context.Background() to the OTel in main?

// How to test client side stats handler (should I ask Doug if I should return a
// new thing or conflate with old I guess is cleaner)
// And server side stats handler

// Both read from instrument registry (test default behavior...)
// vs. what metrics user wants

// unless full meter can't check, but this infra of testing emissions atoms can be reused
// for testing at list layer? But rls/pick first test as described by Mark

// draw out layers in notebook and how to test at each layer...

// For background context:
// leave it up to users/main to scope the context for OTel/Non Per Call: I think so

// "the otel plugin isn't rooted on a channel or server it's independent" - so doesn't make sense to root context on it I guess



// For this PR: 1. Helper to create metrics atoms emitted (will need this for e2e tests anyway)
// Test shows you how to pass the context, creat an otel plugin with new context parameter
// and record on it...


// 2. take a context.Context (pointer I think to not copy) in constructor...

// 3. Switch default metrics to be a helper, and change usages of it like examples
// helper reads/combines default set with exported default set written to by inst registry

// Test shows you how to pass a context
// if opts.Context = nil { set to context.Background }






// On both client and server side:
// build in first class component in new client and new server that creates inline by typecasting the
// stats handlers downward...next PR?

// So server needs same thing solely for xDS Client...do I really need this symetry? Ask group/Yash/Doug or is there another way to do this
// set on a channel/server and that is derived as to what records

// argh this is also on server side which passes to xDS Client, it needs same
// flow to pass to xDS Client (do I need this binary partion)

// For the record calls context:
// Get it from client and server...need to add context.Background() to both. Not needed unless communciate specific kvs from components
// on top of client/servers context, but this gets passed through API anyway.
func (h *clientStatsHandler) RecordIntCount(handle einstrumentregistry.Int64CountHandle, labels []estats.Label, optionalLabels []estats.Label, incr int64) {
	ao := createAttributeOptionFromLabels(labels, optionalLabels)
	h.clientMetrics.intCounts[handle.Index].Add(context.Background()/*switch to background from client/server*/, incr, ao)
}

func (h *clientStatsHandler) RecordFloatCount(handle einstrumentregistry.Float64CountHandle, labels []estats.Label, optionalLabels []estats.Label, incr float64) {
	ao := createAttributeOptionFromLabels(labels, optionalLabels)
	h.clientMetrics.floatCounts[handle.Index].Add(context.Background() /*switch to background from client/server*/, incr, ao)
}

func (h *clientStatsHandler) RecordIntHisto(handle einstrumentregistry.Int64HistoHandle, labels []estats.Label, optionalLabels []estats.Label, incr int64) {
	ao := createAttributeOptionFromLabels(labels, optionalLabels)

	h.clientMetrics.intHistos[handle.Index].Record(context.Background()/*switch to background from client/server*/, incr, ao)
}

func (h *clientStatsHandler) RecordFloatHisto(handle einstrumentregistry.Float64CountHandle, labels []estats.Label, optionalLabels []estats.Label, incr float64) {
	ao := createAttributeOptionFromLabels(labels, optionalLabels)

	h.clientMetrics.floatHistos[handle.Index].Record(context.Background()/*switch to background from client/server*/, incr, ao) // store contexts through tree, shouldn't require context when there's no context for certain operations...this will "break" go style guide but Doug is ok with that :)
}

func (h *clientStatsHandler) RecordIntGauge(handle einstrumentregistry.Int64GaugeHandle, labels []estats.Label, optionalLabels []estats.Label, incr int64) {
	ao := createAttributeOptionFromLabels(labels, optionalLabels)

	h.clientMetrics.intGauges[handle.Index].Record(context.Background()/*switch to background from client/server*/, incr, ao)
}

// Do the same thing server side...

// All gRPC components are underneath a client or server, and you can't use gRPC
// outside of the scope of channel or server. All operations/components happen
// in the scope of a channel or server...so logically the hook point to stats
// handler is client or server...

// xDS Client is not tied 1:1 with channel or server, but handle those causes

// so do need to mirror this server side...

// Defaults aren't tied to client/server, so are non per call metrics...
// So scale up both sides with the same operations it's the same code on both sides...





// next pr is the same changes except symbols are defined elsewhere and default
// becomes a function that reads non per call global as well, will need to
// switch all usages? Or just keep symbol for tests but have what users call a
// function (need to update examples as well in this case)

func (h *clientStatsHandler) initializeMetrics() {
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
		metrics = DefaultMetrics // switch to a function that checks at runtime...shouldn't race because init comes before it channels get created document that as well...set at init time and read only after that, it's not like channels are created at init time anyway...
	}
	// Default Metrics used by users are also DefaultMetrics()...add or remove based off that (both come at runtime so don't intersplice the sets)


	h.clientMetrics.attemptStarted = createInt64Counter(metrics.metrics, "grpc.client.attempt.started", meter, otelmetric.WithUnit("attempt"), otelmetric.WithDescription("Number of client call attempts started."))
	h.clientMetrics.attemptDuration = createFloat64Histogram(metrics.metrics, "grpc.client.attempt.duration", meter, otelmetric.WithUnit("s"), otelmetric.WithDescription("End-to-end time taken to complete a client call attempt."), otelmetric.WithExplicitBucketBoundaries(DefaultLatencyBounds...))
	h.clientMetrics.attemptSentTotalCompressedMessageSize = createInt64Histogram(metrics.metrics, "grpc.client.attempt.sent_total_compressed_message_size", meter, otelmetric.WithUnit("By"), otelmetric.WithDescription("Compressed message bytes sent per client call attempt."), otelmetric.WithExplicitBucketBoundaries(DefaultSizeBounds...))
	h.clientMetrics.attemptRcvdTotalCompressedMessageSize = createInt64Histogram(metrics.metrics, "grpc.client.attempt.rcvd_total_compressed_message_size", meter, otelmetric.WithUnit("By"), otelmetric.WithDescription("Compressed message bytes received per call attempt."), otelmetric.WithExplicitBucketBoundaries(DefaultSizeBounds...))
	h.clientMetrics.callDuration = createFloat64Histogram(metrics.metrics, "grpc.client.call.duration", meter, otelmetric.WithUnit("s"), otelmetric.WithDescription("Time taken by gRPC to complete an RPC from application's perspective."), otelmetric.WithExplicitBucketBoundaries(DefaultLatencyBounds...))

	// Non per call metrics:
	for _, inst := range instrumentregistry.Int64CountInsts {
		ic := createInt64Counter(metrics.metrics, stats.Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.clientMetrics.intCounts = append(h.clientMetrics.intCounts, ic)
	}
	for _, inst := range instrumentregistry.Float64CountInsts {
		fc := createFloat64Counter(metrics.metrics, stats.Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.clientMetrics.floatCounts = append(h.clientMetrics.floatCounts, fc)
	}
	for _, inst := range instrumentregistry.Int64HistoInsts {
		ih := createInt64Histogram(metrics.metrics, stats.Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.clientMetrics.intHistos = append(h.clientMetrics.intHistos, ih)
	}
	for _, inst := range instrumentregistry.Float64HistoInsts {
		fh := createFloat64Histogram(metrics.metrics, stats.Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.clientMetrics.floatHistos = append(h.clientMetrics.floatHistos, fh)
	}
	for _, inst := range instrumentregistry.Int64GaugeInsts {
		ig := createInt64Gauge(metrics.metrics, stats.Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.clientMetrics.intGauges = append(h.clientMetrics.intGauges, ig)
	}
}

func (h *clientStatsHandler) unaryInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	ci := &callInfo{
		target: cc.CanonicalTarget(),
		method: h.determineMethod(method, opts...),
	}
	ctx = setCallInfo(ctx, ci)

	if h.options.MetricsOptions.pluginOption != nil {
		md := h.options.MetricsOptions.pluginOption.GetMetadata()
		for k, vs := range md {
			for _, v := range vs {
				ctx = metadata.AppendToOutgoingContext(ctx, k, v)
			}
		}
	}

	startTime := time.Now()
	err := invoker(ctx, method, req, reply, cc, opts...)
	h.perCallMetrics(ctx, err, startTime, ci)
	return err
}

// determineMethod determines the method to record attributes with. This will be
// "other" if StaticMethod isn't specified or if method filter is set and
// specifies, the method name as is otherwise.
func (h *clientStatsHandler) determineMethod(method string, opts ...grpc.CallOption) string {
	for _, opt := range opts {
		if _, ok := opt.(grpc.StaticMethodCallOption); ok {
			return removeLeadingSlash(method)
		}
	}
	return "other"
}

func (h *clientStatsHandler) streamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	ci := &callInfo{
		target: cc.CanonicalTarget(),
		method: h.determineMethod(method, opts...),
	}
	ctx = setCallInfo(ctx, ci)

	if h.options.MetricsOptions.pluginOption != nil {
		md := h.options.MetricsOptions.pluginOption.GetMetadata()
		for k, vs := range md {
			for _, v := range vs {
				ctx = metadata.AppendToOutgoingContext(ctx, k, v)
			}
		}
	}

	startTime := time.Now()

	callback := func(err error) {
		h.perCallMetrics(ctx, err, startTime, ci)
	}
	opts = append([]grpc.CallOption{grpc.OnFinish(callback)}, opts...)
	return streamer(ctx, desc, cc, method, opts...)
}

func (h *clientStatsHandler) perCallMetrics(ctx context.Context, err error, startTime time.Time, ci *callInfo) {
	s := status.Convert(err)
	callLatency := float64(time.Since(startTime)) / float64(time.Second)
	h.clientMetrics.callDuration.Record(ctx, callLatency, otelmetric.WithAttributes(otelattribute.String("grpc.method", ci.method), otelattribute.String("grpc.target", ci.target), otelattribute.String("grpc.status", canonicalString(s.Code()))))
}

// TagConn exists to satisfy stats.Handler.
func (h *clientStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn exists to satisfy stats.Handler.
func (h *clientStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// TagRPC implements per RPC attempt context management.
func (h *clientStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	// Numerous stats handlers can be used for the same channel. The cluster
	// impl balancer which writes to this will only write once, thus have this
	// stats handler's per attempt scoped context point to the same optional
	// labels map if set.
	var labels *istats.Labels
	if labels = istats.GetLabels(ctx); labels == nil {
		labels = &istats.Labels{
			TelemetryLabels: make(map[string]string),
		}
		ctx = istats.SetLabels(ctx, labels)
	}
	ai := &attemptInfo{ // populates information about RPC start.
		startTime: time.Now(),
		xdsLabels: labels.TelemetryLabels,
		method:    info.FullMethodName,
	}
	ri := &rpcInfo{
		ai: ai,
	}
	return setRPCInfo(ctx, ri)
}

func (h *clientStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	ri := getRPCInfo(ctx)
	if ri == nil {
		logger.Error("ctx passed into client side stats handler metrics event handling has no client attempt data present")
		return
	}
	h.processRPCEvent(ctx, rs, ri.ai)
}

func (h *clientStatsHandler) processRPCEvent(ctx context.Context, s stats.RPCStats, ai *attemptInfo) {
	switch st := s.(type) {
	case *stats.Begin:
		ci := getCallInfo(ctx)
		if ci == nil {
			logger.Error("ctx passed into client side stats handler metrics event handling has no metrics data present")
			return
		}

		h.clientMetrics.attemptStarted.Add(ctx, 1, otelmetric.WithAttributes(otelattribute.String("grpc.method", ci.method), otelattribute.String("grpc.target", ci.target)))
	case *stats.OutPayload:
		atomic.AddInt64(&ai.sentCompressedBytes, int64(st.CompressedLength))
	case *stats.InPayload:
		atomic.AddInt64(&ai.recvCompressedBytes, int64(st.CompressedLength))
	case *stats.InHeader:
		h.setLabelsFromPluginOption(ai, st.Header)
	case *stats.InTrailer:
		h.setLabelsFromPluginOption(ai, st.Trailer)
	case *stats.End:
		h.processRPCEnd(ctx, ai, st)
	default:
	}
}

func (h *clientStatsHandler) setLabelsFromPluginOption(ai *attemptInfo, incomingMetadata metadata.MD) {
	if ai.pluginOptionLabels == nil && h.options.MetricsOptions.pluginOption != nil {
		labels := h.options.MetricsOptions.pluginOption.GetLabels(incomingMetadata)
		if labels == nil {
			labels = map[string]string{} // Shouldn't return a nil map. Make it empty if so to ignore future Get Calls for this Attempt.
		}
		ai.pluginOptionLabels = labels
	}
}

func (h *clientStatsHandler) processRPCEnd(ctx context.Context, ai *attemptInfo, e *stats.End) {
	ci := getCallInfo(ctx)
	if ci == nil {
		logger.Error("ctx passed into client side stats handler metrics event handling has no metrics data present")
		return
	}
	latency := float64(time.Since(ai.startTime)) / float64(time.Second)
	st := "OK"
	if e.Error != nil {
		s, _ := status.FromError(e.Error)
		st = canonicalString(s.Code())
	}

	attributes := []otelattribute.KeyValue{
		otelattribute.String("grpc.method", ci.method),
		otelattribute.String("grpc.target", ci.target),
		otelattribute.String("grpc.status", st),
	}

	for k, v := range ai.pluginOptionLabels {
		attributes = append(attributes, otelattribute.String(k, v))
	}

	for _, o := range h.options.MetricsOptions.OptionalLabels {
		if val, ok := ai.xdsLabels[o]; ok {
			attributes = append(attributes, otelattribute.String(o, val))
		}
	}

	clientAttributeOption := otelmetric.WithAttributes(attributes...)
	h.clientMetrics.attemptDuration.Record(ctx, latency, clientAttributeOption)
	h.clientMetrics.attemptSentTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&ai.sentCompressedBytes), clientAttributeOption)
	h.clientMetrics.attemptRcvdTotalCompressedMessageSize.Record(ctx, atomic.LoadInt64(&ai.recvCompressedBytes), clientAttributeOption)
}

const (
	// ClientAttemptStarted is the number of client call attempts started.
	ClientAttemptStarted stats.Metric = "grpc.client.attempt.started"
	// ClientAttemptDuration is the end-to-end time taken to complete a client
	// call attempt.
	ClientAttemptDuration stats.Metric = "grpc.client.attempt.duration"
	// ClientAttemptSentCompressedTotalMessageSize is the compressed message
	// bytes sent per client call attempt.
	ClientAttemptSentCompressedTotalMessageSize stats.Metric = "grpc.client.attempt.sent_total_compressed_message_size"
	// ClientAttemptRcvdCompressedTotalMessageSize is the compressed message
	// bytes received per call attempt.
	ClientAttemptRcvdCompressedTotalMessageSize stats.Metric = "grpc.client.attempt.rcvd_total_compressed_message_size"
	// ClientCallDuration is the time taken by gRPC to complete an RPC from
	// application's perspective.
	ClientCallDuration stats.Metric = "grpc.client.call.duration"
)
