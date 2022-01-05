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
	"sync"
)

// This is not in xds package in c core...anyways someone will comment about this...even in Java.

const balancerName = "graceful_switch_load_balancer"

func init() {
	balancer.Register(bb{})
}

type bb struct{}

func (bb) Build(cc balancer.ClientConn, opts balancer.BuildOptions) balancer.Balancer {
	b := &gracefulSwitchBalancer{ // Or just return this directly if no run goroutine
		cc: cc,
		bOpts: opts,
		scToSubBalancer: make(map[balancer.SubConn]balancer.Balancer),
	}

	// go b.run() I chose to sync with mutexes but this is still an option...events coming in from lower would be handled later...would this cause sync (fragmented) issues. Need to make sure no nil read on pending...need state to protect this if going this way.
	return b
}

func (bb) Name() string {
	return balancerName
}

type lbConfig struct {
	serviceconfig.LoadBalancingConfig
	ChildBalancerType string
	Config serviceconfig.LoadBalancingConfig
}

type intermediateConfig struct {
	serviceconfig.LoadBalancingConfig
	ChildBalancerType string
	ChildConfigJSON json.RawMessage
}

func (bb) ParseConfig(c json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	var intermediateCfg intermediateConfig
	if err := json.Unmarshal(c, &intermediateCfg); err != nil {
		return nil, fmt.Errorf("graceful switch: unable to unmarshal lbconfig: %s, error: %v", string(c), err)
	}
	builder := balancer.Get(intermediateCfg.ChildBalancerType)
	if builder == nil {
		return nil, fmt.Errorf("balancer of type %v not supported", intermediateCfg.ChildBalancerType)
	}
	parsedChildCfg, err := builder.(balancer.ConfigParser).ParseConfig(intermediateCfg.ChildConfigJSON)
	if err != nil {
		return nil, fmt.Errorf("graceful switch: unable to unmarshal lbconfig: %s of type %v, error: %v", string(intermediateCfg.ChildConfigJSON), intermediateCfg.ChildBalancerType, err)
	}
	return &lbConfig{
		ChildBalancerType: intermediateCfg.ChildBalancerType,
		Config: parsedChildCfg,
	}, nil
}

type gracefulSwitchBalancer struct {
	balancerCurrent balancer.Balancer
	balancerPending balancer.Balancer
	// These are logically idToBalancerConfig except get written to (updated)/read from on the reverse flow

	// Mutex Outgoing/Incoming: Read/Written to (race condition) on both Incoming and Outgoing updates. Thus, what do you protect this with?
	// BOTH - Written to as well in UpdateClientConnState and also UpdateState(). Seperate Mutex? If third Mutex, will this induce any deadlock situations?

	// Will this ever build out (mu.Lock()     mu.Lock()       mu.Unlock()
	// mu.Unlock())...seems like the problem was protecting entire updates with
	// a mutex and thus happens before relationship. I feel like it won't be a
	// problem, but triage and make sure this code (only guaranteeing a happens
	// before relationship) will never be built.

	// Can updating a child balancer child.Update...cause it to call back inline and need to also grab a lock to read the same child balancer?

	// How cluster handler handles this is calling cancel() for xdsclient, thus
	// deleting the node. I don't get how not having the events happening
	// atomically prevents a nil read say if a child is deleted. Aka how do
	// these fragmented events not cause a nil read? Guess it doesn't matter



	outgoingMu sync.Mutex
	// outgoingStarted bool - figure out if we need this or not

	pendingState balancer.State // Do we need to protect this? I think so, child balancers can call updateState concurrently. (again, question about fragmented code segments in balancergroup)
	// Mutex Outgoing/Incoming: passed from child balancers, so INCOMING.

	// C++ state - The most recent config passed to UpdateClientConnState()
	recentConfig *lbConfig
	// Mutex Outgoing/Incoming: Comes from UpdateClientConnState(), so this is OUTGOING. (comes from Client Conn)

	bOpts          balancer.BuildOptions
	// Mutex: Does this need to be protected by anything? I don't think so, as
	// this is static and persisted on build time to be used for both the
	// subbalancers on creation.

	cc balancer.ClientConn

	incomingMu sync.Mutex
	// incomingStarted bool - figure out if we need this or not
	scToSubBalancer map[balancer.SubConn]balancer.Balancer
	// Mutex Outgoing/Incoming: Incoming, as populated by intercepting NewSubConn() calls, which come from the child balancer.

	// Cleaned up on...deletion of a current balancer...on Close() on the whole thing
	// Balancer group has this cleaned up also on receiving connectivity.Shutdown?

	// Used for: figuring out where to forward subconn updates to - also
	// cleaning up this component after it gets closed(). Also, "removing
	// SubConns it created" from the specific child balancer when the child gets
	// deleted by pending.

}

// Once the channel is in a state other than READY, the event is you switch over to the second one
// if it hasn't been instantiated yet - from Java implementation, which again Doug views as the basis.
// I'm assuming this comes from LB policy in UpdateState, since this determines ClientConn state. Looks like I'm not supporting this.


// what if current child is filled but not ready? *** big question I think you're fine



// (Put these API defs in design document)

// Functions of balancer.ClientConn, subbalancer -> client conn (incoming) VVV,
// UpdateState and NewSubconn goes from child upward, needs to know what child
// balancer it came from so ccw handles that by wrapping it also with correct
// balancer and client conn itself.
func (gsb *gracefulSwitchBalancer) updateState(bal balancer.Balancer, state balancer.State) { // Once it gets here - already parsed config
	// This can get called concurrently by multiple Child Balancers, as the balancers have no knowledge of each other.
	// This deletes from scToSubBalancer, so I think this needs the lock - yes - but at what granularity?


	// What connectivity.State do LB's start out at? It seems like they start
	// out at CONNECTING?


	if bal == gsb.balancerPending {
		// Cache the pending state and picker if you don't need to send at this instant (i.e. the LB policy is not ready)
		// you can send it later on an event like current LB exits READY.
		gsb.pendingState = state
		if state.ConnectivityState == connectivity.Ready { // Event - second LB policy switches to READY
			// Close() and also "remove subconns it created", loop through map?
			// Swap the pending to current
			// Def update the Client Conn so that the Client Conn can use the new picker
			// All of the logic is in this function
			gsb.swap()
		}

	} else /*Add if here, make this no op if update comes in from balancer already deleted*/ { // Make a note that this copies Java behavior on swapping on exiting ready and also forwarding current updates to Client Conn even if there is pending lb present
		// specfic case that the current lb exits ready, and there is a pending lb, can forward it up to ClientConn
		if state.ConnectivityState != connectivity.Ready && gsb.balancerPending != nil {
			gsb.swap()
		} else {
			// Java forwards the current balancer's update to the Client Conn
			// even if there is a pending balancer waiting to be gracefully
			// switched to, whereas c-core ignores updates from the current
			// balancer. I agree with the Java more, as the current LB is still
			// being used by RPC's until the pending balancer gets gracefully
			// switched to, and thus should use the most updated form of the
			// current balancer (UpdateClientConnState seems to subscribe to
			// this philosophy too - maybe make it consistent?)
			gsb.cc.UpdateState(state)
		}
		// else just send up the update to client conn

	} // Java has an else branch on switches over to pending (similar to timer) on the current lb policy exiting READY - do we even need this? I think we should support this because Doug views Java as the reference

	// "If the channel is currently in a state other than READY, the new policy will be swapped to place immediately".
	// How does channel communicate what state its in? This is set by the balancer...so UpdateState()? Is this already handled?

	// On an update from either current or pending, you can update the client
	// conn to use new state and new picker - I don't think there's a codepath
	// where you don't send this up to the ClientConn. However, the question is what connectivity State/Picker
	// you send the ClientConn, as there are two balancers that these two pieces of data can come from
	// b.cc.UpdateState(state) // Gets here after interception, so directly calls the ClientConn for intercepted methods just not direct, sends picker up as well THIS DOESN'T SEEM RIGHT, AS IT'S ALREADU BEEN SWAPPED ON ALL CODEPATHS
}

// I LIKE C MORE...THE CURRENT ONE IS OUTDATED SINCE UPDATE REMOVES IT IN THE
// FIRST PLACE ACTUALLY...I LIKE THE JAVA IDEA OF SENDING UPDATES FOR THE
// CURRENT IF PENDING ISN'T READY EVEN FOR GRACEFUL SWITCH.

// Method for sending pending state to ClientConn?
// Also switching pending -> current
// Swap swaps out the current lb with the pending LB and updates the ClientConn
func (gsb *gracefulSwitchBalancer) swap() {
	// update Client Conn with persisted picker - if you choose to go down the current READY state switch to new
	// Either comes from previous or is the current update
	gsb.cc.UpdateState(gsb.pendingState)

	gsb.balancerCurrent.Close()
	for sc, bal := range gsb.scToSubBalancer {
		if bal == gsb.balancerCurrent {
			gsb.cc.RemoveSubConn(sc)
			delete(gsb.scToSubBalancer, sc)
		}
	}

	gsb.balancerCurrent = gsb.balancerPending
	gsb.balancerPending = nil
}

// Receiver of a method to be pointer not a value, interface is a pointer, interfaces slices map (can nil them, so already a pointer on them), lots of things are pointers, slices are pointers
func (gsb *gracefulSwitchBalancer) newSubConn(bal balancer.Balancer, addrs []resolver.Address, opts balancer.NewSubConnOptions) (balancer.SubConn, error) {
	// If run() goroutine (can talk about this with Doug in 1:1) than can check if bal is even in the graceful switch load balancer...
	sc, err := gsb.cc.NewSubConn(addrs, opts)
	if err != nil {
		return nil, err
	}
	gsb.scToSubBalancer[sc] = bal
	return sc, nil
}

// This will be used by cluster manager instead of subbalancer wrapper


// balancer.Balancer methods
// ClientConn (->) Balancer -> Subbalancer
func (gsb *gracefulSwitchBalancer) UpdateClientConnState(state balancer.ClientConnState) error {

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

	// c-core - strcmp the type of config passed in to the persisted config (the most recent one) is this logically equivalent in go? just the !=?
	buildPolicy := gsb.balancerCurrent == nil || gsb.recentConfig.ChildBalancerType != lbCfg.ChildBalancerType /*Some way of comparing the LB policy type of this specific update to the most recent config sent - persist the config, along with the type of config (most recent config received, just store the whole thing which will have type)*/
	gsb.recentConfig = lbCfg
	var balToUpdate balancer.Balancer
	if buildPolicy {
		// Build the LB
		builder := balancer.Get(lbCfg.ChildBalancerType) // ChildBalancerType but balancer.Get() will be called on certain conditions anyway...but I think you can just fill this in
		if builder == nil {
			return fmt.Errorf("balancer of type %v not supported", lbCfg.ChildBalancerType)
		}
		if gsb.balancerCurrent == nil { // This moved to above builder.Build unlike C-Core, will this break anything?
			balToUpdate = gsb.balancerCurrent
		} else {
			balToUpdate = gsb.balancerPending
		}
		bal := builder.Build(&clientConnWrapper{
			ClientConn: gsb.cc,
			gsb:        gsb,
			bal:        balToUpdate,
		}, gsb.bOpts) // Make sure this is accurate - are build options per both?
		// Set either current or pending
		balToUpdate = bal
		// Send update to that LB
	} else {
		// balToUpdate is either current or pending...
		if gsb.balancerPending != nil {
			balToUpdate = gsb.balancerPending
		} else {
			balToUpdate = gsb.balancerCurrent
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
	// both balancers. However, in balancer group, it only forwards the resolver
	// state to the child balancer that the update is relevant for.

	// return nil?
}

func (gsb *gracefulSwitchBalancer) ResolverError(err error) {
	// Mutex Lock?
	if gsb.balancerCurrent != nil {
		gsb.balancerCurrent.ResolverError(err)
	}
	if gsb.balancerPending != nil {
		gsb.balancerPending.ResolverError(err)
	}
	// Mutex Unlock?
}

// Intertwined with BalancerGroup - either reuse balancer group or plumb this in somehow

func (gsb *gracefulSwitchBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	bal, ok := gsb.scToSubBalancer[sc] // protect this with a mutex
	if !ok {
		return
	}
	if state.ConnectivityState == connectivity.Shutdown {
		delete(gsb.scToSubBalancer, sc)
	}

	bal.UpdateSubConnState(sc, state) // Can this eventually be fragmented if not protected with Mutex?
}

func (gsb *gracefulSwitchBalancer) Close() {

	// Either one or two mutex grabs...either have one protecting all or protecting inflow and outflow

	for sc := range gsb.scToSubBalancer {
		gsb.cc.RemoveSubConn(sc)
		delete(gsb.scToSubBalancer, sc)
		// This is it in BalancerGroup, is there anything else I need to do here?
	}

	// Outgoing flow: Stop/delete both balancers if applicable (i.e. not nil)...
	// Mutex grab if needed - also question of incoming/outgoing started
	if gsb.balancerCurrent != nil {
		gsb.balancerCurrent.Close()
		gsb.balancerCurrent = nil
	}
	if gsb.balancerPending != nil {
		gsb.balancerPending.Close()
		gsb.balancerPending = nil
	}
}

// removeSubconn() Menghan mentions you don't really need this, as only delete from map when changed state to connectivity.Shutdown

// "If the old policy is not READY at the moment" - swap to the new one what is this from?

// Java comment: "A load balancer that gracefully swaps to a new lb policy. If the channel is in a state other than ready, the new policy will be swapped into place immediately.
// Otherwise, the channel will keep using the old policy until the new policy reports READY or the old policy exits READY" <- I'm pretty sure what we have in UpdateState covers this sentence.



// How does a LB policy get to ready...is it this long chain of balancers that need to be configured
// properly and then send up READY all the way up the stack of balancers...






// Things I need to do (I don't know if needed in order):

// 1. Figure out which methods of balancer.Balancer and
// balancer.ClientConn to implement...and which to leave to the embedded
// interface (also figure out what happens to missing methods) (this also will apply to the embedded LB Config)

// Client Conn wrapper...balancer.Balancer?

// What needs to be embedded...clientConnWrapper should def embed
// balancer.ClientConn and receive the Client Conn passed into the graceful
// switch balancers Build method. But what about graceful switch itself? What
// interface should this struct embed, if any? I think I answered this



// 2. Finish cleaning up UpdateClientConnState(), also implement the logic of
// persisting the recent config and using that to determine whether there is
// delta in type for a new update) also is c core right? It's Mark though and he
// explained..."which balancers to forward updates to." **This when I get back

// Done outside of cleanup


// 3. UpdateState()...figure out big challenge of how to figure out which subbalancer
// it's sent from...do we need to keep it consistent with Java...looks like it does from
// what Doug said. There's a lot of code to still write here. This sets the connectivity state
// of the ClientConn.

// Done outside that big problem to solve - I think I solved it


// 4. Intercept Subconn Call and build out map which is used to know which balancer to forward updates to.

// I built out map...now I need to delete from this map? I think done




// 5. Protect certain state shared across multiple methods with mutexes...one or two (maybe do this at the end)

// Do we need one or two mutexes? The reason Menghan had two was deadlock
// prevention...i.e. an update call downward could cause an inline Update Call
// from lower level (no run goroutine to sync). Thus, if this balancer doesn't
// ever have an update call from a lower level, from an update call from Client
// Conn, this balancer just needs one. (aka same flow (calls back inline))

// Also, there is a question on whether we need outgoingStarted and incomingStarted.



// 6. Close() and what to do for that...also adding close certain subconns to UpdateState()

// Done I think outside of adding a mutex lock


// 7. ParseConfig() and the question of LoadBalancingConfig()...raw JSON gets spit out from that so you can just send that, returns IsLoadBalancingConfig()
// This gets called before UpdateClientConnState()...so that will receive the parsed type: parsedConfig (IsLoadBalancingConfig())

// * Done outside of rawJSON problem Done


// 8. Clean this tf up


// ClientConn wrapper

// Can it not just pass itself down (gracefulSwitchBalancer), and wrap
// updateClientConnState...no, you wouldn't be able to figure out what child
// balancer it came from. How does subbalancer wrapper do it? It just has one
// balancer.

type clientConnWrapper struct {
	balancer.ClientConn

	gsb *gracefulSwitchBalancer

	bal balancer.Balancer // If synced with a run goroutine...this can possibly not be any
}

func (ccw *clientConnWrapper) UpdateState(state balancer.State) {
	ccw.gsb.updateState(ccw.bal, state)
}

func (ccw *clientConnWrapper) NewSubConn(addrs []resolver.Address, opts balancer.NewSubConnOptions) (balancer.SubConn, error) {
	return ccw.gsb.newSubConn(ccw.bal, addrs, opts)
}

// The rest of these methods (balancer.ClientConn) come from the embedded balancer.ClientConn.

// Do I need to overwrite RemoveSubConn? - Menghan mentions you don't really need this, as only delete from map when changed state to connectivity.Shutdown
// This will need to forward up to a unexported function removeSubConn on graceful switch balancer



// Mutex locks (1, 2 or 3) and then cleanup...and I'm sure I'll find more problems once I cleanup

// What about grabbing a mutex for each function, will that cause deadlock?

// Cleanup (and save) then ask for help (push this to Github and do this in 1:1...run is looking better and better now)?