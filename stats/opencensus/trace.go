package opencensus


import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	"go.opencensus.io/trace"
	"go.opencensus.io/trace/propagation"
)

// right now just for annotations lol:

const traceContextKey = "grpc-trace-bin"

// and obv need to figure out the data structures that actually get emitted in
// my scenarios, but will be much easier once I actually plumb this through, but
// need to verify that it makes sense.



// TagRPC creates a new trace span for the client side of the RPC.
//
// It returns ctx with the new trace span added and a serialization of the
// SpanContext added to the outgoing gRPC metadata.
func (c *ClientHandler) traceTagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	// the c lambda is for c's persisted start options. persisted start options
	// are needed to control things and plumb knobs through system/use the knobs
	// plumbed


	// so what actually gets emitted from the first call

	name := strings.TrimPrefix(rti.FullMethodName, "/") // trims the first /
	name = strings.Replace(name, "/", ".", -1) // replaces the middle / with .
	ctx, span := trace.StartSpan(ctx, name, // "s.m"
		trace.WithSampler(c.StartOptions.Sampler), // theres the trace sampler you plumb in
		trace.WithSpanKind(trace.SpanKindClient)) // span is ended by traceHandleRPC
	traceContextBinary := propagation.Binary(span.SpanContext())
	return metadata.AppendToOutgoingContext(ctx, traceContextKey, string(traceContextBinary))
}

// TagRPC creates a new trace span for the server side of the RPC.
//
// It checks the incoming gRPC metadata in ctx for a SpanContext, and if
// it finds one, uses that SpanContext as the parent context of the new span.
//
// It returns ctx, with the new trace span added.
func (s *ServerHandler) traceTagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	md, _ := metadata.FromIncomingContext(ctx)
	name := strings.TrimPrefix(rti.FullMethodName, "/")
	name = strings.Replace(name, "/", ".", -1)
	traceContext := md[traceContextKey]
	var (
		parent     trace.SpanContext
		haveParent bool
	)
	if len(traceContext) > 0 {
		// Metadata with keys ending in -bin are actually binary. They are base64
		// encoded before being put on the wire, see:
		// https://github.com/grpc/grpc-go/blob/08d6261/Documentation/grpc-metadata.md#storing-binary-data-in-metadata
		traceContextBinary := []byte(traceContext[0])
		parent, haveParent = propagation.FromBinary(traceContextBinary)
		// the public endpoint thing isn't set either, haveParent is just their being a trace ID, should this be the end?
		// nothing in spec about public endpoint...just leave out, add link yourself seems like the right thing to do, parent can be either remote or local so still needs that link regardless
		if haveParent && !s.IsPublicEndpoint { // the public endpoint gate is weird. Persist some other type of data as an option maybe disableRemoteSpanCreation bool or something
			ctx, _ := trace.StartSpanWithRemoteParent(ctx, name, parent, // does this call just add a link like below?
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithSampler(s.StartOptions.Sampler),
			)
			return ctx
			// oh can't return function because two things returned
		}
	}
	ctx, span := trace.StartSpan(ctx, name,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithSampler(s.StartOptions.Sampler))
	// either adds link or doesn't, just do both in my case?
	if haveParent { // this hits if no early return i.e. s.IsPublicEndpoitn set to true...wtf do we still need this? This is one of those arbitrary things that we want to change anyway...
		span.AddLink(trace.Link{TraceID: parent.TraceID, SpanID: parent.SpanID, Type: trace.LinkTypeChild})
	}
	return ctx
}

func traceHandleRPC(ctx context.Context, rs stats.RPCStats) {
	// ctx from whole thing
	span := trace.FromContext(ctx) // gets the span from context for each event no matter what type of event
	// TODO: compressed and uncompressed sizes are not populated in every message.
	switch rs := rs.(type) {
	case *stats.Begin:
		// ** attributes in span populated at first call period client and whether it fails fast
		span.AddAttributes( // bool attribute
			trace.BoolAttribute("Client", rs.Client),
			trace.BoolAttribute("FailFast", rs.FailFast))
		// **
	case *stats.InPayload:
		// data in span - one of those that will change for say streams with
		// multiple messages sent back and forth, important that it just changes
		// events in spans, not more spans

		// I need to do this message id iteration, persist that counter in
		// context - synchronization issues?

		span.AddMessageReceiveEvent(0 /* TODO: messageID */, int64(rs.Length), int64(rs.WireLength)) // message receive event
		// data in span
	case *stats.OutPayload:
		// just data in span - message send events so variable for streams
		span.AddMessageSendEvent(0, int64(rs.Length), int64(rs.WireLength)) // message sent event
		// just data in span
	case *stats.End:
		// data in span - status
		if rs.Error != nil {
			s, ok := status.FromError(rs.Error) // status
			if ok {
				span.SetStatus(trace.Status{Code: int32(s.Code()), Message: s.Message()})
			} else {
				// I think should be codes.Unknown - codes.Internal is for major errors
				span.SetStatus(trace.Status{Code: int32(codes.Internal), Message: rs.Error.Error()})
			}
		}
		// populating data in span ***
		// stats.End (whether derived from success or failure) actually triggers
		// the exported span, span is started from tag, client side no parent server side remote parent to client

		// oh so server obv. has to process the rpc before this event happens and it uploads

		// client starts rpc/span

		// server processes span,, span.End()?

		// then this uploads

		// upstream tooling links the two together

		span.End()
	}
}
