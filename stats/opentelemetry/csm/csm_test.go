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

package csm

import "testing"

// GA Runner for 1.20 to skip OTel...look in go.mod in GA Runner...

func (s) TestCSMPluginOption(t *testing.T) {

	// How to construct OTel?


	// Two global OTel that live in the ether...this is the flow for how I
	// construct the OTel plugin with the option, add something internal, need
	// to represent that in code somehow



	// Then this test tests how this actually creates it, and then verifies
	// metrics on it...

	// Same API I've been musing about wrt OTel options...


	// Set it up and then make an rpc, expect the emitted metrics have same
	// labels... have label creation go through same helper? and persist both
	// sets around

	// same exact thing as OpenTelemetry e2e test

	// started RPC's and others don't see the new labels
	// Sanity check? Any failing? Stuff gets plumbed from unit tests

	// Sanity check in headers (from server)
	// Sanity check in trailers only (from server)

	// unary/streaming...can I induce headers/trailers on both types?

	// Difference between this and global setting is global setting sets it for the binary through global which gets picked up

	// And this explicitly just sets a dial/server option that is created on the heap in this test
	//


	// Next layer PR separate for global?

}

// one of these will need to setup xDS and CSM on... - test the full flow later and mock the labels by
// plumbing into context...

// OTel top level example...
