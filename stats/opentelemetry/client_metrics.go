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
	"google.golang.org/grpc/internal/instrumentregistry"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	istats "google.golang.org/grpc/internal/stats"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	otelattribute "go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

type clientStatsHandler struct {
	options Options

	clientMetrics clientMetrics
}

// create helpers from global instrument registry...need 4 lists to persist around

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
		// This can read internal, so can append here.
		// If they want user can add to DefaultMetrics with a string or something...
		// let's just say this is it
		metrics = DefaultMetrics
	}

	h.clientMetrics.attemptStarted = createInt64Counter(metrics.metrics, "grpc.client.attempt.started", meter, otelmetric.WithUnit("attempt"), otelmetric.WithDescription("Number of client call attempts started."))
	h.clientMetrics.attemptDuration = createFloat64Histogram(metrics.metrics, "grpc.client.attempt.duration", meter, otelmetric.WithUnit("s"), otelmetric.WithDescription("End-to-end time taken to complete a client call attempt."), otelmetric.WithExplicitBucketBoundaries(DefaultLatencyBounds...))
	h.clientMetrics.attemptSentTotalCompressedMessageSize = createInt64Histogram(metrics.metrics, "grpc.client.attempt.sent_total_compressed_message_size", meter, otelmetric.WithUnit("By"), otelmetric.WithDescription("Compressed message bytes sent per client call attempt."), otelmetric.WithExplicitBucketBoundaries(DefaultSizeBounds...))
	h.clientMetrics.attemptRcvdTotalCompressedMessageSize = createInt64Histogram(metrics.metrics, "grpc.client.attempt.rcvd_total_compressed_message_size", meter, otelmetric.WithUnit("By"), otelmetric.WithDescription("Compressed message bytes received per call attempt."), otelmetric.WithExplicitBucketBoundaries(DefaultSizeBounds...))
	h.clientMetrics.callDuration = createFloat64Histogram(metrics.metrics, "grpc.client.call.duration", meter, otelmetric.WithUnit("s"), otelmetric.WithDescription("Time taken by gRPC to complete an RPC from application's perspective."), otelmetric.WithExplicitBucketBoundaries(DefaultLatencyBounds...))



	// Should I use a different API than this thing...Metric typed
	// Default comes at init so idk there...ask Doug?
	// the behavior of registration is: (aka "requirements")
	// 1. on by default gets set if not set
	// 2. if the metrics field is set above, record only those?

	// as default can scale up or down...do this as metrics are being registered...OTel will be called so this will have to be inited...



	// Globally registered metrics...pull into helper?

	// needs to read data structures that are fully internal, how to access
	// instrument registry while keeping it internal? I think move to
	// opentelemetry, keep public APIs but make global instrument data internal two options I listed somewhere

	for _, inst := range instrumentregistry.Int64CountInsts { // maybe I use a wrapping data structure/helper here...
		// cannot use type string as type metric?
		// what to do about this Metric thing?
		// store inst.name as Metric when created?
		ic := createInt64Counter(metrics.metrics, Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.clientMetrics.intCounts = append(h.clientMetrics.intCounts, ic)

		// construct labels at metric recording time...how tf do I persist this?
		// has index, index can look into wrapping data structures? yeah I think so...

		// I think I can ignore these...stored in the instrument registry and persisted in metrics recorder,
		// that can eat labels if doesn't match up, this layer just gets these two pieces of info through API call
		inst.Labels // what to do with these, store these alongside? do I need to wrap?
		inst.OptionalLabels // how does this get combine with optional labels API (I think orthogonal), and stored?


		// default logic:
		inst.Default // how to combine with metrics?
		// build out this data before?
		metrics.metrics // map[Metric] // how to merge this concept of picks up defaults, seperate or same?
	}


	for _, inst := range instrumentregistry.FloatCountInsts {
		fc := createFloat(metrics.metrics, inst.Name, meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.clientMetrics.floatCounts = append(h.clientMetrics.floatCounts, fc)
	} // Do we actually need float count? what is this even used for?


	for _, inst := range instrumentregistry.Int64HistoInsts {
		ih := createInt64Histogram(metrics.metrics, Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.clientMetrics.intHistos = append(h.clientMetrics.intHistos, ih)
	}

	for _, inst := range instrumentregistry.Float64HistoInsts {
		fh := createFloat64Histogram(metrics.metrics, Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.clientMetrics.floatHistos = append(h.clientMetrics.floatHistos, fh)
	}

	// is there a way to parameterize these creations?

	for _, inst := range instrumentregistry.Int64GaugeInsts { // to keep this one would loop over and then typecast downward into 5 different types...
		// wrap name with typecast
		// can I parameterize with inst description? Thanks Doug
		ig := createInt64Gauge(metrics.metrics, Metric(inst.Name), meter, otelmetric.WithUnit(inst.Unit), otelmetric.WithDescription(inst.Description))
		h.clientMetrics.intGauges = append(h.clientMetrics.intGauges, ig)
		inst.Default // already accounted for before? if merged with defualt metrics
	}
}

// What about xDS Client metrics? On something that lives in the ether
// I think this - "pass stats handlers from channel to lb policies", and lb policies are client side only
// what object is this created on?
// generalize to server/client or what? There's agnostic metrics like transport metrics right?

func (h *clientStatsHandler) RecordIntCount(handle IntCountHandle, labels []Label, optionalLabels []Label, incr int64) {

	// the record calls take a typecast...

	// turn labels/
	// optional labels into k:v pairs to record on this?

	/*
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
	*/

	// *** This labels processing codeblock could probably be parameterized
	/*var attributes []otelattribute.KeyValue
	for _, label := range labels { // where does it check if labels/optional labels add up to what is being emitted from system?
		attributes = append(attributes, otelattribute.String(label.Key, label.Value))
	}

	for _, optLabel := range labels { // What other label does this get combine with? there's only one locality...does this call flow eat these labels if sent upward?
		attributes = append(attributes, otelattribute.String(optLabel.Key, optLabel.Value))
	}
	attributeOption := otelmetric.WithAttributes(attributes...)*/
	// ***
	//  (labels, optionalLabels)    attributeOption

	ao := createAttributeOptionFromLabels(labels, optionalLabels)

	// the other types are strongly typed

	// Oh shoot right now context isn't being plumbed up through system. Context of lb operations that I need to record?
	h.clientMetrics.intCounts[handle.index].Add(context.Background()/*what context do I take? the context of the operation in a certain component?*/, incr, ao)
}

func (h *clientStatsHandler) RecordFloatCount(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {
	/*var attributes []otelattribute.KeyValue
	for _, label := range labels { // where does it check if labels/optional labels add up to what is being emitted from system? layer below
		attributes = append(attributes, otelattribute.String(label.Key, label.Value))
	}

	for _, optLabel := range labels { // What other label does this get combine with? there's only one locality...does this call flow eat these labels if sent upward?
		attributes = append(attributes, otelattribute.String(optLabel.Key, optLabel.Value))
	}
	attributeOption := otelmetric.WithAttributes(attributes...)*/

	ao := createAttributeOptionFromLabels(labels, optionalLabels)
	// Could construct attribute option in a helper...
	// couldn't someone trigger a panic, but that isn't really a security vulnerability since callsite is within binary and trusted
	h.clientMetrics.floatCounts[handle.index].Add(context.Background() /*The context gates what operation?*/, incr, ao)
}

func (h *clientStatsHandler) RecordIntHisto(handle IntHistoHandle, labels []Label, optionalLabels []Label, incr int64) {
	/*var attributes []otelattribute.KeyValue
	for _, label := range labels { // where does it check if labels/optional labels add up to what is being emitted from system?
		attributes = append(attributes, otelattribute.String(label.Key, label.Value))
	}

	for _, optLabel := range labels { // What other label does this get combine with? there's only one locality...does this call flow eat these labels if sent upward?
		attributes = append(attributes, otelattribute.String(optLabel.Key, optLabel.Value))
	}
	attributeOption := otelmetric.WithAttributes(attributes...)*/

	ao := createAttributeOptionFromLabels(labels, optionalLabels)

	h.clientMetrics.intHistos[handle.index].Record(context.Background()/*what context do I take? the context of the operation in a certain component?*/, incr, ao)
}

func (h *clientStatsHandler) RecordFloatHisto(handle FloatCountHandle, labels []Label, optionalLabels []Label, incr float64) {
	/*var attributes []otelattribute.KeyValue
	for _, label := range labels { // where does it check if labels/optional labels add up to what is being emitted from system?
		attributes = append(attributes, otelattribute.String(label.Key, label.Value))
	}

	for _, optLabel := range labels { // What other label does this get combine with? there's only one locality...does this call flow eat these labels if sent upward?
		attributes = append(attributes, otelattribute.String(optLabel.Key, optLabel.Value))
	}
	attributeOption := otelmetric.WithAttributes(attributes...)*/

	ao := createAttributeOptionFromLabels(labels, optionalLabels)

	h.clientMetrics.floatHistos[handle.index].Record(context.Background()/*what context do I take? the context of the operation in a certain component?*/, incr, ao)
}
// I'm done, there's a few open ended questions in this layer

func createAttributeOptionFromLabels(labels []Label, optionalLabels []Label) otelmetric.MeasurementOption {
	var attributes []otelattribute.KeyValue
	for _, label := range labels { // where does it check if labels/optional labels add up to what is being emitted from system? layer underneath
		attributes = append(attributes, otelattribute.String(label.Key, label.Value))
	}
	for _, optLabel := range labels {
		attributes = append(attributes, otelattribute.String(optLabel.Key, optLabel.Value))
	}
	return otelmetric.WithAttributes(attributes...)
}

// but I have to figure out a way to test
// unit test registration? and panic?
// test creation of OTel and how it uses global instrument registry?
// only way to test OTel usage is to provide it a meter which it uses to create instruments, otherwise useless

// "Fake stats handler"...

// everything including new metrics recorder object can read from global instrument registry



// layer above: how to typecast down the interface in order to build this list thing?
// Can unit test at this layer too


func (h *clientStatsHandler) unaryInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	ci := &callInfo{
		target: h.determineTarget(cc),
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

// determineTarget determines the target to record attributes with. This will be
// "other" if target filter is set and specifies, the target name as is
// otherwise.
func (h *clientStatsHandler) determineTarget(cc *grpc.ClientConn) string {
	target := cc.CanonicalTarget()
	if f := h.options.MetricsOptions.TargetAttributeFilter; f != nil && !f(target) {
		target = "other"
	}
	return target
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
		target: h.determineTarget(cc),
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
