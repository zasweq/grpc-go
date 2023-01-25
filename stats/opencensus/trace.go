package opencensus

import (
	"context"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"strings"
	"sync/atomic"
)


// stick in context, scoped to per attempt already so stick this in correctly scoped context...
// type messageIDs struct {
//      recv messages // only question I have is wrt sync. requirements...see my opencensus metrics PR overall rewrite for more information
//      sent messages
// }

type msgEventsCountKey struct{}

type msgEventsCount struct {
	countSentMsg int64 // this is what the api uses, but this can be negative and unsigned can't I think it's fine
	countRecvMsg int64
}

// needs to be on the heap to persist over time so pointer

// getMsgEventsCount returns the msgEventsCount stored in the context,
// or nil if there isn't one (WILL NEVER HAPPEN SINCE WE POPULATE IT)
func getMsgEventsCount(ctx context.Context) *msgEventsCount {
	mec, _ := ctx.Value(msgEventsCountKey{}).(*msgEventsCount)
	return mec
}

// how does it clear out data in context (spans which link to traces and this
// count), I think new attempts just create new contexts

// setMsgEventsCount stores a msgEventCount with zero values in the context.
func setMsgEventsCount(ctx context.Context) context.Context { // 0 0 but I do need to add and read but handled in atomic could use mutex...or I'm sure there's an atomic instruction that reads and writes in one instruction
	return context.WithValue(ctx, msgEventsCountKey{}, &msgEventsCount{}) // zero values and allocates new heap memory...
}

// message id - "must be calculated as two different counters starting from 1
// one for sent messages and one for received message. This way we guarantee
// that the values will be consistent between different implementations. In case
// of unary calls only one sent and one received message will be recorded for
// both client and server spans."

// used to attach data client side and deserialize server side
// traceContextMDKey is the speced key for gRPC Metadata that carries binary
// serialized traceContext.
const traceContextMDKey = "grpc-trace-bin"

// tag rpc - populates context with new span
// serializes trace info into wire data
// "grpc-trace-bin" for tracing binary data
// "grpc-tag-bin" for metrics tags
func (csh *clientStatsHandler) traceTagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	// nothing about parent child relationships so I guess is fine
	// Implementations MUST create a span for client/server when the gRPC call
	// starts is all I see, nothing about retry attempts.

	// /s/m -> s.m according to what spec? I'm assuming opencensus spec WRONG LOL

	// this replaces ALL / with ., needs to leave one IIRC Stanley did see Sent/Recv spans
	// so backend takes span kind client/server -> prefixes with Sent or Recv?
	// CAN prefix with Sent. or Recv. so eh just ignore

	// Verification of Span names?

	// spans look off

	mn := strings.Replace(removeLeadingSlash(rti.FullMethodName), "/", ".", -1)

	ctx = setMsgEventsCount(ctx)

	ctx, span := trace.StartSpan(ctx, mn, trace.WithSampler(csh.to.TS), trace.WithSpanKind(trace.SpanKindClient))
	span.SpanContext() // propagation.Binary(spancontext), so I'm assuming this gets populated/created by it's constructor
	tcBin := propagation.Binary(span.SpanContext())
	return metadata.AppendToOutgoingContext(ctx, traceContextMDKey, string(tcBin))

	// ^^^^ REWRITE FURTHER - only one branch of logic for the created span,
	// with knobs sampler and hardcoded span kind populates metadata, just verify
	// trace name

	// NEED TWO COUNTERS PERSISTED IN CONTEXT CORRECTLY SCOPED TO CS ATTEMPT
	// because this all happens globally so need to link the counters into context
	// scope, which scopes it per attempt or use invariant one for client spans here and one for server spans below...

}

// tag rpc - populates context with tag data
// deserializes trace info from wire data
// "grpc-trace-bin" for tracing binary data
// "grpc-tag-bin" for metrics tags
func (ssh *serverStatsHandler) traceTagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	// same thing, / all replace with ., is this correct? used to create span name so need to make sure this is right...ongoing discussion in o11y thread
	mn := strings.Replace(removeLeadingSlash(rti.FullMethodName), "/", ".", -1)

	// create counter up here if decided, will be across both sticks counter in
	// context scoped to the context passed in...
	ctx = setMsgEventsCount(ctx)


	var sc trace.SpanContext
	// gotSpanContext represents, after all the processing in this file, whether
	// there was a span context to link to a parent or not
	var gotSpanContext bool

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		// tell user because incorrect ctx passed in?
		// logger at error because incorrect ctx passed in - shouldn't happen
		// downstream span with no remote parent
	}
	tcMDVal := md[traceContextMDKey]
	if len(tcMDVal) != 0 {
		tcBin := []byte(tcMDVal[0])
		// Only if it passes all of these steps and the binary metadata value
		// successfully unmarshals means the server received a span context,
		// meaning this created server span should have a remote parent
		// corresponding to whatever was received off the wire and unmarshaled
		// into this span context.
		sc, gotSpanContext = propagation.FromBinary(tcBin)
	}

	var span *trace.Span
	if gotSpanContext {
		ctx, span = trace.StartSpanWithRemoteParent(ctx, mn, sc, trace.WithSpanKind(trace.SpanKindServer), trace.WithSampler(ssh.to.TS))
		span.AddLink(trace.Link{TraceID: sc.TraceID, SpanID: sc.SpanID, Type: trace.LinkTypeChild})
	} else {
		ctx, span = trace.StartSpan(ctx, mn, trace.WithSpanKind(trace.SpanKindServer), trace.WithSampler(ssh.to.TS))
	}
	return ctx
}

// populateSpan...
// populates attributes/message events/status based on stats type, an invariant of the RPC lifecycle
// and also ends span which triggers export
func populateSpan(ctx context.Context, rs stats.RPCStats) { // switch name to populateTraceData?
	// switch on stats type (part of rpc - logically an invariant of rpc events happening)

	// stats type 1 - do we need to scale up what they have any more?

	// must be calculated as two different counters starting from 1
	// one for sent messages and one for received message. This way we guarantee
	// that the values will be consistent between different implementations.

	// counter 1 - synchronization?
	// counter 2 - synchronization? How to add to context (wasn't I planning on adding a blob of data anyway?)
	// plumb into context
	// context scoped to span 1:1? Doug told me populate span per attempt locally...yes I think context scope is appropriate

	// will this change when I have overall top level context for the call as a
	// whole? these message attempts are PER SPAN (1:1 with attempt), not per
	// CALL, so still correct for cs attempts context (I still need to figure
	// out how to link in future)

	span := trace.FromContext(ctx) // invariant of [] message send/recv lengths (a property of system which infers something about it), the invariant [] length is the [] of data passed to ExportSpan() and present in what's passed in there
	// span.Internal() // we kind of already interface with all the span methods exported
	// span.IsRecordingEvents() // test is span is recording is important
	// span.Internal()./*typecast...*/ // could somehow use this

	switch rs := rs.(type) {
	case *stats.Begin:
		// Note: not present in Java, but left in because already there, so why not. Thus untested
		span.AddAttributes(
			trace.BoolAttribute("Client", rs.Client),
			trace.BoolAttribute("FailFast", rs.FailFast),
		)
	case *stats.InPayload:
		// operations: read count, write count to message receive event, and write to count - count = count + 1
		// mi := int64(0)/*figure the fuck out, whether through invariant or more persisted data*/

		// only need to read from context here wrt messageIDs...
		mec := getMsgEventsCount(ctx)
		mi := atomic.AddInt64(&mec.countRecvMsg, 1) // to test would need to send two messages...
		// then add
		// read from context - miInfo
		// mi := miInfo.miRecvID
		// miInfo.miRecvID = miInfo.miRecvID + 1 (same as below...)

		span.AddMessageReceiveEvent(mi, int64(rs.Length), int64(rs.WireLength)) // is there a way to avoid all these fing casts
	case *stats.OutPayload:

		// span := trace.FromContext(ctx)
		mec := getMsgEventsCount(ctx)
		// starting from one
		mi := atomic.AddInt64(&mec.countSentMsg, 1)

		// iterate some counter in context by 1 - or use invairant ofm number of sent messages/received message (verified plumbed into exporter)
		// mi := 0/*figure the fuck out, */
		// message send event

		// only need to read from context here wrt messageIDs...
		// then add 1

		span.AddMessageSendEvent(mi, int64(rs.Length), int64(rs.WireLength))
	case *stats.End:
		if rs.Error != nil {
			// do status codes map 1:1 with opencensus stauts codes?
			// "The mapping between gRPC canonical codes and OpenCensus codes can be found here" here links the codes example ***open question to figure out
			s, _ := status.FromError(rs.Error) // ignore second argument because codes.Unknown is fine
			// already there and Stanley's tests work so no need to change honestly
			span.SetStatus(trace.Status{Code: int32(s.Code()), Message: s.Message()}) // is s.Message() correct? I think so
		} else { // status code
			// trace status = ok, I think you need this
			// status ok with empty error message?
			span.SetStatus(trace.Status{Code: trace.StatusCodeOK}) // ok with empty string, perhaps best to get rid of this else conditional and just leave 0 value, but this makes it explicit at least
		}
		print("stats.End passed, span.End() called")
		span.End()
	}
}


/*
no answer about messageID

at the very least I need to link remote spans...link the remote spans at the least...
which part of the constructor does that link or is added link enough?

I'm assuming messageID doesn't matter for stuff...but check Java


// probably just do what they did unless it looks wrong (i.e. attributes), wb status codes just do what they did...
// attributes in air

 // An identifier for the MessageEvent's message that can be used to match
 // SENT and RECEIVED MessageEvents. For example, this field could
      // represent a sequence ID for a streaming RPC. It is recommended to be
      // unique within a Span.

// links sent and recv message events so seems somewhat important link seems important but returns ctx
*/
