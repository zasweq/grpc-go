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
	ocstats "go.opencensus.io/stats"
	"go.opencensus.io/tag"
	"google.golang.org/grpc/status"
	"io"
	"time"

	"go.opencensus.io/trace"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/stats"
)

var (
	joinDialOptions = internal.JoinDialOptions.(func(...grpc.DialOption) grpc.DialOption)
)

// TraceOptions are the tracing options for opencensus instrumentation.
type TraceOptions struct {
	// TS is the Sampler used for tracing.
	TS trace.Sampler
	// DisableTrace determines whether traces are disabled for an OpenCensus
	// Dial or Server option. will overwrite any global option setting.
	DisableTrace bool
}

// DialOption returns a dial option which enables OpenCensus instrumentation
// code for a grpc.ClientConn.
//
// Client applications interested in instrumenting their grpc.ClientConn should
// pass the dial option returned from this function as the first dial option to
// grpc.Dial().
//
// Using this option will always lead to instrumentation, however in order to
// use the data an exporter must be registered with the OpenCensus trace package
// for traces and the OpenCensus view package for metrics. Client side has
// retries, so a Unary and Streaming Interceptor are registered to handle per
// RPC traces/metrics, and a Stats Handler is registered to handle per RPC
// attempt trace/metrics. These three components registered work together in
// conjunction, and do not work standalone. It is not supported to use this
// alongside another stats handler dial option.
func DialOption(to TraceOptions) grpc.DialOption {
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
// for traces and the OpenCensus view package for metrics. Server side does not
// have retries, so a registered Stats Handler is the only option that is
// returned. It is not supported to use this alongside another stats handler
// server option.
func ServerOption(to TraceOptions) grpc.ServerOption {
	return grpc.StatsHandler(&serverStatsHandler{to: to})
}

// unaryInterceptor handles per RPC context management. It also handles per RPC
// tracing and stats.
func unaryInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	// "Start timestamp - After the client application starts the RPC."
	// record timestamp
	startTime := time.Now()
	// s to err := invoker...
	err := invoker(ctx, method, req, reply, cc, opts...)
	// record another timestamp
	// record: timestamp - timestamp...should this record on an error...including e2e time...I think I should record even on an error
	// "before the status of the rpc is delivered to the application" - so even errors...
	// "End timestamp - Before the status of the RPC is delivered to the
	// application."
	// right even on errors, distribution with errors in it too allows you to see it
	callLatency := float64(time.Since(startTime)) / float64(time.Millisecond)

	// Do it before calculating
	// errToCanonicalString
	// st := errToCanonicalString
	var st string
	if err != nil {
		s, _ := status.FromError(err)
		st = canonicalString(s.Code())
	} else {
		st = "OK"
	}
	// can we ignore here - yeah just returns a codes.Unknown if an error is plumbed

	// I do need a measure
	ocstats.RecordWithOptions(ctx /*is this the right context*/,
		ocstats.WithTags(
		// yeah this is client duh...this
		tag.Upsert(keyClientMethod, removeLeadingSlash(method)), // do I need to pull the leading slash off method here...?
		// This status dimension is handled by row iterations....status + method name in row...
		tag.Upsert(keyClientStatus, st), // st is plumbed into data End...now you need to pull status from error...
		),
		ocstats.WithMeasurements(
			// clientCallLatency.M(latency)
			clientApiLatency.M(callLatency),
		), // I think views aggregate automatically...s=and upload every interval or when you unregister
	)

	// return err <- this has status plumbed into it, so just needs to happen beforehand
	return err
}

// just needs to know status

// wrappedStream wraps a grpc.ClientStream to intercept the RecvMsg call to know when RPC ends?
type wrappedStream struct {
	grpc.ClientStream
	// needs start time stamp persisted (or pass through context) in order to take the measure of the diff
	// time after client started rpc
	startTime time.Time
	// persist method otherwise would need something like blocking on channel or something
	// the method for this wrapped stream
	method string
}

// bw.Balancer = newBalancer
func newWrappedStream(s grpc.ClientStream) grpc.ClientStream {
	return &wrappedStream{ClientStream: s} // is this the correct/preferred way to embed? look at gsb
}

/*
RecvMsg blocks until it receives a message into m or the stream is done. It
returns io.EOF when the stream completes successfully. On any other error, the
stream is aborted and the error contains the RPC status.
*/

func (ws *wrappedStream) RecvMsg(m interface{}) error {
	// m gets populated need to populate

	// blocks until:
	// 1. receives a msg into m (I'm assuming err == nil)
	// 2. returns io.EOF (stream completed successfully) is this handled by status.FromError, take timestamp and what is rpc status
	// 3. On any other error, stream is aborted and error contains the RPC status. (this you have status for recording, take timestamp)

	//

	err := ws.ClientStream.RecvMsg(m) // this will either return err or nil + populate msg passed in
	// this event/signal is when the interceptor knows the status

	// two things can happen:
	// err = nil: (rpc isn't over) early return
	if err == nil {
		// RPC isn't over yet, no need to take measurement.
		return nil // or just return err
	}

	// either one of these errors needs to take latency
	callLatency := float64(time.Since(ws.startTime)) / float64(time.Millisecond)

	/*
	if err != nil {
			s, _ := status.FromError(err)
			st = canonicalString(s.Code())
		} else {
			st = "OK"
		}
	*/
	var st string
	if err == io.EOF { // converting error -> status, but you still want to return this error
		st = "OK"
	} else {
		s, _ := status.FromError(err)
		st = canonicalString(s.Code())
	}
	// err != nil: both types you need to record no matter what
	//       io.EOF?, does this convert to status codes right status ok
	//       other errors?

	// status.FromError(err) // codes.Unknown if incompatible with status package
	ocstats.RecordWithOptions(context.Background() /*this might be wrong...what context from the given options in context well just test it and see if it works, if not rearchitect system*/,
		ocstats.WithTags(
			tag.Upsert(keyClientMethod, ws.method),
			tag.Upsert(keyClientStatus, st),
		),
		ocstats.WithMeasurements(
			clientApiLatency.M(callLatency),
		),
	)
	return err
}

// streamInterceptor handles per RPC context management. It also handles per RPC
// tracing and stats.
func streamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	// only clause is "After the client application starts the RPC."
	startTime := time.Now()

	// stream, err := streamer(ctx, desc, cc, method, opts...) // how does this work again, this gives you a stream and err
	// status comes from err?

	// Return a wrapped stream that intercepts operations -
	// operations block until something happens right - so stream.Recv receives error?

	// operation is simply "before the status of the rpc is delivered to the application"

	// finishes timestamp with closure
	// Interceptor knows the status - so interception when this thing knows status
	// stream.Context()
	// stream.Header()
	/*
	RecvMsg blocks until it receives a message into m or the stream is done. It
	returns io.EOF when the stream completes successfully. On any other error,
	the stream is aborted and the error contains the RPC status.
	*/
	// what operations trigger this interceptor knowing the status of rpcs? I think just recv msg
	// stream.RecvMsg() // receives a message into m and returns an error, need to handle error?
	// stream.CloseSend() // does this thing call recv msg - closes send direction of stream
	// stream.SendMsg() // you can check status by calling recv msg to make sure this was processed by server and rpc is complete
	// stream.Trailer() // after recv has returned a non nil error (including io.EOF) or CloseAndRecv has returned
	// which one of these plumbs status code? and how to intercept?

	s, err := streamer(ctx, desc, cc, method, opts...)
	if err != nil {
		return nil, err // do we want this...
	}

	return &wrappedStream{ // I don't think we want a helper for heap construction unless it requires an extra layer
		ClientStream: s,
		startTime: startTime,
		method: method,
	}, nil

	// return streamer(ctx, desc, cc, method, opts...)
	// When does this rpc end?...more important for things like precision for retry metrics
	// clause for metric is "before the status of the rpc is delivered to the application"

	// Convert status here as well...so maybe really make it a helper

}

type clientStatsHandler struct {
	to TraceOptions
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

func (csh *clientStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	recordRPCData(ctx, rs)
}

type serverStatsHandler struct {
	to TraceOptions
}

// TagConn exists to satisfy stats.Handler.
func (ssh *serverStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

// HandleConn exists to satisfy stats.Handler.
func (ssh *serverStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// TagRPC implements per RPC context management.
func (ssh *serverStatsHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	ctx = ssh.statsTagRPC(ctx, rti)
	return ctx
}

// HandleRPC implements per RPC tracing and stats implementation.
func (ssh *serverStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	recordRPCData(ctx, rs)
}


// oh also need to switch o11y callsites to use this and write tests and hopefully Stanley's tests work too
