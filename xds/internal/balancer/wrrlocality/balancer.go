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

// Package wrrlocality provides an implementation of the wrr locality LB policy,
// as defined in
// https://github.com/grpc/proposal/blob/master/A52-xds-custom-lb-policies.md.
package wrrlocality

import (
	"encoding/json"
	"fmt"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/weightedtarget"
	"google.golang.org/grpc/serviceconfig"
)

const Name = "xds_wrr_locality_experimental"

type bb struct{}

// What does build do again?
func (bb) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
// comment here about 1:1 assumption as to why we can hardcode this child to xds_wrr_locality_experimental

	builder := balancer.Get(weightedtarget.Name)
	if builder == nil {
// shouldn't happen, defensive programming
	return nil
	}

wtb := builder.Build(cc, bOpts) // here is what determines whether client conn is pass through or not
if wtb == nil { // can this even happen?
// shouldn't happen, defensive programming
return nil
}
	// so construct the balancer, then Update the child UpdateClientConnState
	return &wrrLocalityExperimental{
		child: wtb/*hardcoded to weighted target - this will handle graceful switch for you in balancer group*/,
	}
}

// balancer.Get as in test is called in cluster resolver orrrrrr
// cluster_impl as build child? I remember validating twice but I think that might be unmarshaling the json
// into a struct in cluster resolver, maybe another balancer.Get when actually build child?


// UpdateClientConnState...does it let the weighted target do graceful switching?
// attributes + endpoint picking = locality config with child weights (see codebase for how this is prepared historically)

func (bb) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
// right now with our JSON parsing (maybe add as a TODO to get rid of double validations)
// the verification of child will happen in this function call

// makes sures structure with child etc.

// childPolicy: lb_policy_type{field1: nice, field2: nice}

	// I think it's fair for emitted JSON to successfully pass this validation
// Unmarshal's childPolicy, sees the type, validates it and converts to JSON
var lbCfg *LBConfig
if err := json.Unmarshal(s, &lbCfg); err != nil { // Validates child config if present as well.
return nil, fmt.Errorf("xds: unable to unmarshal LBconfig: %s, error: %v", string(s), err)
}
// do we want to require child, also feels weird to have this defined her eand also up there with wrrLocalityExperimental perhaps require it here, since prepared by client anyway?
return lbCfg, nil
}

/*
Contains a LoadBalancingPolicy field that has a list of endpoint picking
policies. This field will be processed by recursively calling the xDS LB policy
registry, and the result will be used in the child_policy field in the
configuration for the xds_wrr_locality_experimental policy (see the “xDS WRR
Locality Load Balancer” sections for details on the config format).

we prepare this wrr_locality: round_robin
config string in client for round_robin case

so yeah shiuld be a valid config
*/

type wrrLocalityExperimental struct {

	// what state does this need? this literally just takes child endpoint
	// picking config and wraps with locality weights

	// at what level is this gracefully switching. This child's config, or the
	// wrr locality...is the wrr locality level have a child that is already gracefully switched

	// maybe if endpoint type changes you prepare a whole new weighted target config
	// and then gracefully switch that child itself.


	// one to one with child - only function is to prepare child configuration

	// which operations to intercept?
	// balancer.Balancer?
	// balancer.ClientConn?


	child balancer.Balancer



}
