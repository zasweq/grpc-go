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

// Package opentelemetry implements opentelemetry instrumentation code for
// gRPC-Go clients and servers.
package opentelemetry

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	estats "google.golang.org/grpc/experimental/stats"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal"
	otelinternal "google.golang.org/grpc/stats/opentelemetry/internal"

	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

func init() {
	otelinternal.SetPluginOption = func(o *Options, po otelinternal.PluginOption) {
		o.MetricsOptions.pluginOption = po
	}
}

var logger = grpclog.Component("otel-plugin")

var canonicalString = internal.CanonicalString.(func(codes.Code) string)

var joinDialOptions = internal.JoinDialOptions.(func(...grpc.DialOption) grpc.DialOption)

// Options are the options for OpenTelemetry instrumentation.
type Options struct {
	// MetricsOptions are the metrics options for OpenTelemetry instrumentation.
	MetricsOptions MetricsOptions
}

// MetricsOptions are the metrics options for OpenTelemetry instrumentation.
type MetricsOptions struct {
	// MeterProvider is the MeterProvider instance that will be used to create
	// instruments. To enable metrics collection, set a meter provider. If
	// unset, no metrics will be recorded. Any implementation knobs (i.e. views,
	// bounds) set in the MeterProvider take precedence over the API calls from
	// this interface. (i.e. it will create default views for unset views).
	MeterProvider otelmetric.MeterProvider

	// Metrics are the metrics to instrument. Will create instrument and record telemetry
	// for corresponding metric supported by the client and server
	// instrumentation components if applicable. If not set, the default metrics
	// will be recorded.
	Metrics *estats.Metrics

	// MethodAttributeFilter is to record the method name of RPCs handled by
	// grpc.UnknownServiceHandler, but take care to limit the values allowed, as
	// allowing too many will increase cardinality and could cause severe memory
	// or performance problems. On Client Side, pass a
	// grpc.StaticMethodCallOption as a call option into Invoke or NewStream.
	// This only applies for server side metrics.
	MethodAttributeFilter func(string) bool

	// OptionalLabels are labels received from LB Policies that this component
	// should add to metrics that record after receiving incoming metadata.
	OptionalLabels []string

	// pluginOption is used to get labels to attach to certain metrics, if set.
	pluginOption otelinternal.PluginOption
	// Context is the context of the OpenTelemetry plugin. If unset, will
	// default to a context.Background.
	Context context.Context
}

// DialOption returns a dial option which enables OpenTelemetry instrumentation
// code for a grpc.ClientConn.
//
// Client applications interested in instrumenting their grpc.ClientConn should
// pass the dial option returned from this function as a dial option to
// grpc.NewClient().
//
// For the metrics supported by this instrumentation code, specify the client
// metrics to record in metrics options. Also provide an implementation of a
// MeterProvider. If the passed in Meter Provider does not have the view
// configured for an individual metric turned on, the API call in this component
// will create a default view for that metric.
func DialOption(o Options) grpc.DialOption {
	if o.MetricsOptions.Context == nil { // This context will be important for tracing too so put it in top level object? This writes to a copied struct so I think we're good here...
		o.MetricsOptions.Context = context.Background()
	}
	csh := &clientStatsHandler{options: o}
	csh.initializeMetrics()
	return joinDialOptions(grpc.WithChainUnaryInterceptor(csh.unaryInterceptor), grpc.WithChainStreamInterceptor(csh.streamInterceptor), grpc.WithStatsHandler(csh))
}

var joinServerOptions = internal.JoinServerOptions.(func(...grpc.ServerOption) grpc.ServerOption)

// ServerOption returns a server option which enables OpenTelemetry
// instrumentation code for a grpc.Server.
//
// Server applications interested in instrumenting their grpc.Server should pass
// the server option returned from this function as an argument to
// grpc.NewServer().
//
// For the metrics supported by this instrumentation code, specify the server
// metrics to record in metrics options. Also provide an implementation of a
// MeterProvider. If the passed in Meter Provider does not have the view
// configured for an individual metric turned on, the API call in this component
// will create a default view for that metric.
func ServerOption(o Options) grpc.ServerOption {
	if o.MetricsOptions.Context == nil { // This context will be important for tracing too so put it in top level object? This writes to a copied struct so I think we're good here...
		o.MetricsOptions.Context = context.Background()
	}
	ssh := &serverStatsHandler{options: o}
	ssh.initializeMetrics()
	return joinServerOptions(grpc.ChainUnaryInterceptor(ssh.unaryInterceptor), grpc.ChainStreamInterceptor(ssh.streamInterceptor), grpc.StatsHandler(ssh))
}

// callInfo is information pertaining to the lifespan of the RPC client side.
type callInfo struct {
	target string

	method string
}

type callInfoKey struct{}

func setCallInfo(ctx context.Context, ci *callInfo) context.Context {
	return context.WithValue(ctx, callInfoKey{}, ci)
}

// getCallInfo returns the callInfo stored in the context, or nil
// if there isn't one.
func getCallInfo(ctx context.Context) *callInfo {
	ci, _ := ctx.Value(callInfoKey{}).(*callInfo)
	return ci
}

// rpcInfo is RPC information scoped to the RPC attempt life span client side,
// and the RPC life span server side.
type rpcInfo struct {
	ai *attemptInfo
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

func removeLeadingSlash(mn string) string {
	return strings.TrimLeft(mn, "/")
}

// attemptInfo is RPC information scoped to the RPC attempt life span client
// side, and the RPC life span server side.
type attemptInfo struct {
	// access these counts atomically for hedging in the future:
	// number of bytes after compression (within each message) from side (client
	// || server).
	sentCompressedBytes int64
	// number of compressed bytes received (within each message) received on
	// side (client || server).
	recvCompressedBytes int64

	startTime time.Time
	method    string

	pluginOptionLabels map[string]string // pluginOptionLabels to attach to metrics emitted
	xdsLabels          map[string]string
}

type clientMetrics struct {
	// "grpc.client.attempt.started"
	attemptStarted otelmetric.Int64Counter
	// "grpc.client.attempt.duration"
	attemptDuration otelmetric.Float64Histogram
	// "grpc.client.attempt.sent_total_compressed_message_size"
	attemptSentTotalCompressedMessageSize otelmetric.Int64Histogram
	// "grpc.client.attempt.rcvd_total_compressed_message_size"
	attemptRcvdTotalCompressedMessageSize otelmetric.Int64Histogram

	// "grpc.client.call.duration"
	callDuration otelmetric.Float64Histogram

	// Metrics from metrics registry
	intCounts   map[*estats.MetricDescriptor]otelmetric.Int64Counter
	floatCounts map[*estats.MetricDescriptor]otelmetric.Float64Counter
	intHistos   map[*estats.MetricDescriptor]otelmetric.Int64Histogram
	floatHistos map[*estats.MetricDescriptor]otelmetric.Float64Histogram
	intGauges   map[*estats.MetricDescriptor]otelmetric.Int64Gauge
} // I don't think I need comments outside maybe saying these are non per call...

type serverMetrics struct {
	// "grpc.server.call.started"
	callStarted otelmetric.Int64Counter
	// "grpc.server.call.sent_total_compressed_message_size"
	callSentTotalCompressedMessageSize otelmetric.Int64Histogram
	// "grpc.server.call.rcvd_total_compressed_message_size"
	callRcvdTotalCompressedMessageSize otelmetric.Int64Histogram
	// "grpc.server.call.duration"
	callDuration otelmetric.Float64Histogram

	intCounts   map[*estats.MetricDescriptor]otelmetric.Int64Counter
	floatCounts map[*estats.MetricDescriptor]otelmetric.Float64Counter
	intHistos   map[*estats.MetricDescriptor]otelmetric.Int64Histogram
	floatHistos map[*estats.MetricDescriptor]otelmetric.Float64Histogram
	intGauges   map[*estats.MetricDescriptor]otelmetric.Int64Gauge
}


func createInt64Counter(setOfMetrics map[estats.Metric]bool, metricName estats.Metric, meter otelmetric.Meter, options ...otelmetric.Int64CounterOption) otelmetric.Int64Counter {
	if _, ok := setOfMetrics[metricName]; !ok {
		return noop.Int64Counter{}
	}
	ret, err := meter.Int64Counter(string(metricName), options...)
	if err != nil {
		logger.Errorf("failed to register metric \"%v\", will not record", metricName)
		return noop.Int64Counter{}
	}
	return ret
}


func createFloat64Counter(setOfMetrics map[estats.Metric]bool, metricName estats.Metric, meter otelmetric.Meter, options ...otelmetric.Float64CounterOption) otelmetric.Float64Counter {
	if _, ok := setOfMetrics[metricName]; !ok {
		return noop.Float64Counter{}
	}
	ret, err := meter.Float64Counter(string(metricName), options...) // I could typecast at callsite too
	if err != nil {
		logger.Errorf("failed to register metric \"%v\", will not record", metricName)
		return noop.Float64Counter{}
	}
	return ret
}

func createInt64Histogram(setOfMetrics map[estats.Metric]bool, metricName estats.Metric, meter otelmetric.Meter, options ...otelmetric.Int64HistogramOption) otelmetric.Int64Histogram {

	if _, ok := setOfMetrics[metricName]; !ok {
		return noop.Int64Histogram{}
	}
	ret, err := meter.Int64Histogram(string(metricName), options...)
	if err != nil {
		logger.Errorf("failed to register metric \"%v\", will not record", metricName)
		return noop.Int64Histogram{}
	}
	return ret
}

func createFloat64Histogram(setOfMetrics map[estats.Metric]bool, metricName estats.Metric, meter otelmetric.Meter, options ...otelmetric.Float64HistogramOption) otelmetric.Float64Histogram {
	if _, ok := setOfMetrics[metricName]; !ok {
		return noop.Float64Histogram{}
	}
	ret, err := meter.Float64Histogram(string(metricName), options...)
	if err != nil {
		logger.Errorf("failed to register metric \"%v\", will not record", metricName)
		return noop.Float64Histogram{}
	}
	return ret
}

func createInt64Gauge(setOfMetrics map[estats.Metric]bool, metricName estats.Metric, meter otelmetric.Meter, options ...otelmetric.Int64GaugeOption) otelmetric.Int64Gauge {
	ret, err := meter.Int64Gauge(string(metricName), options...) // switch metric package to otelmetric for this file as well :)
	if err != nil {
		logger.Errorf("failed to register metric \"%v\", will not record", metricName)
		return noop.Int64Gauge{}
	}
	return ret
}

// Users of this component should use these bucket boundaries as part of their
// SDK MeterProvider passed in. This component sends this as "advice" to the
// API, which works, however this stability is not guaranteed, so for safety the
// SDK Meter Provider provided should set these bounds for corresponding
// metrics.
var (
	// DefaultLatencyBounds are the default bounds for latency metrics.
	DefaultLatencyBounds = []float64{0, 0.00001, 0.00005, 0.0001, 0.0003, 0.0006, 0.0008, 0.001, 0.002, 0.003, 0.004, 0.005, 0.006, 0.008, 0.01, 0.013, 0.016, 0.02, 0.025, 0.03, 0.04, 0.05, 0.065, 0.08, 0.1, 0.13, 0.16, 0.2, 0.25, 0.3, 0.4, 0.5, 0.65, 0.8, 1, 2, 5, 10, 20, 50, 100} // provide "advice" through API, SDK should set this too
	// DefaultSizeBounds are the default bounds for metrics which record size.
	DefaultSizeBounds = []float64{0, 1024, 2048, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864, 268435456, 1073741824, 4294967296}
	// DefaultPerCallMetrics are the default metrics provided by this module.
	DefaultPerCallMetrics = estats.NewMetrics(ClientAttemptStarted, ClientAttemptDuration, ClientAttemptSentCompressedTotalMessageSize, ClientAttemptRcvdCompressedTotalMessageSize, ClientCallDuration, ServerCallStarted, ServerCallSentCompressedTotalMessageSize, ServerCallRcvdCompressedTotalMessageSize, ServerCallDuration)
)

// DefaultMetrics returns a set of default metrics for per call joined with any
// default metrics registered through metrics registry.
//
// This should only be invoked after init time.
func DefaultMetrics() *estats.Metrics { // runtime join with defaults...
	return DefaultPerCallMetrics.Join(estats.DefaultMetrics)
}

// Test this with my e2e flow...
// Register some but not others...or should this be a unit test...look at diff to see behaviors

// metrics test infrastructure will need to be reused for RLS anyway...what knobs to provide it...?




// same thing as opentelemetry-use-instrument-registry (pull it up on GitHub)...

// implement default metrics as a runtime join...
// optional label filtering through same API (persists around the label values so I'm good here...)
// layer below just does length check

// instead of five slices
// five maps I think...
// it's a top level map[*MetricDescriptor]->instrument type

// Metrics set combine with runtime, still configured by string, so compare
// string to global registry... all of this stuff happens in the init function
// that is where I should check...

// Client and server


