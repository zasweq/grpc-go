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
	estats "google.golang.org/grpc/experimental/stats"
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
		// should I switch the examples to use this - for users to set...
		metrics = DefaultMetrics() // switch to runtime check...users can also see this, what do I want the returned type to be?
	}

	h.clientMetrics.attemptStarted = createInt64Counter(metrics.Metrics, "grpc.client.attempt.started", meter, otelmetric.WithUnit("attempt"), otelmetric.WithDescription("Number of client call attempts started."))
	h.clientMetrics.attemptDuration = createFloat64Histogram(metrics.Metrics, "grpc.client.attempt.duration", meter, otelmetric.WithUnit("s"), otelmetric.WithDescription("End-to-end time taken to complete a client call attempt."), otelmetric.WithExplicitBucketBoundaries(DefaultLatencyBounds...))
	h.clientMetrics.attemptSentTotalCompressedMessageSize = createInt64Histogram(metrics.Metrics, "grpc.client.attempt.sent_total_compressed_message_size", meter, otelmetric.WithUnit("By"), otelmetric.WithDescription("Compressed message bytes sent per client call attempt."), otelmetric.WithExplicitBucketBoundaries(DefaultSizeBounds...))
	h.clientMetrics.attemptRcvdTotalCompressedMessageSize = createInt64Histogram(metrics.Metrics, "grpc.client.attempt.rcvd_total_compressed_message_size", meter, otelmetric.WithUnit("By"), otelmetric.WithDescription("Compressed message bytes received per call attempt."), otelmetric.WithExplicitBucketBoundaries(DefaultSizeBounds...))
	h.clientMetrics.callDuration = createFloat64Histogram(metrics.Metrics, "grpc.client.call.duration", meter, otelmetric.WithUnit("s"), otelmetric.WithDescription("Time taken by gRPC to complete an RPC from application's perspective."), otelmetric.WithExplicitBucketBoundaries(DefaultLatencyBounds...))

	// users wraps string with Metrics...?

	estats.DefaultMetrics // Combine this with a join against our own

	// experimental dependent on experimental
	estats.MetricsRegistry // map[stats.Metric]->metric descriptor

	for metric, desc := range estats.MetricsRegistry {
		// I don't think I need metric...
		desc.Default // encoded in default set I think
		desc.Labels // []string, combine with the emission from below []string values so need to persist this around wait but you're persisting the whole descriptor around...
		desc.OptionalLabels
		desc.Name // how it links with configuration...I discussed this all with Doug...
		desc // *desc - used as map pointer
	} // reuse with server - metrics set configuration, global...return a metrics set...type it out first...

	// Does it look like five seperate helpers...also figure out default metrics thingy...
	// yeah except set it as map key yeah five seperate helpers called from client and server

	// map key now vs. an index in a slice...but the creation type is still the same...

	// Do I even need to persist the name then or is the set ok since encodes
	// name...no need for fast access or should I iterate through metrics set
	// (ToSlice()?) passed into this

	// Need to switch to this and GetDesc out of the metrics registry...
	for metric := range metrics.Metrics { // This switched to GetMetrics...
		// don't create the non default ones, when get a handle for it won't be present
		// gets handle uncondtionally then otel won't record on anything
		// read nil in recording point

	}


	// Need to typecast the desc type...I think this is determiner, could persist this as just a set then...
	for _, desc := range estats.MetricsRegistry { // this will need to use metrics to read from the metrics reigstry...creates a count
		desc.Type // this type is what typecast
		// create and set for each type based off typecast above...

		// looks like I might need to persist around name, but let Doug comment
		// that and change it in this PR to a set if so...value as a bool would
		// be the tradeoff vs. a Name key so I think minor, I think fast access
		// it provides users/third parties is worth the complexity since the
		// complexity is the same...

		switch desc.Type {
		case estats.MetricTypeIntCount: // These create calls encode the no-op logic as well...yeah if user doesn't set and htis gets hit with a record call calls a no-op...simpler?
			ic := createInt64Counter(metrics.Metrics, stats.Metric(desc.Name), meter, otelmetric.WithUnit(desc.Unit), otelmetric.WithDescription(desc.Description))
			h.clientMetrics.intCounts[desc] = ic
		case estats.MetricTypeFloatCount:
			fc := createFloat64Counter(metrics.Metrics, stats.Metric(desc.Name), meter, otelmetric.WithUnit(desc.Unit), otelmetric.WithDescription(desc.Description))
			h.clientMetrics.floatCounts[desc] = fc
		case estats.MetricTypeIntHisto:
			ih := createInt64Histogram(metrics.Metrics, stats.Metric(desc.Name), meter, otelmetric.WithUnit(desc.Unit), otelmetric.WithDescription(desc.Description))
			h.clientMetrics.intHistos[desc] = ih
		case estats.MetricTypeFloatHisto:
			fh := createFloat64Histogram(metrics.Metrics, stats.Metric(desc.Name), meter, otelmetric.WithUnit(desc.Unit), otelmetric.WithDescription(desc.Description))
			h.clientMetrics.floatHistos[desc] = fh
		case estats.MetricTypeIntGauge:
			ig := createInt64Gauge(metrics.Metrics, stats.Metric(desc.Name), meter, otelmetric.WithUnit(desc.Unit), otelmetric.WithDescription(desc.Description))
			h.clientMetrics.intGauges[desc] = ig
		}
	}

	// Do this server side too...tests are the same except added dimension of
	// optional label being configured and default metric test that
	// functionality too...

	// The Metrics Recorder API's are also different now...

}

func (h *clientStatsHandler) RecordIntCount(handle estats.Int64CountHandle, incr int64, labels ...string) {
	// Already verified in layer below...
	/*handle.MetricDescriptor.Labels // []string - Combine this and OptionalLabels with the labels var arg to create attribute option, but another input optional labels...

	// User configured opitonal labels - combine as an input, if specified in
	// optional labels (loop through at each optional labels recording point...)

	h.options.MetricsOptions.OptionalLabels // []string
	handle.MetricDescriptor.OptionalLabels // []string -

	// values come in as a var arg string...

	// eventually I want an []otelattribute.KeyValue WithAttributes

	var attributes []otelattribute.KeyValue

	// Once it hits here lower level has guaranteed length
	// Algo (maybe pull out into helper):
	// always do label + label value
	for i, label := range handle.MetricDescriptor.Labels { // ordering encoded in append
		labels[i] // value...
		labels // []string, variadic args

		attributes = append(attributes, otelattribute.String(label, labels[i]))
	}

	// i + len (labels)

	for i, label := range handle.MetricDescriptor.OptionalLabels {
		for _, optLabel := range h.options.MetricsOptions.OptionalLabels {
			if label == optLabel { // represent opt label as a set to avoid string compares but this is capped at one anyway...or could build a set before for extra space complexity to save time...
				attributes = append(attributes, otelattribute.String(label, labels[i + len(handle.MetricDescriptor.Labels)]))
			}
		}
	}


	// optional labels are also conditional on what the user configures with...
	ao := otelmetric.WithAttributes(attributes...)*/

	// Figure out a clean way to refactor the attribute creation...
	// Here and everywhere else...what if these reads give nil
	ao := createAttributeOptionFromLabels(handle.MetricDescriptor.Labels, handle.MetricDescriptor.OptionalLabels, h.options.MetricsOptions.OptionalLabels, labels...)


	h.clientMetrics.intCounts[handle.MetricDescriptor].Add(h.options.MetricsOptions.Context, incr, ao)

}

// Copy this over to server too...
func (h *clientStatsHandler) RecordFloatCount(handle estats.Float64CountHandle, incr float64, labels ...string) {
	ao := createAttributeOptionFromLabels(handle.MetricDescriptor.Labels, handle.MetricDescriptor.OptionalLabels, h.options.MetricsOptions.OptionalLabels, labels...)
	h.clientMetrics.floatCounts[handle.MetricDescriptor].Add(h.options.MetricsOptions.Context, incr, ao)
}

func (h *clientStatsHandler) RecordIntHisto(handle estats.Int64HistoHandle, incr int64, labels ...string) {
	ao := createAttributeOptionFromLabels(handle.MetricDescriptor.Labels, handle.MetricDescriptor.OptionalLabels, h.options.MetricsOptions.OptionalLabels, labels...)

	// what happens if this read panics? It shouldn't...but be defensive at the cost of speed?
	// e.g. but many others intHistos[handle.MetricDescriptor] returns nil or something...

	h.clientMetrics.intHistos[handle.MetricDescriptor].Record(h.options.MetricsOptions.Context, incr, ao)
}

func (h *clientStatsHandler) RecordFloatHisto(handle estats.Float64HistoHandle, incr float64, labels ...string) {
	ao := createAttributeOptionFromLabels(handle.MetricDescriptor.Labels, handle.MetricDescriptor.OptionalLabels, h.options.MetricsOptions.OptionalLabels, labels...)
	h.clientMetrics.floatHistos[handle.MetricDescriptor].Record(h.options.MetricsOptions.Context, incr, ao)
}

func (h *clientStatsHandler) RecordIntGauge(handle estats.Int64GaugeHandle, incr int64, labels ...string) {
	ao := createAttributeOptionFromLabels(handle.MetricDescriptor.Labels, handle.MetricDescriptor.OptionalLabels, h.options.MetricsOptions.OptionalLabels, labels...)

	// what happens if this read panics? It shouldn't...but be defensive at the cost of speed?

	h.clientMetrics.intGauges[handle.MetricDescriptor].Record(h.options.MetricsOptions.Context, incr, ao)
}

func createAttributeOptionFromLabels(labelKeys []string, optionalLabelKeys []string, optionalLabels []string, labelVals ...string) otelmetric.MeasurementOption {
	var attributes []otelattribute.KeyValue


	// Or just have it be a closure on client stats handler for access to optional...
	// nah just take a string so can be reused on both sides...anything left to do for this except test it?


	// Once it hits here lower level has guaranteed length
	// Algo (maybe pull out into helper):
	// always do label + label value
	for i, label := range labelKeys {
		attributes = append(attributes, otelattribute.String(label, labelVals[i]))
	}

	for i, label := range optionalLabelKeys {
		for _, optLabel := range optionalLabels {
			if label == optLabel { // represent opt label as a set to avoid string compares but this is capped at one anyway...or could build a set before for extra space complexity to save time...
				attributes = append(attributes, otelattribute.String(label, labelKeys[i + len(labelKeys)]))
			}
		}
	} // unit test or test this as part of emission with corner casey scenarios...





	return otelmetric.WithAttributes(attributes...)
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
