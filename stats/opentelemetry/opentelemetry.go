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

// Package opentelemetry implements opencensus instrumentation code for gRPC-Go
// clients and servers.
package opentelemetry

import (
	"context"
	"strings"
	"time"

	"go.opentelemetry.io/otel/metric"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal"
)

var logger = grpclog.Component("opentelemetry-instrumentation")

var canonicalString = internal.CanonicalString.(func(codes.Code) string)

var (
	joinDialOptions = internal.JoinDialOptions.(func(...grpc.DialOption) grpc.DialOption)
)

// Metrics is a set of metrics to initialize. Once created, metrics is
// immutable.
type Metrics struct {
	// default string
	// map string -> bool
	metrics map[string]bool // loop through this thing when creating...
}

// (default set), enable1, disable1, disableall (add and remove ... metrics)
// start an empty one if don't want, or start with defaults


// empty then add or remove (represents clear all)

// default then add or remove (represents clear all)

// return a new map entirely - immutable

// AddImmu adds the metrics and returns a new copy
func (m *Metrics) Add(metrics ...string) *Metrics {
	newMetrics := make(map[string]bool)
	for metric := range m.metrics {
		newMetrics[metric] = true
	}

	for _, metric := range metrics {
		newMetrics[metric] = true
	}
	return &Metrics{
		metrics: newMetrics,
	}
}

func (m *Metrics) Remove(metrics ...string) *Metrics {
	newMetrics := make(map[string]bool)
	for metric := range m.metrics {
		newMetrics[metric] = true
	}

	for _, metric := range metrics {
		delete(newMetrics, metric)
	}
	return &Metrics{
		metrics: newMetrics,
	}
}

// MetricsOptions are the metrics options for OpenTelemetry instrumentation.
type MetricsOptions struct {
	// MeterProvider is the MeterProvider instance that will be used for access
	// to Named Meter instances to instrument an application. To enable metrics
	// collection, set a meter provider. If unset, no metrics will be recorded.
	// Any implementation knobs (i.e. views, bounds) set in the passed in object
	// take precedence over the API calls from the interface in this component
	// (i.e. it will create default views for unset views).
	MeterProvider metric.MeterProvider


	// *** Do I need these? ***
	// Metrics are the metrics to instrument. Will turn on the corresponding
	// metric supported by the client and server instrumentation components if
	// applicable.
	Metrics Metrics // Unconditionally register all metrics. (see gRFC)
	// will wrap with way we decided with enable, disable, disable all (with certain that are default)

	// *** Do I need these? ***
	// could I do a disable/enable on a set helper as you construct it? default +
	// others (try that)



	// TargetAttributeFilter is a callback that takes the target string and
	// returns a bool representing whether to use target as a label value or use
	// the string "other". If unset, will use the target string as is.
	TargetAttributeFilter func(string) bool
	// MethodAttributeFilter is a callback that takes the method string and
	// returns a bool representing whether to use method as a label value or use
	// the string "other". If unset, will use the method string as is. This is
	// used only for generic methods, and not registered methods.
	MethodAttributeFilter func(string) bool // *Done with these*
}

// use CanonicalTarget() helper on ClientConn (where does this component have access to it?)
// to record target attribute *Done* (need to test all of these)

// security...
// read isStaticMethod call option (I already wrote code for this somewhere...)
// client side to see if it works...

// server side read the pointer to server (I think I already have this done)

// Finished, but will need to test thee behaviors ^^^



// DialOption returns a dial option which enables OpenTelemetry instrumentation
// code for a grpc.ClientConn.
//
// Client applications interested in instrumenting their grpc.ClientConn should
// pass the dial option returned from this function as a dial option to
// grpc.Dial().
//
// For the metrics supported by this instrumentation code, a user needs to
// specify the client metrics to record in metrics options. A user also needs to
// provide an implementation of a MeterProvider. If the passed in Meter Provider
// does not have the view configured for an individual metric turned on, the API
// call in this component will create a default view for that metric.

// Talk about how to instrument here...like setting names of metrics and Meter Provider (which creates default views/bounds/instruments? if not set by caller)
// and also needs an exporter (which contains a metric reader inside it) to actually see recorded metrics.
func DialOption(mo MetricsOptions) grpc.DialOption {
	csh := &clientStatsHandler{mo: mo}
	csh.initializeMetrics()
	return joinDialOptions(grpc.WithChainUnaryInterceptor(csh.unaryInterceptor), grpc.WithStreamInterceptor(csh.streamInterceptor), grpc.WithStatsHandler(csh))
}


// ServerOption returns a server option which enables OpenTelemetry
// instrumentation code for a grpc.Server.
//
// Server applications interested in instrumenting their grpc.Server should pass
// the server option returned from this function as an argument to
// grpc.NewServer().
//
// For the metrics supported by this instrumentation code, a user needs to
// specify the client metrics to record in metrics options. A user also needs to
// provide an implementation of a MeterProvider. If the passed in Meter Provider
// does not have the view configured for an individual metric turned on, the API
// call in this component will create a default view for that metric.

// Talk about how to instrument here...like setting names of metrics and Meter Provider (which creates default views/bounds/instruments? if not set by caller)
// and also needs an exporter (which contains a metric reader inside it) to actually see recorded metrics.
func ServerOption(mo MetricsOptions) grpc.ServerOption {
	ssh := &serverStatsHandler{mo: mo}
	ssh.initializeMetrics() // build "fixed" "hardcoded"?
	return grpc.StatsHandler(ssh)
}

// callInfo is information pertaining to the lifespan of the RPC client side.
type callInfo struct {
	target string

	method string
}

type callInfoKey struct {}

func setCallInfo(ctx context.Context, ci *callInfo) context.Context {
	return context.WithValue(ctx, callInfoKey{}, ci)
}

// getCallInfo returns the callInfo stored in the context, or nil
// if there isn't one.
func getCallInfo(ctx context.Context) *callInfo { // if this errors in attempt component error out right? Or should this set method to empty string if call info isn't set?
	ci, _ := ctx.Value(callInfoKey{}).(*callInfo)
	return ci
}

// retry delay per call (A45)...through interceptor will be wrt the talking
// between interceptor and stats handler right...actually retry stats are
// handled in top level call object.

// rpcInfo is RPC information scoped to the RPC attempt life span client side,
// and the RPC life span server side.
type rpcInfo struct {
	mi *metricsInfo
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

// metricsInfo is RPC information scoped to the RPC attempt life span client
// side, and the RPC life span server side.
type metricsInfo struct {
	// access these counts atomically for hedging in the future:
	// number of bytes after compression (within each message) from side (client || server)
	sentCompressedBytes int64
	// number of compressed bytes received (within each message) received on
	// side (client || server)
	recvCompressedBytes int64

	startTime time.Time
	method    string
	authority string
}

// built out at client/server handler init time
// nil pointers mean don't record, populate with a pointer
// count if set, record on pointer.
type registeredMetrics struct { // nil or not nil means presence
	// "grpc.client.attempt.started"
	clientAttemptStarted metric.Int64Counter
	// "grpc.client.attempt.duration"
	clientAttemptDuration metric.Float64Histogram
	// "grpc.client.attempt.sent_total_compressed_message_size"
	clientAttemptSentTotalCompressedMessageSize metric.Int64Histogram
	// "grpc.client.attempt.rcvd_total_compressed_message_size"
	clientAttemptRcvdTotalCompressedMessageSize metric.Int64Histogram

	// per call client metrics:
	clientCallDuration metric.Float64Histogram

	// "grpc.server.call.started"
	serverCallStarted metric.Int64Counter
	// "grpc.server.call.sent_total_compressed_message_size"
	serverCallSentTotalCompressedMessageSize metric.Int64Histogram
	// "grpc.server.call.rcvd_total_compressed_message_size"
	serverCallRcvdTotalCompressedMessageSize metric.Int64Histogram
	// "grpc.server.call.duration"
	serverCallDuration metric.Float64Histogram
} // use this for both client and server side, so that way you can have metrics recording for both (and Yash mentioned orthogonal metrics that aren't tied to client or server)


// start pulling these into different files and cleaning up*** :)

var EmptyMetrics = Metrics{}
