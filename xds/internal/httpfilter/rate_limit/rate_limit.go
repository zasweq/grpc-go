/*
 *
 * Copyright 2021 gRPC authors.
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

// Package rate limit implements the Envoy Rate Limiting Filter.
package rate_limit

import (
	"context"
	"fmt"
	route_pb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	rls_pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/golang/protobuf/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/resolver"

	"google.golang.org/grpc/xds/internal/httpfilter"
)

func init() {
	// Guard with env var?
	httpfilter.Register(builder{})
}

type builder struct{
}

type config struct {
	httpfilter.FilterConfig
	config *route_pb.RateLimit
}

func (builder) TypeURLs() []string {
	return []string{
		"type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.HTTPRateLimit", // <- make sure this is correct
	}
}

// Parsing config will come later - requires validations to be defined

func parseConfig(rlCfg route_pb.RateLimit/*Data type of rate limiting configuration - use this for figuring out what's branches of logic*/) {
	rlCfg
	// Just validations - not defined yet in Sergiis design doc
}

func (builder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	// Envoy changed this to be a simple top level configuration - figure out
	// what data type this ends up being.
}

func (builder) ParseFilterConfigOverride(override proto.Message) (httpfilter.FilterConfig, error) {
	// This is the override configured per route and vh - I don't know if
	// these have two different data types.
}

func (builder) IsTerminal() bool {
	return false
}

var _ httpfilter.ServerInterceptorBuilder = builder{}

// BuildServerInterceptor is an optional interface builder implements in order
// to signify it works server side.
func (builder) BuildServerInterceptor(cfg httpfilter.FilterConfig, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	// This actually uses the config
	if cfg == nil {
		return nil, fmt.Errorf("rate limit: nil config provided")
	}

	c, ok := cfg.(config)
	if !ok {
		return nil, fmt.Errorf("rate limit: incorrect config type provided (%T): %v", cfg, cfg)
	}

	if override != nil {
		// override completely replaces the listener configuration; but we
		// still validate the listener config type.
		c, ok = override.(config)
		if !ok {
			return nil, fmt.Errorf("rbac: incorrect override config type provided (%T): %v", override, override)
		}
	}

	icfg := c.config
	descriptor_generator{
		actions: icfg.Actions,
	} // Pass this over to descriptor_generator builder - just do this inline
	icfg.DisableKey // ?
	icfg.Limit // ?
	icfg.Stage // ?

	// I don't think a global one makes sense

	// And connection to RLS Server...?, will have to throw this into a service or something
	// RLS Server in go - FIND IT!
	cc, err := grpc.Dial()
	if err != nil {
		return nil, fmt.Errorf("rate limit: error dialing to RLS Server: %v", err)
	}

	// rlsConn := pb.NewGreeterClient(conn)

	return &interceptor{
		rlsConnection: cc,
		dg: descriptor_generator{
			actions: icfg.Actions,
		},
	}, nil
}

// Holds onto descriptor entry
// (Descriptor: quota) could get more than one per response
type rqCache struct {
	// descriptor: ((key, value) (key, value)) // <- this needs to be matched to
	// Full descriptor, or just a descriptor entry...look at cache proto
	// I'm pretty sure that it's for a full descriptor, so a list of Key value pairs? How to check for existence?
	quota int // will go down by 1 each time quota met
	// GoLang version of a time limit
}

type interceptor struct {
	// What state does this need?
	// Connection to RLS Server to ask it what's going on
	// Does this share a global connection to RLS Server, or does it create one per filter
	rlsConnection *grpc.ClientConn // << change this to the greeter client type?
	// "CA and TD will provide an RLS implementation backed by Bouncer"
	// "Feature parity with Envoy's GLOBAL (not local!) rate limiting (HTTP Filter only)"

	// Quota cache: Descriptor (key, value) -> (quota number, time limit)
	// Define this data structure ^^^, and also finish doing actions

	dg descriptor_generator
}

func (i *interceptor) AllowRPC(ctx context.Context) error {
	// Build out descriptors - this could be a data structure that you call into - all the RPC Info you need is in context
	// Pass in ctx (RPC Info inside it), get back a list of descriptors to pass query management server, you need to build this out
	// in both branches of logic, because this also will match to the cached descriptors
	// You now have descriptor(s) built!
	// This descriptor generator needs to spit out: descriptor (descriptor entry, descriptor entry), descriptor (descriptor entry, descriptor entry), descriptor (descriptor entry, descriptor entry)
	//                                                  how do actions fit into this puzzle ^^^?

	rls_pb.RateLimitRequest{
		// All rate limit requests must specify a domain. This enables the configuration to be per
		// application without fear of overlap. E.g., "envoy".
		Domain: /*What is the domain? I'm guessing grpc?*/,

		Descriptors: /*Once you have a black box that spits back out descriptors*/,
		// Optionally specify number of hits a matching request adds to limit - I don't think grpc will support this
		HitsAddend: /*What is this?*/,
	}



	//
	// Query RLS Server - Link proto API here for this definition and to play around with it in the case where there
	// is no quota currently available?

	// Look into cache - three options for querying - list them (by linking proto :P)
	// Quota granted for the descriptor. This is a certain number of requests over a period of time.
	// The client may cache this result and apply the effective RateLimitResponse to future matching
	// requests containing a matching descriptor without querying rate limit service.
	//
	// Quota is available for a request if its descriptor set has cached quota available for all
	// descriptors.
	//
	// If quota is available, a RLS request will not be made and the quota will be reduced by 1 for
	// all matching descriptors.
	//
	// If there is not sufficient quota, there are three cases:
	// 1. A cached entry exists for a RLS descriptor that is out-of-quota, but not expired.
	//    In this case, the request will be treated as OVER_LIMIT.
	// 2. Some RLS descriptors have a cached entry that has valid quota but some RLS descriptors
	//    have no cached entry. This will trigger a new RLS request.
	//    When the result is returned, a single unit will be consumed from the quota for all
	//    matching descriptors.
	//    If the server did not provide a quota, such as the quota message is empty for some of
	//    the descriptors, then the request admission is determined by the
	//    :ref:`overall_code <envoy_v3_api_field_service.ratelimit.v3.RateLimitResponse.overall_code>`.
	// 3. All RLS descriptors lack a cached entry, this will trigger a new RLS request,
	//    When the result is returned, a single unit will be consumed from the quota for all
	//    matching descriptors.
	//    If the server did not provide a quota, such as the quota message is empty for some of
	//    the descriptors, then the request admission is determined by the
	//    :ref:`overall_code <envoy_v3_api_field_service.ratelimit.v3.RateLimitResponse.overall_code>`.
	//
	// When quota expires due to timeout, a new RLS request will also be made.
	// The implementation may choose to preemptively query the rate limit server for more quota on or
	// before expiration or before the available quota runs out.

	// Logically, this is tied to a list of descriptors, not a single descriptor

	// What exactly are we caching?...(key and value) linked to value?

}