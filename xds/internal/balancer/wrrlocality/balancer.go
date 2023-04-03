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

// switch this to Easwar's comment about URL formatting

// Package wrrlocality provides an implementation of the wrr locality LB policy,
// as defined in
// https://github.com/grpc/proposal/blob/master/A52-xds-custom-lb-policies.md.
package wrrlocality

import (
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/weightedtarget"
	"google.golang.org/grpc/internal/grpclog"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
	"google.golang.org/grpc/xds/internal"
)

// Name is the name of wrr_locality balancer.
const Name = "xds_wrr_locality_experimental"

func init() {
	balancer.Register(bb{})
}

type bb struct{}

func (bb) Name() string {
	return Name
}

// To plumb in a different child in tests.
var weightedTargetName = weightedtarget.Name

func (bb) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	builder := balancer.Get(weightedTargetName)
	if builder == nil {
		// Shouldn't happen, registered through imported weighted target,
		// defensive programming.
		return nil
	}

	// Doesn't need to intercept any balancer.ClientConn operations; pass
	// through by just giving cc to child balancer.
	wtb := builder.Build(cc, bOpts)
	if wtb == nil {
		// shouldn't happen, defensive programming.
		return nil
	}
	wrrL := &wrrLocality{
		child: wtb,
	}

	wrrL.logger = prefixLogger(wrrL)
	wrrL.logger.Infof("Created")
	return wrrL
}

func (bb) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	var lbCfg *LBConfig
	if err := json.Unmarshal(s, &lbCfg); err != nil {
		return nil, fmt.Errorf("xds: unable to unmarshal LBConfig for wrrlocality: %s, error: %v", string(s), err)
	}
	if lbCfg == nil || lbCfg.ChildPolicy == nil {
		return nil, errors.New("xds: unable to unmarshal LBConfig for wrrlocality: child policy field must be set")
	}
	return lbCfg, nil
}

type attributeKey struct{}

// Do I need Equal? Do Balancer attributes get checked for equality?
// Equal allows the values to be compared by Attributes.Equal.
func (a AddrInfo) Equal(o interface{}) bool {
	oa, ok := o.(AddrInfo)
	return ok && oa.LocalityWeight == a.LocalityWeight
} // idk I don't like this code block...


// AddrInfo...
type AddrInfo struct {
	LocalityWeight uint32
}

// SetAddrInfo...
func SetAddrInfo(addr resolver.Address, addrInfo AddrInfo) resolver.Address {
	addr.BalancerAttributes = addr.BalancerAttributes.WithValue(attributeKey{}, addrInfo)
	return addr
}

// getAddrInfo...
func getAddrInfo(addr resolver.Address) AddrInfo { // Do I even need this exported if it's called internally?
	v := addr.BalancerAttributes.Value(attributeKey{})
	ai, _ := v.(AddrInfo)
	return ai
}

// what state does this need and what operations do you need to intercept here?
// It's only function is to build the config. I think this is all you need.
type wrrLocality struct {
	// 1:1 with weighted target - prepares it and is coupled, this never changes after construction right just updates?
	child balancer.Balancer

	logger *grpclog.PrefixLogger
}

func (b *wrrLocality) UpdateClientConnState(s balancer.ClientConnState) error {
	lbCfg, ok := s.BalancerConfig.(*LBConfig)
	if !ok {
		b.logger.Errorf("received config with unexpected type %T: %v", s.BalancerConfig, s.BalancerConfig)
		return balancer.ErrBadResolverState
	}

	// Noop synchronous, adds it's own synchronous instructions (happens before, no run() etc.) but doesn't change
	// how the rest of the guarantees are synchronous
	// built in cluster impl previous?
	// wrr_locality combines the priority child config with locality weights to
	// generate weighted_target configuration.
	weightedTargets := make(map[string]weightedtarget.Target)
	for _, addr := range s.ResolverState.Addresses { // how to get collisions? same localityStr, should it not set if wants to use first one encountered or is this an implementation detail that doesn't matter
		// is this collision possible in the getter?
		// logically equivalent:
		// weightedTargets[localityStr] = weightedtarget.Target{Weight: locality.Weight, ChildPolicy: childPolicy}
		locality := internal.GetLocalityID(addr)
		localityString, err := locality.ToString() // where do I populate this?
		if err != nil {
			// Shouldn't happen
			logger.Infof("failed to marshal LocalityID: %v, skipping this locality in weighted target")
		}
		// localityWeight = getLocalityWeight(addr) I think the setting is where it takes the "first one" logically
		// what happens in the failure case?
		ai := getAddrInfo(addr) // make unexported since just used here?
		// Are all these reads safe? i.e. any nil dereferences like Doug was worried about?
		weightedTargets[localityString] = weightedtarget.Target{Weight: ai.LocalityWeight, ChildPolicy: lbCfg.ChildPolicy/*config.ChildPolicy verbatim*/}
	}
	wtCfg := &weightedtarget.LBConfig{Targets: weightedTargets}
	/*
	For testing this is what it looks like:
	{
	  "targets": {
	    "locality_1": {
	      "weight":1,
	      "childPolicy": [{"round_robin": ""}]
	    }
	  }
	}
	*/

	// so for e2e (i.e. it as top level balancer of channel) I think you
	// just expect more than 2/3rds of RPCs to go to (or the weights) to go to that backend - part of e2e xDS testing should work as usual
	return b.child.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: s.ResolverState, // This is what Outlier Detection does, so I think we're good here
		BalancerConfig: wtCfg,
	})
}

func (b *wrrLocality) ResolverError(err error) {
	// forward to child? Guaranteed to be sync since calls into API are sync
	b.child.ResolverError(err)
}

func (b *wrrLocality) UpdateSubConnState(sc balancer.SubConn, scState balancer.SubConnState) {
	b.child.UpdateSubConnState(sc, scState)
}

func (b *wrrLocality) Close() {
	b.child.Close()
}
