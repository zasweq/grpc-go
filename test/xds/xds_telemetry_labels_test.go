/*
 *
 * Copyright 2024 gRPC authors.
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
package xds_test

import "testing"

// TestTelemetryLabels tests that telemetry labels from CDS make their way to
// the stats handler. The stats handler sets the mutable context value that the
// cluster impl picker will write telemetry labels to, and then the stats
// handler asserts that subsequent HandleRPC calls from the RPC lifecycle
// contain telemetry labels that it can see.

func (s) TestTelemetryLabels(t *testing.T) {
	// need the xDS configuration that's needed to work...
	// basic xDS configuration that will make it work + CDS with correct labels plumbed in
	// (see unit tests in client for the two labels I want)

	// then make an RPC

	// stats handler plumbed in

	// stats handler asserts it can see telemetry labels...

}

type fakeStatsHandler struct {

}

// define an interceptor on it, have it plumb into context the labels needed

func (fsh *fakeStatsHandler) HandleRPC(/*sh method signature here...*/) {
	// for the events that we know can get it (i.e. not client started
	// metrics)...


}
