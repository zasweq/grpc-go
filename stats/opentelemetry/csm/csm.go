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

package csm // package csm? exposed to users in same package, takes dependency on otel so yeah

import (
	"google.golang.org/grpc/stats/opentelemetry"
	"google.golang.org/grpc/stats/opentelemetry/internal/csm"
)

// Rebase this onto new thing

// The setting of csm plugin option happens through an internal function
// unexported internal (internal not important - the determinant of interface not showing up is unexported methods, which it can't search for outside package)
// set through an internal only function - see ORCA for example



// global setup - one global function that takes in otel options
func GlobalSetup(options opentelemetry.Options) {
	// seems like we're going two global instances route:
	// dial option + otel (late apply dial option)
	// just dial option

	// server option + otel (join server options? blocks this is this already present)
	// just server option
}

func DialOption(options opentelemetry.Options) {
	options.MetricsOptions.PluginOption = csm.NewPluginOption() // I guess this type can be kept internal
	opentelemetry.DialOption(options)
}

func ServerOption(options opentelemetry.Options) {
	options.MetricsOptions.PluginOption = csm.NewPluginOption()
	opentelemetry.ServerOption(options)
}

// need to add example for this...probably local example as well for this...
// example for this layer or otel layer?

// musing

func configureOTelWithOptions(options opentelemetry.Options) {
	opentelemetry.DialOption(options)
	opentelemetry.ServerOption(options)
	options.MetricsOptions
	options.MetricsOptions.PluginOption
}

// once I figure out this API this is how I will unit test...

// Just extra layer around DialOption() (adds csm plugin option)
// and ServerOption() (adds csm plugin option)
// e2e test for this layer will call this - same thing as OTel e2e with metrics expected
// except with extra labels, and also figure out a way to induce trailers only

// global will call that ^^^
