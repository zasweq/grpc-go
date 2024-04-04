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

// Package balanceraggregator implements a BalancerAggregator helper.
//
// # Experimental
//
// Notice: This package is EXPERIMENTAL and may be changed or removed in a
// later release.
package balanceraggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/grpc/internal/grpcsync"
	"sync"
	"sync/atomic"

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

// NewBalancer returns a new BalancerAggregator.
func NewBalancer(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
	ctx, cancel := context.WithCancel(context.Background())
	return &BalancerAggregator{
		cc:       cc,
		bOpts:    opts,
		children: resolver.NewEndpointMap(),
		serializer: grpcsync.NewCallbackSerializer(ctx), // this guarantees we don't need a lock around UpdateState, and that it comes serially and not concurrently...
		serializerCancel: cancel, // when to cancel this?
	}
}

// BalancerAggregator is a balancer that wraps child balancers. It creates a
// child balancer with child config for every unique Endpoint received. It
// updates the child states on any update from parent or child.
type BalancerAggregator struct {
	cc    balancer.ClientConn
	bOpts balancer.BuildOptions

	children *resolver.EndpointMap



	// inhibitChildUpdates is set during UpdateClientConnState/ResolverError
	// calls (calls to children will each produce an update, only want one
	// update).
	inhibitChildUpdates bool // protect with atomic...? this and child state...

	// set by
	// inhibitChildUpdates set (writes) by UpdateCCS and ResolverError
	// UpdateState() from child reads

	// recentState read/written in UpdateCCS/ResolverError
	// written in UpdateState()


	// The serializer and its cancel func are initialized at build time(((, and the
	// rest of the fields here are only accessed from serializer callbacks (or
	// from balancer.Balancer methods, which themselves are guaranteed to be
	// mutually exclusive) and hence do not need to be guarded by a mutex.))) stick inhibit in this...?


	serializer       *grpcsync.CallbackSerializer // Serializes updates from gRPC and xDS client.
	serializerCancel context.CancelFunc           // Stops the above serializer.
}

// UpdateClientConnState creates a child for new endpoints and deletes children
// for endpoints that are no longer present. It also updates all the children,
// and sends a single synchronous update of the children's aggregate state at
// the end of the UpdateClientConnState operation. If any endpoint has no
// addresses, returns error. Otherwise returns first error found from a child,
// but fully processes the new update.
func (ba *BalancerAggregator) UpdateClientConnState(state balancer.ClientConnState) error {
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
	endpointSet := resolver.NewEndpointMap()
	ba.inhibitChildUpdates = true
	defer func() {
		ba.inhibitChildUpdates = false
		ba.updateState()
	}()
	var ret error
	// Update/Create new children.
	for _, endpoint := range state.ResolverState.Endpoints {
		if _, ok := endpointSet.Get(endpoint); ok {
			// Endpoint child was already created, continue to avoid duplicate
			// update.
			continue
		}
		endpointSet.Set(endpoint, nil)
		var bal *balancerWrapper
		if child, ok := ba.children.Get(endpoint); ok {
			bal = child.(*balancerWrapper)
		} else {
			bal = &balancerWrapper{
				childState: ChildState{Endpoint: endpoint},
				ClientConn: ba.cc,
				ba:         ba,
			}
			bal.Balancer = gracefulswitch.NewBalancer(bal, ba.bOpts)
			ba.children.Set(endpoint, bal)
		}
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
	for _, e := range ba.children.Keys() {
		child, _ := ba.children.Get(e)
		bal := child.(balancer.Balancer)
		if _, ok := endpointSet.Get(e); !ok {
			bal.Close()
			ba.children.Delete(e)
		}
	}
	return ret
}

// ResolverError forwards the resolver error to all of the BalancerAggregator's
// children and sends a single synchronous update of the childStates at the end
// of the ResolverError operation.
func (ba *BalancerAggregator) ResolverError(err error) {
	ba.inhibitChildUpdates = true
	defer func() {
		ba.inhibitChildUpdates = false
		ba.updateState()
	}()
	for _, child := range ba.children.Values() {
		bal := child.(balancer.Balancer)
		bal.ResolverError(err)
	}
}

func (ba *BalancerAggregator) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	// UpdateSubConnState is deprecated.
}

func (ba *BalancerAggregator) Close() {
	/*
	b.serializer.Schedule(func(ctx context.Context) {
			b.closeAllWatchers()

			if b.childLB != nil {
				b.childLB.Close()
				b.childLB = nil
			}
			if b.cachedRoot != nil {
				b.cachedRoot.Close()
			}
			if b.cachedIdentity != nil {
				b.cachedIdentity.Close()
			}
			b.logger.Infof("Shutdown")
		})
		b.serializerCancel()
		<-b.serializer.Done()
	*/

	// Looks like he schedules cleanup, and then cancels, and then blocks until
	// done.

	for _, child := range ba.children.Values() {
		bal := child.(balancer.Balancer)
		bal.Close()
	}
}

// synchronization of child state, bool of inhibit - atomic or sync by that
// event loop

// updateState updates this component's state. It sends the aggregated state,
// and a picker with round robin behavior with all the child states present if
// needed.
func (ba *BalancerAggregator) updateState() {
	ba.serializer.Schedule(func() { // what context do I pass in here...allows timeouts?
		if ba.inhibitChildUpdates { // needs an atomic, does this serialization prevent this?
			return
		}

		// do all the stuff after...

		// does child state need to have a mutex around it?

	})


	if ba.inhibitChildUpdates {
		return
	}
	readyPickers := make([]balancer.Picker, 0)
	connectingPickers := make([]balancer.Picker, 0)
	idlePickers := make([]balancer.Picker, 0)
	transientFailurePickers := make([]balancer.Picker, 0)

	childStates := make([]ChildState, 0, ba.children.Len())
	for _, child := range ba.children.Values() {
		bw := child.(*balancerWrapper)
		bw.mu.Lock()
		childState := bw.childState
		bw.mu.Unlock()
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
	}) // these calls out need to come serially


	// Unlock() here after update state, sync whole operation - can handle
	// updatestate atomically read and write bool that inhibits...

	// I sync on an individual child...could switch to one lock that
	// is scoped to all children...Unlock() here

	// inhibit with a bool
}

// pickerWithChildStates delegates to the pickers it holds in a round robin
// fashion. It also contains the childStates of all the BalancerAggregator's
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

	ba *BalancerAggregator

	mu         sync.Mutex
	childState ChildState
}

func (bw *balancerWrapper) UpdateState(state balancer.State) {
	bw.mu.Lock()
	bw.childState.State = state
	bw.mu.Unlock()
	bw.ba.updateState() // this is called by sync UpdateCCS and UpdateState, need to add a mutex somewhere...how to prevent deadlock?
}

/*
Doug's commment about synchronization of state:


> The latest state/inhibit will need something to protect the r/w data race

This can probably also be a simple atomic?
Either that or you always push something to the serializer that updates the child state and then only calls updateState if you aren't inhibiting (or uS does the inhibit check as it does already)


*/


// not a problem to block on processing UpdateState, can't callback inline

// sync inhibitPickerUpdates, childstate, and the operation of updating the parent (doesn't call back inline)

// inhibit be atomic (I think unconditional)
// callback serializer - handles eventual consistency and deadlock or channel which you drain...
// or gate calls into callback serializer that do updatestate by the inhibit

// orrrrr

// callback serializer that updates state only if inhibit - this can write state
// and UpdateState if inhibit not set

func ParseConfig(cfg json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	return gracefulswitch.ParseConfig(cfg)
}

// PickFirstConfig is a pick first config without shuffling enabled.
const PickFirstConfig = "[{\"pick_first\": {}}]"

// against master, add either atomics or locks to prevent deadlocks
// forget about callback serializer for now...
