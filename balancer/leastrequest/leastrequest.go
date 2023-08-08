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
	"google.golang.org/grpc/serviceconfig"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal/grpcrand"
)

// Name is the name of the least request balancer.
const Name = "least_request_experimental"

var logger = grpclog.Component("least request")

type bb struct{}

func (bb) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	lbConfig := &leastRequestConfig{
		choiceCount: 2,
	}
	if err := json.Unmarshal(s, lbConfig); err != nil {
		return nil, fmt.Errorf("least request: unable to unmarshal LBConfig: %v", err)
	}
	// "If a LeastRequestLoadBalancingConfig with a choice_count > 10 is
	// received, the least_request_experimental policy will set choice_count =
	// 10."
	if lbConfig.choiceCount > 10 {
		lbConfig.choiceCount = 10
	}
	// I asked about this in chat but what happens if choiceCount < 2?
	return lbConfig, nil
}

func (bb) Name() string {
	return Name
}

func (bb) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	b := &leastRequestBalancer{}
	baseBuilder := base.NewBalancerBuilder(Name, b, base.Config{HealthCheck: true}) // hooks into this way
	baseBalancer := baseBuilder.Build(cc, bOpts) // b links here to child
	b.Balancer = baseBalancer // intercepts child calls here...base layer
	return b
}

type leastRequestBalancer struct { // exported?
	// hook in base...
	balancer.Balancer // 
	// persist number?

	// mu guards choiceCount.
	mu sync.Mutex
	choiceCount uint32
}

type subConnWrapper struct { // how does this fit in with new API?
	balancer.SubConn
	// counter for weights...is there a way to do this in leastRequestBalancer that's easier?
}

/*
message LeastRequestLoadBalancingConfig {
  uint32 choice_count = 1;  // Optional, defaults to 2.
}
*/
type leastRequestConfig struct {
	serviceconfig.LoadBalancingConfig `json:"-"`
	// Optional - defaults to 2.
	choiceCount uint32 `json:"maxEjectionPercent,omitempty"`
}

// same exact thing as base balancer except this has a config with a choice count

/*
In keeping with this, the least_request_experimental policy will de-duplicate
the addresses in the address list it receives so that it does not create more
than one connection to a given address. However, because the policy will not
initially support the weighted least request algorithm, we will ignore the
additional weight given to the address. If at some point in the future we do add
support for the weighted least request algorithm, then an address that is
present multiple times in the list will be given appropriate weight in that
algorithm.
I think this is a part of the RR implementation?
*/

// no talk about choiceCount racing. If read at picker build time and immutable doesn't race so I'm good right?
func (lrb *leastRequestBalancer) UpdateClientConnState(s balancer.ClientConnState) error {
	// this sits at bottom
	s.BalancerConfig // specifies a single number - write it sync? for picker to read atomic load/store?

	s.ResolverState.Addresses // like RR maintain an active connection to each of these...

	// The least_request_experimental policy will associate each subchannel to
	// an outstanding request counter (active_requests).

	// hold it here
	lrCfg, ok := s.BalancerConfig.(*leastRequestConfig)
	// give up
	if !ok {
		// global logger?
		logger.Errorf("received config with unexpected type %T: %v", s.BalancerConfig, s.BalancerConfig)
		return balancer.ErrBadResolverState
	}

	// typecast to leastRequestConfig (otherwise error)
	// lrb.mu.Lock() // won't deadlock with other operations because just writing to a field.
	lrb.choiceCount = lrCfg.choiceCount // uint32, need to store somewhere for the picker or persist whole config I think this is fine for now. easy enough to change if they want
	// lrb.mu.Unlock()
	// ResolverError
	// At end of UpdateCCS
	// Also UpdateSCS (which can be async)...so needs to persist and read
	// You can get a concurrent UpdateState() or an inline...
	// regeneratePicker()
	// 			forward back an errPicker on TF (err picker)
	//          triggers Build() with ready SubConns (will cause our picker to be returned). (causes immutable sc data structures to be built)

	// could do something in UpdateState if typecast to this picker read the field (with mu held) for number of SubConns. (I think better to do it with mu than event loop)
	// a piece of data from this event - choice_count
	// a piece of data from child (with plumbing) - ready scs, which we use and overwrite into immutable counting data structures that can be accessed sync for multiple RPCs

	// current architecture have no way of plumbing new balancer as builder, plus I think I need own parse config






	// lrb.choiceCount = lrc.choiceCount?
	// and then either picker holds ref bal.choiceCount
	// or Build is a function that captures this leastRequestBalancer and instantiates picker with it



	// finish forwarding to Base balancer it wraps (make sure all the operations and plumbing work as expected etc.)
}

// in build though will need to call base balancer builder...

// the only difference is this field and the picker logic

// this isn't through Build(). base calls Build and then UpdateState
// intercept UpdateState() call to persist this extra field (only function of this wrapper layer)

// intercept just UpdateCCS and UpdateState? - the stuff for the wrapper.
// The issue is reading the new and plumbing around field etc. from current
// architecture

func (lrb *leastRequestBalancer) UpdateState(s balancer.State) {
	// process in an event loop like run() for sync?
	// typecast to picker and add it?

	// custom lb takes child picker and straight add a wrapper
}


func (lrb *leastRequestBalancer) ResolverError(err error) {
	// what to do with this? Oh it's a leaf...
}

/*no UpdateSubConnState, but this creates a lis right? What behavior do state updates trigger?*/
func (lrb *leastRequestBalancer) updateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {} // is this even needed

func (lrb *leastRequestBalancer) Close() {
	// What resources need to be cleaned up here? Clean up SubConns or let Graceful Switch handle it?
}
// ^^^ do I need to plumb these operations somewhere? RR doesn't even have these operations




// doesn't need to intercept any balancer.ClientConn operations because it's the bottom one...

// but needs to do the aggregated state change stuff

// I think I do want rr picker builders and stuff

// just have it on the top level bal
func (lrb *leastRequestBalancer) Build(info base.PickerBuildInfo) balancer.Picker { // Do I need to change this API?
	logger.Infof("leastRequestPicker: Build called with info: %v", info)
	if len(info.ReadySCs) == 0 {
		return base.NewErrPicker(balancer.ErrNoSubConnAvailable)
	}
	scs := make([]balancer.SubConn, 0, len(info.ReadySCs)) // gets the ready scs here
	for sc := range info.ReadySCs {
		scs = append(scs, sc)
	}
	numRPCs := make(map[balancer.SubConn]*int32, len(scs))
	for _, sc := range scs {
		numRPCs[sc] = new(int32)
	}
	// mu.Lock - this can't race, because just with update ccs. Won't have any ready subconns or anything triggering this picker to be built so you're good this will always have a value
	// because can also come in on a UpdateSCS which is async
	// persist whole config or just choice count for future?

	// Can race with another UpdateCCS coming in concurrently with UpdateSCS...but if listener callbacks are also guarnateed to be sync I don't need this mutex
	// lrb.mu.Lock()
	// choiceCount := lrb.choiceCount
	// lrb.mu.Unlock()
	return &picker{
		// document immutable
		choiceCount: lrb.choiceCount,
		subConns: scs,
		numRPCs: numRPCs,
	}
}




type lrPickerBuilder struct {}

func (*lrPickerBuilder) Build(info base.PickerBuildInfo) balancer.Picker { // Do I need to change this API?
	logger.Infof("leastRequestPicker: Build called with info: %v", info)
	if len(info.ReadySCs) == 0 {
		return base.NewErrPicker(balancer.ErrNoSubConnAvailable)
	}
	scs := make([]balancer.SubConn, 0, len(info.ReadySCs)) // gets the ready scs here
	for sc := range info.ReadySCs {
		scs = append(scs, sc)
	}
	numRPCs := make(map[balancer.SubConn]*int32, len(scs))
	for _, sc := range scs {
		numRPCs[sc] = new(int32)
	}
	return &picker{
		// document immutable
		subConns: scs,
		numRPCs: numRPCs,
	}
}


type picker struct {
	// access to the same list of subconns as round robin picker right?
	// rr comment - do the same thing or what? create at the time it receives address list?

	// document immutable, scs outside of counters immutable...
	choiceCount uint32

	// read from bal with a mutex protection or something? persist from
	// UpdateClientConnState in UpdateCCS and read from that persisted?

	// UpdateCCS on base doesn't take into account balancer config, just the SubConns...





	// "These counters should additionally share the lifecycle with its
	// corresponding subchannel."
	// "This approach closely resembles the implementation for outstanding
	// request counters already present in the cluster_impl_experimental policy"

	// subConns is the snapshot of the roundrobin balancer when this picker was
	// created. The slice is immutable. Each Get() will do a round robin
	// selection from it and return the selected SubConn.
	subConns []balancer.SubConn // immutable at build time? either attach {subconn, num rpcs} or others
	numRPCs map[balancer.SubConn]*int32 // initialize with 0? as building out ready scs?
} // build an immutable picker at build time, the only thing that can race on it are two RPCs

// raciness amongst multiple RPCs on same picker causing weird behavior is allowed...
func (p *picker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	// needs to read choice count...
	// persist it in picker? Writing to it you'll need to hold onto picker?
	// p.bal.count? You need to protect this access...

	// The picker should be given a list of all subchannels with the READY
	// state. With this list, the picker should pick a subchannel based on the
	// following pseudo-code: so gets a []SubChannel

	// at this point...gets a []SubChannel in READY state from where? in Java it's an immutable slice constructed at creation time...
	// var subConns []balancer.SubConn // can I use this as a key?
	// var choiceCount int // where is this read from...p.bal.count or p.count? picker built from below. Read this in UpdateCCS into a balancer struct, and read it in Build?
	// Outlier Detection took into account SubConn attributes...same thing here or just addr subconn 1:1?

	// If this is persisted at build time it can't race with UpdateCCS right?
	// this data. But can race with other pick calls? (from the same balancer) (more than one rpc)
	// var weights map[balancer.SubConn]*int32 // access value atomically...can I use balancer.SubConn as key? Do I need to persist this in balancer?
	var pickedSC balancer.SubConn
	for i := 0; i < int(p.choiceCount); i++ { // config knob
		// get a random number from 0 -> subconn length, use it to index
		index := grpcrand.Uint32() % uint32(len(p.subConns))
		sc := p.subConns[index] // can I use this as a key? if not just append a 0 to each thing at build time wrapper {sc, int which is 0}
		if pickedSC == nil {
			pickedSC = sc
			continue
		}
		// I need to prevent nil panic here - if I build out both data structures I'm fine. p can't race eihter
		if *p.numRPCs[sc] < *p.numRPCs[pickedSC] { // how to do this operation without a panic?
			pickedSC = sc
		}
	}
	// how to get a pointer to map value go
	// weights[pickedSC]++ // a. atomically increment the heap. b. does the map access need protection? can be written to in other operations...
	atomic.AddInt32(p.numRPCs[pickedSC], 1)

	// call back to decrement weights[pickedSC] by 1
	done := func(di balancer.DoneInfo) {
		// regardless of error - di.err != nil or not

		// builds a new weight map on Update ccs
		atomic.AddInt32(p.numRPCs[pickedSC], -1)

		// Is this possible? For petiole
		// this gets ready subconns so is this plumbed into build?

		// if pr.Done != nil {}
	}

	return balancer.PickResult{
		SubConn: pickedSC,
		Done: done,
		// any metadata
	}, nil // any error case?


	// and manages the []subconns

	// The counter for a subchannel should be atomically incremented by one
	// after it has been successfully picked by the picker.

	// the picker should add a callback for atomically decrementing the
	// subchannel counter once the RPC finishes (regardless of Status code).

}

// have to be probability based for testing
