/*
 *
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
 *
 */

// Package experimental is a collection of experimental features that might
// have some rough edges to them. Housing experimental features in this package
// results in a user accessing these APIs as `experimental.Foo`, thereby making
// it explicit that the feature is experimental and using them in production
// code is at their own risk.
//
// All APIs in this package are experimental.
package experimental

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/instrumentregistry"
)

// WithRecvBufferPool returns a grpc.DialOption that configures the use of
// bufferPool for parsing incoming messages on a grpc.ClientConn. Depending on
// the application's workload, this could result in reduced memory allocation.
//
// If you are unsure about how to implement a memory pool but want to utilize
// one, begin with grpc.NewSharedBufferPool.
//
// Note: The shared buffer pool feature will not be active if any of the
// following options are used: WithStatsHandler, EnableTracing, or binary
// logging. In such cases, the shared buffer pool will be ignored.
//
// Note: It is not recommended to use the shared buffer pool when compression is
// enabled.
func WithRecvBufferPool(bufferPool grpc.SharedBufferPool) grpc.DialOption {
	return internal.WithRecvBufferPool.(func(grpc.SharedBufferPool) grpc.DialOption)(bufferPool)
}

// RecvBufferPool returns a grpc.ServerOption that configures the server to use
// the provided shared buffer pool for parsing incoming messages. Depending on
// the application's workload, this could result in reduced memory allocation.
//
// If you are unsure about how to implement a memory pool but want to utilize
// one, begin with grpc.NewSharedBufferPool.
//
// Note: The shared buffer pool feature will not be active if any of the
// following options are used: StatsHandler, EnableTracing, or binary logging.
// In such cases, the shared buffer pool will be ignored.
//
// Note: It is not recommended to use the shared buffer pool when compression is
// enabled.
func RecvBufferPool(bufferPool grpc.SharedBufferPool) grpc.ServerOption {
	return internal.RecvBufferPool.(func(grpc.SharedBufferPool) grpc.ServerOption)(bufferPool)
}

// This needs to be public but takes a handle...but internal can't depend on external,
// takes a handle...so symbol needs to be public?

// external thing it depends on, just can't have a cycle (this blocks everything
// right) can have internal depend on external, even of the same name so maybe
// try that? Even labels then won't have two/try and conflate the string
// underneath

// Alongside this Metric thing, can figure out how to merge the defaults...
// and share Metric symbol...

// MetricsRecorder...
type MetricsRecorder interface {
	RecordIntCount(instrumentregistry.Int64CountHandle, []instrumentregistry.Label, []instrumentregistry.Label, int64)

	RecordFloatCount(instrumentregistry.Float64CountHandle, []instrumentregistry.Label, []instrumentregistry.Label, float64)

	RecordIntHisto(instrumentregistry.Int64HistoHandle, []instrumentregistry.Label, []instrumentregistry.Label, int64)

	RecordFloatHisto(instrumentregistry.Float64CountHandle, []instrumentregistry.Label, []instrumentregistry.Label, float64)

	RecordIntGauge(instrumentregistry.Int64GaugeHandle, []instrumentregistry.Label, []instrumentregistry.Label, int64)
} // symbols external, move all these symbols, move exported types here experimental/something (something actual package maybe stats this prevents stuttering)



// This PR: OpenTelemetry usage of this...OTel reads instrument registry including defaults...
// where to define Metric?

// Move handles and labels to somewhere shared (conflicts with PR in flight), ask Doug. Users need to be able to implement this so
// need the symbols exported, registration internal.

// And then figure out where to put Metric, can't take a dependency on OTel, somewhere shared that opensource users
// and other packages can use like this for this typecast. "Can move stuff around this stuff isn't stable"

// OTel usage of it and also Default logic is the main thing being tested wrt symbols...
// New Metrics Recorder and Default metrics that get picked up are thing main things,
// test at this layer but how...could make part of below...

// Could put the symbols needed in this package, and have the instrument
// registry take a dependency on this, it is experimental (subject to change
// Yash really cared, there's is internal for now)

// Test typecast? It returns a stats handler that can get type asserted to it...

// In some form or another need to call methods





// How to test? vvv
// Main thing layer does is eat optional labels/required labels and forward
// to all underlying stats handler, so set some dial options and test typecasting and eating labels...

// PR after: (MetricsRecorder list built in to client conn creation that
// typecasts sh down). Once I do this can do full e2e testing with real otel
// plugin, or fake stats plugins (set on the channel). See Mark's comment for
// tests.

// Dial Option? Not needed since typecast


// Create a component inline in NewClient that typecasts all set local and
// global dial options to this, builds a component inline to send to the
// balancers...

// MetricsRecorder list from full musing, eats labels/optional labels for
// programmer error iteration as well...





// Then: pick first/rls metrics :):

// So fake stats plugin above?

// So edge casey things just check emissions at fake stats handler

// Full e2e with OTel emissions is just to make sure plumbing works, unit tests
// in the lower state space with specific test the main scenarios


/* Mark's comment about testing at this layer:

In C-core, we try to have both unit tests and e2e tests to verify that things
work at both levels.  The unit tests tend to be very detailed, covering a lot of
edge cases, whereas the e2e tests are more coarse-grained, really just making
sure that all of the components are plumbed together correctly.

In this case, for unit tests, we have them for WRR and PF, showing that they
export data to a fake stats plugin.  We also have a unit test for XdsClient
showing that it exposes the right data, although it doesn't directly speak to
the gRPC stats plugin due to the need to abstract this for use in stubby, so
that unit test uses a different interface than a fake stats plugin.  We do not
currently have any unit tests for RLS due to pre-existing tech debt, so I didn't
add any for RLS metrics.

For e2e tests, we have basic tests for all four components showing that the
expected metrics are exported to OT with all of the expected labels.  Note that
at this level, we're really just testing a single case for each metric/label,
not worrying about every single possible edge case, since those should generally
be covered in unit tests.  (For RLS, since there are currently no unit tests, I
did strive for more full coverage in the e2e tests.)

*/

// When I come back start fresh or rebase?
