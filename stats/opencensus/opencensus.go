/*
 * Copyright 2022 gRPC authors.
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

// Package opencensus implements opencensus instrumentation code for gRPC-Go
// clients and servers.
package opencensus

import (
	"context"
	"go.opencensus.io/trace"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/stats"
)

var (
	joinDialOptions = internal.JoinDialOptions.(func(...grpc.DialOption) grpc.DialOption)
)

type traceOptions struct {
	ts trace.Sampler // is this allowed I feel like it will glitch eh
	// // if set, won't record the traces for this, will be set by o11y module
	// for a client conn that you pass...but it's set global dial option so
	// perhaps have this bool override if set...
	disableTrace bool
}

// DialOption returns a dial option which enables OpenCensus instrumentation
// code for a grpc.ClientConn.
//
// Client Applications interested in instrumenting their grpc.ClientConn should
// pass the dial option returned from this function as the first dial option to
// grpc.Dial().
//
// Using this option will always lead to instrumentation, however in order to
// use the data an exporter must be registered with the OpenCensus trace package
// for traces and the OpenCensus view package for metrics. Client Side has
// retries, so a Unary and Streaming Interceptor are registered to handle per
// RPC traces/metrics, and a Stats Handler is registered to handle per RPC
// attempt trace/metrics. These three components registered work together in
// conjunction, and do not work standalone.
func DialOption(to traceOptions /*<- pointer or not pointer?*/) grpc.DialOption {
	// trace.StartOptions...right now contains sampling rate and span kind, but doesn't even use span kind
	// just needs a {
	//     trace.Sampler
	//     disableTracing
	// }
	// add selective disablement
	// disableTrace bool to start options, does it need to be that type?
	return joinDialOptions(grpc.WithChainUnaryInterceptor(unaryInterceptor), grpc.WithChainStreamInterceptor(streamInterceptor), grpc.WithStatsHandler(&clientStatsHandler{to: to}))
}

// ServerOption returns a server option which enables OpenCensus instrumentation
// code for a grpc.Server.
//
// Server applications interested in instrumenting their grpc.Server should
// pass the server option returned from this function as the first argument to
// grpc.NewServer().
//
// Using this option will always lead to instrumentation, however in order to
// use the data an exporter must be registered with the OpenCensus trace package
// for traces and the OpenCensus view package for metrics. Server Side does not
// have retries, so a registered Stats Handler is the only component you need
// for RPC traces/metrics.
func ServerOption(to traceOptions) grpc.ServerOption {
	return grpc.StatsHandler(&serverStatsHandler{to: to})
}

// unaryInterceptor handles per RPC context management. It also handles per RPC
// tracing and stats.
func unaryInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	return invoker(ctx, method, req, reply, cc, opts...)
}

// streamInterceptor handles per RPC context management. It also handles per RPC
// tracing and stats.
func streamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	return streamer(ctx, desc, cc, method, opts...)
}

type clientStatsHandler struct {
	to traceOptions // read in traces rewrite (pending) for disablement of traces and also sampling rate
}

// TagConn exists to satisfy stats.Handler.
func (csh *clientStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn exists to satisfy stats.Handler.
func (csh *clientStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// TagRPC implements per RPC attempt context management.
func (csh *clientStatsHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	ctx = csh.statsTagRPC(ctx, rti)
	return ctx
}

// HandleRPC handles per RPC attempt tracing and stats instrumentation. - per
// rpc attempt trace as well...I thought the linkage of them was already there?
func (csh *clientStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	statsHandleRPC(ctx, rs)
}

type serverStatsHandler struct {
	to traceOptions // read in traces rewrite (pending) for disablement of traces and sampling rate
}

// TagConn exists to satisfy stats.Handler.
func (ssh *serverStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn exists to satisfy stats.Handler.
func (ssh *serverStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// TagRPC implements per RPC context management.
func (ssh *serverStatsHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	ctx = ssh.statsTagRPC(ctx, rti) // maybe restructure this and have it inline? this feels weird?
	return ctx
}

// HandleRPC implements per RPC tracing and stats implementation.
func (ssh *serverStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	statsHandleRPC(ctx, rs)
}
