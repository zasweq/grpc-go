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

package customroundrobin

import (
	"encoding/json"
	"fmt"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

func init() {
	balancer.Register(customRoundRobinBuilder{})
}

// Get this working then move over.
const customRRName = "custom_round_robin"

type customRRConfig struct {
	serviceconfig.LoadBalancingConfig `json:"-"`

	// N represents how often pick iterations chose the second SubConn
	// in the list. Defaults to 3. If 0 never chose second SubConn.
	N uint32 `json:"n,omitempty"`
}

type customRoundRobinBuilder struct{}

func (customRoundRobinBuilder) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	lbConfig := &customRRConfig{
		N: 3,
	}
	if err := json.Unmarshal(s, lbConfig); err != nil {
		return nil, fmt.Errorf("custom-round-robin: unable to unmarshal customRRConfig: %v", err)
	}
	return lbConfig, nil
}

func (customRoundRobinBuilder) Name() string {
	return customRRName
}

func (customRoundRobinBuilder) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	pfBuilder := balancer.Get(grpc.PickFirstBalancerName)
	if pfBuilder == nil {
		return nil
	}
	return &customRoundRobin{
		cc:               cc,
		bOpts:            bOpts,
		pfs:              resolver.NewEndpointMap(),
		pickFirstBuilder: pfBuilder,
	}
}

var logger = grpclog.Component("example")

type customRoundRobin struct {
	// All state and operations on this balancer are either initialized at build
	// time and read only after, or are only accessed as part of it's
	// balancer.Balancer API (UpdateState from children only comes in from
	// balancer.Balancer calls as well, and children are called one at a time),
	// in which calls are guaranteed to come synchronously. Thus, no extra
	// synchronization is required in this balancer.
	cc    balancer.ClientConn
	bOpts balancer.BuildOptions
	// Note that this balancer is a petiole policy which wraps pick first (see
	// gRFC A61). This is the intended way a user written custom lb should be
	// specified, as pick first will contain a lot of useful functionality, such
	// as Sticky Transient Failure, Happy Eyeballs, and Health Checking.
	pickFirstBuilder balancer.Builder
	pfs              *resolver.EndpointMap

	n                    uint32
	inhibitPickerUpdates bool
}

func (crr *customRoundRobin) UpdateClientConnState(state balancer.ClientConnState) error {
	if logger.V(2) {
		logger.Info("custom_round_robin: got new ClientConn state: ", state)
	}
	crrCfg, ok := state.BalancerConfig.(*customRRConfig)
	if !ok {
		return balancer.ErrBadResolverState
	}
	crr.n = crrCfg.N

	endpointSet := resolver.NewEndpointMap()
	crr.inhibitPickerUpdates = true
	for _, endpoint := range state.ResolverState.Endpoints {
		endpointSet.Set(endpoint, nil)
		var pickFirst *balancerWrapper
		pf, ok := crr.pfs.Get(endpoint)
		if ok {
			pickFirst = pf.(*balancerWrapper)
		} else {
			pickFirst = &balancerWrapper{
				ClientConn: crr.cc,
				crr:        crr,
			}
			pfb := crr.pickFirstBuilder.Build(pickFirst, crr.bOpts)
			pickFirst.Balancer = pfb
			crr.pfs.Set(endpoint, pickFirst)
		}
		// update child uncondtionally, in case attributes or address ordering
		// changed. Let pick first deal with any potential diffs, too
		// complicated to only update if we know something changed.
		pickFirst.UpdateClientConnState(balancer.ClientConnState{
			ResolverState: resolver.State{
				Endpoints:  []resolver.Endpoint{endpoint},
				Attributes: state.ResolverState.Attributes,
			},
			// no service config, never needs to turn on address list shuffling
			// bool in petiole policies.
		})
	}
	for _, e := range crr.pfs.Keys() {
		ep, _ := crr.pfs.Get(e)
		pickFirst := ep.(balancer.Balancer)
		// pick first was removed by resolver (unique endpoint logically corresponding to pick first was removed)
		if _, ok := endpointSet.Get(e); !ok {
			pickFirst.Close()
			crr.pfs.Delete(e) // is this working? I guess unit tests will find out needs a second clientconn update.
		}
	}
	crr.inhibitPickerUpdates = false
	crr.regeneratePicker() // one synchronous picker update per Update Client Conn State operation.
	return nil             // what do I do about updating pick first children and that error?
}

func (crr *customRoundRobin) ResolverError(err error) {
	crr.inhibitPickerUpdates = true
	for _, pf := range crr.pfs.Values() {
		pickFirst := pf.(*balancerWrapper)
		pickFirst.ResolverError(err)
	}
	crr.inhibitPickerUpdates = false
	crr.regeneratePicker()
}

// This function is deprecated. SubConnState updates now come through listener
// callbacks. This balancer does not deal with SubConns directly or need to
// intercept listener callbacks.
func (crr *customRoundRobin) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	logger.Errorf("custom_round_robin: UpdateSubConnState(%v, %+v) called unexpectedly", sc, state)
}

func (crr *customRoundRobin) Close() {
	for _, pf := range crr.pfs.Values() {
		pickFirst := pf.(balancer.Balancer)
		pickFirst.Close()
	}
}

// regeneratePicker generates a picker based off persisted child balancer state
// and forwards it upward. This is intended to be fully executed once per
// relevant balancer.Balancer operation into custom round robin balancer.
func (crr *customRoundRobin) regeneratePicker() {
	if crr.inhibitPickerUpdates { // no need for picker updates...
		return
	}

	// generate and send. I don't know if needs to persist
	var readyPickers []balancer.Picker
	for _, bw := range crr.pfs.Values() { // oh need to implement this map as well
		pickFirst := bw.(*balancerWrapper) // when you create it, wrap it and store it in endpoint map
		if pickFirst.state.ConnectivityState == connectivity.Ready {
			readyPickers = append(readyPickers, pickFirst.state.Picker)
		}
	}

	if len(readyPickers) != 2 { // does this need to return something? to trigger sc creation?
		return
	}

	picker := &customRoundRobinPicker{
		pickers: readyPickers,
		n:       crr.n,
		next:    0,
	}
	// For determinism, this balancer only updates it's picker when both
	// backends of the example are ready. Thus, no need to keep track of
	// aggregated state and can simply specify this balancer is READY once it
	// has two ready children.

	// maybe just do a whole new example with this for a good read me
	// either a. rewrite example to use manual resolver completly, or write a
	// seperate example with just manual resolver. Honestly might do b.

	crr.cc.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker:            picker,
	})
}

type balancerWrapper struct {
	balancer.Balancer
	balancer.ClientConn // embed to intercept UpdateState, doesn't deal with SubConns

	crr *customRoundRobin

	state balancer.State
}

// Picker updates from pick first are all triggered by synchronous calls down
// into balancer.Balancer (client conn state updates, resolver errors, subconn
// state updates (through listener callbacks, which is still treated as part of
// balancer API)).
func (bw *balancerWrapper) UpdateState(state balancer.State) { // gets called inline multiple times from UpdateClientConnState and resolver error, persist new state and prevent upwards picker calls...
	bw.state = state

	// in update ccs: inhibit, then call regeneratePicker() at the end of operation (maybe make this comment somewhere)
	// in resolver error: inhibit, then call regeneratePicker() at the end of operation
	// in sc state update, calls down into pick first, calls back up here...so regeneratePicker() at end and gate at end
	// sc state updates will come in with no inhibit picker updates. Same logic as base, calls into it trigger picker update, and calls into it are guarnateed sync.

	bw.crr.regeneratePicker() // call uncondtionally, don't gate, this regneration of picker won't happen
}

type customRoundRobinPicker struct {
	pickers []balancer.Picker
	n       uint32
	next    uint32
}

func (crrp *customRoundRobinPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	next := atomic.AddUint32(&crrp.next, 1)
	index := 0
	if next != 0 && next%crrp.n == 0 {
		index = 1
	}
	childPicker := crrp.pickers[index%len(crrp.pickers)]
	return childPicker.Pick(info)
}

// newErrPicker returns a Picker that always returns err on Pick().
func newErrPicker(err error) balancer.Picker { // should I use this for the case where you have an error? maybe from UpdateClientConnState?
	return &errPicker{err: err}
}

type errPicker struct {
	err error // Pick() always returns this err.
}

func (p *errPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	return balancer.PickResult{}, p.err
}
