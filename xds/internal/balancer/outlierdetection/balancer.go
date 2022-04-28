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
		odAddrs: am,
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

	ejectionTime time.Time // timestamp used ejecting addresses per iteration, don't make state?

	odCfg *LBConfig

	cc balancer.ClientConn // Priority parent right?

	child balancer.Balancer // cluster impl - when is this built? See others for example, is it in Build()?

	timerStartTime time.Time

	intervalTimer time.Timer


	// map sc (all parent knows about) -> scw (all children know about, this balancer wraps scs in scw)

	// I plan to sync all operations with a run() goroutine - so no need for mutexes? Wrong,
	// sometimes interspliced between reading in non run() and operations synced in run()
}

// noopConfig returns whether this balancer is configured with a logical no-op
// configuration or not.
func (b *outlierDetectionBalancer) noopConfig() bool {
	return b.odCfg.SuccessRateEjection == nil && b.odCfg.FailurePercentageEjection == nil
}

func (b *outlierDetectionBalancer) UpdateClientConnState(s balancer.ClientConnState) error {
	lbCfg, ok := s.BalancerConfig.(*LBConfig)
	if !ok {
		// b.logger.Warningf("xds: unexpected LoadBalancingConfig type: %T", s.BalancerConfig)
		return balancer.ErrBadResolverState
	}
	b.odCfg = lbCfg


	// Perhaps move to a handle function - after coding/putting in run() goroutine


	// When the outlier_detection LB policy receives an address update, it will
	// create a map entry for each subchannel address in the list, and remove
	// each map entry for a subchannel address not in the list, then pass the
	// address list along to the child policy.

	// When the outlier_detection LB policy receives an address update, it will
	// create a map entry for each subchannel address in the list, and remove
	// each map entry for a subchannel address not in the list
	addrs := make(map[resolver.Address]bool) // for fast lookups during deletion phase
	for _, addr := range s.ResolverState.Addresses {
		addrs[addr] = true

		// Cool, stores a pointer, so will affect underlying heap memory and not make a copy
		b.odAddrs.Set(addr, &object{}) // Do we need to initialize any part of this or are zero values sufficient?
	}

	// remove each map entry for a subchannel address not in the list
	for _, addr := range b.odAddrs.Keys() { // []Address, full object struct, I think once you get evaluate this expression, it is immutable
		if !addrs[addr] {
			b.odAddrs.Delete(addr)
		}
	}



	// When a new config is provided, if the timer start timestamp is unset, set
	// it to the current time and start the timer for the configured interval,
	// then for each address, reset the call counters. If the timer start
	// timestamp is set, instead cancel the existing timer and start the timer
	// for the configured interval minus the difference between the current time
	// and the previous start timestamp, or 0 if that would be negative.
	var interval time.Duration
	if b.timerStartTime.IsZero() {
		b.timerStartTime = time.Now()
		// for each address, "reset the call counters" << what does this mean I'm pretty sure it means clear both active and inactive
		for _, obj := range b.objects() {
			obj.callCounter.activeBucket = bucket{}
			obj.callCounter.inactiveBucket = bucket{}
		}
		interval = b.odCfg.Interval
	} else {
		// cancel the existing timer and start the timer
		// for the configured interval minus the difference between the current time
		// and the previous start timestamp, or 0 if that would be negative.
		// configured interval - (current time - previous start timestamp (b.timerStartTime)), if negative make 0
		interval := b.odCfg.Interval - (time.Now().Sub(b.timerStartTime))
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
		// Do we need to clear the timer as well? Or when it fires it will be logical no-op (from no-op config) and not count...
	} // Do we need to move somewhere else? I.e. beginning of function


	// then pass the address list along to the child policy.
	return b.child.UpdateClientConnState(s)
}

func (b *outlierDetectionBalancer) ResolverError(err error) {
	// This operation isn't defined in the gRFC. Pass through? If so, I don't think you need to sync.
	// If not pass through, and it actually does something you'd need to sync. (See other balancers)

	// See other balancers...sometimes they combine into one ClientConn update struct which gets synced in run()
	// Or just pass through...see other balancers, seems like an operation that needs to be part of synced

	// What is the desired behavior of this balancer when it receives a resolver error?
	// Does it propogate all the way down the balancer tree? When does it get acted on? What happens in the other xds balancers?

	// Graceful switch simply forwards the error downward (to either current or pending)

	// What happens to all the state when you receive an error? (Again, see other xds balancers), what does a resolver error signify logically? Are we clearing out this balancer's state?
	// Clearing out map + lb config etc. until you get a new Client Conn update with a good config? Is this the state machine?
}

func (b *outlierDetectionBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	// This gets called from the parent, which has no knowledge of scw (gets
	// called NewSubConn(), that returned sc gets put into a scw in this
	// balancer), thus need to convert sc to scw. This can either be done
	// through typecasting (I don't think would work) or the map thing. Cluster
	// Impl persists a map that maps sc -> scw...see how it's done in other
	// parts of codebase.

	scw, ok := sc.(subConnWrapper) // Need to get scw data from a map, this is wrong, parent has no knowledge of the wrapper
	if !ok {
		// Return, shouldn't happen if passed up scw
	}

	scw.latestState = state
	if !scw.ejected { // eject() - "stop passing along updates from the underlying subchannel."
		b.child.UpdateSubConnState(scw, state)
	}
}

func (b *outlierDetectionBalancer) Close() {
	// This operation isn't defined in the gRFC. Pass through? If so, I don't think you need to sync.
	// If not pass through, and it actually does something you'd need to sync. (See other balancers)

	// Cleanup stage - go specific cleanup
}

func (b *outlierDetectionBalancer) ExitIdle() {
	// This operation isn't defined in the gRFC. Pass through? If so, I don't think you need to sync.
	// If not pass through, and it actually does something you'd need to sync. (See other balancers)

	// Sync required unless child balancer is only built at build time, then
	// it's a guarantee the rest of the time to simply read it and forward down
	// no need for mutexes or run()...but see other balancers to confirm.

	// Exit Idle - go specific logic, see other balancers, I'm pretty sure
	// this simply forwards this call down the balancer hierarchy, typecast, etc.
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
	// Why do you need a map? ((A), BBB) if you only get A you need to map it to the object that wraps it with extra code?
	return balancer.PickResult{
		SubConn: pr.SubConn, // Should this send back the wrapped SubConn or the underlying SubConn in the wrapped SubConn? If sc, look reverse in map?
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


func (b *outlierDetectionBalancer) UpdateState(s balancer.State) {
	// this needs to do not the counting if the config is unset
	// hold up...do you ever **not determined by update addrs (i.e. map state), determined by config state***, a synced operation with UpdateClientConnState

	// gate it here...but then a config update can cause a Picker update..., for sync stuff tho
	// UpdateClientConnState causes inline UpdateState callback. This callback/function writes to a buffer
	// then picked up by run goroutine operations now synced.

	// Update Client Conn state can cause an inline UpdateState |||| but happens before, so write that determines
	// noopConfig will be read here



	b.cc.UpdateState(balancer.State{ // Is this all you need?
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

	// What to do with connectivity state? Simply forward, or does it affect anything here?

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

	if err != nil {
		return nil, err
	}
	scw := &subConnWrapper{
		SubConn: sc,
		addresses: addrs,
	}
	if len(addrs) != 1 { // This addresses list plurality can change from A (what it is right now, in this call) to B, which can cause logic changes in UpdateAddresses. How do I persist here so I know A?
		return scw, nil
	}

	val, ok := b.odAddrs.Get(addrs[0])
	if !ok {
		return scw, nil
	}

	// Does this actually write to the corresponding heap memory?
	obj, ok := val.(*object) // is this right?

	obj.sws = append(obj.sws, *scw) // or do we make this an array of pointers to scw and don't dereference?

	// If that address is currently ejected, that subchannel wrapper's eject
	// method will be called.
	if !obj.latestEjectionTimestamp.IsZero() {
		scw.eject()
	}
	return scw, nil
}

func (b *outlierDetectionBalancer) UpdateAddresses(sc balancer.SubConn, addrs []resolver.Address) {
	// Comes from child lb - so implicitly a scw
	// typecast to scw
	scw, ok := sc.(subConnWrapper)
	if !ok {
		// Return, shouldn't happen if passed up scw
	}

	// see if scw is ejected, if so don't forward? I'm pretty sure. This is the question that started it in the first place.
	if scw.ejected {
		// Don't forward?
	}


	// what data do you have now, can you scale it up

	// Pluralities L (the len(addrs) of old subconn) -> R: Right side is addrs function argument
	len(scw.addresses)
	// len(addrs) = 0...what happens here, can this happen? Or is this equivalent to remove subconn? This adds a massive
	// amount of permutation to the space

	// len(addrs) = 1, single

	// len(addrs) > 1, multiple



	// How to determine left side? I.e. is this subconn -> address list persisted, implicit (no), or what? ***See other parts of codebase


	// Where to determine the plurality list? Helper function?


	// 1 single to single:

	// 1a. Forward the update to the Client Conn.
	//    b.cc.UpdateAddresses(sc, addrs)

	// 1b. Update (create/delete map entries) the map of addresses if applicable.
	// There's a lot to this ^^^

	// 1c. Relay state with eject() recalculated

	/*
	if !obj.latestEjectionTimestamp.IsZero() {
			scw.eject()
		}
	*/





	// 2 single to multiple:

	// 2a. Remove Subchannel from Addresses map entry.
	//    delete from mapEntry.sws...how to do this in non linear time?

	// 2b. Remove the map entry if only subchannel for that address
    //    if len(mapEntry.sws) == 0
	//         delete(mapEntry)

	// 2c. Clear the Subchannel wrapper's Call Counter entry
	//    scw.obj.callCounter = callCounter{} // <- something like this



	// 3 multiple to single:

	// 3a. Add map entry for that Address if applicable (i.e. if not already there)
	//    if right (single) is not a key in map, add a whole new map entry


	// 3b. Add Subchannel to Addresses map entry.
	//   mapValue.sws = append(mapValue.sws, sc)



	// 4 multiple to multiple
		// no-op, subchannel continues to be ignored by outlier detection load balancer


	// I think best way is (if zero add another if at the top, will have 9 possibilities
	// if single {
		// if new single {
		//     single -> single algorithm
		// } else if multiple { // or just else
		//     single -> multiple algorithm
		// }
	// else if multiple { // or just else
		// if new single {
		//     single -> single algorithm
		// } else if multiple { // or just else
		//     single -> multiple algorithm
		// }
	// }

	// Have 0 be a special case of multiple. Wait, if 0 is a special case of
	// multiple, addrs[0] will cause a nil panic.

	// if len(scw.addresses) == 0 if it supports that, thus it being 1 is the logical conditional
	if len(scw.addresses) == 1 {
		if len(addrs) == 1 {
			// single to single algorithm - make helper function?

			// 1a. Forward the update to the Client Conn.
			//    b.cc.UpdateAddresses(sc, addrs)
			b.cc.UpdateAddresses(sc, addrs) // callback inline



			// 1b. Update (create/delete map entries) the map of addresses if applicable.
			// There's a lot to this ^^^, draw out each step and what goes on in each step, check it for correctness and try to break

			scw.addresses[0] // old single address

			addrs[0] // new single address




			// 1c. Relay state with eject() recalculated
			// call it with latest state like uneject
			// scw.ejected = false // this can now be true or false always
			scw.ejected = /*mapEntry.latestEjectionTimestamp.IsZero()*/

			// always send down?
			// scw.childPolicy.UpdateSubConnState(sc/*scw.SubConn or scw (I think just sc i.e. scw.SubConn)*/, scw.latestState)

			// MAKE SURE SCW POINTS TO THE CORRECT OBJ AT THE END OF THIS

		} else {
			// single to multiple algorithm

			// the problem is we have only the subchannel
			scw.addresses // wait this is old addresses



			// 2a. Remove Subchannel from Addresses map entry.
			//    delete from mapEntry.sws...how to do this in non linear time?
			scw.obj.sws // []subConnWrapper, search through this and delete subchannel if you found it

			// 2b. Remove the map entry if only subchannel for that address
			//    if len(mapEntry.sws) == 0
			//         delete(mapEntry)
			if len(scw.obj.sws) == 0 {
				b.odAddrs.Delete(scw.addresses[0]) // invariant that used to be single (top level if), guarantee this never becomes nil
				// delete the pointer
				scw.obj = nil // does this cause any negative downstream effects?
			}

			// 2c. Clear the Subchannel wrapper's Call Counter entry - this might not be tied to an Address you want to count, as you potentially
			// delete the whole map entry
			if scw.obj != nil {
				scw.obj.callCounter.activeBucket = bucket{}
				scw.obj.callCounter.inactiveBucket = bucket{}
			}
		}
	} else if len(scw.addresses) > 1 { // s else
		if len(addrs) == 1 {
			// multiple to single algorithm
			val, ok := b.odAddrs.Get(addrs[0])
			var obj *object
			if !ok {
				// 3a. Add map entry for that Address if applicable (i.e. if not already there)
				obj = &object{}
				b.odAddrs.Set(addrs[0], obj)
			} else {
				obj , ok = val.(*object)
				if !ok {
					// shouldn't happen, logical no-op
				}
			}


			// 3b. Add Subchannel to Addresses map entry. **Note, look over,
			// this should come coupled with adding and removing (updating what
			// object the scw points to, as logically it's not a part of an
			// object anymore or added to an object)
			scw.obj = obj
			obj.sws = append(obj.sws, scw)



			// is that really the best way to get the object? Yes, might be
			// brand new. But you need to update the object the subchannel
			// points to


		} // else is multiple to multiple - no op, continued to be ignored by outlier detection load balancer
		/*else {
			// multiple to multiple algorithm - no op, you don't even need this else just a no-op
		}*/
	}

	// MAKE SURE YOU UPDATE THE OBJECT THE SUBCHANNEL WRAPPER IS POINTING TO WHEN YOU NEED TO
	// scw.addresses (data used for previous addresses - can clear out now because already used) = addrs
	scw.addresses = addrs
}

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
	return objs // what happens when you range over a nil slice?
}

// We also don't need just []{object, object, object, object}

// We need []{{addr, obj}, {addr, obj}, {addr, obj}, {addr, obj}}, what is best data type/way to represent this? did that inline

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

	// Interval trigger:
	b.timerStartTime = time.Now() // could also use this for ejection time, unless something can also write to it before reading...
	// 1. Record the timestamp for use when ejecting addresses in this iteration. "timestamp that was recorded when the timer fired"
	b.ejectionTime = time.Now() // I can write it here - is it because it's not a value..."referenced by the subchannel wrapper that was picked" - so object value needs to be a pointer?
	// ejectionTime could be a field plumbed through methods defined - but writing to a field
	// makes more sense?

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

	// can't really pull stuff out, I think it's fine to switch each for each Address to this

	// for addr, obj := range b.odAddrs (what is logically equivalent to ranging over map entry)
	for _, addr := range b.odAddrs.Keys() {
		val, ok := b.odAddrs.Get(addr) // value interface{}, ok bool
		if !ok { // Shouldn't happen
			continue
		}
		// typecast val to obj,
		// shouldn't error either
		obj, ok := val.(*object)
		if !ok {
			continue
		}
		// Logically equivalent, you have addr, obj in the for each
		// If the address is not ejected and the multiplier is greater than 0, decrease the multiplier by 1.
		if obj.latestEjectionTimestamp.IsZero() && obj.ejectionTimeMultiplier > 0 {
			obj.ejectionTimeMultiplier--
			continue
		}
		// If it hits here the address is ejected

		// If the address is ejected, and the current time is after
		// ejection_timestamp + min(base_ejection_time (type: time.Time) * multiplier (type: int),
		// max(base_ejection_time (type: time.Time), max_ejection_time (type: time.Time))), un-eject the address.
		// obj.latestEjectionTimestamp /*ejection_timestamp*/ + /*min(bet * multiplier, max(bet, met))*/

		// min(b.odCfg.BaseEjectionTime.Nanoseconds() * obj.ejectionTimeMultiplier, max(b.odCfg.BaseEjectionTime.Nanoseconds(), b.odCfg.MaxEjectionTime.Nanoseconds()))

		// max(b.odCfg.BaseEjectionTime.Nanoseconds(), b.odCfg.MaxEjectionTime.Nanoseconds())

		if time.Now().After(obj.latestEjectionTimestamp.Add(time.Duration(min(b.odCfg.BaseEjectionTime.Nanoseconds() * obj.ejectionTimeMultiplier, max(b.odCfg.BaseEjectionTime.Nanoseconds(), b.odCfg.MaxEjectionTime.Nanoseconds()))))) {
			// uneject address - see algorithm, either do that in a function or do it inline
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
	var totalFractionOfSuccessfulRequests float64 // could also pull this out into a helper for cleanliness
	var mean float64 // Right type?
	// var stddev float64 // Right type?

	for _, obj := range b.objects() {
		// "of at least success_rate_ejection.request_volume"
		if uint32(obj.callCounter.inactiveBucket.requestVolume) >= b.odCfg.SuccessRateEjection.RequestVolume { // Is inactive bucket the right one to look at?
			totalFractionOfSuccessfulRequests += float64(obj.callCounter.inactiveBucket.numSuccesses)/float64(obj.callCounter.inactiveBucket.requestVolume) // Does this cause any problems...? This shouldn't, just adds 000000000 decimals to the end of it
		}
	}
	mean = totalFractionOfSuccessfulRequests / float64(b.odAddrs.Len()) // TODO: Figure out types - should this be a float and what's on the left? I feel like with means/std dev you need float for decimal


	// to calculate std dev:
	// Find each scores deviation from the mean - makes sense for me to use decimal points as well, but to what precision

	// Square each deviation from the mean

	// Find the sum of squares

	var sumOfSquares float64

	for _, obj := range b.objects() { // Comparing the fractions of successful requests
		// either calculate it inline or store a list and divide
		devFromMean := float64(obj.callCounter.inactiveBucket.numSuccesses) / float64(obj.callCounter.inactiveBucket.requestVolume)
		sumOfSquares += devFromMean * devFromMean
	}

	variance := sumOfSquares / float64(b.odAddrs.Len())

	// Find the variance - divide the sum of the squares by n (it's population because you use every data point)
	// Take square root of the variance - you now have std dev
	return mean, math.Sqrt(variance)

}

func (b *outlierDetectionBalancer) successRateAlgorithm() {

	// Also need success rate (num of successes) / (num of successes + num of
	// failures), again, either have this as a field of call counter or
	// calculate it every time. (I think is the same as "fractions of successful
	// requests")



	// Percentage of ejected addresses (num of addresses ejected) <- (easy to
	// increment and decrement every time address ejected/unejected, as this
	// whole algorithm happens synchronously) / (total number of addresses -
	// len(b.addressMap))


	// 1. If the number of addresses with request volume of at least
	// success_rate_ejection.request_volume is less than
	// success_rate_ejection.minimum_hosts, stop.

	// Can either do a o(n) linear search of the map, or persist this over time

	// persisting this over time would be hard since this gets incremented every time a request volume
	// hits, which would require updating every time callback hits, which won't be able to
	// read this balancer.



	if b.numAddrsWithAtLeastRequestVolume() < b.odCfg.SuccessRateEjection.MinimumHosts {
		return
	}

	// 2. Calculate the mean and standard deviation of the fractions of
	// successful requests among addresses with total request volume of at least
	// success_rate_ejection.request_volume.
	/*var totalRequests int64 // could also pull this out into a helper for cleanliness
	var mean float64 // Right type?
	var stddev float64 // Right type?

	for _, obj := range b.odAddrs {
		// "of at least success_rate_ejection.request_volume"
		if uint32(obj.callCounter.inactiveBucket.requestVolume) >= b.odCfg.SuccessRateEjection.RequestVolume { // Is inactive bucket the right one to look at?
			totalRequests += obj.callCounter.inactiveBucket.numSuccesses
		}
	}
	mean = totalRequests / len(b.odAddrs) // TODO: Figure out types - should this be a float and what's on the left? I feel like with means/std dev you need float for decimal


	// to calculate std dev:
	// Find each scores deviation from the mean

	// Square each deviation from the mean

	// Find the sum of squares

	// Find the variance - divide the sum of the squares by n (it's population because you use every data point)
	// Take square root of the variance - you now have std dev*/

	mean, stddev := b.meanAndStdDevOfSuccessesAtLeastRequestVolume()

	for _, addr := range b.odAddrs.Keys() { // This for is so you can get addr, and also val, I don't see a way you can do it in a helper function
		val, ok := b.odAddrs.Get(addr)
		if !ok {
			continue
		}
		// typecast val to obj,
		// shouldn't error either
		obj, ok := val.(*object)
		if !ok {
			continue
		}
		// If the percentage of ejected addresses is greater than max_ejection_percent, stop.

		if float64(b.numAddrsEjected) / float64(b.odAddrs.Len()) * 100 > float64(b.odCfg.MaxEjectionPercent) {
			// stop.
			return
		}

		// If the address's total request volume is less than success_rate_ejection.request_volume, continue to the next address.
		if obj.callCounter.inactiveBucket.requestVolume < int64(b.odCfg.SuccessRateEjection.RequestVolume) {
			continue
		}

		// ccb := obj.callCounter.inactiveBucket (both of these would get rid of a lot of verbosity)
		// sre := b.odCfg.SuccessRateEjection

		//  If the address's success rate is less than (mean - stdev *
		//  (success_rate_ejection.stdev_factor / 1000)), then choose a random
		//  integer in [0, 100). If that number is less than
		//  success_rate_ejection.enforcement_percentage, eject that address.
		successRate := obj.callCounter.inactiveBucket.numSuccesses / obj.callCounter.inactiveBucket.requestVolume // This needs to be a float, not an int
		if float64(successRate) < (mean - stddev * (float64(b.odCfg.SuccessRateEjection.StdevFactor) / 1000) ) {

			// choose a random integer in [0, 100). If that number is less than
			// success_rate_ejection.enforcement_percentage, eject that address.
			if uint32(rand.Int31n(100)) < b.odCfg.SuccessRateEjection.EnforcementPercentage {
				b.ejectAddress(addr)
			}
		}
	}

	// Ejecting the addresses logically means: (addresses get unejected at the end of the steps that are tied to the interval timer going off)
	// (see function ^^^ but I think it makes more sense to do this inline)

}

func (b *outlierDetectionBalancer) failurePercentageAlgorithm() {
	// Mathematical properties calculated from the system at each interval timer going off:
	// Failure percentage is (num of failures) / (num of successes + num of failures)
	// ^^^ similar question to successRateEjection

	// Percentage of ejected addresses (num of addresses ejected) <- (easy to
	// increment and decrement every time address ejected/unejected, as this
	// whole algorithm happens synchronously) / (total number of addresses -
	// len(b.addressMap)) <- makes sense, as keeps ejecting and incrementing num
	// of addresses ejected, and then eventually stops once percentage of
	// ejected addresses is greater than max_ejection_percent.




	// 1. If the number of addresses (len(map)) is less than failure_percentage_ejection.minimum_hosts, stop.
	if uint32(b.odAddrs.Len()) < b.odCfg.FailurePercentageEjection.MinimumHosts {
		return
	}

	for _, addr := range b.odAddrs.Keys() {
		val, ok := b.odAddrs.Get(addr)
		if !ok {
			continue
		}
		obj, ok := val.(*object)
		if !ok {
			continue
		}
		// If the percentage of ejected addresses is greater than max_ejection_percent, stop.

		// ejected address int as state, add 1 to it when ejected, subtract 1 from it when unejected

		// for loop through addrs, count number of ejected addresses/ len(b.odAddrs)

		// Needs to be a float * 100 VVV
		if float64(b.numAddrsEjected) / float64(b.odAddrs.Len()) * 100 > float64(b.odCfg.MaxEjectionPercent) {
			// stop.
			return
		}
		// If the address's total request volume is less than failure_percentage_ejection.request_volume, continue to the next address.
		if uint32(obj.callCounter.inactiveBucket.requestVolume) < b.odCfg.FailurePercentageEjection.RequestVolume {
			continue
		}
		//  2c. If the address's failure percentage is greater than
		//  failure_percentage_ejection.threshold, then choose a random integer
		//  in [0, 100). If that number is less than
		//  failiure_percentage_ejection.enforcement_percentage, eject that
		//  address.
		failurePercentage := obj.callCounter.inactiveBucket.numFailures / obj.callCounter.inactiveBucket.requestVolume
		if uint32(failurePercentage) > b.odCfg.FailurePercentageEjection.EnforcementPercentage {
			b.ejectAddress(addr)
		}
	}
}

func (b *outlierDetectionBalancer) ejectAddress(addr resolver.Address) {
	val, ok := b.odAddrs.Get(addr)
	if !ok { // Shouldn't happen - perhaps I should see how other places in the codebase use it
		return
	}
	obj, ok := val.(*object)
	if !ok { // Shouldn't happen - perhaps I should see how other places in the codebase use it
		return
	}

	b.numAddrsEjected++

	// set the current ejection timestamp to the timestamp that was recorded when the timer fired
	obj.latestEjectionTimestamp = b.ejectionTime/*timestamp that was recorded when the timer fired - need to plumb this in somehow*/
	// increase the ejection time multiplier by 1
	obj.ejectionTimeMultiplier++
	// call eject() on each subchannel wrapper in that address's subchannel wrapper list.
	for _, sbw := range obj.sws {
		sbw.eject()
	}
}

func (b *outlierDetectionBalancer) unejectAddress(addr resolver.Address) {
	val, ok := b.odAddrs.Get(addr)
	if !ok { // Shouldn't happen - perhaps I should see how other places in the codebase use it
		return
	}
	obj, ok := val.(*object)
	if !ok { // Shouldn't happen - perhaps I should see how other places in the codebase use it
		return
	}
	b.numAddrsEjected--
	// set the current ejection timestamp to null (doesn't he mean latest ejection timestamp?)
	obj.latestEjectionTimestamp = time.Time{} // or nil if not currently ejected - logically equivalent to time zero value

	// call uneject() on each subchannel wrapper in that address's subchannel wrapper list
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
	sws []subConnWrapper
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