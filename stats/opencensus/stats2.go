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
	ocstats "go.opencensus.io/stats"
	"go.opencensus.io/tag"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"sync/atomic"
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

	/*
	// reqCount and respCount has to be the first words
	// in order to be 64-aligned on 32-bit architectures.
	sentCount, sentBytes, recvCount, recvBytes int64 // access atomically

	// sent messages? yes called from cs attempt and server stream
	// sent bytes?
	// recv messages? yes called from cs attempt and server stream
	// recv bytes?

	*/

	// access atomically for hedging in the future

	// number of messages sent from side (client || server) using rpcData
	sentMsgs int64
	// number of bytes sent (within each message) from side (client || server) using rpcData
	sentBytes int64
	// number of messages received on side (client || server) using rpcData
	recvMsgs int64
	// number of bytes received (within each message) received on side (client || server) using rpcData
	recvBytes int64

	startTime time.Time // is this still the correct way to count latency?
	method    string
}
// used by both client and server


// rename? what do these actually do?

// statsTagRPC...better explanation of what it does

// 1. creates a recording object (to derive measurements from) scoped to context
// (in this case per attempt)

// 2. Populates the context with opencensus specfic tags (set by application for
// deserialization and addition on server side). *maybe not needed if my tests work

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
	return context.WithValue(ctx, rpcDataKey, d)

	// the time to live shit has open census specific concepts like time to live, part of tags propagate accross hops logic
	// but again these are tags that need to be filled out by the application and it never is or read so what's the point...

}

// statsTagRPC...better explanation of what it does

// 1. creates a recording object (to derive measurements from) scoped to context
// (in this case per overall call because that's the scope of when server calls
// into)

// 2. deserializes the binary tags from the wire into opencensus tags, adds method to tags for use
// in measurement cardinality/partioning later. *maybe not needed if my tests work

func (ssh *serverStatsHandler) statsTagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	d := &rpcData{
		startTime: time.Now(),
		method: info.FullMethodName,
	}

	// just persist this

	// ignore the weird serialization of opencensus tag bytes I don't think used for anything
	// nothing in the spec requires this...I guess can see if my tests work?
	return context.WithValue(ctx, rpcDataKey, d) // this creates a copy of the parent context is that significant?
}

// rpc data is IN the context, so not a method on csh, context scoped to attempt
func statsHandleRPC(ctx context.Context, s stats.RPCStats) {
	switch st := s.(type) {
	case *stats.OutHeader, *stats.InHeader, *stats.InTrailer, *stats.OutTrailer:
		// headers and trailers are not relevant to the measures, as the
		// measures concern number of messages and bytes for messages. This
		// aligns with flow control.

		// so headers and trailers don't count as msgs or bytes...

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

// rpc begin function here

// recordDataBegin takes a measurement related to the RPC beginning,
// client/server started RPCs dependent on the caller.
func recordDataBegin(ctx context.Context, b *stats.Begin) {
	d, ok := ctx.Value(rpcDataKey).(*rpcData)
	if !ok {
		// Shouldn't happen, as gRPC calls tagRPC which populates the rpcData in context.
		return
	}

	if b.IsClient() {
		print("recording client started rpcs + 1")
		ocstats.RecordWithOptions(ctx,
			ocstats.WithTags(tag.Upsert(KeyClientMethod, methodName(d.method))), // puts method here and adds one to started rpcs
			ocstats.WithMeasurements(ClientStartedRPCs.M(1)))
		return
	}
	ocstats.RecordWithOptions(ctx,
		// SERVER METHOD?!?!?!
		ocstats.WithTags(tag.Upsert(KeyClientMethod, methodName(d.method))), // ugh do we want this on server method actually....but doesn't persist that in tags but my tests owrk so do you need ot sereialize? FUCK FUCK. Client mehtod is persisted somewher so who even knows
		ocstats.WithMeasurements(ServerStartedRPCs.M(1)))
}

// recordDataOutPayload records the length in bytes of outgoing messages and
// increases total count of sent messages both stored in the RPCs (attempt on
// client side) context for use in taking measurements at RPC end.
func recordDataOutPayload(ctx context.Context, op *stats.OutPayload) {
	d, ok := ctx.Value(rpcDataKey).(*rpcData)
	if !ok {
		// Shouldn't happen, as gRPC calls tagRPC which populates the rpcData in context.
		return
	}
	atomic.AddInt64(&d.sentMsgs, 1)
	atomic.AddInt64(&d.sentBytes, int64(op.Length)) // what is s.WireLength used for?
}

// recordDataInPayload records the length in bytes of incoming messages and
// increases total count of sent messages both stored in the RPCs (attempt on
// client side) context for use in taking measurements at RPC end.
func recordDataInPayload(ctx context.Context, ip *stats.InPayload) {
	d, ok := ctx.Value(rpcDataKey).(*rpcData)
	if !ok {
		// Shouldn't happen, as gRPC calls tagRPC which populates the rpcData in context.
		return
	}
	atomic.AddInt64(&d.recvMsgs, 1)
	atomic.AddInt64(&d.recvBytes, int64(ip.Length))
}

// rpc end function here

// recordDataEnd takes per RPC measurements derived from information derived
// from the lifetime of the RPC (RPC attempt client side).
func recordDataEnd(ctx context.Context, e *stats.End) {
	d, ok := ctx.Value(rpcDataKey).(*rpcData)
	if !ok {
		// Shouldn't happen, as gRPC calls tagRPC which populates the rpcData in context.
		return
	}

	// calculate latency ms here...
	// latency buckets have fractions of millisecond, thus need a float
	latency := float64(time.Since(d.startTime)) / float64(time.Millisecond)

	// status string here I remember some || across status ok vs. status string
	// status
	var st string
	if e.Error != nil {
		s, _ := status.FromError(e.Error) // ignore second argument because codes.Unknown is fine
		if ok {
			st = s.Code().String() // partions measure/views on client status, I think ok whether uppercase or lowercase unless user tooling cares about this tag partion as lower or upper case...
		}
	} else {
		st = "OK"
	}

	// status.FromError()

	// st := codes.String()
	// FINISH POPULATING STATUS STRING AND ALSO RPC DATA ATTACHED IN TAG RPC
	// st := FIND STATUS HELPER STATUSHELPER()

	// attachments? WithAttachments...do I even really need this? let's ignore this for now

	if s.Client {
		ocstats.RecordWithOptions(ctx,
			ocstats.WithTags(
				tag.Upsert(KeyClientMethod, methodName(d.method)),
				tag.Upsert(KeyClientStatus, st)),
			ocstats.WithMeasurements(
				ClientSentBytesPerRPC.M(atomic.LoadInt64(&d.sentBytes)),
				ClientSentMessagesPerRPC.M(atomic.LoadInt64(&d.sentMsgs)),
				ClientReceivedMessagesPerRPC.M(atomic.LoadInt64(&d.recvMsgs)),
				ClientReceivedBytesPerRPC.M(atomic.LoadInt64(&d.recvBytes)),
				ClientRoundtripLatency.M(latency)))
		return
	}
	// server side
	// populate server method string and see if my tests work...
	ocstats.RecordWithOptions(ctx,
		ocstats.WithTags(
			// all the views are tagged with keyservermethod...but my test seems to work?!?!?!?!
			// why noooo keyServerMethod? does this exit (all serer metrics should be tagged with the following server method and server status?)
			tag.Upsert(KeyServerMethod, methodName(d.method)),
			tag.Upsert(KeyServerStatus, st),
		),
		ocstats.WithMeasurements(
			ServerSentBytesPerRPC.M(atomic.LoadInt64(&d.sentBytes)),
			ServerSentMessagesPerRPC.M(atomic.LoadInt64(&d.sentMsgs)),
			ServerReceivedMessagesPerRPC.M(atomic.LoadInt64(&d.recvMsgs)),
			ServerReceivedBytesPerRPC.M(atomic.LoadInt64(&d.recvBytes)),
			ServerLatency.M(latency)))
}


// func status code to string...?
// status codes should be stringified according to see https://github.com/grpc/grpc/blob/master/doc/statuscodes.md
func statusCodeToString(c codes.Code) string {
	c.String()
	switch c {
	case codes.OK:
		return "OK"
	case codes.Canceled:
		return "CANCELLED"
	case codes.Unknown:
		return "UNKNOWN"
	case codes.InvalidArgument:
		return "INVALID_ARGUMENT"
	case codes.DeadlineExceeded:
		return "DEADLINE_EXCEEDED"
	case codes.NotFound:
		return "NOT_FOUND"
	case codes.AlreadyExists:
		return "ALREADY_EXISTS"
	case codes.PermissionDenied:
		return "PERMISSION_DENIED"
	case codes.ResourceExhausted:
		return "RESOURCE_EXHAUSTED"
	case codes.FailedPrecondition:
		return "FAILED_PRECONDITION"
	case codes.Aborted:
		return "ABORTED"
	case codes.OutOfRange:
		return "OUT_OF_RANGE"
	case codes.Unimplemented:
		return "UNIMPLEMENTED"
	case codes.Internal:
		return "INTERNAL"
	case codes.Unavailable:
		return "UNAVAILABLE"
	case codes.DataLoss:
		return "DATA_LOSS"
	case codes.Unauthenticated:
		return "UNAUTHENTICATED"
	default:
		// won't happen
		return ""
	}
}

// I'm sure there's a helper in our codebase to do this for you?


// spancontext...linking metrics and traces?


// func about tracing data....shoudn't span data be handled in the tracing part of this?!?!
// what is this even being used for







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

