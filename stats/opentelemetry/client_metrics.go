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

// figure out how to import go otel go get...?
import (
	"go.opentelemetry.io/otel/trace"
)


// distribution bounds...here or in other package
// We keep the same definitions for the histogram buckets as the ones in the OC
// spec.

// For buckets with sizes in bytes (‘By’) they have the following boundaries: 0,
// 1024, 2048, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864,
// 268435456, 1073741824, 4294967296. Called SizeBuckets.

// For buckets with latencies in seconds (‘s’) (float64 number) they have the
// following boundaries: 0, 0.00001, 0.00005, 0.0001, 0.0003, 0.0006, 0.0008,
// 0.001, 0.002, 0.003, 0.004, 0.005, 0.006, 0.008, 0.01, 0.013, 0.016, 0.02,
// 0.025, 0.03, 0.04, 0.05, 0.065, 0.08, 0.1, 0.13, 0.16, 0.2, 0.25, 0.3, 0.4,
// 0.5, 0.65, 0.8, 1, 2, 5, 10, 20, 50, 100. Called LatencyBuckets.

// Note that the OTel API does not currently provide the ability to add in
// boundaries to the instrument, but the new iteration on the API makes it
// possible to give ’advice’ on the boundaries.





// grpc.client.attempt.started
// the total number of RPC attempts started, including those that have not completed...




// grpc.client.attempt.completed