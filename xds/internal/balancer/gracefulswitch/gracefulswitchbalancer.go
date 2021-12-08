/*
 *
 * Copyright 2021 gRPC authors.
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

// Package graceful switch implements a graceful switch load balancer.
package gracefulswitch

import (
	"encoding/json"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

// This is not in xds package in c core...anyways

const balancerName = "graceful_switch_load_balancer"

func init() {
	balancer.Register(bb{})
}

type bb struct{}

// Instantiate with it's configuration, then
// Then constantly update it with new configuration

func (bb) Build(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
	// A and B are nil still - is there anything else we need to setup here?
}

func (bb) Name() string {
	return balancerName
}

func (bb) ParseConfig(c json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	return nil, nil
}


// Gracefully switch from A to B

type gracefulSwitchBalancer struct {
	// "it should just have two children and the LB policy configs for them I think"

	// LBConfig?

	// scToBalancer map - balancer group uses subbalancer

	// Two things - current config, balancer object, "pending" config and balancer object

	// static part

	// This builder is literally balancer.Get(name builder is registered on)
	builderCurrent balancer.Builder // Is this the right thing to hold onto for builder?
	buildOptsCurrent balancer.BuildOptions // Are these two logically equivalent to a config?
	// builder.Build(sbc, sbc.buildOpts)
	balancerCurrent balancer.Balancer // This is going to be nil? Represents the balancer object itself...

	// When do you pass these in to persist? When do you actually build with these?

	builderPending balancer.Builder
	buildOptsPending balancer.BuildOptions // Do we need these two above?
	balancerPending balancer.Balancer



	// What other state do we need here? (besides (config, balancer) and (config, balancer))

	// Java state -
	// pendingState, pendingPicker, bool currentLbIsReady

	// C++ state -
	// Nothing else

	// This is handled by the user I believe
	// idToBalancerConfig map[string]*subBalancerWrapper


	// Subconn to subbalancer map (populate it same way)...
	scToSubBalancer map[balancer.SubConn]*balancer.Balancer // How to branch these two?
	// Used for: figuring out where to forward subconn updates to


	// Mutexes on the outflow and inflow
}

// Main difference is switching over after a set time limit use a timer and
// channel? - Have this be state and configurable at build time

// Once the channel expires, the event is you switch over to the second one
// if it hasn't been instantiated yet.


// Or switch over once the second is ready

// Starts off with what...then what...then what

// What events can happen at each state of the state machine?

//





// METHOD HERE REPRESENTING GETTING AN SERVICE CONFIG FROM ANOTHER PLACE

// Definitely persists the config itself, but does it actually build the balancer?

// What gets passed in, a balancer builder or a LB Config itself

// cluster manager gets the builder from the name of the config passed in...

// serviceconfig.LoadBalancingConfig

func (b *gracefulSwitchBalancer) ServiceConfigUpdate(/**/) {
	// if b.currentConfig = nil
	// 	Populate current config
	//  Do you build this?
	// else
	//  Populate future config
	//  Do you build this?
}


// run loop, but really only make it

func (b *gracefulSwitchBalancer) run() {
	// select

	// case child balancer becomes ready - if its current no op

	// what if current child is filled but not ready? *** big question



	// In the specific scenario child balancer is populated but there's pending

	// both of these events move pending into current:

	// fixed timer running out...(<-timer.C)

	// or LB transitioning into ready (how is this signaled - UpdateState right()...except you'd need to know what balancer it's for...
}

// Most of this flow is just pass throughs

// UpdateState( LB Policy State ) <-- Needs to branch - something about subconns

// Ah, on the flow downward



// (Put these API defs in design document)
// Main functions VVV, UpdateState goes from child upward, needs to know
// Balancer (->) ClientConn
func (b *gracefulSwitchBalancer) UpdateState(state balancer.State) { // Only overwrites
	// Couldn't this be called by either or?
	// How do you update state?
	state.ConnectivityState
	// TODO: Figure out how to branch this into correct balancer
	// c does CalledByPendingChild() child_
	// child_ == parent_->pending_child_policy_.get();
	// CalledByCurrentChild() child_ == parent_->child_policy_.get()


	// Java - lb == pendingLb
	// lb == currentLb

	// (lb) <- where do they get this state from? closure in Java,  == (pending||current)State persisted
	// Helper in c++
	// Will this even be called by lower levels or will they update the client conn...
	// How do LB policies logically report ready?


	state.Picker // Do I need to do anything with this? Java has pending picker

	// "UpdateState overrides balancer.ClientConn, to keep state and picker."
	// Definitely needs this because this determines when to gracefully switch over
	state.ConnectivityState // Enum across IDLE, CONNECTING, READY, TRANSIENT_FAILURE, SHUTDOWN
}

// API definitions (implementation):
// Causes it to implement balancer.Balancer
// ClientConn (->) Balancer -> Subbalancer
// config A to config B
func (b *gracefulSwitchBalancer) UpdateClientConnState(state balancer.ClientConnState) error {
	// Needs this to forward to the two children
	// compare config a to config b - if it's a whole different type or not

	// We don't know precedence though...
	// if no current - persist config and build current

	// if same config as current (and current instantiated)
	// send config down to instantiated balancer

	// if config is different than current (implies current is instantiated)
	// persist config and build in pending


	// Does it even persist a builder?
	// newT comes from lb config (in cluster manager state.BalancerConfig.(*lbConfig)
	// balancer.Get(newT.ChildPolicy.Name) -> spits out a builder, use that builder
	// state.ResolverState. // where is the LB Config...what do we store?

	// lbCfg, ok := state.BalancerConfig.(*lbConfig) <- touches the
	// BalancerConfig (for cds and higher level balancers)

	state.BalancerConfig./*lbConfig <- can't just typecast this to anything*/
	// Unmarshals into ChildPolicy.Name, or Unmarshals into lbconfig with children, gets the name for that
	// Adds a builder with a name to the balancer group...
	// Uses this type of lbConfig - sepcific to the balancer
}

func (b *gracefulSwitchBalancer) ResolverError(err error) {
	// Simply forward the error to both children if the (current/pending) balancer is instantiated
	// Same question as before...can you forward an error to an LB policy that is not in state READY?

	// Mutex Lock?
	if b.balancerCurrent != nil {
		b.balancerCurrent.ResolverError(err)
	}
	if b.balancerPending != nil {
		b.balancerPending.ResolverError(err)
	}
	// Mutex Unlock? - unless we're assuming this gets called synchronously
}

// Intertwined with BalancerGroup - either reuse balancer group or plumb this in somehow

// Do you need a mutex lock anywhere?

func (b *gracefulSwitchBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	// "Called by grpc when the state of a subconn switches"

	// Doesn't seem to be supported in Java...also not in c++

	// Needs the SubConnToBalancer map to know which balancer to forward update
	// to...can you forward to balancer before balancer switches to READY? I'm assuming so if Menghan thinks you need
	// a subconn map

	// Grab a mutex for these subbalancers

	bal, ok := b.scToSubBalancer[sc]
	if !ok {
		return
	}
	if state.ConnectivityState == connectivity.Shutdown {
		delete(b.scToSubBalancer, sc)
	}

	// Unlock a mutex

	// Grab a mutex

	// bal.UpdateSubConnState(sc, state)
	// Unlock a mutex
}

func (b *gracefulSwitchBalancer) Close() {
	// Does this need to do anything? Is there a start for this?
}

// Overwrites balancer.ClientConn...

// Does it need to intercept both flows?

// Subbalancer -> (API) ClientConn balancer.ClientConn

// NewSubConn()? Forwards upward to parent load balancer...or can you skip this part of the flow? I.e. pass a client conn down the lb tree and have the lower level load balancers call that

func NewSubConn([]resolver.Address, balancer.NewSubConnOptions) (balancer.SubConn, error) {
	// This one is in c++

	// What are the functions in the different balancers?

	// SubBalancerWrapper - "NewSubConn overrides balancer.ClientConn, so balancer group can keep track of
	// the relation between subconns and sub-balancers."
}

// RemoveSubConn() Forwards upward to Parents Load Balancer...or can you skip this part of the flow?
func RemoveSubConn(balancer.SubConn) {
	// Do we need this?
}

// ResolveNow()

// Target()

// ^^^ do you need these two?

/*func (b *gracefulSwitchBalancer) UpdateState(state balancer.State) { // Only overwrites
	// "UpdateState overrides balancer.ClientConn, to keep state and picker."
	state.ConnectivityState
	// Definitely needs this because this determines when to gracefully switch over
	state.ConnectivityState // Enum across IDLE, CONNECTING, READY, TRANSIENT_FAILURE, SHUTDOWN
}*/

// "If the channel is currently in a state other than READY, the new policcy will be swapped to place immediately".
// How does channel communicate what state its in?

// "If the old policy is not READY at the moment" - swap to the new one...will need to persist
// old state somehow.

// Java comment: "A load balancer that gracefully swaps to a new lb policy. If the channel is in a state other than ready, the new policy will be swapped into place immediately.
// Otherwise, the channel will keep using the old policy until the new policy reports READY or the old policy exits READY"


// New updates go to B, if its equal just keep it, if its not, then delete whats pending (logically equivalent to 1 2 (building) 3 (only one we care about now - pending))

// A (current) B (pending)
// B
// Factory, Picker
// A
// factory (config) + actual policy -> converted on Build(), when do you do this

// B
// factory (config) + actual policy

// What comes in from xds resolver is the configs wrapped in a lb config, can get names from those


// "It should just have two children and the LB policy configs for them I think", logically equivalent to having the factory + actual policy

// A <- State machine that determines logic (aka swapping to new one if this one
// isn't ready....or if pending isn't ready)...how do other things around the
// codebase persist state?


// B <- State machine that determines logic as well...(only use this once READY and gracefully started)


// When B switches to ready seems to be: wait on a notification channel

// State machine but binary with Ready vs Not Ready, I wanna say the only things that are
// Ready are when oh...there is literally a state called READY.

// How does a LB policy get to ready...is it this long chain of balancers that need to be configured
// properly and then send up READY all the way up the stack of balancers...

// How does it Notify a State change? Is it when it updates upwards?

// Entrance function is that API...do we even need to sync to put the updates on a channel
// or do we just grab a mutex on an entrance function?

// Subconn (connectivity) state is client conn to balancer

// Subbalancer to ClientConn
// ClientConn.UpdateState(State) <- this is where state lies

// So theres your event, when UpdateState gets called (with the state of the load balancing policy)

// Can both of the children UpdateState?

// Uses similar logic to balancer group where UpdateSubConn needs to find the correct one to forward to