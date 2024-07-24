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

package weightedroundrobin

import "testing"

func (s) TestMetricsScheduler(t *testing.T) {

	// Induce operations on scheduler...or do I need to hit balancer operations
	// on the top level balancer?

	// need a fake stats handler (which reads recorded instruments, and snapshot
	// it for testing...) with balancer top level just for metrics plumbing...

	// but also need to induce operations on the balancer...
	// including mocking weights...how does it mock weights?
	// What are the ways weight is even represented?

	// Load reporting comes either per call...
	// or oob...

	// So mock it that way?


}

// Full e2e deployment for OTel...just test Metrics Plumbing...basic...