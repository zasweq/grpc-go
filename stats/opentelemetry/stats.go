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
	"google.golang.org/grpc/stats"
)

// same distributions here...

// also figure out what info/state you need to persist...

// for both client and server side
// stats handlers, need to figure out OpenCensus way of TagRPC and sticking shit in metadata

func (csh *clientStatsHandler) statsTagRPC(ctx context.Context, info *stats.RPCTagInfo) (context.Context /*, info you need to take metrics*/) {

}

func (ssh *serverStatsHandler) statsTagRPC(ctx context.Context, info *stats.RPCTagInfo) (context.Context /*, info needed to record it here...*/) {
	// timestamp for start time and also method name...helps record shit

	// it gets tag info from the bin header and then decodes
	// it using an ***opencensus*** package


}




// switch on type sending to recording points...

func switchLol(ctx context.Context, s stats.RPCStats) {
	// gate if tag hasn't been called just to be defensive and branch doesn't affect that much...

	// is it all through the ctx?
	// opencensus takes metrics info which are recording points
	// and uses that to take measuremetns, expect one which needs context

	switch st := s.(type) {
	// I thinkkk this not recording still applies...
	// because I think new metrics are a literal subset of old metrics...
	case *stats.InHeader, *stats.OutHeader, *stats.InTrailer, *stats.OutTrailer:
		// Headers and Trailers are not relevant to the measures, as the
		// measures concern number of messages and bytes for messages. This
		// aligns with flow control.
	case *stats.Begin:

	case *stats.OutPayload:

	case *stats.InPayload:

	case *stats.End:

	default:
	}

}

// figure out interface with opentel metrics recording equivalent to opencensus recording equivalent

// the docs tell me OpenTel metrics are in Beta...so only thing I can really do is trances
