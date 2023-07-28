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
	"testing"

	"google.golang.org/grpc/internal/grpctest"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// same structure? client and server side with instrumentation turned on?
// verify exporter?


// need to verify can plumb more than one instrumentation component into same ClientConn



func (s) TestMessAround(t *testing.T) {
	// fake exporter I think is way to go
	/*
	// David mentioned that using SDK/default global is similar to using
	// OpenCensus originally...
	sdktrace.NewTracerProvider(
			// batcher on the exporter
			// exp = sdktrace.SpanExporter...do I even need this NewTraceExporter
			// sdktrace.WithBatcher(exporter) <- configure exporter through passed in trace options?
			// and resource
			)
	*/

	// trace.Tracer is a TracerProvider.Tracer() <- Tracer provider comes from SDK and you register a SpanExporter

	// call Start() on trace.Tracer
}

// similar flow for metrics?