/*
 *
 * Copyright 2022 gRPC authors.
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

package leastrequest

import (
	"encoding/json"
	"fmt"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/grpcrand"
	"google.golang.org/grpc/serviceconfig"
	"sync/atomic"
)

// Globals to stub out in tests.
var (
	grpcranduint32 = grpcrand.Uint32
)

// Name is the name of the least request balancer.
const Name = "least_request_experimental"

var logger = grpclog.Component("least request")

func init() {
	balancer.Register(bb{})
}

// LBConfig is the balancer config for least_request_experimental balancer.
type LBConfig struct {
	serviceconfig.LoadBalancingConfig `json:"-"`

	// ChoiceCount is the number of random SubConns to sample to try and find
	// the one with the Least Request. If unset, defaults to 2. If set to < 2,
	// will become 2, and if set to > 10, will become 10.
	ChoiceCount uint32 `json:"choiceCount,omitempty"`
}

type bb struct{}

func (bb) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	lbConfig := &LBConfig{
		ChoiceCount: 2,
	}
	if err := json.Unmarshal(s, lbConfig); err != nil {
		return nil, fmt.Errorf("least-request: unable to unmarshal LBConfig: %v", err)
	}
	// "If `choice_count < 2`, the config will be rejected." - A48
	if lbConfig.ChoiceCount < 2 { // sweet
		return nil, fmt.Errorf("least-request: lbConfig.choiceCount: %v, must be >= 2", lbConfig.ChoiceCount)
	}
	// "If a LeastRequestLoadBalancingConfig with a choice_count > 10 is
	// received, the least_request_experimental policy will set choice_count =
	// 10." - A48
	if lbConfig.ChoiceCount > 10 {
		lbConfig.ChoiceCount = 10
	}
	return lbConfig, nil
}

func (bb) Name() string {
	return Name
}

func (bb) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	b := &leastRequestBalancer{scRPCCounts: make(map[balancer.SubConn]*int32)}
	baseBuilder := base.NewBalancerBuilder(Name, b, base.Config{HealthCheck: true})
	baseBalancer := baseBuilder.Build(cc, bOpts)
	b.Balancer = baseBalancer
	return b
}

type leastRequestBalancer struct {
	// Embeds balancer.Balancer because needs to intercept UpdateClientConnState
	// to learn about choiceCount.
	balancer.Balancer

	choiceCount uint32
	// no access to map atomically...
	scRPCCounts map[balancer.SubConn]*int32 // does this need a mutex, but only build reads it which is sync
}

func (lrb *leastRequestBalancer) UpdateClientConnState(s balancer.ClientConnState) error {
	lrCfg, ok := s.BalancerConfig.(*LBConfig)
	if !ok {
		logger.Errorf("least-request: received config with unexpected type %T: %v", s.BalancerConfig, s.BalancerConfig)
		return balancer.ErrBadResolverState
	}

	lrb.choiceCount = lrCfg.ChoiceCount
	return lrb.Balancer.UpdateClientConnState(s)
}

type scWithRPCCount struct {
	sc      balancer.SubConn
	numRPCs *int32 // the ref copied over from map that lrb holds...ref gets atomically loaded
}

func (lrb *leastRequestBalancer) Build(info base.PickerBuildInfo) balancer.Picker {
	logger.Infof("least-request: Build called with info: %v", info)
	if len(info.ReadySCs) == 0 {
		return base.NewErrPicker(balancer.ErrNoSubConnAvailable)
	}
	// agg clusters were [] so had some manipulation there...
	// info.ReadySCs // map[balancer]
	// lrb.scRPCCounts // map[balancer.SubConn]*int32

	// interface so whether points to same thing or not
	// delete from map if the map isn't in there
	for sc := range lrb.scRPCCounts {
		if _, ok := info.ReadySCs[sc]; !ok { // If no longer ready, no more need for the ref.
			delete(lrb.scRPCCounts, sc)
		}
	}

	// create in map if not there - new(0) or else will get what's originally in that memory
	for sc := range info.ReadySCs {
		if _, ok := lrb.scRPCCounts[sc]; !ok {
			lrb.scRPCCounts[sc] = new(int32)
		}
	}

	// delete from map
	// can you delete as you iterate through?


	// ref to the same memory will eventually be handled with done


	// by the time it hits here map should be trimmed/pruned/updated with new 0s
	scs := make([]scWithRPCCount, 0, len(info.ReadySCs))
	for sc := range info.ReadySCs {
		scs = append(scs, scWithRPCCount{
			sc: sc,
			numRPCs: lrb.scRPCCounts[sc], // guaranteed to be present due to algorithm
		})
	}

	return &picker{
		choiceCount: lrb.choiceCount,
		subConns:    scs,
	}
}

type picker struct {
	// choiceCount is the number of random SubConns to find the one with
	// the least request.
	choiceCount uint32
	// Built out when receives list of ready RPCs.
	subConns []scWithRPCCount
}

func (p *picker) Pick(balancer.PickInfo) (balancer.PickResult, error) {
	var pickedSC *scWithRPCCount
	for i := 0; i < int(p.choiceCount); i++ {
		print("iterating through choiceCount, i: ", i, ". ")
		// this p.subconns is built from a ready map so has non deterministic ordering..
		index := grpcranduint32() % uint32(len(p.subConns)) // inject randomness here for deterministic picks/tests/expectations of backends routed to
		print("index in picker: ", index, ". ")
		sc := p.subConns[index]
		if pickedSC == nil {
			pickedSC = &sc
			continue
		}
		print("sc.numRPCs: ", *sc.numRPCs, ". ")
		print("pickedSC.numRPCs", *pickedSC.numRPCs, ". ")
		// are these derefs guaranteed to not panic? I think so because all derived from data structure build out at picker build time
		if *sc.numRPCs < *pickedSC.numRPCs {
			pickedSC = &sc
		}
	}
	// "The counter for a subchannel should be atomically incremented by one
	// after it has been successfully picked by the picker." - A48
	atomic.AddInt32(pickedSC.numRPCs, 1)

	// "the picker should add a callback for atomically decrementing the
	// subchannel counter once the RPC finishes (regardless of Status code)." -
	// A48.
	done := func(balancer.DoneInfo) {
		atomic.AddInt32(pickedSC.numRPCs, -1)
	} // if do the case where you make unary RPC and block this counter will decrement. needs to be long lived streams.

	return balancer.PickResult{
		SubConn: pickedSC.sc,
		Done:    done,
	}, nil
}


// testing...
// top level balancer of channel - will inherently have to be a random selection
// and rpc distribution

// any unit style tests?


// xDS plumbing
// plumbed into both fields
// new thing in registry  -> both converted into the same JSON that hits this file and becomes a real lb config
// old thing              ->

// don't mind cluster resolver speced stuff...

// xDS - sanity check



// inject randomness, pick first
// expect random thing to be called return 1, 2

// are all 3 subconns guaranteed to be ready for the first picker send upward to cc?


// also after this rr? test (if non deterministic ordering how do you even induce...one to another?
// need to test that counts persist after a new address update (which will trigger ready subconns)
