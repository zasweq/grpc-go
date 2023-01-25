package opencensus

import (
	"context"
	"strings"
	"sync/atomic"

	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

type msgEventsCountKey struct{}

// msgEventCounts are counters for sent messages and recv messages to populate
// message ID on message events on spans.
type msgEventsCount struct {
	countSentMsg int64
	countRecvMsg int64
}

// getMsgEventsCount returns the msgEventsCount stored in the context,
// or nil if there isn't one (WILL NEVER HAPPEN SINCE WE POPULATE IT I don't think we need to be robust)
func getMsgEventsCount(ctx context.Context) *msgEventsCount {
	mec, _ := ctx.Value(msgEventsCountKey{}).(*msgEventsCount)
	return mec
}

// setMsgEventsCount stores a msgEventCount with zero values in the context.
func setMsgEventsCount(ctx context.Context) context.Context { // 0 0 but I do need to add and read but handled in atomic could use mutex...or I'm sure there's an atomic instruction that reads and writes in one instruction
	// needs to be on the heap to persist changes over time so pointer
	return context.WithValue(ctx, msgEventsCountKey{}, &msgEventsCount{}) // zero values and allocates new heap memory...
}

// message id - "must be calculated as two different counters starting from 1
// one for sent messages and one for received message. This way we guarantee
// that the values will be consistent between different implementations. In case
// of unary calls only one sent and one received message will be recorded for
// both client and server spans." add this quote to test

// used to attach data client side and deserialize server side
// traceContextMDKey is the speced key for gRPC Metadata that carries binary
// serialized traceContext.
const traceContextMDKey = "grpc-trace-bin"

// tag rpc - populates context with new span
// serializes trace info into wire data
// "grpc-trace-bin" for tracing binary data
// "grpc-tag-bin" for metrics tags

// traceTagRPC populates context with a new span, and serializes information
// about this span into gRPC Metadata.
func (csh *clientStatsHandler) traceTagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	// nothing about parent child relationships so I guess is fine
	// Implementations MUST create a span for client/server when the gRPC call
	// starts is all I see, nothing about retry attempts. But w/e make an arbitrary call

	// /s/m -> s.m according to what spec? I'm assuming opencensus spec WRONG LOL

	// this replaces ALL / with ., needs to leave one IIRC Stanley did see Sent/Recv spans
	// so backend takes span kind client/server -> prefixes with Sent or Recv?
	// CAN prefix with Sent. or Recv. so eh just ignore

	mn := strings.Replace(removeLeadingSlash(rti.FullMethodName), "/", ".", -1)
	// This will be scoped to per attempt, which is the scope of the context passed in, and seems to be correct even when add top level to still have the message events per attempt.
	ctx = setMsgEventsCount(ctx)

	ctx, span := trace.StartSpan(ctx, mn, trace.WithSampler(csh.to.TS), trace.WithSpanKind(trace.SpanKindClient))
	tcBin := propagation.Binary(span.SpanContext())
	return metadata.AppendToOutgoingContext(ctx, traceContextMDKey, string(tcBin))
}

// traceTagRPC populates context with new span data, with a parent based on the
// spanContext deserialized from context passed in (wire data in gRPC metadata)
// if present.
func (ssh *serverStatsHandler) traceTagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	// same thing, / all replace with ., is this correct? used to create span name so need to make sure this is right...ongoing discussion in o11y thread
	mn := strings.Replace(removeLeadingSlash(rti.FullMethodName), "/", ".", -1)
	ctx = setMsgEventsCount(ctx)


	var sc trace.SpanContext
	// gotSpanContext represents if after all the logic to get span context out
	// of context passed in, whether there was a span context which represents
	// the future created server span's parent or not.
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

// populateSpan populates span information based on stats passed in (invariants
// of the RPC lifecycle), and also ends span which triggers the span to be
// exported.
func populateSpan(ctx context.Context, rs stats.RPCStats) {
	span := trace.FromContext(ctx) // wrapper that holds it and other information
	// span.IsRecordingEvents() // test is span is recording is important add this to comparison test (part of span context/trace options)

	// wrap everything nil check log error and test will fail log error and test will fail...do this for ctx metadata too

	// pull wrapped span out here
	//      nil check here for the wrapped span


	switch rs := rs.(type) {
	case *stats.Begin:
		// Note: not present in Java, but left in because already there, so why not. Thus untested.
		span.AddAttributes(
			trace.BoolAttribute("Client", rs.Client),
			trace.BoolAttribute("FailFast", rs.FailFast),
		)
	case *stats.InPayload:
		mec := getMsgEventsCount(ctx)
		mi := atomic.AddInt64(&mec.countRecvMsg, 1) // to test would need to send two messages...
		span.AddMessageReceiveEvent(mi, int64(rs.Length), int64(rs.WireLength))
	case *stats.OutPayload:
		mec := getMsgEventsCount(ctx)
		mi := atomic.AddInt64(&mec.countSentMsg, 1)
		span.AddMessageSendEvent(mi, int64(rs.Length), int64(rs.WireLength))
	case *stats.End:
		if rs.Error != nil {
			// "The mapping between gRPC canonical codes and OpenCensus codes
			// can be found here", which implies 1:1 mapping to gRPC statuses
			// (OpenCensus statuses are based off gRPC statuses and a subset).
			s, _ := status.FromError(rs.Error) // ignore second argument because codes.Unknown is fine and is correct the correct status to populate span with.
			span.SetStatus(trace.Status{Code: int32(s.Code()), Message: s.Message()})
		} else {
			span.SetStatus(trace.Status{Code: trace.StatusCodeOK}) // could get rid of this else conditional and just leave as 0 value, but this makes it explicit
		}
		print("stats.End passed, span.End() called")
		span.End()
	}
}
