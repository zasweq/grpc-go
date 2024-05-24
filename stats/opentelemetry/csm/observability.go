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

import (
	"context"
	"net/url"

	"google.golang.org/grpc"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/stats/opentelemetry"
	otelinternal "google.golang.org/grpc/stats/opentelemetry/internal"
)

var clientSideOTelWithCSM grpc.DialOption
var clientSideOTel grpc.DialOption

// Observability sets up CSM Observability for the binary globally. It sets up
// two client side OpenTelemetry instrumentation components configured with the
// provided options, one with a CSM Plugin Option configured, one without.
// Created channels will pick up one of these dependent on whether channels are
// CSM Channels or not, which is derived from the Channel's parsed target after
// dialing.
//
// It sets up a server side OpenTelemetry instrumentation component configured
// with options provided alongside a CSM Plugin Option to be registered globally
// and picked up for every server.
//
// The CSM Plugin Option is instantiated with local labels and metadata exchange
// labels pulled from the environment, and emits metadata exchange labels from
// the peer and local labels. Context timeouts do not trigger an error, but set
// certain labels to "unknown".
//
// This function is not thread safe, and should only be invoked once in main
// before any channels or servers are created. Returns a cleanup function to be
// deferred in main.
func Observability(ctx context.Context, options opentelemetry.Options) func() {
	csmPluginOption := newPluginOption(ctx)
	clientSideOTelWithCSM = dialOptionWithCSMPluginOption(options, csmPluginOption)
	clientSideOTel = opentelemetry.DialOption(options)

	serverSideOTelWithCSM := serverOptionWithCSMPluginOption(options, csmPluginOption)

	internal.AddGlobalPerTargetDialOptions.(func(opt any))(perTargetDialOption)

	internal.AddGlobalServerOptions.(func(opt ...grpc.ServerOption))(serverSideOTelWithCSM)

	return func() {
		internal.ClearGlobalServerOptions()
		internal.ClearGlobalPerTargetDialOptions()
	}
} // fail, can run it locally and on cloudtop, replace to point to this stats/opentelemetry (replace grpc or something else)?

// make it's own go mod
// go get a random version using hash
// then replace
// try and build docker image using that docker run thing, this is the hook into interop scripts...

// replace grpc module and otel module I guess

func perTargetDialOption(parsedTarget url.URL) grpc.DialOption {
	if determineTargetCSM(&parsedTarget) {
		return clientSideOTelWithCSM
	}
	return clientSideOTel
}

func dialOptionWithCSMPluginOption(options opentelemetry.Options, po otelinternal.PluginOption) grpc.DialOption {
	options.MetricsOptions.OptionalLabels = []string{"csm.service_name", "csm.service_namespace"} // Attach the two xDS Optional Labels for this component to not filter out.
	otelinternal.SetPluginOption.(func(options *opentelemetry.Options, po otelinternal.PluginOption))(&options, po)
	return opentelemetry.DialOption(options)
}

func serverOptionWithCSMPluginOption(options opentelemetry.Options, po otelinternal.PluginOption) grpc.ServerOption {
	otelinternal.SetPluginOption.(func(options *opentelemetry.Options, po otelinternal.PluginOption))(&options, po)
	return opentelemetry.ServerOption(options)
}
