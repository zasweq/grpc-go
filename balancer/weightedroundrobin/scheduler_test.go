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

	// WRR deployed as top level (is that what Doug wrote)...
	// Specific checks

	// Into a metrics recorder which truly verifies...

	// specific cases

	// Think of cool scenarios...take basis off Doug's e2e tests...

	// right now verifies on a certain output...it waits for a md want maybe
	// wait for Doug to give his take on that...

	/*
	mdWant = stats.MetricsData{
			Handle:    (*estats.MetricDescriptor)(intGaugeHandle),
			IntIncr:   5,
			LabelKeys: []string{"int gauge label", "int gauge optional label"},
			LabelVals: []string{"int gauge label val", "int gauge optional label val"},
		}
		mr1.WaitForInt64Gauge(ctx, mdWant)
	*/


	// How do you deploy and also induce operations? See his testing infra with orca servers etc...?

}

// TestMetricsRecorder for granular validations...

// Full e2e deployment for OTel...just test Metrics Plumbing...basic...
func (s) TestFullDeployment(t *testing.T) {


	// I'm assuming test all 5 emissions, how to trigger in base case... there
	// has to be infra to deploy this custom lb...with it running in xDS tree...

	// Custom LB I think has this? or just rr and wrr

	// It depends on whether I want locality label to show up, if not from
	// weighted target mock it in resolver attributes configured...


}

