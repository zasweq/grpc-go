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

const customRRName = "custom_round_robin"

type customRRConfig struct {
	serviceconfig.LoadBalancingConfig `json:"-"`

	// ChooseSecond represents how often pick iterations choose the second
	// SubConn in the list. Defaults to 3. If 0 never choose the second SubConn.
	ChooseSecond uint32 `json:"chooseSecond,omitempty"`
}

// pick first config passed down from the parent

type customRoundRobinBuilder struct{}

func (customRoundRobinBuilder) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	// This will switch to parseconfig using arbitary child config type...
	lbConfig := &customRRConfig{
		ChooseSecond: 3,
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
	// time and read only after, or are only accessed as part of its
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

	cfg *customRRConfig

	// InhibitPickerUpdates determines whether picker updates from the child
	// forward to cc or not.
	inhibitPickerUpdates bool
}

func (crr *customRoundRobin) UpdateClientConnState(state balancer.ClientConnState) error {
	return /*crr.child.UpdateClientConnState?*/
}

func (crr *customRoundRobin) ResolverError(err error) {
	// forward to child
}

// This function is deprecated. SubConn state updates now come through listener
// callbacks. This balancer does not deal with SubConns directly and has no need
// to intercept listener callbacks.
func (crr *customRoundRobin) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	logger.Errorf("custom_round_robin: UpdateSubConnState(%v, %+v) called unexpectedly", sc, state)
}

func (crr *customRoundRobin) Close() { // hold child...delegate to that, this gets callouts and that determines updatestate
	// simply now forward to child
}

// regeneratePicker generates a picker if both child balancers are READY and
// forwards it upward.
func (crr *customRoundRobin) UpdateChildState(childStates []childState) { // pointer?
	var readyPickers []balancer.Picker
	// Check if two are ready...if ready
	for _, childState := range childStates {
		if childState.state.ConnectivityState == connectivity.Ready {
			readyPickers = append(readyPickers, childState.state.Picker)
		}
	}

	// For determinism, this balancer only updates it's picker when both
	// backends of the example are ready. Thus, no need to keep track of
	// aggregated state and can simply specify this balancer is READY once it
	// has two ready children. Other balancers can keep track of aggregated
	// state and interact with errors as part of picker.
	if len(readyPickers) != 2 {
		return
	}
	// can do what I want with the errors, rr across all those
	picker := &customRoundRobinPicker{
		pickers:      readyPickers, // ignores the errors, but can rr to them...
		chooseSecond: crr.cfg.ChooseSecond,
		next:         0,
	}
	crr.cc.UpdateState(balancer.State{
		ConnectivityState: connectivity.Ready,
		Picker:            picker,
	})
}

type customRoundRobinPicker struct {
	pickers      []balancer.Picker
	chooseSecond uint32
	next         uint32
}

func (crrp *customRoundRobinPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	next := atomic.AddUint32(&crrp.next, 1)
	index := 0
	if next != 0 && next%crrp.chooseSecond == 0 {
		index = 1
	}
	childPicker := crrp.pickers[index%len(crrp.pickers)]
	return childPicker.Pick(info)
}
