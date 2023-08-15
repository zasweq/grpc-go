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

// what is the global default for MeterProvider? the no-op or the SDK

// I think we still want this to be dynamic...
var (
	joinDialOptions = internal.JoinDialOptions.(func(...grpc.DialOption) grpc.DialOption)
)

// make sure to mark as experimental, this is in flux.

// traceoptions and metricsOptions?

// takes this at registration time...not dynamic

// MetricsOptions are the metrics options for OpenTelemetry instrumentation.
type MetricsOptions struct {
	// MeterProvider is the MeterProvider instance that will be used for access
	// to Named Meter instances to instrument an application. If unset, no
	// metrics will be supported. The implementation knobs of this field take
	// precedence over the API calls from the field in this component.
	MeterProvider metric.MeterProvider // To enable metrics collection, set a meter provider. If unset, no metrics will be recorded. Merge this in with ^^^.

	// This is api ^^^, my api will just be a []string that defaults are exposed to users and then you can set or not...

	// Export Default Metrics...take this and build a set and you have that set part of my algorithm

	// Metrics are the metrics to instrument. Will turn on the corresponding
	// metric supported by this component if applicable.
	Metrics []string // export the default names like opencensus


}

// DialOption returns a dial option which enables OpenTelemetry instrumentation
// code for a grpc.ClientConn.
//
// Client applications interested in instrumenting their grpc.ClientConn should
// pass the dial option returned from this function as the first dial option to
// grpc.Dial().
//
// Talk about how to instrument here...like setting names of metrics and Meter Provider (which creates default views/bounds/instruments? if not set by caller)
// and also needs an exporter (which contains a metric reader inside it) to actually see recorded metrics.
func DialOption(mo MetricsOptions) grpc.DialOption {
	csh := &clientStatsHandler{mo: mo}
	csh.buildMetricsDataStructuresAtInitTime() // perhaps make this a constructor...that links the two together what two interceptors? Or operations?
	return joinDialOptions(grpc.WithChainUnaryInterceptor(csh.unaryInterceptor), grpc.WithStreamInterceptor(csh.streamInterceptor), grpc.WithStatsHandler(csh))
} // multiple components by grpc.DialOption, grpc.DialOption, need to add a unit test eh this isn't even speced out...


// ServerOption returns a server option which enables OpenTelemetry
// instrumentation code for a grpc.Server.
//
// Server applications interested in instrumenting their grpc.Server should pass
// the server option returned from this function as the first argument to
// grpc.NewServer().
//
// Talk about how to instrument here...like setting names of metrics and Meter Provider (which creates default views/bounds/instruments? if not set by caller)
// and also needs an exporter (which contains a metric reader inside it) to actually see recorded metrics.
func ServerOption(mo MetricsOptions) grpc.ServerOption {
	ssh := &serverStatsHandler{mo: mo}
	ssh.buildMetricsDataStructuresAtInitTime() // or make it as part of new?...make one constructor. Build or something like that?
	return grpc.StatsHandler(&serverStatsHandler{mo: mo})
}

// opencensus top level span + timestamp (only call metric outside) we also will
// have retry metrics which need to persist a counter so add something to
// context at interceptor level, Doug mentioned because the Dial Option always
// has both components it's fine to assume need both to function properly, not
// an API within a component

// is mutable over the call lifespan as per Java
type callInfo struct { // mutable, will be written to for timestamps for time without attempts...
	// top level span - in OpenCensus starts span from ctx for attempt and for call holds a local var.

	target string

	// Eventually for retry metrics some sort of full time - Doug mentioned communication between these two components is handled by stats handler

	// TODO: When implementing retry metrics, this top level call object will be
	// mutable and record time with no call attempt.
}

type callInfoKey struct {}

func setCallInfo(ctx context.Context, ci *callInfo) context.Context {
	return context.WithValue(ctx, callInfoKey{}, ci)
}

// getCallInfo returns the callInfo stored in the context, or nil
// if there isn't one.
func getCallInfo(ctx context.Context) *callInfo { // if this errors in attempt component error out right?
	ci, _ := ctx.Value(callInfoKey{}).(*callInfo)
	return ci
}

// rename metrics info to this? no call info
type attemptInfo struct { // created at beginning of stats handler tag, scoped to attempt, data needed per attempt and/attempt is handled
	// I think this is mi and ti
}

// retry delay per call (A45)...through interceptor will be wrt the talking between interceptor and stats handler right...
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
	method  string
	authority string
}

type registeredMetrics struct { // nil or not nil means presence
	// "grpc.client.attempt.started"
	clientAttemptStarted metric.Int64Counter
	// "grpc.client.attempt.duration"
	clientAttemptDuration metric.Float64Histogram
	// "grpc.client.attempt.sent_total_compressed_message_size"
	clientAttemptSentTotalCompressedMessageSize metric.Int64Histogram
	// "grpc.client.attempt.rcvd_total_compressed_message_size"
	clientAttemptRcvdTotalCompressedMessageSize metric.Int64Histogram

	// per call metrics - wait no that's at higher level, figure out how to plumb all these objects up there
	clientCallDuration metric.Float64Histogram

	// "grpc.server.call.started"
	serverCallStarted metric.Int64Counter // /call, needs method and authority
	// "grpc.server.call.sent_total_compressed_message_size"
	serverCallSentTotalCompressedMessageSize metric.Int64Histogram
	// "grpc.server.call.rcvd_total_compressed_message_size"
	serverCallRcvdTotalCompressedMessageSize metric.Int64Histogram
	// "grpc.server.call.duration"
	serverCallDuration metric.Float64Histogram
} // use this for both client and server side, so that way you can have metrics recording for both (and Yash mentioned orthogonal metrics that aren't tied to client or server)


// start pulling these into different files and cleaning up*** :)
