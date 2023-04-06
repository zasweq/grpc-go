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
	"google.golang.org/grpc/balancer/weightedtarget"
	"google.golang.org/grpc/internal/grpclog"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/xds/internal"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/serviceconfig"
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

func (bb) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	// I think you build child component here, and update config in UpdateClientConn state once you get it
	builder := balancer.Get(weightedtarget.Name)
	if builder == nil {
		// shouldn't happen, defensive programming
		return nil
	}
	// Doug says just put it here which is fine logically...

	// you just give the cc to child and it thus bypasses all the operations on the Client Conn...
	wtb := builder.Build(cc, bOpts) // here is what determines whether client conn is pass through or not
	if wtb == nil { // can this even happen?
		// shouldn't happen, defensive programming
		return nil
	}
	// or have it a graceful switch but can't switch over time anyway
	wrrL := &wrrLocality{
		child: wtb,
	}
	wrrL.logger = prefixLogger(b)
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

// wrr_locality combines the priority child config with locality weights to
// generate weighted_target configuration.

// it's sole purpose is to construct configuration of weighted target as the cluster resolver used to do
// so doesn't need to intercept any operations?




type attributeKey struct{}



// Equal allows the values to be compared by Attributes.Equal.
func (a AddrInfo) Equal(o interface{}) bool {
	oa, ok := o.(AddrInfo)
	return ok && oa.LocalityWeight == a.LocalityWeight
} // idk I don't like this code block...


type AddrInfo struct {
	LocalityWeight uint32
}

// Do I need Equal? Do Balancer attributes get checked for equality?

func SetAddrInfo(addr resolver.Address, addrInfo AddrInfo) resolver.Address {
	addr.BalancerAttributes = addr.BalancerAttributes.WithValue(attributeKey{}, addrInfo)
	return addr
}

func GetAddrInfo(addr resolver.Address) AddrInfo { // Do I even need this exported if it's called internally?
	v := addr.BalancerAttributes.Value(attributeKey{})
	ai, _ := v.(AddrInfo)
	return ai
}


// triggered by new addresses:, so part of the UpdateClientConnState operation I'm assuming
// weightedTargets[localityStr] = weightedtarget.Target{Weight: locality.Weight, ChildPolicy: childPolicy}

// the whole state or just the addresses?
func buildWeightedTargetConfig(state *balancer.ClientConnState) *weightedtarget.LBConfig {
	weightedTargets := make(map[string]weightedtarget.Target)
	// state.ResolverState.Addresses // [] addresses
	// now iterates over addresses not localities
	for _, addr := range state.ResolverState.Addresses { // how to get collisions? same localityStr, should it not set if wants to use first one encountered or is this an implementation detail that doesn't matter
		// logically equivalent:
		// weightedTargets[localityStr] = weightedtarget.Target{Weight: locality.Weight, ChildPolicy: childPolicy}
		// localityStr := getLocalityID(addr)
		locality := internal.GetLocalityID(addr)
		localityString, err := locality.ToString()
		if err != nil {
			// logger.Infof("failed to marshal LocalityID: %#v, loads won't be reported", scw.localityID())
		}
		// localityWeight = getLocalityWeight(addr) I think the setting is where it takes the "first one" logically
		// what happens in the failure case?
		ai := GetAddrInfo(addr) // make unexported since just used here?
		// Are all these reads safe?
		weightedTargets[localityString] = weightedtarget.Target{Weight: ai.LocalityWeight, ChildPolicy: lbCfg.ChildPolicy/*config.ChildPolicy verbatim*/}
	}
	return &weightedtarget.LBConfig{Targets: weightedTargets}
}


// what state does this need and what operations do you need to intercept here?

type wrrLocality struct { // experimental is only type of name
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

	// move helper here cleaner and can easily return error

	// lbCfg.ChildPolicy // here's the data the weighted target stuff needs

	// wtCfg := buildWeightedTargetConfig(s)
	// stick this as part of balancer.ClientConnState ^^^, then send to child
	/*ccs := balancer.ClientConnState{
		// wtCfg somewhere in here
		// is the rest of the field the same or different? I think the same
		ResolverState: s.ResolverState, // This is what Outlier Detection does, so I think we're good here
		BalancerConfig: wtCfg,
	}*/

	// Noop synchronous, adds it's own synchronous instructions but doesn't change
	// how the rest of the guarantees are synchronous
	// built in cluster impl previous?

	weightedTargets := make(map[string]weightedtarget.Target)
	// state.ResolverState.Addresses // [] addresses
	// now iterates over addresses not localities
	for _, addr := range s.ResolverState.Addresses { // how to get collisions? same localityStr, should it not set if wants to use first one encountered or is this an implementation detail that doesn't matter
		// logically equivalent:
		// weightedTargets[localityStr] = weightedtarget.Target{Weight: locality.Weight, ChildPolicy: childPolicy}
		// localityStr := getLocalityID(addr)
		locality := internal.GetLocalityID(addr)
		localityString, err := locality.ToString() // where do I populate this?
		if err != nil {
			// logger.Infof("failed to marshal LocalityID: %#v, loads won't be reported", scw.localityID())
			logger.Infof("failed to marshal LocalityID, skipping")
		}
		// localityWeight = getLocalityWeight(addr) I think the setting is where it takes the "first one" logically
		// what happens in the failure case?
		ai := GetAddrInfo(addr) // make unexported since just used here?
		// Are all these reads safe?
		weightedTargets[localityString] = weightedtarget.Target{Weight: ai.LocalityWeight, ChildPolicy: lbCfg.ChildPolicy/*config.ChildPolicy verbatim*/}
	}
	wtCfg := &weightedtarget.LBConfig{Targets: weightedTargets}

	// If you populate child here:
	// if b.child == nil { child is determined by config, but in this case it's fixed
	//       balancer.Get flow I currently have in build
	// }



	b.child.UpdateClientConnState(balancer.ClientConnState{
		// wtCfg somewhere in here
		// is the rest of the field the same or different? I think the same
		ResolverState: s.ResolverState, // This is what Outlier Detection does, so I think we're good here
		BalancerConfig: wtCfg,
	})
}

// Full balancer - so needs to implement full API
func (b *wrrLocality) ResolverError(err error) {
	// forward to child? Guaranteed to be sync since calls into API are sync
	b.child.ResolverError(err)
}

func (b *wrrLocality) UpdateSubConnState(sc balancer.SubConn, scState balancer.SubConnState) {
	b.child.UpdateSubConnState(sc, scState)
}

func (b *wrrLocality) Close() { // can't call other operations after close, but nothing gets called after this close so unless other things can close child don't need to worry
	b.child.Close()
}

// two concepts: (at what layer do each of these happen and in what operations build and update client conn state)

// wrrlocality:
// building the balancer
// updating the balancers configuration

// child:
// building the balancer - statically build since 1:1 when you build wrrlocality?
// updating the balancers configuration - needs locality weights


// balancer.ClientConn calls are pass through right this doesn't need to intercept anything that way
// just build downward
