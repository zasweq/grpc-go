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
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

var (
	keyClientMethod = tag.MustNewKey("grpc_client_method")
	keyClientStatus = tag.MustNewKey("grpc_client_status")
)

// Measures, which are recorded by client stats handler: Note that due to the
// nature of how stats handlers are called on gRPC's client side, the per rpc
// unit is actually per attempt throughout this definition file.
var (
	clientSentMessagesPerRPC     = stats.Int64("grpc.io/client/sent_messages_per_rpc", "Number of messages sent in the RPC (always 1 for non-streaming RPCs).", stats.UnitDimensionless)
	clientSentBytesPerRPC        = stats.Int64("grpc.io/client/sent_bytes_per_rpc", "Total bytes sent across all request messages per RPC.", stats.UnitBytes)
	clientReceivedMessagesPerRPC = stats.Int64("grpc.io/client/received_messages_per_rpc", "Number of response messages received per RPC (always 1 for non-streaming RPCs).", stats.UnitDimensionless)
	clientReceivedBytesPerRPC    = stats.Int64("grpc.io/client/received_bytes_per_rpc", "Total bytes received across all response messages per RPC.", stats.UnitBytes)
	clientRoundtripLatency       = stats.Float64("grpc.io/client/roundtrip_latency", "Time between first byte of request sent to last byte of response received, or terminal error.", stats.UnitMilliseconds)
	clientStartedRPCs            = stats.Int64("grpc.io/client/started_rpcs", "The total number of client RPCs ever opened, including those that have not completed.", stats.UnitDimensionless)
	clientServerLatency          = stats.Float64("grpc.io/client/server_latency", `Propagated from the server and should have the same value as "grpc.io/server/latency".`, stats.UnitMilliseconds)
	// Per call
	clientApiLatency             = stats.Float64("grpc.io/client/api_latency", "The end-to-end time the gRPC library takes to complete an RPC from the application’s perspective", stats.UnitMilliseconds)
)

var (
	// ClientSentMessagesPerRPCView is the distribution of sent messages per
	// RPC, keyed on method.
	ClientSentMessagesPerRPCView = &view.View{
		Measure:     clientSentMessagesPerRPC,
		Name:        "grpc.io/client/sent_messages_per_rpc",
		Description: "Distribution of sent messages per RPC, by method.",
		TagKeys:     []tag.Key{keyClientMethod},
		Aggregation: countDistribution,
	}
	// ClientReceivedMessagesPerRPCView is the distribution of received messages
	// per RPC, keyed on method.
	ClientReceivedMessagesPerRPCView = &view.View{
		Measure:     clientReceivedMessagesPerRPC,
		Name:        "grpc.io/client/received_messages_per_rpc",
		Description: "Distribution of received messages per RPC, by method.",
		TagKeys:     []tag.Key{keyClientMethod},
		Aggregation: countDistribution,
	}
	// ClientSentBytesPerRPCView is the distribution of sent bytes per RPC,
	// keyed on method.
	ClientSentBytesPerRPCView = &view.View{
		Measure:     clientSentBytesPerRPC,
		Name:        "grpc.io/client/sent_bytes_per_rpc",
		Description: "Distribution of sent bytes per RPC, by method.",
		TagKeys:     []tag.Key{keyClientMethod},
		Aggregation: bytesDistribution,
	}
	// ClientReceivedBytesPerRPCView is the distribution of received bytes per
	// RPC, keyed on method.
	ClientReceivedBytesPerRPCView = &view.View{
		Measure:     clientReceivedBytesPerRPC,
		Name:        "grpc.io/client/received_bytes_per_rpc",
		Description: "Distribution of received bytes per RPC, by method.",
		TagKeys:     []tag.Key{keyClientMethod},
		Aggregation: bytesDistribution,
	}
	// ClientStartedRPCsView is the count of opened RPCs, keyed on method.
	ClientStartedRPCsView = &view.View{
		Measure:     clientStartedRPCs,
		Name:        "grpc.io/client/started_rpcs",
		Description: "Number of opened client RPCs, by method.",
		TagKeys:     []tag.Key{keyClientMethod},
		Aggregation: view.Count(),
	}
	// ClientCompletedRPCsView is the count of completed RPCs, keyed on method
	// and status.
	ClientCompletedRPCsView = &view.View{
		Measure:     clientRoundtripLatency,
		Name:        "grpc.io/client/completed_rpcs",
		Description: "Number of completed RPCs by method and status.",
		TagKeys:     []tag.Key{keyClientMethod, keyClientStatus},
		Aggregation: view.Count(),
	}
	// ClientRoundtripLatencyView is the distribution of round-trip latency in
	// milliseconds per RPC, keyed on method.
	ClientRoundtripLatencyView = &view.View{
		Measure:     clientRoundtripLatency,
		Name:        "grpc.io/client/roundtrip_latency",
		Description: "Distribution of round-trip latency, by method.",
		TagKeys:     []tag.Key{keyClientMethod},
		Aggregation: millisecondsDistribution,
	}

	// The following metric is per call:
	ClientApiLatencyView = &view.View{
		// Measure client per call latency or something
		Measure: clientApiLatency,
		Name: "grpc.io/client/api_latency",
		Description: "Distribution of client api latency, by method and status",
		TagKeys: []tag.Key{keyClientMethod, keyClientStatus},
		Aggregation: millisecondsDistribution, // is this the right aggregation?
	}

)

// DefaultClientViews is the set of client views which are considered the
// minimum required to monitor client side performance.
var DefaultClientViews = []*view.View{
	ClientSentBytesPerRPCView,
	ClientReceivedBytesPerRPCView,
	ClientRoundtripLatencyView,
	ClientCompletedRPCsView,
	ClientStartedRPCsView,
}

// fixing import:
// manually put changes into citc
// fix the p2c diff
// delete GOA exemption

// you can run tests and stuff but you don't need to...run them through cl front end


// then interop on call stuff

// then per call latency stuff/design docs


// filtering out will need a new pr with a client option to disable global
// options - perhaps wait on import to go through?
// then use that with my musings about filtering in that pr


// Oh also have his tracing comments now

// top level ctx population per package

// o(2n) on every event, smaller you can make that the better
// metrics stuff
// traces stuff

// think about it

// md needs to be immutable...stores updates to md in the middle of context hierarchy
// both grpc metadata, ctx value needs to be immutable
// because of derived context logic, you don't want it to change for all of them

// fill it out once at beginning

// then repull it out of context
// wrapper for
//     span
//     message id
//     all the metrics data

// how does the disableTrace boolean gate relate to this?
// you only have to turn on one, but you can still reuse this memory


// add the three bullet points for how to link traces/span

// tagRPC
//   info :=  statsTagRPC
//   info :=  traceTagRPC
//   concatanate info and store in context, return context




// (how will this happen wrt aggregation between metrics and traces, return data
// then aggregate at top level tagRPC call?)

// handleRPC
// pull Data out of context, if nil return
// statsTagRPC
// traceTagRPC

// disableTrace just gate both tagRPC and handleRPC calls? (and eventually top
// level interceptor)






// note: traces disable trace bit done once traces pr merged
// as part of filtering out calls to cloudops endpoints