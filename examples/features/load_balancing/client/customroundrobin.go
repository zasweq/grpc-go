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

package main

import (
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
)

type customRoundRobin3 struct { // if Doug wants more complexity he'll ask for it...
	cc balancer.ClientConn

	bOpts balancer.BuildOptions

	state connectivity.State

	pfs *resolver.EndpointMap

	// resolverError, conn error...maybe handle this in pick first...
	n uint32
	pickFirstBuilder balancer.Builder

	inhibitPickerUpdates bool
}

// still use balancer wrapper?

func (crr3 *customRoundRobin3) UpdateClientConnState(state balancer.ClientConnState) error {
	if logger.V(2) {
		logger.Info("custom_round_robin: got new ClientConn state: ", state)
	}
	crrCfg, ok := state.BalancerConfig.(*customRRConfig)
	if !ok {
		return balancer.ErrBadResolverState
	}
	crrCfg.N = crrCfg.N

	endpointSet := resolver.NewEndpointMap()
	crr3.inhibitPickerUpdates = true
	for _, endpoint := range state.ResolverState.Endpoints { // so manual resolver needs to spit out endpoints/scale up support for endpoints...
		endpointSet.Set(endpoint, nil)
		var pickFirst *balancerWrapper2 // need to id child...
		pf, ok := crr3.pfs.Get(endpoint)
		if ok {
			pickFirst = pf.(*balancerWrapper2) // will these typecasts ever panic (it is an example though...)
		} else {
			bw := &balancerWrapper2{
				ClientConn: crr3.cc,
			}
			pfb := crr3.pickFirstBuilder.Build(bw, crr3.bOpts)
			bw.Balancer = pfb
			crr3.pfs.Set(endpoint, bw)
		}
		// update child uncondtionally...
		pickFirst.UpdateClientConnState(balancer.ClientConnState{
			ResolverState: resolver.State{
				Endpoints: []resolver.Endpoint{endpoint},
				Attributes: state.ResolverState.Attributes,
			},
			// no service config, never needs to turn on address list shuffling bool
		})
	}
	// gets keys then immediately uses as value
	for _, e := range crr3.pfs.Keys() { // yes uses endpoints as unique keys
		ep, _ := crr3.pfs.Get(e)
		pickFirst := ep.(balancer.Balancer)
		// pick first was removed by resolver (unique endpoint logically corresponding to pick first was removed)
		if _, ok := endpointSet.Get(e); !ok {
			pickFirst.Close()
			crr3.pfs.Delete(e) // is this allowed? I guess unit tests will find out
		}

		// I FEEL LIKE DELETE FROM ENDPOINT MAP


	}
	crr3.inhibitPickerUpdates = false
	crr3.regeneratePicker() // one picker update per...
}

func (crr3 *customRoundRobin3) ResolverError(err error) {
	// what to do with resolver error and conn error? let the child send that back and determine?
	crr3.inhibitPickerUpdates = true
	for _, pf := range crr3.pfs.Values() { // ohhh unordered set operations...
		pickFirst := pf.(*balancerWrapper2)
		pickFirst.ResolverError(err)
	}
	crr3.inhibitPickerUpdates = false
	crr3.regeneratePicker()
}


// This function is deprecated. SubConnState updates now come through listener
// callbacks. This balancer does not deal with SubConns directly or need to
// intercept listener callbacks.
func (crr3 *customRoundRobin3) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	logger.Errorf("custom_round_robin: UpdateSubConnState(%v, %+v) called unexpectedly", sc, state)
}

func (crr3 *customRoundRobin3) Close() {
	for _, pf := range crr3.pfs.Values() {
		pickFirst := pf.(balancer.Balancer)
		pickFirst.Close()
	}
}

func (crr3 *customRoundRobin3) regeneratePicker() {
	// generates a picker based off state and sends it upward...only send upward once per call
	if crr3.inhibitPickerUpdates {
		// log ignoring picker updates...maybe write comments explaining this stuff
		return
	}

	// generate and send. I don't know if needs to persist
	var readyPickers []balancer.Picker
	for _, bw := range crr3.pfs.Values() { // oh need to implement this map as well
		pickFirst := bw.(*balancerWrapper) // when you create it, wrap it and store it in endpoint map
		if pickFirst.state.ConnectivityState == connectivity.Ready {
			readyPickers = append(readyPickers, pickFirst.state.Picker)
		}
	} // called at the end, so no need for aggregated state logic

	// maybe just do a whole new example with this for a good read me
	if len(readyPickers) != 2 {
		return
	}

	// Do I want to do anyhting with this later (aka need to persist?)
	picker := &customRoundRobinPicker2{
		pickers: readyPickers,
		n: crr3.n,
		next: 0,
	}

	crr3.cc.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready/*either aggregated but since determinism we know it's ready so just say ready*/,
		Picker: picker,
	})
}

type balancerWrapper2 struct {
	balancer.Balancer // embed this or send upward or what?
	balancer.ClientConn // embed to intercept UpdateState, doesn't deal with SubConns

	crr *customRoundRobin2 // heap memory of top level custom round robin...

	state balancer.State // most recent state (ids this, and provides a wrapper)
}

// this gets called from subconn state updates, which are also thread safe, so
// do Java way of recording the balancer.State
// inhibit picker updates
// child one calls back, persist state

// child two calls back, persist state

// child three calls back, persist state
// build picker update idempotently...i.e. no transitions and just the state above...transitions to the right complicate

// all the calls down happen in the box (goroutine) timer and rpc happening come outside box

// Picker updates from pick first are all triggered by synchrnous calls down
// into balancer.Balancer (client conn state updates, resolver errors, subconn
// state updates (through listener callbacks, which is still treated as part of
// balancer API)).
func (bw2 *balancerWrapper2) UpdateState(state balancer.State) { // gets called inline multiple times from UpdateClientConnState and resolver error, persist new state and prevent upwards picker calls...
	bw2.state = state // persist recent connectivity state and picker
	// persist the state but not the picker...

	// in update ccs: inhibit, then call regeneratePicker() at the end of operation (maybe make this comment somewhere)
	// in resolver error: inhibit, then call regeneratePicker() at the end of operation
	// in sc state update, calls down into pick first, calls back up here...so regeneratePicker() at end and gate at end
	// sc state updates will come in with no inhibit picker updates. Same logic as base, calls into it trigger picker update, and calls into it are guarnateed sync.

	bw2.crr.regeneratePicker() // call uncondtionally, don't gate, this regneration of picker won't happen
}

// Think about synchronization of data and events...run()? mutex?

// java: not thread safe...callbacks on the helper within the sync context
// sync context such as rls gets something back (thread that runs, executor, operation that you want to run, run it behind a queue, in order, implementation is closer to lock)
// calldowns

// All of the picker updates, channel client conn gives it update
// subconn updates come from the channel, so this is sync

// update state can be triggered from async outlier detection

// async -> sync, (adding to queue), executing code synchronization
// pickers have to be thread safe
// load balancers are not thread safe


// priority inhibits picker updates
// resolver error + addresses into one call


// bool, calls all down, gets the picker inline and does update at end
// state for children {state, picker} // update this on a picker update, update states persisted, inhibit picker
// then update picker after persisted states (idempotent, doesn't look at state transitions - more transitions than states, hard to reason, eventual consitency, how we got there is different, state to dictate)
