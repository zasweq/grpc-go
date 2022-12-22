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

// The following variables are measures are recorded by ClientHandler:
var (
	ClientSentMessagesPerRPC     = stats.Int64("grpc.io/client/sent_messages_per_rpc", "Number of messages sent in the RPC (always 1 for non-streaming RPCs).", stats.UnitDimensionless)
	ClientSentBytesPerRPC        = stats.Int64("grpc.io/client/sent_bytes_per_rpc", "Total bytes sent across all request messages per RPC.", stats.UnitBytes)
	ClientReceivedMessagesPerRPC = stats.Int64("grpc.io/client/received_messages_per_rpc", "Number of response messages received per RPC (always 1 for non-streaming RPCs).", stats.UnitDimensionless)
	ClientReceivedBytesPerRPC    = stats.Int64("grpc.io/client/received_bytes_per_rpc", "Total bytes received across all response messages per RPC.", stats.UnitBytes)
	ClientRoundtripLatency       = stats.Float64("grpc.io/client/roundtrip_latency", "Time between first byte of request sent to last byte of response received, or terminal error.", stats.UnitMilliseconds)
	ClientStartedRPCs            = stats.Int64("grpc.io/client/started_rpcs", "Number of started client RPCs.", stats.UnitDimensionless)
	ClientServerLatency          = stats.Float64("grpc.io/client/server_latency", `Propagated from the server and should have the same value as "grpc.io/server/latency".`, stats.UnitMilliseconds)
)

// Predefined views may be registered to collect data for the above measures.
// As always, you may also define your own custom views over measures collected by this
// package. These are declared as a convenience only; none are registered by
// default.
var (
	ClientSentBytesPerRPCView = &view.View{
		Measure:     ClientSentBytesPerRPC,
		Name:        "grpc.io/client/sent_bytes_per_rpc",
		Description: "Distribution of sent bytes per RPC, by method.",
		TagKeys:     []tag.Key{KeyClientMethod},
		Aggregation: DefaultBytesDistribution,
	}
	ClientReceivedBytesPerRPCView = &view.View{
		Measure:     ClientReceivedBytesPerRPC,
		Name:        "grpc.io/client/received_bytes_per_rpc",
		Description: "Distribution of received bytes per RPC, by method.",
		TagKeys:     []tag.Key{KeyClientMethod},
		Aggregation: DefaultBytesDistribution,
	}

	ClientRoundtripLatencyView = &view.View{
		Measure:     ClientRoundtripLatency,
		Name:        "grpc.io/client/roundtrip_latency",
		Description: "Distribution of round-trip latency, by method.",
		TagKeys:     []tag.Key{KeyClientMethod},
		Aggregation: DefaultMillisecondsDistribution,
	} // for latency, selectively test not the distribution
	ClientCompletedRPCsView = &view.View{
		Measure:     ClientRoundtripLatency,
		Name:        "grpc.io/client/completed_rpcs",
		Description: "Number of completed RPCs by method and status.",
		TagKeys:     []tag.Key{KeyClientMethod, KeyClientStatus},
		Aggregation: view.Count(),
	}
	ClientStartedRPCsView = &view.View{
		Measure:     ClientStartedRPCs,
		Name:        "grpc.io/client/started_rpcs",
		Description: "Number of started client RPCs.",
		TagKeys:     []tag.Key{KeyClientMethod},
		Aggregation: view.Count(),
	}
	ClientSentMessagesPerRPCView = &view.View{
		Measure:     ClientSentMessagesPerRPC,
		Name:        "grpc.io/client/sent_messages_per_rpc",
		Description: "Distribution of sent messages per RPC, by method.",
		TagKeys:     []tag.Key{KeyClientMethod},
		Aggregation: DefaultMessageCountDistribution,
	}
	ClientReceivedMessagesPerRPCView = &view.View{
		Measure:     ClientReceivedMessagesPerRPC,
		Name:        "grpc.io/client/received_messages_per_rpc",
		Description: "Distribution of received messages per RPC, by method.",
		TagKeys:     []tag.Key{KeyClientMethod},
		Aggregation: DefaultMessageCountDistribution,
	}
	ClientServerLatencyView = &view.View{
		Measure:     ClientServerLatency,
		Name:        "grpc.io/client/server_latency",
		Description: "Distribution of server latency as viewed by client, by method.",
		TagKeys:     []tag.Key{KeyClientMethod},
		Aggregation: DefaultMillisecondsDistribution,
	}
)

// DefaultClientViews are the default client views provided by this package.
var DefaultClientViews = []*view.View{
	ClientSentBytesPerRPCView,
	ClientReceivedBytesPerRPCView,
	ClientRoundtripLatencyView,
	ClientCompletedRPCsView,
}