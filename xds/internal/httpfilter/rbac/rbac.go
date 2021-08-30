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

// Package rbac implements the Envoy RBAC HTTP filter.
package rbac

import (
	"context"
	"errors"
	"fmt"
	v3rbacpb "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes"
	"google.golang.org/grpc/internal/xds/rbac"
	"strings"

	//envoy_config_rbac_v3 "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
	//envoy_config_rbac_v3 "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
	//envoy_config_rbac_v3 "github.com/envoyproxy/go-control-plane/envoy/config/rbac/v3"
	"google.golang.org/grpc/internal/resolver"
	"google.golang.org/grpc/xds/internal/httpfilter"
	"google.golang.org/protobuf/types/known/anypb"

	rpb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/rbac/v3"
)

func init() {
	httpfilter.Register(builder{})
}

type builder struct {
}

type config struct {
	httpfilter.FilterConfig
	config *rpb.RBAC
	// rpb.RBACPerRoute // Contains a pointer to RBAC only
}

func (builder) TypeURLs() []string {
	return []string{"type.googleapis.com/envoy.extensions.filters.http.rbac.v3.RBAC"} // <- make sure this is correct
}

// Parsing is the same for the base config and the override config.
func parseConfig(cfg proto.Message) (httpfilter.FilterConfig, error) { // what's flow of how these are called again - look at xds.go
	if cfg == nil {
		// Are we sure we need this?
	}
	any, ok := cfg.(*anypb.Any)
	if !ok {
		return nil, fmt.Errorf("rbac: error parsing config %v: unknown type %T", cfg, cfg)
	}
	// All the validation logic described in A41.
	msg := new(rpb.RBAC)
	if err := ptypes.UnmarshalAny(any, msg); err != nil {
		return nil, fmt.Errorf("rbac: error parsing config %v: %v", cfg, err)
	}


	// IS THIS WHERE I PUT VALIDATION ERRORS? CHECK VALIDATION AS PER A41
	// I'm gonna put them here
	/*msg.Rules.Policies[0].Condition // These must cause validation failure if present, cause listener to be nacked
	msg.Rules.Policies[0].CheckedCondition // What level is actually used to instantiate the RBAC Configuration?

	// If a permission or principal has a header matcher for a grpc- prefixed
	// header name or :scheme, it is a validation failure. - A41
	// msg.Rules.Policies[0].Principals[0]. // Header matcherrrrrr
	// There is many policies...how do we convert these and check them
	msg.Rules.Policies[0].Principals[0] // Iterate through both of these for validation
	msg.Rules.Policies[0].Permissions[0]*/

	// How this gets interfaced with? Validation will get piped back to NACK Listener eventually

	// Iterate through policies
	for _, policy := range msg.Rules.Policies {
		// check for condition and checked condition in each one

		// "Policy.condition and Policy.checked_condition must cause a
		// validation failure if present." - A41
		if policy.Condition != nil {
			// validation failure - return error?
			return nil, errors.New("rbac: Policy.condition is present")
		}
		if policy.CheckedCondition != nil {
			// validation failure - return error?
			return nil, errors.New("rbac: policy.CheckedCondition is present")
		}

		// "It is also a validation failure if Permission or Principal has a
		// header matcher for a grpc-prefixed header name or :scheme." - A41

		// loop through principals
		// check if header matcher and clause
		for _, principal := range policy.Principals {
			// Look at what I wrote in RBAC Engine, get an example from that
			if principal.GetHeader() != nil {
				// grpc-prefixed header name or :scheme
				name := principal.GetHeader().GetName()
				if name == ":scheme" || strings.HasPrefix(name, "grpc-") {
					return nil, fmt.Errorf("rbac: principal header matcher for %v is :scheme or starts with grpc", name)
				}
			}
		}

		// loop through permissions
		// check if header matcher and clause
		for _, permission := range policy.Permissions {
			if permission.GetHeader() != nil {
				// grpc-prefixed header name or :scheme
				name := permission.GetHeader().GetName()
				if name == ":scheme" || strings.HasPrefix(name, "grpc-") {
					return nil, fmt.Errorf("rbac: permission header matcher for %v is :scheme or starts with grpc", name)
				}
			}
		}
	}



	// Look at how Ashitha calls this layer
	// right now you take a list[] of stuff with action and long policies

	// Due to the RBAC policy validation logic being specific to RBAC itself,
	// I think this should do it's own iteration through the full proto, and then instantiate the chain engine
	// with the single length list used to configure this

	return config{config: msg}, nil
}

func (builder) ParseFilterConfig(cfg proto.Message) (httpfilter.FilterConfig, error) {
	return parseConfig(cfg)
}

func (builder) ParseFilterConfigOverride(override proto.Message) (httpfilter.FilterConfig, error) {
	return parseConfig(override) // Look into it somehow for RBAC?
}

var _ httpfilter.ServerInterceptorBuilder = builder{}

// server side only
// Before it even gets here, validated using ^^^?
func (builder) BuildServerInterceptor(cfg httpfilter.FilterConfig, override httpfilter.FilterConfig) (resolver.ServerInterceptor, error) {
	if cfg == nil {
		return nil, fmt.Errorf("rbac: nil config provided")
	}

	c, ok := cfg.(config)
	if !ok {
		return nil, fmt.Errorf("rbac: incorrect config type provided (%T): %v", cfg, cfg)
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
	ce, err := rbac.NewChainEngine([]*v3rbacpb.RBAC{icfg.Rules})
	if err != nil {
		return nil, fmt.Errorf("error constructing matching engine: %v", err)
	}
	return &interceptor{chainEngine: ce}, nil
}

type interceptor struct {
	// config *rpb.RBAC
	chainEngine *rbac.ChainEngine
}

func (i *interceptor) AllowRPC(ctx context.Context) error {
	// ":method can be hard-coded to POST if unavailable and a code audit confirms the server denies requests for all other method types"

	// Validated and now instantiated with this config
	// Now what behavior comes from the config into this method
	// How does Ashitha call this?
	// A singular action + policy list to match to.
	// Rather than []{action + policy}{action + policy}
	// This new chain engine thing should happen once, as this AllowRPC will be called many times in the xds interceptors

	//rbac.NewChainEngine([]*v3rbacpb.RBAC{i.config.Rules}) // Do you do this once, or do it per config?
	return i.chainEngine.IsAuthorized(ctx)
}