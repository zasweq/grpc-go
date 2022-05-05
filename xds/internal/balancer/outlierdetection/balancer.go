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

// Package outlierdetection implements a balancer that implements
// Outlier Detection.
package outlierdetection

import (
	"encoding/json"
	"fmt"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/grpcsync"
	"math"
	"math/rand"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

// Name is the name of the outlier detection balancer.
const Name = "outlier_detection_experimental"

func init() {
	balancer.Register(bb{})
}

type bb struct{}

func (bb) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	am := resolver.NewAddressMap()
	// go b.run() <- way of synchronizing
	return &outlierDetectionBalancer{
		cc: cc,
		bOpts: bOpts,
		closed: grpcsync.NewEvent(),
		done: grpcsync.NewEvent(),

		odAddrs: am,
		scWrappers: make(map[balancer.SubConn]*subConnWrapper),
	}
}

func (bb) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	var lbCfg *LBConfig
	if err := json.Unmarshal(s, &lbCfg); err != nil {
		return nil, fmt.Errorf("xds: unable to unmarshal LBconfig: %s, error: %v", string(s), err)
	}

	// Note: in the xds flow, these validations will never fail. The xdsclient
	// performs the same validations as here on the xds Outlier Detection
	// resource before parsing into the internal struct which gets marshaled
	// into JSON before calling this function. A50 defines two separate places
	// for these validations to take place, the xdsclient and this ParseConfig
	// method. "When parsing a config from JSON, if any of these requirements is
	// violated, that should be treated as a parsing error." - A50

	// "The google.protobuf.Duration fields interval, base_ejection_time, and
	// max_ejection_time must obey the restrictions in the
	// google.protobuf.Duration documentation and they must have non-negative
	// values." - A50

	// Approximately 290 years is the maximum time that time.Duration (int64)
	// can represent. The restrictions on the protobuf.Duration field are to be
	// within +-10000 years. Thus, just check for negative values.
	if lbCfg.Interval < 0 {
		return nil, fmt.Errorf("LBConfig.Interval = %v; must be >= 0", lbCfg.Interval)
	}
	if lbCfg.BaseEjectionTime < 0 {
		return nil, fmt.Errorf("LBConfig.BaseEjectionTime = %v; must be >= 0", lbCfg.BaseEjectionTime)
	}
	if lbCfg.MaxEjectionTime < 0 {
		return nil, fmt.Errorf("LBConfig.MaxEjectionTime = %v; must be >= 0", lbCfg.MaxEjectionTime)
	}

	// "The fields max_ejection_percent,
	// success_rate_ejection.enforcement_percentage,
	// failure_percentage_ejection.threshold, and
	// failure_percentage.enforcement_percentage must have values less than or
	// equal to 100." - A50
	if lbCfg.MaxEjectionPercent > 100 {
		return nil, fmt.Errorf("LBConfig.MaxEjectionPercent = %v; must be <= 100", lbCfg.MaxEjectionPercent)
	}
	if lbCfg.SuccessRateEjection != nil && lbCfg.SuccessRateEjection.EnforcementPercentage > 100 {
		return nil, fmt.Errorf("LBConfig.SuccessRateEjection.EnforcementPercentage = %v; must be <= 100", lbCfg.SuccessRateEjection.EnforcementPercentage)
	}
	if lbCfg.FailurePercentageEjection != nil && lbCfg.FailurePercentageEjection.Threshold > 100 {
		return nil, fmt.Errorf("LBConfig.FailurePercentageEjection.Threshold = %v; must be <= 100", lbCfg.FailurePercentageEjection.Threshold)
	}
	if lbCfg.FailurePercentageEjection != nil && lbCfg.FailurePercentageEjection.EnforcementPercentage > 100 {
		return nil, fmt.Errorf("LBConfig.FailurePercentageEjection.EnforcementPercentage = %v; must be <= 100", lbCfg.FailurePercentageEjection.EnforcementPercentage)
	}
	return lbCfg, nil
}

func (bb) Name() string {
	return Name
}

type outlierDetectionBalancer struct {
	// TODO: The fact that this is an address string + attributes needs to be documented in the gRFC?
	odAddrs *resolver.AddressMap

	numAddrsEjected int // For fast calculations of percentage of addrs ejected

	odCfg *LBConfig

	cc balancer.ClientConn // Priority parent right?

	bOpts balancer.BuildOptions

	child balancer.Balancer // cluster impl - when is this built? See others for example, is it in Build()? This gets built in different places in cluster impl, so, it has nil checks on forwawrding it
	// clusterimpl config as part of the OD config, can be different enough to warrant a creation of this child?

	timerStartTime time.Time // The outlier_detection LB policy will store the timestamp of the most recent timer start time. Also used for ejecting addresses per iteration

	intervalTimer *time.Timer


	// map sc (all parent knows about) -> scw (all children know about, this balancer wraps scs in scw)...explain and talk about why you need this map
	// clusterimpl also has a mutex protecting this
	scWrappers map[balancer.SubConn]*subConnWrapper

	// I plan to sync all operations with a run() goroutine - so no need for mutexes? Wrong,
	// sometimes interspliced between reading in non run() and operations synced in run() **
	closed *grpcsync.Event // Fires off when close hits
	done *grpcsync.Event // Waits until the resources are cleaned up? fires once run() goroutine returns (implicitly cleaning things up), maybe
	// represents run() goroutine as a resource being cleaned up?
	// see one more example for these two events
}

// noopConfig returns whether this balancer is configured with a logical no-op
// configuration or not.
func (b *outlierDetectionBalancer) noopConfig() bool {
	return b.odCfg.SuccessRateEjection == nil && b.odCfg.FailurePercentageEjection == nil
}

func (b *outlierDetectionBalancer) UpdateClientConnState(s balancer.ClientConnState) error {
	// Close guard
	// if b.closed

	lbCfg, ok := s.BalancerConfig.(*LBConfig)
	if !ok {
		// b.logger.Warningf("xds: unexpected LoadBalancingConfig type: %T", s.BalancerConfig)
		return balancer.ErrBadResolverState
	}

	// Reject whole config if errors, don't persist it for later

	bb := balancer.Get(lbCfg.ChildPolicy.Name)
	if bb == nil {
		return fmt.Errorf("balancer %q not registered", lbCfg.ChildPolicy.Name)
	}


	// Perhaps move to a handle function - after coding/putting in run() goroutine

	// b.odCfg.ChildPolicy.

	// The child gets updated at the end of this function, thus you need to
	// build the child and return early if the child doesn't build

	// When do you want to build the whole balancer...if it's nil right
	if b.child == nil { // Want to hit the first UpdateClientConnState...could also use config being nil like clusterimpl, although I like this idea more
		// What if this is nil? clusterimpl has an explicit check for this
		b.child = bb.Build(b /*Again, do we want to make this a functioning ClientConn or just embed?*/, b.bOpts)
	}
	b.odCfg = lbCfg


	// When the outlier_detection LB policy receives an address update, it will
	// create a map entry for each subchannel address in the list, and remove
	// each map entry for a subchannel address not in the list.
	addrs := make(map[resolver.Address]bool)
	for _, addr := range s.ResolverState.Addresses {
		addrs[addr] = true
		b.odAddrs.Set(addr, &object{}) // Do we need to initialize any part of this or are zero values sufficient? make([]sws)?
	}
	for _, addr := range b.odAddrs.Keys() {
		if !addrs[addr] {
			b.odAddrs.Delete(addr)
		}
	}

	// When a new config is provided, if the timer start timestamp is unset, set
	// it to the current time and start the timer for the configured interval,
	// then for each address, reset the call counters.
	var interval time.Duration
	if b.timerStartTime.IsZero() {
		b.timerStartTime = time.Now()
		for _, obj := range b.objects() {
			obj.callCounter.activeBucket = bucket{}
			obj.callCounter.inactiveBucket = bucket{}
		}
		interval = b.odCfg.Interval
	} else {
		// If the timer start timestamp is set, instead cancel the existing
		// timer and start the timer for the configured interval minus the
		// difference between the current time and the previous start timestamp,
		// or 0 if that would be negative.
		interval = b.odCfg.Interval - (time.Now().Sub(b.timerStartTime))
		if interval < 0 {
			interval = 0
		}
	}

	if !b.noopConfig() {
		b.intervalTimer = time.NewTimer(interval)
	} else {
		// "If a config is provided with both the `success_rate_ejection` and
		// `failure_percentage_ejection` fields unset, skip starting the timer and
		// unset the timer start timestamp."
		b.timerStartTime = time.Time{}
		// Do we need to clear the timer as well? Or when it fires it will be
		// logical no-op (from no-op config) and not count... How else is the
		// no-op config plumbed through system? Does this gate at beginning of
		// interval timer?
	}

	// then pass the address list along to the child policy.
	return b.child.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: s.ResolverState,
		BalancerConfig: b.odCfg.ChildPolicy.Config,
	})
}

func (b *outlierDetectionBalancer) ResolverError(err error) {
	// This operation isn't defined in the gRFC. Pass through? If so, I don't think you need to sync.
	// If not pass through, and it actually does something you'd need to sync. (See other balancers)

	// See other balancers...sometimes they combine into one ClientConn update struct which gets synced in run()
	// Or just pass through...see other balancers, seems like an operation that needs to be part of synced

	// What is the desired behavior of this balancer when it receives a resolver error?
	// Does it propogate all the way down the balancer tree? When does it get acted on? What happens in the other xds balancers?

	// Graceful switch simply forwards the error downward (to either current or pending)

	// What happens to all the state when you receive an error? (Again, see other xds balancers), what does a resolver error signify logically? Are we clearing out this balancer's state - ask team?
	// Clearing out map + lb config etc. until you get a new Client Conn update with a good config? Is this the state machine?


	// Closed check in both cluster resolver and clusterimpl

	// if childLB != nil
	if b.child != nil { // implicit close guard...does this need a mutex protecting this read?
		b.child.ResolverError(err)
	}
	//    forward
}

func (b *outlierDetectionBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	// This gets called from the parent, which has no knowledge of scw (gets
	// called NewSubConn(), that returned sc gets put into a scw in this
	// balancer), thus need to convert sc to scw. This can either be done
	// through typecasting (I don't think would work) or the map thing. Cluster
	// Impl persists a map that maps sc -> scw...see how it's done in other
	// parts of codebase.

	scw, ok := b.scWrappers[sc]
	if !ok {
		// Return, shouldn't happen if passed up scw
		// Because you wrap subconn always, clusterimpl i think only some
		return
	}
	if state.ConnectivityState == connectivity.Shutdown {
		// Remove this SubConn from the map on Shutdown.
		delete(b.scWrappers, scw.SubConn)
	}
	scw.latestState = state
	if !scw.ejected && b.child != nil { // eject() - "stop passing along updates from the underlying subchannel."
		b.child.UpdateSubConnState(scw, state)
	}
}

func (b *outlierDetectionBalancer) Close() {
	// This operation isn't defined in the gRFC. Def requires synchronization points,
	// stuff in the balancer shouldn't happen after Close().

	// Cleanup stage - go specific cleanup, clean up goroutines and memory? See
	// other balancers on how they implement close() and what they put in
	// close(). Ping run() <- right now has child.Close() i.e. pass through of the operation?
}

func (b *outlierDetectionBalancer) ExitIdle() {
	// This operation isn't defined in the gRFC. Pass through? If so, I don't think you need to sync.
	// If not pass through, and it actually does something you'd need to sync. (See other balancers)

	// Sync required unless child balancer is only built at build time, then
	// it's a guarantee the rest of the time to simply read it and forward down
	// no need for mutexes or run()...but see other balancers to confirm.

	// Exit Idle - go specific logic, see other balancers, I'm pretty sure
	// this simply forwards this call down the balancer hierarchy, typecast, etc.

	// Cluster Impl -
	/*
	if b.childLB == nil {
			return
		}
		if ei, ok := b.childLB.(balancer.ExitIdler); ok {
			ei.ExitIdle()
			return
		}
		// Fallback for children that don't support ExitIdle -- connect to all
		// SubConns.
		for _, sc := range b.scWrappers { // Do we now need this in od since top level?
			sc.Connect()
		}
	*/
	if b.child == nil {
		return
	}
	if ei, ok := b.child.(balancer.ExitIdler); ok {
		ei.ExitIdle()
		return
	}
	// Do we want this fallback for children that don't support ExitIdle - connect to all SubConns.
	// This is replacing clusterimpl as top level, but if functionality is already coded in clusterimpl
	// you can just delegate will hit up in this call (ei.ExitIdle()) ^^^
}


// The outlier_detection LB policy will provide a picker that delegates to
// the child policy's picker, and when the request finishes, increment the
// corresponding counter in the map entry referenced by the subchannel
// wrapper that was picked.
type wrappedPicker struct {
	// childPicker, embedded or wrapped, I think wrapped is right, maybe see how other parts of codebase do it? I'm pretty sure I based it off other parts of codebase
	childPicker balancer.Picker
	noopPicker bool
}

func (wp *wrappedPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	// How does the picker work logically?

	pr, err := wp.childPicker.Pick(info)
	if err != nil {
		return balancer.PickResult{}, err
	}

	done := func(di balancer.DoneInfo) {
		if !wp.noopPicker {
			incrementCounter(pr.SubConn, di)
		}
		pr.Done(di)
	}
	return balancer.PickResult{
		SubConn: pr.SubConn,
		Done: done,
	}, nil
}

func incrementCounter(sc balancer.SubConn, info balancer.DoneInfo) {
	scw, ok := sc.(subConnWrapper)
	if !ok {
		// Shouldn't happen, as comes from child
		return
	}
	if info.Err != nil {
		// Is there anything else that is required to make this a successful RPC?
		scw.obj.callCounter.activeBucket.numSuccesses++ // is this the thing that needs to protected by the mutex?
	} else {
		scw.obj.callCounter.activeBucket.numFailures++
	}
}


func (b *outlierDetectionBalancer) UpdateState(s balancer.State) { // Can this race with anything???? esp that b.noopConfig()
	b.cc.UpdateState(balancer.State{
		ConnectivityState: s.ConnectivityState,
		// The outlier_detection LB policy will provide a picker that delegates to
		// the child policy's picker, and when the request finishes, increment the
		// corresponding counter in the map entry referenced by the subchannel
		// wrapper that was picked.
		Picker: &wrappedPicker{
			childPicker: s.Picker,
			// If both the `success_rate_ejection` and
			// `failure_percentage_ejection` fields are unset in the
			// configuration, the picker should not do that counting.
			noopPicker: b.noopConfig(),
		},
	})

	// What to do with connectivity state? Simply forward, or does it affect
	// anything here? Similar question to ResolverError coming in and affecting
	// balancer state. Nothing explicitly defined in gRFC, but there can
	// probably be go specific logic.

	// related to algorithms: is no-op logically treated same as any config with
	// both unset, if both are unset other parts of config can be set that
	// aren't maxing interval?
}

func (b *outlierDetectionBalancer) NewSubConn(addrs []resolver.Address, opts balancer.NewSubConnOptions) (balancer.SubConn, error) { // Intercepts call

	// Now that there's UpdateAddresses, each SubConn needs to be wrapped. But
	// even though you wrap each SubConn, you still logically ignore the SubConn
	// for Outlier Detection (i.e. don't add to Outlier Detection map scw lists)

	// When the child policy asks for a subchannel, the outlier_detection will
	// wrap the subchannel with a wrapper.
	sc, err := b.cc.NewSubConn(addrs, opts)
	// Wait, the SubConn passed downward from parent policy is a sc constructed
	// in the parent policy. Thus, it has no knowledge of this types scw. like
	// cluster impl, need a map from sc -> scw (how is this keyed?)

	// Reword all three of these comments ^^^.

	if err != nil {
		return nil, err
	}
	scw := &subConnWrapper{
		SubConn: sc,
		addresses: addrs,
	}
	b.scWrappers[sc] = scw
	if len(addrs) != 1 {
		return scw, nil
	}

	val, ok := b.odAddrs.Get(addrs[0])
	if !ok {
		return scw, nil
	}

	obj, ok := val.(*object)
	obj.sws = append(obj.sws, scw)

	// If that address is currently ejected, that subchannel wrapper's eject
	// method will be called.
	if !obj.latestEjectionTimestamp.IsZero() {
		scw.eject()
	}
	return scw, nil
}

func (b *outlierDetectionBalancer) RemoveSubConn(sc balancer.SubConn) {
	scw, ok := sc.(*subConnWrapper)
	if !ok { // Shouldn't happen
		return
	}
	// Remove the wrpaped SubConn from the parent Client Conn. We don't remove
	// from map entry until we get a Shutdown state for the SubConn, as we need
	// that data to forward that state down.
	b.cc.RemoveSubConn(scw.SubConn)
}

// addMapEntryIfNeeded adds a map entry for an address if required, and returns
// the corresponding map entry (object) associated with that address.
func (b *outlierDetectionBalancer) addMapEntryIfNeeded(addr resolver.Address) *object { // Have these be methods on the map type?
	val, ok := b.odAddrs.Get(addr)
	var obj *object
	if !ok {
		// Add map entry for that Address if applicable (i.e. if not already
		// there).
		obj = &object{}
		b.odAddrs.Set(addr, obj)
	} else {
		obj , ok = val.(*object)
		if !ok {
			// shouldn't happen, logical no-op
			obj = &object{} // to protect against nil panic
		}
	}
	return obj
}

// 2a. Remove Subchannel from Addresses map entry (if singular address changed).
// 2b. Remove the map entry if only subchannel for that address
func (b *outlierDetectionBalancer) removeSubConnFromAddressesMapEntry(scw *subConnWrapper) {
	for i, sw := range scw.obj.sws {
		if scw == sw {
			scw.obj.sws = append(scw.obj.sws[:i], scw.obj.sws[i+1:]...)
			if len(scw.obj.sws) == 0 {
				// []resolver.Address just automatically use [0], invariant of the caller
				b.odAddrs.Delete(scw.addresses[0]) // invariant that used to be single (top level if), guarantee this never becomes nil
				scw.obj = nil // does this cause any negative downstream effects?
			}
			break
		}
	}
}

// sameAddrForMap returns if two addresses are the same in regards to subchannel
// uniqueness/identity (i.e. what the addresses map is keyed on - address
// string, Server Name, and Attributes).
func sameAddrForMap(oldAddr resolver.Address, newAddr resolver.Address) bool {
	if oldAddr.Addr != newAddr.Addr {
		return false
	}
	if oldAddr.ServerName != newAddr.ServerName {
		return false
	}
	return oldAddr.Attributes.Equal(newAddr.Attributes)
}

func (b *outlierDetectionBalancer) UpdateAddresses(sc balancer.SubConn, addrs []resolver.Address) {
	scw, ok := sc.(*subConnWrapper) // Seems like these two are only places this typecast can happen, any others?
	if !ok {
		// Return, shouldn't happen if passed up scw
		return
	}

	// Note that 0 addresses is a valid update/state for a SubConn to be in.
	// This is correctly handled by this algorithm (handled as part of a non singular
	// old address/new address).
	if len(scw.addresses) == 1 {
		if len(addrs) == 1 { // single address to single address
			// If everything we care for in regards to address specificity for a
			// list of SubConn's (Addr, ServerName, Attributes) is the same,
			// then there is nothing to do (or do we still need to forward
			// update).
			if sameAddrForMap(scw.addresses[0], addrs[0]) {
				return
			}



			// 1a. Forward the update to the Client Conn.
			b.cc.UpdateAddresses(sc, addrs)

			// 1b. Update (create/delete map entries) the map of addresses if applicable.
			// There's a lot to this ^^^, draw out each step and what goes on in each step, check it for correctness and try to break

			// Remove Subchannel from Addresses map entry
			// Remove the map entry if only subchannel for that address
			b.removeSubConnFromAddressesMapEntry(scw)
			obj := b.addMapEntryIfNeeded(addrs[0])

			// 1c. Relay state with eject() recalculated
			if obj.latestEjectionTimestamp.IsZero() {
				scw.eject() // will send down correct SubConnState and set bool
			} else {
				scw.uneject() // will send down correct SubConnState and set bool - the one where it can race with the latest state from client conn vs. persisted state, but this race is ok
			}
			scw.obj = obj
			obj.sws = append(obj.sws, scw)
		} else { // single address to multiple addresses
			// 2a. Remove Subchannel from Addresses map entry.
			// 2b. Remove the map entry if only subchannel for that address
			b.removeSubConnFromAddressesMapEntry(scw)

			// 2c. Clear the Subchannel wrapper's Call Counter entry - this might not be tied to an Address you want to count, as you potentially
			// delete the whole map entry - wait, but only a single SubConn left this address list, are we sure we want to clear in this situation?
			if scw.obj != nil {
				scw.obj.callCounter.activeBucket = bucket{}
				scw.obj.callCounter.inactiveBucket = bucket{}
			}
		}
	} else {
		// Wait, but what if the multiple goes from 0 -> something
		// This can be start with 0 addresses if it hits this else block...***document this, how 0 addresses are supported and len(0)
		// I think this should be fine if it starts out with zero, correctly adds the singular address no harm done no difference
		// from switching from multiple.
		if len(addrs) == 1 { // multiple addresses to single address
			// 3a. Add map entry for that Address if applicable (i.e. if not already there)
			obj := b.addMapEntryIfNeeded(addrs[0])

			// 3b. Add Subchannel to Addresses map entry.
			scw.obj = obj
			obj.sws = append(obj.sws, scw)
		} // else is multiple to multiple - no op, continued to be ignored by outlier detection load balancer.
	}

	scw.addresses = addrs
	b.cc.UpdateAddresses(scw.SubConn, addrs)
}

// ResolveNow() - cluster impl balancer doesn't have it because it embeds a ClientConn, do we want that or just declared?

// Target() - cluster impl balancer doesn't have it because it embeds ClientConn, do we want that or just pass through?

// These two get called from SubConn, so don't need to forward down to child balancer


func max(x, y int64) int64 {
	if x < y {
		return y
	}
	return x
}

func min(x, y int64) int64 {
	if x < y {
		return x
	}
	return y
}

// objects returns a list of objects corresponding to every address in the address map.
func (b *outlierDetectionBalancer) objects() []*object {
	var objs []*object
	for _, addr := range b.odAddrs.Keys() {
		val, ok := b.odAddrs.Get(addr)
		if !ok { // Shouldn't happen
			continue
		}
		// shouldn't error - everywhere you set in the map is of type object
		obj, ok := val.(*object)
		if !ok {
			continue
		}
		objs = append(objs, obj)
	}
	return objs
}

func (b *outlierDetectionBalancer) run() {
	// ** Triage other xds balancers to see what operations they sync
	for {
		select {
		// Need to fill out the other operations...are they pass through (i.e. Resolver Error and stuff) or do they read here?
		// Each operation both ways?...what type of channel do they read from? You can combine multiple onto a single channel, have a channel for each one etc.

		// UpdateClientConnState is def one....the whole of the od algorithm reads from the config this writes...
		// What amount of statements executed before it gets to here?

		// ResolverError?
		// UpdateSubConnState?
		// Close (like graceful switch, this is an event that can be synced in many different ways)
			// cleanup any resources, close/nil child, return from this goroutine (cleans this one up)
			if b.child != nil {
				b.child.Close()
				b.child = nil
			}

		// NewSubConn (yes for sure, adds to map - or protect map with mutex like cluster impl)
		// RemoveSubConn (removes from map, no, this is determined by address list, I think just removes scw from address list, still reading it so sync it here. Need to add functionality for this)
		// UpdateAddresses (algorithm I proposed, uses map so yes needs to be synced)
		// UpdateState (yes, reads sc...I think so?)
		// ResolveNow?
		// Target?

		// Should I finish all of these operations before figuring any of this out? I.e. ordering of operations

		case <-b.intervalTimer.C:
			// Outlier Detection algo here. Quite large, so maybe make a helper function - "logic can either be inline or in a handle function"
		}
	}

	// Grounded blob: look over/cleanup/make sure this algorithm is correct.
	// Interval trigger:
	// When the timer fires, set the timer start timestamp to the current time. Record the timestamp for use when ejecting addresses in this iteration. "timestamp that was recorded when the timer fired"
	// these are logically the same thing
	b.timerStartTime = time.Now()

	// 2. For each address, swap the call counter's buckets in that address's map entry.
	for _, obj := range b.objects() {
		obj.callCounter.swap()
	}

	// 3. If the success_rate_ejection configuration field is set, run the success rate algorithm.
	if b.odCfg.SuccessRateEjection != nil {
		b.successRateAlgorithm()
	}

	// 4. If the failure_percentage_ejection configuration field is set, run the failure percentage algorithm.
	if b.odCfg.FailurePercentageEjection != nil {
		b.failurePercentageAlgorithm()
	}

	// 5. For each address in the map:
	for _, addr := range b.odAddrs.Keys() {
		val, ok := b.odAddrs.Get(addr)
		if !ok {
			continue
		}
		obj, ok := val.(*object)
		if !ok {
			continue
		}
		// If the address is not ejected and the multiplier is greater than 0, decrease the multiplier by 1.
		if obj.latestEjectionTimestamp.IsZero() && obj.ejectionTimeMultiplier > 0 {
			obj.ejectionTimeMultiplier--
			continue
		}
		// If the address is ejected, and the current time is after
		// ejection_timestamp + min(base_ejection_time (type: time.Time) *
		// multiplier (type: int), max(base_ejection_time (type: time.Time),
		// max_ejection_time (type: time.Time))), un-eject the address.
		if time.Now().After(obj.latestEjectionTimestamp.Add(time.Duration(min(b.odCfg.BaseEjectionTime.Nanoseconds() * obj.ejectionTimeMultiplier, max(b.odCfg.BaseEjectionTime.Nanoseconds(), b.odCfg.MaxEjectionTime.Nanoseconds()))))) {
			b.unejectAddress(addr)
		}

		b.odCfg.BaseEjectionTime.Nanoseconds()
	}
}

// numAddrsWithAtLeastRequestVolume returns the number of addresses present in the map
// that have request volume of at least requestVolume.
func (b *outlierDetectionBalancer) numAddrsWithAtLeastRequestVolume() uint32 {
	var numAddrs uint32
	for _, obj := range b.objects() {
		if uint32(obj.callCounter.inactiveBucket.requestVolume) >= b.odCfg.SuccessRateEjection.RequestVolume {
			numAddrs++
		}
	}
	return numAddrs
}

// meanAndStdDevOfSucceseesAtLeastRequestVolume returns the mean and std dev of the number of requests
// of addresses that have at least requestVolume.
func (b *outlierDetectionBalancer) meanAndStdDevOfSuccessesAtLeastRequestVolume() (float64, float64) {
	// 2. Calculate the mean and standard deviation of the fractions of
	// successful requests among addresses with total request volume of at least
	// success_rate_ejection.request_volume.
	var totalFractionOfSuccessfulRequests float64
	var mean float64

	for _, obj := range b.objects() {
		// "of at least success_rate_ejection.request_volume"
		if uint32(obj.callCounter.inactiveBucket.requestVolume) >= b.odCfg.SuccessRateEjection.RequestVolume {
			totalFractionOfSuccessfulRequests += float64(obj.callCounter.inactiveBucket.numSuccesses)/float64(obj.callCounter.inactiveBucket.requestVolume)
		}
	}
	mean = totalFractionOfSuccessfulRequests / float64(b.odAddrs.Len())


	// To calculate std dev:
	// 1. Find each scores deviation from the mean

	// 2. Square each deviation from the mean

	// 3. Find the sum of squares

	var sumOfSquares float64

	for _, obj := range b.objects() { // You've calculated the means already, could store that in list to get rid of this recalculation, but would have to reiterate through...so still linear search
		devFromMean := (float64(obj.callCounter.inactiveBucket.numSuccesses) / float64(obj.callCounter.inactiveBucket.requestVolume)) - mean
		sumOfSquares += devFromMean * devFromMean
	}

	variance := sumOfSquares / float64(b.odAddrs.Len())

	// Divide the sum of the squares by n (where n is the total population size
	// because you use every data point) The sqrt of the variance is the std
	// dev.
	return mean, math.Sqrt(variance)

}

func (b *outlierDetectionBalancer) successRateAlgorithm() {
	// 1. If the number of addresses with request volume of at least
	// success_rate_ejection.request_volume is less than
	// success_rate_ejection.minimum_hosts, stop.
	if b.numAddrsWithAtLeastRequestVolume() < b.odCfg.SuccessRateEjection.MinimumHosts { // TODO: O(n) search, is there a way to optimize this?
		return
	}

	// 2. Calculate the mean and standard deviation of the fractions of
	// successful requests among addresses with total request volume of at least
	// success_rate_ejection.request_volume.
	mean, stddev := b.meanAndStdDevOfSuccessesAtLeastRequestVolume()

	// 3. For each address:
	for _, addr := range b.odAddrs.Keys() {
		val, ok := b.odAddrs.Get(addr)
		if !ok {
			continue
		}
		obj, ok := val.(*object)
		if !ok {
			continue
		}
		ccb := obj.callCounter.inactiveBucket
		sre := b.odCfg.SuccessRateEjection
		// i. If the percentage of ejected addresses is greater than
		// max_ejection_percent, stop.
		if float64(b.numAddrsEjected) / float64(b.odAddrs.Len()) * 100 > float64(b.odCfg.MaxEjectionPercent) {
			return
		}

		// ii. If the address's total request volume is less than
		// success_rate_ejection.request_volume, continue to the next address.
		if ccb.requestVolume < int64(sre.RequestVolume) {
			continue
		}

		//  iii. If the address's success rate is less than (mean - stdev *
		//  (success_rate_ejection.stdev_factor / 1000))
		successRate := float64(ccb.numSuccesses) / float64(ccb.requestVolume)
		if successRate < (mean - stddev * (float64(sre.StdevFactor) / 1000) ) {
			// then choose a random integer in [0, 100). If that number is less
			// than success_rate_ejection.enforcement_percentage, eject that
			// address.
			if uint32(rand.Int31n(100)) < sre.EnforcementPercentage {
				b.ejectAddress(addr)
			}
		}
	}
}

func (b *outlierDetectionBalancer) failurePercentageAlgorithm() {
	// 1. If the number of addresses (len(map)) is less than
	// failure_percentage_ejection.minimum_hosts, stop.
	if uint32(b.odAddrs.Len()) < b.odCfg.FailurePercentageEjection.MinimumHosts {
		return
	}

	// 2. For each address:
	for _, addr := range b.odAddrs.Keys() {
		val, ok := b.odAddrs.Get(addr)
		if !ok {
			continue
		}
		obj, ok := val.(*object)
		if !ok {
			continue
		}
		ccb := obj.callCounter.inactiveBucket
		fpe := b.odCfg.FailurePercentageEjection
		// i. If the percentage of ejected addresses is greater than
		// max_ejection_percent, stop.
		if float64(b.numAddrsEjected) / float64(b.odAddrs.Len()) * 100 > float64(b.odCfg.MaxEjectionPercent) {
			return
		}
		// ii. If the address's total request volume is less than
		// failure_percentage_ejection.request_volume, continue to the next
		// address.
		if uint32(ccb.requestVolume) < fpe.RequestVolume {
			continue
		}
		//  2c. If the address's failure percentage is greater than
		//  failure_percentage_ejection.threshold
		failurePercentage := (float64(obj.callCounter.inactiveBucket.numFailures) / float64(obj.callCounter.inactiveBucket.requestVolume)) * 100
		if failurePercentage > float64(b.odCfg.FailurePercentageEjection.Threshold) {
			// then choose a random integer in [0, 100). If that number is less
			// than failiure_percentage_ejection.enforcement_percentage, eject
			// that address.
			if uint32(rand.Int31n(100)) < b.odCfg.FailurePercentageEjection.EnforcementPercentage {
				b.ejectAddress(addr)
			}
		}
		if uint32(failurePercentage) > b.odCfg.FailurePercentageEjection.EnforcementPercentage {
			b.ejectAddress(addr)
		}
	}
}

func (b *outlierDetectionBalancer) ejectAddress(addr resolver.Address) {
	val, ok := b.odAddrs.Get(addr)
	if !ok { // Shouldn't happen
		return
	}
	obj, ok := val.(*object)
	if !ok { // Shouldn't happen
		return
	}

	b.numAddrsEjected++

	// To eject an address, set the current ejection timestamp to the timestamp
	// that was recorded when the timer fired, increase the ejection time
	// multiplier by 1, and call eject() on each subchannel wrapper in that
	// address's subchannel wrapper list.
	obj.latestEjectionTimestamp = b.timerStartTime // this read is guaranteed to observe only the write when the timer fires because UpdateClientConnState() will be a synced event
	obj.ejectionTimeMultiplier++
	for _, sbw := range obj.sws {
		sbw.eject()
	}
}

func (b *outlierDetectionBalancer) unejectAddress(addr resolver.Address) {
	val, ok := b.odAddrs.Get(addr)
	if !ok { // Shouldn't happen
		return
	}
	obj, ok := val.(*object)
	if !ok { // Shouldn't happen
		return
	}
	b.numAddrsEjected--

	// To un-eject an address, set the current ejection timestamp to null
	// (doesn't he mean latest ejection timestamp?, in Golang null for time is
	// logically equivalent in practice to the time zero value) and call
	// uneject() on each subchannel wrapper in that address's subchannel wrapper
	// list.
	obj.latestEjectionTimestamp = time.Time{}
	for _, sbw := range obj.sws {
		sbw.uneject()
	}
}

type object struct { // Now that this is a pointer, does this break anything?*
	// The call result counter object
	callCounter callCounter

	// The latest ejection timestamp, or null if the address is currently not ejected
	latestEjectionTimestamp time.Time // We represent the branching logic on the null with a time.Zero() value

	// The current ejection time multiplier, starting at 0
	ejectionTimeMultiplier int64

	// A list of subchannel wrapper objects that correspond to this address
	sws []*subConnWrapper
}



// Today after cold brew:

// Learn logically what a subchannel is

// Implement the two objects since they are dependencies and can help you learn main implementation
// sync issues seem to be a question

// The actual balancer.go implementation


// Stop forwarding State downward to child...


// Need to document in gRFC:
// No-op logic max int being treated differently
// Update Address...needs to be documented what behavior should be
// Can keep subconn but change addresss...totally possible in go...discussed in whiteboarding meeting

// Interval timer clarification added to gRFC

// You can ask if someone has conflict

// Finish algorithm definition (fix type problems, finish the rest of unimplemented)
//
// Add sync stuff (run go routine)
//
// Operations regarding the interval timer should be fine and never called concurrently (i.e. downstream of interval timer)
// UpdateClientConnState and trigger interval need to be synced
// What about other operations? I.e. SubConns, the other operations both ways, whether dealing with a wrapped SubConn or not. (Do any operations not get synced?, Which are just pass through?)?
// How is SubConn linked to Client Conn. Is the Outlier Detection balancer a Client Conn. Yes, because intercepts UpdateState()
// Interval timer stuff (5 + 3, not wait 8)
//
// Finish sub channel wrapper (Eric’s algorithm for Update Addresses, but downward flow is set in regards to UpdateState).
//
// Tests
//
// Balancer test
// Unit tests
// E2E test
// Also cleanup






// (Do any operations not get synced?, Which are just pass through?)
// Which operations can get called concurrently?

// -> represents this outlier detection balancer...how do we sync these and any interesting things if we wrap each operation atomically
// Operations from grpc -> balancer

// UpdateClientConnState() can go ahead and start implementing this, but maybe think about operations first, dependent on logical thought
// Has both persissitng of the config (not defined in gRFC), and also the updating of the map as defined by gRFC and then forward downward

// ResolverError(error) <- simply forward down?

// ** UpdateSubConnState() <- wrapper deals with, intercepts call if ejected to persist most recent config
// sends down this once unejected, does this happen implicitly in wrapped SubConn or do you need to do something special?

// This operation, rather than being defined on the SubConn, actually seems to be interfacing with a wrapped SubConn's
// state it persists from getting called and written to

// eject/uneject changes internal state

// can also persist recent update state by writing directly to it (with a mutex protecting it)

// Two things:
// scw (two blobs of functionality) - track the latest state from the underlying subchannel,
// don't pass updates along if ejected, update SubConn with TRANSIENT FAILURE state
// once unejected pass latest state update from underlying subchannel

// not an API that the balancer talks to, but state that changes based off operations
// that happen in this balancer that also determine logic in other operations that happen in the balancer...

// why does this hold the map entry?
// the scw that was picker by picker will increase counter in map entry once picked

// vs. (when do you use either of these? i.e. ignore you could use old sc not scw...)
// sc plain




// Close()

// ExitIdle()



// Operations from balancer -> grpc

// ** NewSubConn() - Intercepted to wrap subconn

// RemoveSubConn() - pass through?

// UpdateAddresses() - complicated algorithm Eric mentioned

// ** UpdateState() -> also wraps picker, language from gRFC about picker that
// delegates to child and "increment the corresponding counter in the map entry
// referenced by the subchannel wrapper that was picked" this wraps picker in this types picker.
// The wrapping isn't dependent on SubConnWrapper logic but the picker logic def is (done func())

// ResolveNow()

// Target()



// Figure out operations...and also this whole per subchannel attributes



// Figure out linkage between a wrapped SubConn and the balancer...the SubConn API doesn't do anything.
// The SubConn is part of the balancer.Balancer API, and is an expression in some of the return/arguments.
// See example somewhere in codebase?


// Wrapped picker that increments corresponding counter in map entry <- this I don't know if it's an operation or part of an existing part of UpdateState!

// Interval timer going off and triggering eject/uneject behavior (also synced with others based on run() operation? Does that sound right?)



// I don't think any of these operations has sync issues if we put it on the run goroutine
// I should write out the logic for each operation, and then see if any weird racey things pop up

// 2. Figure out run() goroutine and syncing operations

// I try to avoid a run goroutine as much as possible
// But if I need a run(), I will probably try to move all the operations there
// Unless it's to just forward the update, without needing to sync any field

// Syncing fields require either putting operations in run() or lock unlock
// on mutex. So figure out which ones are just forward without syncing field
// - these operations don't have to be synced by putting in the run() goroutine

// Can also intermingle cleanup with 2 after you fix odAddrs





// Do another pass - cleanup/look for blobs of functionality that still need to be implemented


// List of blobs of functionality that still need to be implemented here

// sc -> scw map, parent only knows sc, children know scw
// and also all the logic this entails, see other examples in codebase

// * Cluster impl literally just has it to convert UpdateSubConnState() from parent (only knows sc)
// to child (only knows scw), do we need it for any other operation? *Triage

// **Done...


// Should scws[] be pointers to heap memory or value types...to map and how pointers work
// logically.

// A zero value struct is simply a struct variable where each key's value is set
// to their respective zero value. So different than pointer nil vs. not nil, underlying heap memory
// vs. struct copyyyyy?

// ** Switched to [] of pointers to heap memory





// The passthrough? passthrough or not operations, or even if some of them are defined or not.
// This relates to when the child balancer is built (at config receive time because that has the config
// for the child balancer)/cleared/rebuilt **See other balancers for example

// Close - clear child and a sync point where other operations can't happen after Close()

// Can merge these two VVV
// Cleanup...another pass, UpdateAddresses needs some cleaning up (kinda done)

// Pass through operations are dependent on the state of childLB (i.e. childLB != nil)
// so figure this out first in terms of the possible states of the childLB over time

// childLB starts out as nil when it's being built - as hasn't received the config yet

// Built in UpdateClientConnState using child policy config, cleared in Close() (closed as well)
// closed if type switched and call new balancer (this is the one question - do we need type change guard?)

// Assuming it can't change type over time...but it can in clusterimpl, why? seems to
// be weighted target only


// Def need if nil checks gating passing operations down, as the child LB can be nil

// What can trigger build? Only ClientConnUpdates right (no watch updates like cluster resolver) Yup



// Add check for same address for single -> single in UpdateAddresses() (Done)

// Build on client conn update, the only question is if we gate against child type switching (no)
// FINISHING BUILDING ON CLIENT CONN - Same logic as cluster impl (I think done)

// nil checks around passing operations down, FINISH CALLING OPERATIONS ON CHILD WITH NIL CHECK (Done)

// Close/clear on this balancer closing, (Done)

// Close() event synchronization, inside run goroutine, or all in Close()

// Def a Close() guard in all the other operations (event that fires)

// Mutex locks/whether to put in run goroutine for some operations

// Do we even need some operations such as target() or resolveNow()...this is related to overall question of embedding vs. implementing
// important to think about and learn

// Syncing reads and writes to other declared objects...

// More cleanup logic passes on each operation





// Close, Done - why do some balancers have this protection of close before other operations
// and some don't? How do I know if I need?

// only grpc has guarantee of not calling stuff after close, others like xdsclient don't have it...look at other balancers to see examples of this

// Then sync the rest of operations - how do I go about thinking about this

// Example of thought: interval timer going off and algorithm running dependent on config, config written to in UpdateClientConnState
// so those must be synced atomic operations

// Other balancers have some operations in run(), some not with just mutex protecting stuff...how do you decide which one to put where

// Fields and behaviors to keep consistent, inline/mutex, or put everything into run...

// if you have a method that has a return...you can't put in goroutine

// incoming and outgoing calls...keep one direction in goroutine and one in mutex/inline
// keep things out of goroutine as much as possible, can't run in parallel, causes deadlock if inline, but inline/mutex can cause deadlocks
// goroutine you don't need mutex


// Should I write a document for sync stuff wrt Outlier Detection?
// Close
// Operations from grpc -> balancer
// no xdsclient, so guarantee Close() will not happen

// However, balancer -> grpc has no guarantee of this...





// Ping Doug/Menghan for help in regards to syncing operations, esp once you get all the operations down/a mental model of operations in your head

// Esp in regards to his perf comment about stepping back and thinking about codepaths/operations etc. I have all the data structures, operations, etc. Just need to sync them

// Operations to put in the run goroutine, fields to sync with mutexes if outside of run() goroutine, maybe atomics

// Fields in balancer, SubConn (and methods), callcounter, etc.

// When you switch to run, need to move stuff around to handleClientConnUpdate etc.



// I plan to sync all operations with a run() goroutine - so no need for mutexes? Wrong,
// sometimes interspliced between reading in non run() and operations synced in run() **

// mutex locks/unlocks first

// then run goroutine if needed to prevent deadlock

// mutex lock/unlock don't call into other system as that has high possibility of deadlock

// a call being blocking rather than put on a run, parent waits for it to return before calling
// another operation that is a plus

// defensive programming against close() is grpc violating the balancer API