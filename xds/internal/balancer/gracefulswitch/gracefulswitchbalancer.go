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
	"fmt"
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
	b := gracefulSwitchBalancer{
		cc: cc,
		bOpts: opts,
	}

	// go b.run() - Do we really need this now that no channel receive? I think not because events all come from API call
	return b
}

func (bb) Name() string {
	return balancerName
}

// Does this need to implement json.Unmarshaler interface?
type lbConfig struct {
	serviceconfig.LoadBalancingConfig // This is a "opaque data type"...which one do you use? There's two config pieces of data now...
	ChildBalancerType string
	// Raw JSON First...what is the child?
	Config serviceconfig.LoadBalancingConfig // Should this unmarshal into json.RawMessage
}

/*type BalancerConfig struct { - A type already defined in the codebase
	Name   string
	Config serviceconfig.LoadBalancingConfig
}*/

func (bb) ParseConfig(c json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	// This is an actual LB policy so needs to support parsing the config
	var cfg lbConfig // json problem: needs an intermediate here that has a rawJSON as one of it's fields
	if err := json.Unmarshal(c, &cfg); err != nil {
		return nil, fmt.Errorf("graceful switch: unable to unmarshal lbconfig: %s, error: %v", string(c), err)
	}
	// This right here parses the config
	// balancer.Get(lb name)...
	// send the raw JSON to that parsed config
	builder := balancer.Get(cfg.ChildBalancerType)
	if builder == nil {
		return nil, fmt.Errorf("balancer of type %v not supported", cfg.ChildBalancerType)
	}
	// Do you convert the lbConfig.Config into just JSON for that level of json.Unmarshal?
	parsedCfg, err := builder.(balancer.ConfigParser).ParseConfig(cfg.Config/*json.RawMessage*/)
	if err != nil {
		return nil, fmt.Errorf()
	}
	cfg.LoadBalancingConfig = parsedCfg // Stick this emitted parsed lbCfg and stick it in the config to return
	// Does this persist anything?...I still don't get how Doug thinks this will persist something
	return &cfg, nil
}

// From the highest level - what does this balancer do?

// Gracefully switch from A to B

// Functionally, what is the point of a balancer

type gracefulSwitchBalancer struct {
	// "it should just have two children and the LB policy configs for them I think"

	builderCurrent balancer.Builder // Is there any reason to hold onto this?
	// buildOptsCurrent balancer.BuildOptions // Do we just get this from top level?
	// builder.Build(sbc, sbc.buildOpts)
	balancerCurrent balancer.Balancer

	builderPending balancer.Builder
	// buildOptsPending balancer.BuildOptions // Do we need these two above?
	balancerPending balancer.Balancer

	// "Only used when balancer group is started. Gets cleared when sub-balancer is closed." - For dynamic balancers


	// Java state -
	// pendingState, pendingPicker, bool currentLbIsReady

	// C++ state -
	// Nothing else

	// idToBalancerConfig map[string]*subBalancerWrapper - I really don't think
	// we will need this, as the logic for current vs. pending configs are
	// handled just on UpdateClientConnState.

	// How do we plumb in new build options? // Are these build options constant
	// for both balancers? Persisted at graceful switch balancer build time, do
	// we update both balancers with this? Yes, cluster manager updates all it's children with
	// these build options, thus you can use these to udpate both child balancers.
	bOpts          balancer.BuildOptions

	// Subconn to subbalancer map (populate it same way)...
	scToSubBalancer map[balancer.SubConn]*balancer.Balancer // How to branch these two?
	// Populated by: intercepting upward calls on AddSubConn()

	// Used for: figuring out where to forward subconn updates to - also
	// cleaning up this component after it gets closed(). Also, "removing
	// SubConns it created" from the specific child balancer when the child gets
	// deleted by pending.

	cc balancer.ClientConn // Passed in on construction...from grpc?
	// Used for: forwarding updateState() calls upward

	// Mutexes on the outflow and inflow - actually...seems like you just need
	// one mutex to protect this...do you need to grab it on both inflow and
	// outflow? Or are these separate? Both of these codepaths def touch same
	// state. But remember, there's also UpdateSubConnState, which also reads from the map
}

// Once the channel is in a state other than READY, the event is you switch over to the second one
// if it hasn't been instantiated yet - from Java implementation, which again Doug views as the basis.
// I'm assuming this comes from LB policy in UpdateState, since this determines ClientConn state

// What events can happen at each state of the state machine? - draw this out


// what if current child is filled but not ready? *** big question I think you're fine



// (Put these API defs in design document)
// Main functions VVV, UpdateState goes from child upward, needs to know
func (b *gracefulSwitchBalancer) UpdateState(state balancer.State) { // Once it gets here - already parsed config
	// Java and C have pretty solid implementations of this

	// What connectivity.State do LB's start out at? It seems like they start
	// out at CONNECTING?

	// Couldn't this be called by either or?
	// How do you update state?
	// state.ConnectivityState
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


	// state.Picker // Do I need to do anything with this? Java has pending picker

	// "UpdateState overrides balancer.ClientConn, to keep state and picker."
	// Definitely needs this because this determines when to gracefully switch over
	// state.ConnectivityState // Enum across IDLE, CONNECTING, READY, TRANSIENT_FAILURE, SHUTDOWN



	// Switches over once new one is ready...except this is what signifies the
	// event that second LB policy is ready, so simply forward the update
	// (connectivity state and new picker) up to the Client Conn.

	// There is some logic on what picker and connectivity state sent up to the ClientConn
	// and how you get there (i.e. State persisted and A vs B)


	// if branch on lb == pendingLB - I still don't get how we'd determine which
	// lower level balancer called - see Java and C for reference?
	if /*lb == pendingLB*/ { // TODO: figure out how to know which balancer an update from Balancer came from...

		if state.ConnectivityState == connectivity.Ready { // Event - second LB policy switches to READY
			// Close() and also "remove subconns it created", loop through map?

			// Swap the pending to current
			b.balancerCurrent = b.balancerPending // Switches over
			b.balancerPending = nil
			// Do we even need builder and build opts?

			// Def update the Client Conn so that the Client Conn can use the new picker
		}
	} // Java has an else branch on switches over to pending (similar to timer) on the current lb policy exiting READY - do we even need this? I think we should support this because Doug views Java as the reference


	// On an update from either current or pending, you can update the client
	// conn to use new state and new picker - I don't think there's a codepath
	// where you don't send this up to the ClientConn.
	b.cc.UpdateState(state)



	// from Build()...you have ClientConn
	// b.cc.UpdateState(/*State - {connectivity.State and Picker} - what do I send here*/)
}

// ^^^
// both of these lb policies interact with the same state, so need a mutex lock
// and unlock on both methods
// vvv

// This will be used by cluster manager

// How do we use LB policies traditionally? Maybe that will give me a better picture

// API definitions (implementation):
// Causes it to implement balancer.Balancer
// ClientConn (->) Balancer -> Subbalancer
// config A to config B
func (b *gracefulSwitchBalancer) UpdateClientConnState(state balancer.ClientConnState) error {

	// lbCfg.ChildBalancerType // Now have the logic based on type ("name" in c-core)

	// lbCfg.LoadBalancingConfig // And config to use...is this the right
	// thing...this is an interface. Is it json.RawMessage? This is anonomyous
	// field, but is logically the same type.


	// Need some way to compare the lbCfg.ChildBalancerType to the current and pending

	// I think you get a JSON version of LB type: json.RawMessage, use this to
	// build a config in this LB policies ParseConfig, that gets to this method,
	// it's already parsed, so you just send it to the built balancers
	// .UpdateClientConnState(state).

	// the balancer you just built with the builder which was pulled from the lb
	// type from the global registry now takes this config, and Parses it
	// (ParseConfig), no, this happens in this own balancers ParseConfig().


	// First case described in c-core: we have no existing child policy, in this
	// case, create a new child policy and store it in current child policy.


	// Second case described in c-core:
	// Existing Child Policy (i.e. BalancerConfig == nil) but no pending

	// a. If config is same type...simply pass update down to existing policy

	// b. If config is different type...create a new policy and store in pending
	// child policy. This will be swapped into child policy once the new child
	// transitions into state READY.



	// Third case described in c-core:
	// We have an existing child policy nad a pending child policy from a previous update (i.e. Pending not transitioned into state READY)

	// a. If going from current config to new config does not require a new policy, update existing pending child policy.

	// b. If going from current to new does require a new, create a new policy, and replace the pending policy.


	// I feel like they missed a case...what if it's same policy as current? - Ping Mark with a line link in c core
	lbCfg, ok := state.BalancerConfig.(*lbConfig) // Or could just make this the internal data type...BalancerConfig?
	if !ok {
		// b.logger.Warningf("xds: unexpected LoadBalancingConfig type: %T", state.BalancerConfig)
		// But I don't have a logger...
		return balancer.ErrBadResolverState
	}


	buildPolicy := b.balancerCurrent == nil || true /*Some way of comparing the LB policy type of this specific update to the most recent config sent - persist the config, along with the type of config (most recent config received, just store the whole thing which will have type)*/
	var balToUpdate balancer.Balancer
	if buildPolicy {
		// Build the LB
		builder := balancer.Get(lbCfg.ChildBalancerType) // ChildBalancerType but balancer.Get() will be called on certain conditions anyway...but I think you can just fill this in
		if builder == nil {
			return fmt.Errorf("balancer of type %v not supported", lbCfg.ChildBalancerType)
		}
		bal := builder.Build(b, b.bOpts) // Make sure this is accurate - are build options per both?
		// Set either current or pending
		if b.balancerCurrent == nil {
			balToUpdate = b.balancerCurrent
		} else {
			balToUpdate = b.balancerPending
		}
		balToUpdate = bal
		// Send update to that LB
	} else {
		// balToUpdate is either current or pending...
		if b.balancerPending != nil {
			balToUpdate = b.balancerPending
		} else {
			balToUpdate = b.balancerCurrent
		}
	}

	// Does this need to use pointers in order to not make a copy? I'm pretty sure it does
	balToUpdate.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: state.ResolverState, // I don't think there's anything else you need to add...
		BalancerConfig: lbCfg.LoadBalancingConfig, // Loses child balancer type
	})
	// This block of code plus the unmarshaling into a config is all I need I think for this function

	// from resolver state passed in, Doug mentioned you might need to update
	// both child balancers. Resolver State (like Resolver Error) is relevant to
	// both balancers.

	// return nil?
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

	// Logically a destructor...? Think of this as cleaning up resources?

	// I'm assuming it means to Close() both balancers - look at cluster manager and other balancers for inspiration


	// BalancerGroup Close() is (when I come back...implement this)


	// Incoming flow - loop through the persisted subbalancer data structure and tell ClientConn to remove it
	// and delete from map. Is removing subconn equivalent to closing subconn?

	// Either one or two mutex grabs...either have one protecting all or protecting inflow and outflow

	for sc := range b.scToSubBalancer {
		b.cc.RemoveSubConn(sc)
		delete(b.scToSubBalancer, sc)
		// This is it in BalancerGroup, is there anything else I need to do here?
	}

	// Outgoing flow: Stop/delete both balancers if applicable (i.e. not nil)...
	// Mutex grab if needed
	if b.balancerCurrent != nil {
		b.balancerCurrent.Close()
	}
	if b.balancerPending != nil {
		b.balancerCurrent.Close()
	}
}

// Overwrites balancer.ClientConn...

// Does it need to intercept both flows?

// Subbalancer -> (API) ClientConn balancer.ClientConn

// NewSubConn()? Forwards upward to parent load balancer...or can you skip this part of the flow? I.e. pass a client conn down the lb tree and have the lower level load balancers call that

func NewSubConn([]resolver.Address, balancer.NewSubConnOptions) (balancer.SubConn, error) {
	// This one is in c++, doesn't look like it's in Java

	// What are the functions in the different balancers?

	// SubBalancerWrapper - "NewSubConn overrides balancer.ClientConn, so
	// balancer group can keep track of the relation between subconns and
	// sub-balancers." - intercepts new subconn call and persists that
	// relationship for future use.
}

// RemoveSubConn() Forwards upward to Parents Load Balancer...or can you skip this part of the flow?
func RemoveSubConn(balancer.SubConn) {
	// Do we need this? Yeah, probably, use this to delete the created subconn
	// from the map on a RemoveSubConn call from a lower level balancer.
}

// ResolveNow()

// Target()

// ^^^ do you need these two? to implement balancer.balancer? Even
// UpdateAddresses() for balancer.ClientConn. SubBalancerWrapper embeds
// balancer.ClientConn but only "overrides" UpdateState() and NewSubConn()


















// "If the channel is currently in a state other than READY, the new policy will be swapped to place immediately".
// How does channel communicate what state its in? This is set by the balancer...so UpdateState()?

// "If the old policy is not READY at the moment" - swap to the new one

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




// Things I need to do (I don't know if needed in order):

// 1. Figure out which methods of balancer.Balancer and
// balancer.ClientConn to implement...and which to leave to the embedded
// interface (also figure out what happens to missing methods)

// 2. Finish cleaning up UpdateClientConnState(), also implement the logic of
// persisting the recent config and using that to determine whether there is
// delta in type for a new update) also is c core right? It's Mark though and he
// explained..."which balancers to forward updates to." **This when I get back

// 3. UpdateState()...figure out big challenge of how to figure out which subbalancer
// it's sent from...do we need to keep it consistent with Java...looks like it does from
// what Doug said. There's a lot of code to still write here.

// 4. Intercept Subconn Call and build out map which is used to know which balancer to forward updates to.


// 5. Protect certain state shared across multiple methods with mutexes...one or two (maybe do this at the end)



// 6. Close() and what to do for that...also adding close certain subconns to UpdateState()

// Done I think outside of adding a mutex lock


// 7. ParseConfig() and the question of LoadBalancingConfig()...raw JSON gets spit out from that so you can just send that, returns IsLoadBalancingConfig()
// This gets called before UpdateClientConnState()...so that will receive the parsed type: parsedConfig (IsLoadBalancingConfig())

// * Done outside of rawJSON problem