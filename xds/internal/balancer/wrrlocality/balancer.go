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
	"errors"
	"fmt"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/serviceconfig"
)

const Name = "xds_wrr_locality_experimental" // use this instead at callsites

func init() {
	/*if envconfig.XDSCustomLBPolicy {
		balancer.Register(bb{})
	}*/ // this won't pick up the write in wrrlocality/balancer_test.go, so need a hook through internal to register only for testing
	balancer.Register(bb{}) // can't I just register unconditionally?
}

type bb struct{}

func (bb) Name() string {
	return Name
}

// What does build do again?
func (bb) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
// comment here about 1:1 assumption as to why we can hardcode this child to xds_wrr_locality_experimental
	/*
	builder := balancer.Get(weightedtarget.Name)
	if builder == nil {
// shouldn't happen, defensive programming
	return nil
	}

wtb := builder.Build(cc, bOpts) // here is what determines whether client conn is pass through or not
if wtb == nil { // can this even happen?
// shouldn't happen, defensive programming
return nil
}*/
	// ^^^ this whole codeblock move to UpdateClientConnState() - getting from registry and building child - validated mulitple times at this point in the client and in the construction of this balancer


	/*wrr := &wrrLocalityExperimental{
		// data structures here you need
	}

	// fork any goroutines corresponding to component here*/

	// so construct the balancer, then Update the child UpdateClientConnState
	/*return &wrrLocalityExperimental{
		child: wtb/*hardcoded to weighted target - this will handle graceful switch for you in balancer group,
	}*/ // in order to return this neeeds to implement all the operations

	// top level lb of the channel

	return nil

	// the scope of this is to build data structures and fork goroutines corresponding to the component
	// the child balancer will actually get built in UpdateClientConnState
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
/*var lbCfg *LBConfig
if err := json.Unmarshal(s, &lbCfg); err != nil { // Validates child config if present as well.
return nil, fmt.Errorf("xds: unable to unmarshal LBconfig: %s, error: %v", string(s), err)
}
// do we want to require child, also feels weird to have this defined her eand also up there with wrrLocalityExperimental perhaps require it here, since prepared by client anyway?
return lbCfg, nil*/




// Valid JSON (unmarshal into LB Config) - does this do validations on child for you?

// Child is *present*

// Child is present in registry and the config parsing is correct (i.e. child config is correct)

	/*get s json.RawMessage 010101010101 byte string as an argument*/
	// are the three things one atomic thingy or happen across three seperate instructions?

	// valid json
	// does the structure match
	// will validate the structure but also couple it with *all* the validations
	var lbCfg *LBConfig
	/*
	If there's a syntax error, return syntax error
	*/
	if err := json.Unmarshal(s, &lbCfg); err != nil { // so this is all you need, but see comments for error buckets representing the behavior mapping to certain errors, are we sure we want to couple the string to this helper, eh idk wouldn't mind
		// Have assertions on this be on the string the error wraps

		// are second and third validations handled in this Unmarshal() call?

		// Does the json structure valid? (handled by invalid json string)

		// Child is not set/present - no available
		// no valid type - balancer.Get returns false, merges with above, "invalid loadBalancingConfig, no supported policies found in: %v

		// Error parsing config returns error (i.e. validation error from the balancer registry)

		return nil, fmt.Errorf("xds: unable to unmarshal LBConfig for wrrlocality: %s, error: %v", string(s), err)

		// {childPolicy: childPolicyJSON here}
		// so just {} around it
	}
	// nil check on lbCfg itself? this will panic otherwise
	if lbCfg.ChildPolicy == nil {
		return nil, errors.New("xds: unable to unmarshal LBConfig for wrrlocality: child policy field must be set")
	}
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
