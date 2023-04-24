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
// as defined in [A52 - xDS Custom LB Policies].
//
// [A52 - xDS Custom LB Policies]: https://github.com/grpc/proposal/blob/master/A52-xds-custom-lb-policies.md
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
	wrrL := &wrrLocalityBalancer{
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
	print("LocalityWeight1: ", oa.LocalityWeight, ". LocalityWeight2: ", a.LocalityWeight, ".")
	return ok && oa.LocalityWeight == a.LocalityWeight // check if this is being called in failing tests
} // idk I don't like this code block...


// AddrInfo is the locality weight of the locality an address is a part of.
type AddrInfo struct {
	LocalityWeight uint32
}

// SetAddrInfo returns a copy of addr in which the BalancerAttributes field is
// updated with AddrInfo.
func SetAddrInfo(addr resolver.Address, addrInfo AddrInfo) resolver.Address {
	addr.BalancerAttributes = addr.BalancerAttributes.WithValue(attributeKey{}, addrInfo)
	return addr
}

func (a AddrInfo) String() string {
	return fmt.Sprintf("Locality Weight: %d", a.LocalityWeight)
}

// getAddrInfo...
func getAddrInfo(addr resolver.Address) AddrInfo {
	v := addr.BalancerAttributes.Value(attributeKey{})
	// in the failure case, return 1 even though failure case shouldn't happen
	// or does this already implicitly happen? This is what wrr does so maybe it implicitly happens...
	ai, _ := v.(AddrInfo) // can this fail? handle it at this layer anyway...
	return ai
}

// wrrLocalityBalancer wraps a child weighted target balancer, and builds configuration
// for the child once it receives configuration and locality weight information.
type wrrLocalityBalancer struct {
	// child will be a weighted target balancer, and this balancer will build
	// configuration for this child. Thus, build it at wrrLocalityBalancer build time,
	// and configure it once wrrLocalityBalancer received configurations. Other balancer
	// operations you pass through.
	child balancer.Balancer

	logger *grpclog.PrefixLogger
}

func (b *wrrLocalityBalancer) UpdateClientConnState(s balancer.ClientConnState) error {
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
	for _, addr := range s.ResolverState.Addresses {
		locality := internal.GetLocalityID(addr) // what happens if this fails? also happens in that layer
		localityString, err := locality.ToString()
		if err != nil {
			// Should never happen.
			logger.Infof("failed to marshal LocalityID: %v, skipping this locality in weighted target")
		}
		ai := getAddrInfo(addr)

		// if this isn't set this ai becomes 0 due to this being value type...
		weightedTargets[localityString] = weightedtarget.Target{Weight: ai.LocalityWeight, ChildPolicy: lbCfg.ChildPolicy}
	}
	wtCfg := &weightedtarget.LBConfig{Targets: weightedTargets}
	return b.child.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: s.ResolverState,
		BalancerConfig: wtCfg,
	})
}

func (b *wrrLocalityBalancer) ResolverError(err error) {
	b.child.ResolverError(err)
}

func (b *wrrLocalityBalancer) UpdateSubConnState(sc balancer.SubConn, scState balancer.SubConnState) {
	b.child.UpdateSubConnState(sc, scState)
}

func (b *wrrLocalityBalancer) Close() {
	b.child.Close()
}
