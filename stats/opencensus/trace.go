package opencensus

import (
	"context"
	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
	"strings"
)

// does this even have a working go.mod? If not rebase off my PR and copy paste
// these two files onto rebased commit

// language from spec I don't know if is being followed:

// span name is formatted as:
// * $package.$service/$method // we replace all / with .

// propagation seems fine, propagate metadata grpc-trace-bin
// client/server side wrt SpanContext.

// status - must set status, must be same as gRPC client/server status,
// mapping between gRPC canonical codes -> OpenCensus status codes:
// https://github.com/grpc/grpc-go/blob/master/codes/codes.go
// there is nothing what so fing ever about opencensus in that
// my gut tells me this will be off WRONG wrt codes.Internal vs. codes.Unknown

// stick in context, scoped to per attempt already so stick this in correctly scoped context...
// type messageIDs struct {
//      recv messages
//      sent messages
// }


// message events:
// -> [time], MessageEventTypeSent, MessageId, UncompressedByteSize, CompressedByteSize

// time is there, message event type I'm assuming is correct, uncompressed/compressed isn't there

// message id - "must be calculated as two different counters starting from 1
// one for sent messages and one for received message. This way we guarantee
// that the values will be consistent between different implementations. In case
// of unary calls only one sent and one received message will be recorded for
// both client and server spans."



// used to attach data client side and deserialize server side
// traceContextMDKey is the speced key for gRPC Metadata that carries binary
// serialized traceContext.
const traceContextMDKey = "grpc-trace-bin"

// interceptor needs to be present to link all of these attempts/contexts scoped to the attempts vvv

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

	// create counter up here if decided, will be across both

	// normal span if no parent vvv (this flow)

	var sc trace.SpanContext
	// gotSpanContext represents, after all the processing in this file, whether
	// there was a span context to link to a parent or not
	var gotSpanContext bool

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		// tell user because incorrect ctx passed in?
		// downstream span with no data
	}
	tcMDVal := md[traceContextMDKey]
	if len(tcMDVal) != 0 {
		tcBin := []byte(tcMDVal[0])
		sc, gotSpanContext = propagation.FromBinary(tcBin)
	} // else: normal, but not literally incorrect like not having a md. I use gotSpanContext as the logical trigger invariant

	// create normal span - if no md/trace context passed in (i.e. no parent)
	// ctx, span := trace.StartSpan(ctx, mn, trace.WithSpanKind(trace.SpanKindServer), trace.WithSampler(ssh.to.TS))
	var span *trace.Span
	// invariant to create a span with remote parent - ok
	if gotSpanContext {
		// 2. create a span using remote parent (then would need to gate the normal span with an if
		// to not create two spans
		ctx, span = trace.StartSpanWithRemoteParent(ctx, mn, sc, trace.WithSpanKind(trace.SpanKindServer), trace.WithSampler(ssh.to.TS))


		// two options (I thinkk you only need to choose 1):
		// 1. create a link child (idk if this is even right)
		span.AddLink(trace.Link{TraceID: sc.TraceID, SpanID: sc.SpanID, Type: trace.LinkTypeChild})
		// vd passed in has []links, idk if related to HasRemoteParent bool or not though

		// literally a .add call to a FIFO queue of links...so I think just adds linls

		// what is even the difference between ^^^ and vvv
		// the other thing I need to figure out is a way to use counters (see ExportView)



		// this function - only different between it and start span is the bool remoteParent,
		// I see nothing about links - but idk if there implementation is right anyway seems wrong. and stanley will be able to verify plus I write testing verifications.
		// which gets plumbed into
	} else { // after this (choosing the creation of span), Stanley will be able to verify if it actually works, and see what gets uploaded to cloud trace. It should somewhat match.
		ctx, span = trace.StartSpan(ctx, mn, trace.WithSpanKind(trace.SpanKindServer), trace.WithSampler(ssh.to.TS))
	}

	// result
	// normal span if no md/trace context passed in (i.e. no parent)
	// ctx, span := trace.StartSpan(ctx, mn, trace.WithSpanKind(trace.SpanKindServer), trace.WithSampler(ssh.to.TS))


	// if parent, append to span. I thinkkkk you can either call a separate constructor == just adding a link?
	// what does the spec say should happen?

	// theres no security risk imo to link spans with a remote, esp since we
	// control sender, if Doug mentions it or they care can plumb in that bool
	// through options later


	// vs. regular construction like above, this construction + the data added
	// below all goes into the final data structure that's emitted to exporter
	// and will need to test that final data from all these knobs


	// server spans would also need a counter for message received/sent events.

	return ctx
}

// ^^^ these operations handle the creation of the span/trace


// these operations populate span information and trigger the span to upload vvv


// handleRPC
// populates attributes/message events/status
// and also ends span which triggers export
func traceHandleRPC(ctx context.Context, rs stats.RPCStats) { // switch name to populateTraceData?
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

	switch rs := rs.(type) {
	// begin
	case *stats.Begin:
		// are these needed attribute annotations speced anywhere/linked to definitons in some spec somewhere?
		// attribute annotations for the span, are they used for any functionality speced?
		span.AddAttributes(
			trace.BoolAttribute("Client", rs.Client), // do you need either of these really?
			trace.BoolAttribute("FailFast", rs.FailFast),
		) // This feels useless/wrong to me? eh ok
		// anything else you need to do here?

	// in payload
	case *stats.InPayload:
		// operations: read count, write count to message receive event, and write to count - count = count + 1
		mi := int64(0)/*figure the fuck out, whether through invariant or more persisted data*/

		// only need to read from context here wrt messageIDs...
		// then add
		// read from context - miInfo
		// mi := miInfo.miRecvID
		// miInfo.miRecvID = miInfo.miRecvID + 1 (same as below...)

		span.AddMessageReceiveEvent(mi, int64(rs.Length), int64(rs.WireLength)) // is there a way to avoid all these fing casts
	// out payload
	case *stats.OutPayload:
		// iterate some counter in context by 1 - or use invairant ofm number of sent messages/received message (verified plumbed into exporter)
		mi := 0/*figure the fuck out, */
		// message send event

		// only need to read from context here wrt messageIDs...
		// then add 1

		span.AddMessageSendEvent(int64(mi), int64(rs.Length), int64(rs.WireLength))
	// end
	case *stats.End:
		if rs.Error != nil {
			// do status codes map 1:1 with opencensus stauts codes?
			// "The mapping between gRPC canonical codes and OpenCensus codes can be found here" here links the codes example ***open question to figure out
			s, _ := status.FromError(rs.Error) // ignore second argument because codes.Unknown is fine
			// already there and Stanley's tests work so no need to change honestly
			span.SetStatus(trace.Status{Code: int32(s.Code()), Message: s.Message()}) // is s.Message() correct? I think so
		} else {
			// trace status = ok, I think you need this
			// status ok with empty error message?
			span.SetStatus(trace.Status{Code: trace.StatusCodeOK}) // ok with empty string, perhaps best to get rid of this else conditional and just leave 0 value, but this makes it explicit at least
		}
		print("stats.End passed, span.End() called")
		span.End() // do we still want tis trigger point?
		// set status
		// span.End() - causing upload, maximum linkage you can get now is just remote linkages, no parent child across span attempts
		// any other events that we need to pogress
		// I just see status/message event/obv uploading...and the creation/linkage of span happens in TagRPC this is
		// HandleRPC i.e. when certain RPC events actually happen...

		// default (or can just get rid of if no op)

		// I don't think really needed to process any other events...

	}
}


// specific testing data structures that get emitted/we expect to get emitted

/*
i think it's "obvious" how to link the remote spans with the local ones so not in spec?
i would say reference the Go impl and the Java impl and see how they differ/what's the same. if it's in both it's probably something we should keep doing. if it's in 1 but not the other then we have to find out more

if that's the case you should probably just do what they did, unless it looks wrong or you see some other thing that looks more appropriate

no answer about messageID

at the very least I need to link remote spans...link the remote spans at the least...
which part of the constructor does that link or is added link enough?

I'm assuming messageID doesn't matter for stuff...but check Java


// probably just do what they did unless it looks wrong (i.e. attributes), wb status codes just do what they did...
// attributes in air

// status - "This proto's fields are a subset of those of rpc status which is used by gRPC.
// Safe to assume 0 (OK) when not set.
// "Implementations MUST set status which should be the same as the gRPC client/server status"

 // An identifier for the MessageEvent's message that can be used to match
 // SENT and RECEIVED MessageEvents. For example, this field could
      // represent a sequence ID for a streaming RPC. It is recommended to be
      // unique within a Span.

// links sent and recv message events so seems somewhat important link seems important but returns ctx



// startSpanWithRemoteParent just seems to determine sampler logic and set a bool in span data struct.
// sets hasRemoteParentBool, ortogonal to hasParent..determine sampler

// so maybe add a link yourself...although it was working for Stanley's linking them

// I see nothing about links...

// how does this link???

// parent can be remote, local, or no parent...

// StartSpan - starts child span of span IN CONTEXT
// if none create

// StartSpanWithRemoteParent - starts a child span of the span from given parent (passed in explictly instead of in context, ignores span in context)

// if I call both anyway Stanley's test will work because at worst would be extra...

// could do messageID in a separate commit

*/

// then make quick decision about the two traces present...

// traceTagRPC server side I think I've answered how I'll handle that
// attributes - not harmful, just leave them in
// messageID need to add
// status is correct

// the tests are just a unary and streaming RPC and defining in line
// representations for both of those so simpler...
// e2e test:
// unary call

// streaming call


// downstream effect, unlike multiple metrics that result as a downstream result from these calls,
// traces emits two spans

// unary vs. streaming is simply the amount of message events as part of the emitted spans

// both
// client span      <-       server span pointing to client span as remote
// unary: two spans - no message events?
// streaming: two spans - more message events on each span only diff? so if you're wrong about span not the end of world


// pull what I had so far from e2e test

// important verification: message send/recv events, diff between unary and streaming, anything else I need to do?


// other thing need testing infrastructure to mess around with it... start span
// with remote parent and link, the security bool seems useless...so just do
// both and see what happens?


// message id ask doug if yes add something to context
// what's it used for and everything works fine without it? Links messages
