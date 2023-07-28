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

package opentelemetry

import (
	"context"
	"google.golang.org/grpc/status"

	"google.golang.org/grpc/stats"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace" // doesn't have this, this might be the actual implementation
)

// exporter
// resources
// tracer provider
// tracer

var tracer trace.Tracer // tracer = tp.Tracer(), tp is trace provider and you call global method on it...

// you program against their API, not against their SDK

/*
OTel differentiates between ‘API’ and ‘SDK’ where API is just meant to contain
interfaces for libraries to use to instrument their code, whereas ‘SDK’ is the
actual implementation of the API. There is a strong intention from the OTel team
for libraries like gRPC to use the API for their OTel instrumentation and not
the SDK. (In other words, if we have to use the SDK for instrumentation, the
design has failed.)

// setup libraries can still depend on OTel SDK and would need to do that if
// configuring exporters through libraries...
*/

func messAround() *sdktrace.TracerProvider { // but Yash says to program to their API, not to use the specific sdk provided?
	r, err := resource.Merge() // on their sdk resource package...
	sdktrace.NewTracerProvider(
		// batcher on the exporter
		// exp = sdktrace.SpanExporter...do I even need this NewTraceExporter
		// sdktrace.WithBatcher(exporter) <- configure exporter through passed in trace options?
		// and resource
		)
	// it seems this is dependent on the exporter...unless you can construct than call methods on constructed object
}


/*
The OpenCensus spanContext and OpenTelemetry spanContext transmitted in binary over the wire are identical, therefore a gRPC OpenCensus client can speak with a gRPC OpenTelemetry server and vice versa
*/


func (csh *clientStatsHandler) traceTagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	// also is attempt span logic the same...wrt naming?

	// same trace structure? triage from design doc?
	// sent (come in interceptor)
	//    attempt (comes here)
	//    attempt
	// If the current context.Context you have a handle on already contains a
	// span inside of it, creating a new span makes it a nested span. For
	// example:
	// trace.Start - parent span
	// trace.Start - span is nested span whose parent is parentSpan

	// does this stick the created span in the context...?
	// need to put both span and metrics shit in one object for fast gets?
	ctx, span := tracer.Start(ctx, "the desired span name here - same as opencensus? from spec I thinkkkk so") // global tracer, this makes a child if not a child...
	// In Go, the context package is used to store the active span. When you start a span, you’ll get a handle on not only the span that’s created, but the modified context that contains it.
	span.End() // persist it in context
	// Once a span has completed, it is immutable and can no longer be modified.

}

// internal can return whatever you want
func (ssh *serverStatsHandler) traceTagRPC(ctx context.Context, rti *stats.RPCTagInfo) {
	// OpenCensus pulls method name off tag rpc info to populate span here...


	// either the api is different and you don't need to do this (if you can do both at once)
	// or it's the same and you just do this branch in different ways
	// opencensus functionality (need to figure out how this maps to otel)
	// try and get the trace context from the ctx passed in...
	//         if can pull off, start span with remote parent and also add a link
	// if not
	//         just start span



	// ivy has a lot of logic in hers about this
	// especially wrt c encoding binary headers or something like that...

}

func (csh *clientStatsHandler) traceHandleRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	// pull something out from context...
	// either traceinfo or span...

	var span trace.Span // this is an interface lol


	// fill out span data


}

// both sides call populate span? or server side just top level call...

// what does Ivy say about the relationship between server metrics
// and client traces...logically equivalent

// also metrics such as per call no retry delay need to stick something somewhere...
func populateSpan(rs stats.RPCStats/*the same span type except the opentelemetry...*/) {
	var span trace.Span
	switch rs := rs.(type) {
	case *stats.Begin:
		// same attributes and are we constructing same trace object as previously?
		// if so then: (although I'm pretty sure these attributes are just
		// arbitrary...and not really speced or needed?
		// client rs.Client
		// failfast rs.FailFast
		span.SetAttributes(attribute.Bool("Client", rs.FailFast))

	case *stats.InPayload:
		// span.AddMessageReceiveEvent(int64(mi), int64(rs.Length), int64(rs.CompressedLength))
		// option 1:
		span.SetAttributes(/*attributes corresponding to message recv here?*/)

		// also is there a way to plumb in count to these event options???
		// option 2:
		span.AddEvent("name here...?", /*EventOption interface in trace package, find corresponding EventOption that represents MessageReceiveEvent*/)
	case *stats.OutPayload:
		// should I could message receives, it it scoped to same thing?
		// after atomically iterating mi
		// span.AddMessageSendEvent(int64(mi), int64(rs.Length), int64(rs.CompressedLength))
		// is the equivalent:
		// 1.
		span.SetAttributes(/*attributes corresponding to message send events*/)
		span.AddEvent()

	// picker callout...do something with that?
	// also any other stats handler processing?
	case *stats.End:
		if rs.Error != nil {
			s := status.Convert(rs.Error)
			// there's only three possible codes in their package:
			// 1. codes.Ok
			// 2. codes.Error
			// 3. codes.Unset
			// that's a subset of the full status state space...
			span.SetStatus(codes.Ok, s.Message())
		} else {
			// trace.SpanKind status
			span.SetStatus(codes.Ok, "")
		}
		// populate status stuff?
		// could do it exactly the same

		span.End()
	}

	// any other events to process...?
}



// ALSO WRITE A README FOR MIGRATING FROM OLD TO NEW

// also how to test this module...same as opencensus except with opentelemetry concepts?
