/*
 *
 * Copyright 2024 gRPC authors.
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

// Package balanceraggregator implements a balancerAggregator helper.
//
// # Experimental
//
// Notice: This package is EXPERIMENTAL and may be changed or removed in a
// later release.
package balanceraggregator

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/balancer/gracefulswitch"
	"google.golang.org/grpc/internal/grpcrand"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

// ChildState is the balancer state of a child along with the endpoint which
// identifies the child balancer.
type ChildState struct {
	Endpoint resolver.Endpoint
	State    balancer.State
}

// NewBalancer returns a new balancerAggregator.
func NewBalancer(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
	// atomic.StorePointer(&ba.children2, unsafe.Pointer(newChildren))
	return &balancerAggregator{
		cc:       cc,
		bOpts:    opts,
		// children: resolver.NewEndpointMap(),
		children: unsafe.Pointer(resolver.NewEndpointMap()),
	}
}

// look at least request
// in dynamic rds PR you don't have access to the top lebel object, this one you do like atomic.Bool
// uint32 (it's part of the struct so when you take the address you get it)

// balancerAggregator is a balancer that wraps child balancers. It creates a
// child balancer with child config for every unique Endpoint received. It
// updates the child states on any update from parent or child.
type balancerAggregator struct {
	cc    balancer.ClientConn
	bOpts balancer.BuildOptions

	children unsafe.Pointer // *resolver.EndpointMap

	// inhibitChildUpdates is set during UpdateClientConnState/ResolverError
	// calls (calls to children will each produce an update, only want one
	// update).
	inhibitChildUpdates atomic.Bool // should I also add this to the mutex below

	// for correctness, does not affect logic...
	mu sync.Mutex // Sync updateState callouts and childState recent state updates
}

// update the map atomically
// map a (if get a new TF, compute aggregated state and reflect this to parent)
// map b (atomically write this, aggregated state now becomes this)

// keep the bool (doesn't guarantee sync, just an optimization and prevents
// interspliced TF), and the child state guarded by that mu
// swap of the map (look at least request with newChildren and that "snapshot" of state)
// is what guarantees no weird behavior here wrt interslicing, keep that operation atomic
// for consistent aggregated state updates to parent

// UpdateClientConnState creates a child for new endpoints and deletes children
// for endpoints that are no longer present. It also updates all the children,
// and sends a single synchronous update of the childrens' aggregated state at
// the end of the UpdateClientConnState operation. If any endpoint has no
// addresses, returns error without forwarding any updates. Otherwise returns
// first error found from a child, but fully processes the new update.
func (ba *balancerAggregator) UpdateClientConnState(state balancer.ClientConnState) error {
	if len(state.ResolverState.Endpoints) == 0 {
		return errors.New("endpoints list is empty")
	}

	// Check/return early if any endpoints have no addresses.
	// TODO: make this configurable if needed.
	for i, endpoint := range state.ResolverState.Endpoints {
		if len(endpoint.Addresses) == 0 {
			return fmt.Errorf("endpoint %d has empty addresses", i)
		}
	}

	ba.inhibitChildUpdates.Store(true)
	defer func() {
		ba.inhibitChildUpdates.Store(false)
		ba.updateState()
	}()
	var ret error

	/*
	uPtr := atomic.LoadPointer(&ba.children2)
		children := (*resolver.EndpointMap)(uPtr)
	*/
	children := (*resolver.EndpointMap)(ba.children)

	// is it a * to a map? can I combine with endpointSet above?
	newChildren := resolver.NewEndpointMap() // *EndpointMap, can I typecast that to unsafe?

	// loop through UpdateCCS after building it out? shouldn't update children...
	// inhibits anyway


	// the old state still needs to be read to determine the diff...could send a commit that
	// only does his other comments...

	// build new children map, and atomic write
	// update state atomic read of the whole map...

	// inhibit race

	// Update/Create new children.
	for _, endpoint := range state.ResolverState.Endpoints {
		if _, ok := newChildren.Get(endpoint); ok {
			// Endpoint child was already created, continue to avoid duplicate
			// update.
			continue
		}
		var bal *balancerWrapper
		if child, ok := children.Get(endpoint); ok { // read of the children map here - this is ok since updateState is read
			bal = child.(*balancerWrapper)
		} else {
			bal = &balancerWrapper{
				childState: ChildState{Endpoint: endpoint},
				ClientConn: ba.cc,
				ba:         ba,
			}
			bal.Balancer = gracefulswitch.NewBalancer(bal, ba.bOpts)
			// ba.children.Set(endpoint, bal) // write
		}
		newChildren.Set(endpoint, bal) // unconditionally set, no longer need to create new children map
		if err := bal.UpdateClientConnState(balancer.ClientConnState{
			BalancerConfig: state.BalancerConfig,
			ResolverState: resolver.State{
				Endpoints:  []resolver.Endpoint{endpoint},
				Attributes: state.ResolverState.Attributes,
			},
		}); err != nil && ret == nil {
			// Return first error found, and always commit full processing of
			// updating children. If desired to process more specific errors
			// across all endpoints, caller should make these specific
			// validations, this is a current limitation for simplicities sake.
			ret = err
		}
	}
	// Delete old children that are no longer present.
	for _, e := range children.Keys() {
		child, _ := children.Get(e) // read of the children map here
		bal := child.(balancer.Balancer)
		if _, ok := newChildren.Get(e); !ok { // presense check, doesn't write to that pointer...so safe below writes to pointer
			bal.Close()
			// ba.children.Delete(e) // write - I think reads are sync
		}
	} // these closes can come in spliced...

	// build a pointer to the newly constructed map
	atomic.StorePointer(&ba.children, unsafe.Pointer(newChildren))

	return ret
}

// ResolverError forwards the resolver error to all of the balancerAggregator's
// children and sends a single synchronous update of the childStates at the end
// of the ResolverError operation.
func (ba *balancerAggregator) ResolverError(err error) {
	ba.inhibitChildUpdates.Store(true)
	defer func() {
		ba.inhibitChildUpdates.Store(false)
		ba.updateState()
	}()
	children := (*resolver.EndpointMap)(ba.children)
	for _, child := range children.Values() {
		bal := child.(balancer.Balancer)
		bal.ResolverError(err)
	}
}

func (ba *balancerAggregator) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	// UpdateSubConnState is deprecated.
}

func (ba *balancerAggregator) Close() {
	children := (*resolver.EndpointMap)(ba.children)
	for _, child := range children.Values() {
		bal := child.(balancer.Balancer)
		bal.Close()
	}
}

// UpdateCCS a b c updateState for each, grab lock, write new state, give up

// Update for a and b come in...can either write state before (both updates will
// show state) or after (next update will show state), and update once or twice,

// updateState updates this component's state. It sends the aggregated state,
// and a picker with round robin behavior with all the child states present if
// needed.
func (ba *balancerAggregator) updateState() {
	if ba.inhibitChildUpdates.Load() {
		return
	}
	var readyPickers, connectingPickers, idlePickers, transientFailurePickers []balancer.Picker

	ba.mu.Lock()
	defer ba.mu.Unlock()
	// load children into an unsafe pointer on the stack

	// mention mutex guards interface data, top level map structure is guarded
	// by unsafe.Pointer so you're good there...
	uPtr := atomic.LoadPointer(&ba.children)
	children := (*resolver.EndpointMap)(uPtr)
	childStates := make([]ChildState, 0, children.Len())

	for _, child := range children.Values() { // top level structure is fine to keep
		bw := child.(*balancerWrapper)
		childState := bw.childState // this write is to the heap pointers, this never gets read
		childStates = append(childStates, childState)
		childPicker := childState.State.Picker
		switch childState.State.ConnectivityState {
		case connectivity.Ready:
			readyPickers = append(readyPickers, childPicker)
		case connectivity.Connecting:
			connectingPickers = append(connectingPickers, childPicker)
		case connectivity.Idle:
			idlePickers = append(idlePickers, childPicker)
		case connectivity.TransientFailure:
			transientFailurePickers = append(transientFailurePickers, childPicker)
			// connectivity.Shutdown shouldn't appear.
		}
	}

	// Construct the round robin picker based off the aggregated state. Whatever
	// the aggregated state, use the pickers present that are currently in that
	// state only.
	var aggState connectivity.State
	var pickers []balancer.Picker
	if len(readyPickers) >= 1 {
		aggState = connectivity.Ready
		pickers = readyPickers
	} else if len(connectingPickers) >= 1 {
		aggState = connectivity.Connecting
		pickers = connectingPickers
	} else if len(idlePickers) >= 1 {
		aggState = connectivity.Idle
		pickers = idlePickers
	} else if len(transientFailurePickers) >= 1 {
		aggState = connectivity.TransientFailure
		pickers = transientFailurePickers
	} else {
		aggState = connectivity.TransientFailure
		pickers = append(pickers, base.NewErrPicker(errors.New("no children to pick from")))
	} // No children (resolver error before valid update).

	p := &pickerWithChildStates{
		pickers:     pickers,
		childStates: childStates,
		next:        uint32(grpcrand.Intn(len(pickers))),
	}
	ba.cc.UpdateState(balancer.State{
		ConnectivityState: aggState,
		Picker:            p,
	})
}

// pickerWithChildStates delegates to the pickers it holds in a round robin
// fashion. It also contains the childStates of all the balancerAggregator's
// children.
type pickerWithChildStates struct {
	pickers     []balancer.Picker
	childStates []ChildState
	next        uint32
}

func (p *pickerWithChildStates) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	nextIndex := atomic.AddUint32(&p.next, 1)
	picker := p.pickers[nextIndex%uint32(len(p.pickers))]
	return picker.Pick(info)
}

// ChildStatesFromPicker returns the child states from the picker if the picker
// is one provided in a call to UpdateState from this balancer.
func ChildStatesFromPicker(picker balancer.Picker) []ChildState {
	p, ok := picker.(*pickerWithChildStates)
	if !ok {
		return nil
	}
	return p.childStates
}

// balancerWrapper is a wrapper of a balancer. It ID's a child balancer by
// endpoint, and persists recent child balancer state.
type balancerWrapper struct {
	balancer.Balancer   // Simply forward balancer.Balancer operations.
	balancer.ClientConn // embed to intercept UpdateState, doesn't deal with SubConns

	ba *balancerAggregator

	childState ChildState
}

func (bw *balancerWrapper) UpdateState(state balancer.State) {
	bw.ba.mu.Lock()
	bw.childState.State = state
	bw.ba.mu.Unlock()
	bw.ba.updateState()
}

func ParseConfig(cfg json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	return gracefulswitch.ParseConfig(cfg)
}

// PickFirstConfig is a pick first config without shuffling enabled.
const PickFirstConfig = "[{\"pick_first\": {}}]"
