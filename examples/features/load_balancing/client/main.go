/*
 *
 * Copyright 2018 gRPC authors.
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

// Binary client is an example client.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/resolver/manual"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/credentials/insecure"
	ecpb "google.golang.org/grpc/examples/features/proto/echo"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

const (
	exampleScheme      = "example"
	exampleServiceName = "lb.example.grpc.io"
)

var addrs = []string{"localhost:50051", "localhost:50052"}

func callUnaryEcho(c ecpb.EchoClient, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := c.UnaryEcho(ctx, &ecpb.EchoRequest{Message: message})
	if err != nil {
		log.Fatalf("could not greet: %v", err)
	}
	fmt.Println(r.Message)
}

func makeRPCs(cc *grpc.ClientConn, n int) {
	hwc := ecpb.NewEchoClient(cc)
	for i := 0; i < n; i++ {
		callUnaryEcho(hwc, "this is examples/load_balancing")
	}
}

func main() {
	mr := manual.NewBuilderWithScheme("example")
	defer mr.Close()
	// helper to parse...just don't have service config?
	// json :=
	addrs := []resolver.Address{
		{Addr: "localhost:50051"},
		{Addr: "localhost:50052"},
	}
	mr.InitialState(resolver.State{
		Addresses: addrs,
	})

	// dependent on scheme
	target := fmt.Sprintf("%s:///%s", mr.Scheme(), exampleServiceName)

	// "pick_first" is the default, so there's no need to set the load balancing policy.
	pickfirstConn, err := grpc.Dial(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer pickfirstConn.Close()

	fmt.Println("--- calling helloworld.Greeter/SayHello with pick_first ---")
	makeRPCs(pickfirstConn, 10)

	fmt.Println()

	json := `{"loadBalancingConfig": [{"round_robin":{}}]}`
	// Make another ClientConn with round_robin policy.
	sc := internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(json)
	mr.UpdateState(resolver.State{ // blocks until it finishes processing?
		Addresses: addrs,
		ServiceConfig: sc,
	})

	roundrobinConn, err := grpc.Dial(
		target,
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`), // This sets the initial balancing policy.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer roundrobinConn.Close()

	fmt.Println("--- calling helloworld.Greeter/SayHello with round_robin ---")
	makeRPCs(roundrobinConn, 10)

	// You can also plug in your own custom lb policy, which needs to be
	// configurable. This n is configurable. Try changing it and see how the
	// behavior changes.
	json = `{"loadBalancingConfig": [{"custom_round_robin":{"n": 3}}]}` // I guess the parsing pulls it from registry
	sc = internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(json)
	mr.UpdateState(resolver.State{
		Addresses: addrs,
		ServiceConfig: sc,
	})
	customroundrobinConn, err := grpc.Dial( // does this pick up the mr, do I even need to declare service config?
		target,
		// grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"custom_round_robin":{"n": 3}}]}`),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer customroundrobinConn.Close()
	fmt.Println("--- calling helloworld.Greeter/SayHello with custom_round_robin ---")
	makeRPCs(customroundrobinConn, 20)
}

func init() {
	balancer.Register(&customRoundRobinBuilder{})
}

const customRRName = "custom_round_robin"

type customRRConfig struct {
	serviceconfig.LoadBalancingConfig `json:"-"`

	// N represents how often pick iterations chose the second SubConn
	// in the list. Defaults to 3.
	N uint32 `json:"n,omitempty"`
}

func (customRoundRobinBuilder) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	lbConfig := &customRRConfig{
		N: 3,
	}
	if err := json.Unmarshal(s, lbConfig); err != nil {
		return nil, fmt.Errorf("custom-round-robin: unable to unmarshal customRRConfig: %v", err)
	}
	return lbConfig, nil
}

type customRoundRobinBuilder struct{}

func (customRoundRobinBuilder) Name() string {
	return customRRName
}

func (customRoundRobinBuilder) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	/*crr := &customRoundRobin2{
		cc: cc,
	}*/ // bopts anywhere?
	return &customRoundRobin2{
		cc: cc,
		cse: &balancer.ConnectivityStateEvaluator{},
		state: connectivity.Connecting,
		picker: base.NewErrPicker(balancer.ErrNoSubConnAvailable), // take away the coupling of this dependency?
		subConns: resolver.NewAddressMap(),
		scStates: make(map[balancer.SubConn]connectivity.State),
		// config has health check...ignore?
	}

	// picker and state perssited
}

// Connect is what the base node needs to do...

var logger = grpclog.Component("example")

// same behavior, but implements the full balancer.Balancer API
// same behavior as base but don't call upward
// call NewSubConn and connect() and stuff, will need to change after pick first becomes petiole
// same connectivity semantics on updating on a READY, how to keep deterministic?
// he's fine with copying all of the logic into here sans Build
// Doesn't need to be 1:1

// as much complexity as needed
type customRoundRobin2 struct { // move this to own seperate package - more complicated the better
	cc balancer.ClientConn // initialized, and then read only after that
	cse *balancer.ConnectivityStateEvaluator

	state connectivity.State
	picker balancer.Picker

	subConns *resolver.AddressMap // shows the specificity of the SCs
	scStates map[balancer.SubConn]connectivity.State

	// needs both
	resolverErr error
	connErr error




	mu sync.Mutex // mu protects n
	n uint32 // persisted in UpdateClientConnState, read in UpdateState to persist picker so needs a mutex grab before and after read building picker because not calling out into another system
} //

func (crr2 *customRoundRobin2) UpdateClientConnState(state balancer.ClientConnState) error {
	if logger.V(2) {
		logger.Info("base.baseBalancer: got new ClientConn state: ", state)
	}
	crrCfg, ok := state.BalancerConfig.(*customRRConfig)
	if !ok {
		return balancer.ErrBadResolverState
	}
	crrCfg.N = crrCfg.N // update scs - yes guaranteed from Doug's chat
	// Successful resolution; clear resolver error and ensure we return nil.
	crr2.resolverErr = nil // should this come before or after

	addrsSet := resolver.NewAddressMap()
	// for _, a := range

	for _, a := range state.ResolverState.Addresses {
		addrsSet.Set(a, nil)
		if _, ok := crr2.subConns.Get(a); !ok { // new address
			// create new subconns
			var sc balancer.SubConn // pass the ref
			opts := balancer.NewSubConnOptions{
				// no health check enabled...to complicated
				// client has no access to scs?
				StateListener: func(scs balancer.SubConnState) { crr2.updateSubConnState(sc, scs) },
			}
			sc, err := crr2.cc.NewSubConn([]resolver.Address{a}, opts)
			if err != nil {
				logger.Warningf("custom_round_robin: failed to create new SubConn: %v", err)
				continue
			}
			crr2.subConns.Set(a, sc)
			// starts at IDLE
			crr2.scStates[sc] = connectivity.Idle
			// record a transition from shutdown to idle
			crr2.cse.RecordTransition(connectivity.Shutdown, connectivity.Idle)
			sc.Connect()
		}
	} // [] Addresses what to do with these?
	// accounting for the new set...^^^

	// accounting for old set that got deleted vvv
	for _, a := range crr2.subConns.Keys() {
		sci, _ := crr2.subConns.Get(a)
		sc := sci.(balancer.SubConn)
		// a was removed by resolver
		if _, ok := addrsSet.Get(a); ok {
			sc.Shutdown() // need to call the creation and cleanup on the subconns
			crr2.subConns.Delete(a) // keep track of these deletions by recent state updates
		}
	}

	if len(state.ResolverState.Addresses) == 0 {
		crr2.ResolverError(errors.New("produced zero addresses"))
		return balancer.ErrBadResolverState
	}
	// updateSCS (in callback), UpdateClientConnState, and ResolverError are
	// where you regenerate picker nothing can come in concurrently.
	crr2.regeneratePicker()
	crr2.cc.UpdateState(balancer.State{ConnectivityState: crr2.state, Picker: crr2.picker})
	return nil
}

func (crr2 *customRoundRobin2) ResolverError(err error) {
	// persists error and writes that back (so it gets most recent error)
	crr2.resolverErr = err
	if crr2.subConns.Len() == 0 {
		crr2.state = connectivity.TransientFailure
	}

	if crr2.state != connectivity.TransientFailure {
		// The picker will not change since the balancer does not currently
		// report an error.
		return
	}
	crr2.regeneratePicker()
	crr2.cc.UpdateState(balancer.State{
		ConnectivityState: crr2.state,
		Picker:            crr2.picker,
	})
}

// gets the state it needs from this closure

// mergeErrors builds an error from the last connection error and the last
// resolver error.  Must only be called if b.state is TransientFailure.
func (crr2 *customRoundRobin2) mergeErrors() error {
	if crr2.connErr == nil {
		return fmt.Errorf("last resolver error: %v", crr2.resolverErr)
	}
	if crr2.resolverErr == nil {
		return fmt.Errorf("last connection error: %v", crr2.connErr)
	}
	return fmt.Errorf("last connection error: %v; last resolver error: %v", crr2.connErr, crr2.resolverErr)
}

// regeneratePicker generates a picker from the balancer's state. If the
// balancer is in transient failure, an error picker is returned which always
// gives an error. Otherwise, it returns a picker with all the ready SubConns.
func (crr2 *customRoundRobin2) regeneratePicker() {
	if crr2.state == connectivity.TransientFailure {
		// just sets this field
		crr2.picker = newErrPicker(crr2.mergeErrors())
		return
	}

	// loops through the subconns, keyed on address specifity, still only one total subconn value
	var readySCs []balancer.SubConn

	for _, addr := range crr2.subConns.Keys() {
		sci, _ := crr2.subConns.Get(addr)
		sc := sci.(balancer.SubConn)
		if st, ok := crr2.scStates[sc]; ok && st == connectivity.Ready {
			readySCs = append(readySCs, sc)
		}
	}


	// if you want determinism:
	if len(readySCs) != 2 {
		return
	}


	// logically equivalent to what I did in Build
	// constructs a new round robin picker
	crr2.picker = &customRoundRobinPicker{ // won't get called until update ccs doesn't error, otherwise tf
		subConns: readySCs,
		n: crr2.n, // read doesn't need sync since called sync
		next: 0, // always start it at 0, otherwise would need a random number generator
	}
}

func (crr2 *customRoundRobin2) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	// logger.Errorf("base.baseBalancer: UpdateSubConnState(%v, %+v) called unexpectedly", sc, state)
	logger.Errorf("custom_round_robin: UpdateSubConnState(%v, %+v) called unexpectedly", sc, state)
}

// set as a listener callback in what is passed upward.
func (crr2 *customRoundRobin2) updateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	if logger.V(2) {
		logger.Infof("custom_round_robin: handle SubConn state change: %p, %v", sc, state.ConnectivityState)
	}
	oldState, ok := crr2.scStates[sc]
	if !ok {
		if logger.V(2) {
			logger.Infof("custom_round_robin: got state changes for an unknown SubConn: %p, %v", sc, state.ConnectivityState)
		}
		return
	}
	newState := state.ConnectivityState

	// sticky tf - will eventually move to pf once it's universal leaf policy...
	if oldState == connectivity.TransientFailure &&
		(newState == connectivity.Connecting || newState == connectivity.Idle) { // eats calls...
		// Once a subconn enters TRANSIENT_FAILURE, ignore subsequent IDLE or
		// CONNECTING transitions to prevent the aggregated state from being
		// always CONNECTING when many backends exist but are all down.
		if newState == connectivity.Idle { // wtf does this mean?
			sc.Connect()
		}
		return
	}
	crr2.scStates[sc] = newState
	switch newState {

	}

	switch state.ConnectivityState {
	case connectivity.Idle: // an idle sent down tree is what triggers sc to connect
		sc.Connect()
	// case connectivity.Connecting: // eats it in tf case, if not nothing happens...just ignore

	// oh handled from persisting ready sc in it
	// case connectivity.Ready: // how base does it, Java does it another way

	case connectivity.TransientFailure:
		crr2.connErr = state.ConnectionError // so transient failure sc triggers whole balancer to be errored with that error
	case connectivity.Shutdown:
		delete(crr2.scStates, sc)
	} // Do I need all this connectivity state transitioning logic?

	crr2.state = crr2.cse.RecordTransition(oldState, newState)

	// Regenerate picker if the sc entered or left ready, or if the aggregated
	// state of the balancer is TRANSIENT_FAILURE (may need to update error
	// message).
	if (newState == connectivity.Ready) != (oldState == connectivity.Ready) ||
		crr2.state == connectivity.TransientFailure {
		crr2.regeneratePicker()
	}
	crr2.cc.UpdateState(balancer.State{
		ConnectivityState: crr2.state,
		Picker: crr2.picker,
	}) // before I just intercept, now this is representing the thing doing the calling so I don't need the layer
}

// it is itself the leaf, so doesn't need to be a balancer.ClientConn

func (crr2 *customRoundRobin2) Close() {
	// does this need to close anything? this is the leaf...
}

func (crr2 *customRoundRobin2) ExitIdle() { // This is optional, I don't think I need this...
	// does this need to close anything
}

type customRoundRobin struct {
	// Embeds balancer.Balancer because needs to intercept UpdateClientConnState
	// to learn about N.
	balancer.Balancer
	n uint32
}

func (crr *customRoundRobin) UpdateClientConnState(state balancer.ClientConnState) error {
	crrCfg, ok := state.BalancerConfig.(*customRRConfig)
	if !ok {
		return balancer.ErrBadResolverState
	}
	crr.n = crrCfg.N
	return crr.Balancer.UpdateClientConnState(state)
}

func (crr *customRoundRobin) Build(info base.PickerBuildInfo) balancer.Picker {
	if len(info.ReadySCs) == 0 {
		return base.NewErrPicker(balancer.ErrNoSubConnAvailable)
	}

	scs := make([]balancer.SubConn, 0, len(info.ReadySCs))
	for sc := range info.ReadySCs {
		scs = append(scs, sc)
	}
	return &customRoundRobinPicker{
		subConns: scs,
		n:        crr.n,
		next:     0,
	}
}

type customRoundRobinPicker struct {
	subConns []balancer.SubConn
	n        uint32

	next uint32
}

func (crrp *customRoundRobinPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	next := atomic.AddUint32(&crrp.next, 1)
	index := 0
	if next%crrp.n == 0 {
		index = 1
	}
	sc := crrp.subConns[index%len(crrp.subConns)]
	return balancer.PickResult{SubConn: sc}, nil
}

// newErrPicker returns a Picker that always returns err on Pick().
func newErrPicker(err error) balancer.Picker {
	return &errPicker{err: err}
}

type errPicker struct {
	err error // Pick() always returns this err.
}

func (p *errPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	return balancer.PickResult{}, p.err
}