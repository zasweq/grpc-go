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
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.opencensus.io/metric/metricdata"
	ocstats "go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"go.opencensus.io/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

// todo:
// 1. rewrite metrics recording points
// perhaps split into files...


// for recording the measures, maybe we structure things differently. Maybe we have fewer files or more fiels

// the reusing makes sense for client and server outside that one bool partition

// rename to stats handler.go or something


var logger = grpclog.Component("opencensus-instrumentation")

type grpcInstrumentationKey string

// rpcData holds the instrumentation RPC data that is needed between the start
// and end of an call. It holds the info that this package needs to keep track
// of between the various GRPC events.
type rpcData struct {
	// reqCount and respCount has to be the first words
	// in order to be 64-aligned on 32-bit architectures.
	sentCount, sentBytes, recvCount, recvBytes int64 // access atomically

	// startTime represents the time at which TagRPC was invoked at the
	// beginning of an RPC. It is an appoximation of the time when the
	// application code invoked GRPC code.
	startTime time.Time
	method    string
}

// The following variables define the default hard-coded auxiliary data used by
// both the default GRPC client and GRPC server metrics.

// rename this and also are these bounds defined by a spec or do I have a knob over these?
// def rename ugly names
var (
	defaultBytesDistributionBounds        = []float64{1024, 2048, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864, 268435456, 1073741824, 4294967296}
	DefaultBytesDistribution              = view.Distribution(defaultBytesDistributionBounds...)
	DefaultMillisecondsDistribution       = view.Distribution(0.01, 0.05, 0.1, 0.3, 0.6, 0.8, 1, 2, 3, 4, 5, 6, 8, 10, 13, 16, 20, 25, 30, 40, 50, 65, 80, 100, 130, 160, 200, 250, 300, 400, 500, 650, 800, 1000, 2000, 5000, 10000, 20000, 50000, 100000)
	defaultMessageCountDistributionBounds = []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536}
	DefaultMessageCountDistribution       = view.Distribution(defaultMessageCountDistributionBounds...)
)

// Server tags are applied to the context used to process each RPC, as well as
// the measures at the end of each RPC.
var (
	KeyServerMethod = tag.MustNewKey("grpc_server_method")
	KeyServerStatus = tag.MustNewKey("grpc_server_status")
)

// Client tags are applied to measures at the end of each RPC.
var (
	KeyClientMethod = tag.MustNewKey("grpc_client_method") // how the data is partioned wrt view data...each bucket right will come back when I look at it
	KeyClientStatus = tag.MustNewKey("grpc_client_status")
)

var (
	rpcDataKey = grpcInstrumentationKey("opencensus-rpcData")
)

// REWRITE
func methodName(fullname string) string {
	return strings.TrimLeft(fullname, "/")
}

// statsTagRPC gets the tag.Map populated by the application code, serializes
// its tags into the GRPC metadata in order to be sent to the server.
func (csh *clientStatsHandler) statsTagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	startTime := time.Now()
	if info == nil {
		if logger.V(2) {
			logger.Info("clientHandler.TagRPC called with nil info.")
		}
		return ctx
	}

	d := &rpcData{
		startTime: startTime,
		method:    info.FullMethodName,
	}
	// WHY DO I HAVE OPENCENSUS SPECIFIC TAGS BEING SENT ACCROSS THE WIRE IS THIS NEEDED GRPC NEVER POPULATES THIS???

	// tag.Map populated by the application code...can you do this as an opencensus user for what...

	// i really dont think you need this, leave out and see what happens

	ts := tag.FromContext(ctx) // where is this populated, and is this tested...
	if ts != nil {
		encoded := tag.Encode(ts) // encodes from tag[map] -> binary format to be sent on wire
		// attaches stats tagging data to context...sent in the outgoing rpc
		// with header grpc-tags-bin. It says new uses shouldn't use it, but use
		// a non reserved header ** perhaps ping Doug about this.
		ctx = stats.SetTags(ctx, encoded) // encoded the tags here after opencensus specific encoding
	}

	return context.WithValue(ctx, rpcDataKey, d) // returns a context with rpc data, per attempt, server side doesn't have this concept so is per overall call caleld once
}

// statsTagRPC gets the metadata from gRPC context, extracts the encoded tags from
// it and creates a new tag.Map and puts them into the returned context.
func (ssh *serverStatsHandler) statsTagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	startTime := time.Now()
	if info == nil {
		if logger.V(2) {
			logger.Infof("opencensus: TagRPC called with nil info.")
		}
		return ctx
	}
	d := &rpcData{ // per attempt, so traces really are per attempt? Unless they point to the same trace on the heap...
		startTime: startTime,
		method:    info.FullMethodName,
	}
	/*

	Package tag contains OpenCensus tags. Tags are key-value pairs. Tags provide
	additional cardinality to the
	OpenCensus instrumentation data. Tags can be propagated on the wire and in the same process via
	context.Context. Encode and Decode should be used to represent tags into
	their binary propagation form.

	*/

	// keyServerMethod (how they partition metrics "cardinality"): fullmethod
	// name need to to do this client and server side

	// takes the []byte inside the gRPC metadata, and
	// converts to tag map, sticks it in the context,
	// and also attaches the full method name in the tag map
	propagated := ssh.extractPropagatedTags(ctx) // tags plumbed into context?
	ctx = tag.NewContext(ctx, propagated)
	// have knobs over the tab key as well

	// Server tags are applied to the context used to process each RPC, as well
	// as the measures at the end of each RPC - so yeah this is used for the cardinality

	// STICKS THIS EXTRACTED TAGS + FULL METHOD NAME SERVER SIDE, BUT THIS IS
	// USED ONLY IN GRPC, I REALLY DON'T THINK I NEED THIS

	// gets []byte from wire creates tag map adds full method name to it

	ctx, _ = tag.New(ctx, tag.Upsert(KeyServerMethod, methodName(info.FullMethodName))) // do we even need this? Test without and see what Doug says, method logic is handled from data plumbed in...
	return context.WithValue(ctx, rpcDataKey, d)
}

// extractPropagatedTags creates a new tag map containing the tags extracted from the
// gRPC metadata.
func (ssh *serverStatsHandler) extractPropagatedTags(ctx context.Context) *tag.Map {
	buf := stats.Tags(ctx) // this gets the []byte representing the encoded bytes from the tag map in context
	if buf == nil {
		return nil
	}

	// LEAVE THIS OUT AND SEE IF STILL WORKS
	// sends opencensus tags across the sire and also adds server method as a tag
	propagated, err := tag.Decode(buf) // opencensus specific decoding of tag map
	// keep it, server can register without client break off the wire, how do tags work wrt the view partion
	if err != nil {
		if logger.V(2) {
			logger.Warningf("opencensus: Failed to decode tags from gRPC metadata failed to decode: %v", err)
		}
		return nil
	}
	return propagated
}

// statsHandleRPC processes the RPC events.
func statsHandleRPC(ctx context.Context, s stats.RPCStats) {
	switch st := s.(type) {
	case *stats.OutHeader, *stats.InHeader, *stats.InTrailer, *stats.OutTrailer:
		// do nothing for client
	case *stats.Begin:
		handleRPCBegin(ctx, st)
	case *stats.OutPayload:
		handleRPCOutPayload(ctx, st)
	case *stats.InPayload:
		handleRPCInPayload(ctx, st)
	case *stats.End:
		handleRPCEnd(ctx, st)
	default:
		if logger.V(2) {
			logger.Infof("unexpected stats: %T", st)
		}
	}
}

func handleRPCBegin(ctx context.Context, s *stats.Begin) {
	d, ok := ctx.Value(rpcDataKey).(*rpcData)
	if !ok {
		if logger.V(2) {
			logger.Infof("Failed to retrieve *rpcData from context.")
		}
	}

	if s.IsClient() { // only partition on client/server in the whole file...
		print("recording client started rpcs + 1")
		ocstats.RecordWithOptions(ctx,
			ocstats.WithTags(tag.Upsert(KeyClientMethod, methodName(d.method))), // puts method here and adds one to started rpcs
			ocstats.WithMeasurements(ClientStartedRPCs.M(1)))
	} else {
		ocstats.RecordWithOptions(ctx,
			// Tags it here anyway, sicne we own it leave it out
			ocstats.WithTags(tag.Upsert(KeyClientMethod, methodName(d.method))), // ugh do we want this on server method actually....
			ocstats.WithMeasurements(ServerStartedRPCs.M(1)))
	}
}

// records length in bytes of outgoing messages and increases total count of
// sent messages for use in taking measurements at rpc end

// recordDataOutPayload

func handleRPCOutPayload(ctx context.Context, s *stats.OutPayload) {
	d, ok := ctx.Value(rpcDataKey).(*rpcData)
	if !ok { // don't need this...return, shouldn't happen since system populates rpcData by tagging rpcdata
		if logger.V(2) {
			logger.Infoln("Failed to retrieve *rpcData from context.")
		}
		return
	}

	atomic.AddInt64(&d.sentBytes, int64(s.Length)) // records length and count
	atomic.AddInt64(&d.sentCount, 1)
}


// records length in bytes of incoming messages and increases total count of
//// sent messages for use in taking measurements at rpc end

// recordDataInPayload

func handleRPCInPayload(ctx context.Context, s *stats.InPayload) {
	d, ok := ctx.Value(rpcDataKey).(*rpcData) // stores data here, used for measures as derived...
	if !ok {
		if logger.V(2) {
			logger.Infoln("Failed to retrieve *rpcData from context.")
		}
		return
	}

	atomic.AddInt64(&d.recvBytes, int64(s.Length))
	atomic.AddInt64(&d.recvCount, 1)
}

// takes overall measurements and

func handleRPCEnd(ctx context.Context, s *stats.End) {
	d, ok := ctx.Value(rpcDataKey).(*rpcData) // high level - gets the rpc data collected over the lifetime of the rpc ATTEMPT, then records at end in handleRPCEnd()
	if !ok {
		if logger.V(2) {
			logger.Infoln("Failed to retrieve *rpcData from context.")
		}
		return
	}

	elapsedTime := time.Since(d.startTime)

	var st string
	if s.Error != nil {
		s, ok := status.FromError(s.Error)
		s.Code()
		if ok {
			st = statusCodeToString(s)
		}
	} else {
		st = "OK"
	}

	attachments := getSpanCtxAttachment(ctx) // what tbe fuck?

	// other per rpc measurements read off rpc data directly,
	// this one takes the difference between start and end time
	// and divides by millisecond
	latencyMillis := float64(elapsedTime) / float64(time.Millisecond)

	if s.Client { // another branch :(
		ocstats.RecordWithOptions(ctx,
			ocstats.WithTags(
				tag.Upsert(KeyClientMethod, methodName(d.method)),
				tag.Upsert(KeyClientStatus, st)),
			ocstats.WithAttachments(attachments),
			ocstats.WithMeasurements(
				ClientSentBytesPerRPC.M(atomic.LoadInt64(&d.sentBytes)),
				ClientSentMessagesPerRPC.M(atomic.LoadInt64(&d.sentCount)),
				ClientReceivedMessagesPerRPC.M(atomic.LoadInt64(&d.recvCount)),
				ClientReceivedBytesPerRPC.M(atomic.LoadInt64(&d.recvBytes)),
				ClientRoundtripLatency.M(latencyMillis)))
	} else {
		ocstats.RecordWithOptions(ctx,
			ocstats.WithTags(
				// all the views are tagged with keyservermethod...but my test seems to work?!?!?!?!
				// why noooo keyServerMethod? does this exit (all serer metrics should be tagged with the following server method and server status?)
				tag.Upsert(KeyServerStatus, st),
			),
			ocstats.WithAttachments(attachments),
			ocstats.WithMeasurements(
				ServerSentBytesPerRPC.M(atomic.LoadInt64(&d.sentBytes)),
				ServerSentMessagesPerRPC.M(atomic.LoadInt64(&d.sentCount)),
				ServerReceivedMessagesPerRPC.M(atomic.LoadInt64(&d.recvCount)),
				ServerReceivedBytesPerRPC.M(atomic.LoadInt64(&d.recvBytes)),
				ServerLatency.M(latencyMillis)))
	}
}

func statusCodeToString(s *status.Status) string {
	// see https://github.com/grpc/grpc/blob/master/doc/statuscodes.md
	switch c := s.Code(); c {
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
		return "CODE_" + strconv.FormatInt(int64(c), 10)
	}
}

// spanCtx attachment?!?! ignore this like propgating opencensus tags set by application on the wire and see what happens/if works
func getSpanCtxAttachment(ctx context.Context) metricdata.Attachments {
	attachments := map[string]interface{}{}
	span := trace.FromContext(ctx)
	if span == nil {
		return attachments
	}
	spanCtx := span.SpanContext()
	if spanCtx.IsSampled() { // why the fuckkkkkk do I need trace context in metrics, have we decided how to link logging and traces, is it just trace id tracing and logs so seperate partionwise/logically, handled by tracing flow...
		attachments[metricdata.AttachmentKeySpanContext] = spanCtx
	}
	return attachments
}
