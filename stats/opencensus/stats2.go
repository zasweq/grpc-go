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

package opencensus

import (
	"context"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/stats"
	"time"
)

// do this new way?
var logger = grpclog.Component("opencensus-instrumentation")

var rpcDataKey = "rpc-data-key" // see other components to keep structure

// declare the bounds here...need to reuse the bounds for testing...
// the bounds are speced out and defined strongly

// make the tags unexported...plus, try to reuse them for testing...?

// per rpc attempt client side
// per rpc (no concept of attempts) server side
// as that's the nature of how stats handlers are called
type rpcData struct {
	// a bunch of int64 stats here...

	startTime time.Time // is this still the correct way to count latency?
	method    string
}


// rename? what do these actually do?

// statsTagRPC...better explanation of what it does
func (csh *clientStatsHandler) statsTagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	// this is never called with nil info in codebase, fast past but not too important...
	// this seems very defensive
	d := &rpcData{ // i think you still want this in the context for latency even if called with nil info which is defensive programmign
		startTime: time.Now(),
		method: info.FullMethodName,
	}

	// what is going on with these propagated tags/
	// is it correct/do I need to do it?

	// Def need to stick this rpcData into the context...
	context.WithValue(ctx, rpcDataKey, d)

	// the time to live shit has open census specific concepts like time to live, part of tags

}

// statsTagRPC...better explanation of what it does
func (ssh *serverStatsHandler) statsTagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	d := &rpcData{
		startTime: time.Now(),
		method: info.FullMethodName,
	}
}


// rpc data is IN the context, so not a method on csh, context scoped to attempt
func statsHandleRPC(ctx context.Context, s stats.RPCStats) {
	switch st := s.(type) {
	case *stats.OutHeader, *stats.InHeader, *stats.InTrailer, *stats.OutTrailer:

	case *stats.Begin:
		handleRPCBegin(ctx, st)
	case *stats.OutPayload:
		handleRPCOutPayload(ctx, st)
	case *stats.InPayload:
		handleRPCInPayload(ctx, st)
	case *stats.End:
		handleRPCEnd(ctx, st)
	default:
		/*if logger.V(2) {
			logger.Infof("unexpected stats: %T", st) // I don't like logging bad calls, we call this ourselves
		}*/
	}
}

// where is the spec for this stuff? views tags and measures are speced
// grounding point make sure the descriptions/logic match up and then work up to rpcData


// support:
// rpcData in the context recorded based on stats handler events (comprised of
// sentCount, sentBytes, recvCount, recvBytes, startTime, and method)...

// *** speced
// measurements are derived from rpcData

// views are defined on the measurements, which aggregate data in certain ways...
// ***

// default views now have started rpcs
// change name and add

