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
	"google.golang.org/grpc/stats"
)

// can structure it so it doesn't call into unexported functions...

func (ch *clientStatsHandler) traceTagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	// does that name make sense how they do it?

	ch.to.disableTrace // have this option determine whether the trace actually should be recorded...like gate it almost...
	ch.to.ts // plumb this (assuming like they do into trace.StartSpan)
}

// for metrics - are my expectations of input/output correct?