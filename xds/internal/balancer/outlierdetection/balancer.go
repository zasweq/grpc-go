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
	"google.golang.org/grpc/internal/buffer"
	"google.golang.org/grpc/internal/grpcsync"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

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
	b := &outlierDetectionBalancer{
		cc: cc,
		bOpts: bOpts,
		closed: grpcsync.NewEvent(),

		odAddrs: am,
		scWrappers: make(map[balancer.SubConn]*subConnWrapper),
		scUpdateCh: buffer.NewUnbounded(),
		pickerUpdateCh: buffer.NewUnbounded(),
	}
	go b.run()
	return b
}

func (bb) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	var lbCfg *LBConfig
	if err := json.Unmarshal(s, &lbCfg); err != nil { // This validates child's type/config, should I add validation for it being cluster impl?
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

// scUpdate wraps a subConn update to be sent to the child balancer.
type scUpdate struct {
	scw *subConnWrapper
	state balancer.SubConnState
}

type ejectedUpdate struct {
	scw *subConnWrapper
	ejected bool // true for ejected, false for unejected
}

type outlierDetectionBalancer struct {
	numAddrsEjected int // For fast calculations of percentage of addrs ejected

	childState balancer.State
	recentPickerNoop bool

	closed *grpcsync.Event // Fires off when close hits
	cc balancer.ClientConn
	bOpts balancer.BuildOptions


	// closeMu guards against run() reading a subconn update, reading that the child is not nil,
	// and then a Close() call comes in, clears the balancer, and then run() continues to try
	// and write the SubConn update to the child.
	closeMu sync.Mutex
	// child gets first written to on UpdateClientConnState and niled on Close.
	// The only concurrent read that can happen is SubConnUpdates that are
	// processed by run() (The rest of the child balancer calls are guaranteed
	// to be called concurrently with Close(), as they are present in operations
	// defined as part of the balancer.Balancer API.). This can only race with
	// Close(), (child has to be built to receive SubConn updates) so protect
	// SubConn updates and Close() with mu. nil checks on the child for
	// forwarding updates updates are used as an invariant of the outlier
	// detection balancer if it is closed.
	child balancer.Balancer

	// closeMu...canUpdateSubConnState cause close? If so move move niling to run(), and protect other
	// reads with mu


	// interval timer goes off, outlier detection algorithm starts running based on knobs in odCfg.
	// in the middle of running the algorithm, a ClientConn update comes in and writes to odCfg.
	// This causes undefined behavior for the algorithm.


	// newMu prevents interspliced operations and data structures (ex. odCfg getting written to in middle of interval timer algo)
	// (ex. map getting updated in the middle of each operation)
	newMu sync.Mutex
	odAddrs *resolver.AddressMap
	odCfg *LBConfig
	scWrappers map[balancer.SubConn]*subConnWrapper
	timerStartTime time.Time // The outlier_detection LB policy will store the timestamp of the most recent timer start time. Also used for ejecting addresses per iteration

	intervalTimer *time.Timer // Is this receive correct, I think so

	scUpdateCh *buffer.Unbounded
	pickerUpdateCh *buffer.Unbounded
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

	// Reject whole config if any errors, don't persist it for later
	bb := balancer.Get(lbCfg.ChildPolicy.Name)
	if bb == nil {
		return fmt.Errorf("balancer %q not registered", lbCfg.ChildPolicy.Name)
	}

	if b.child == nil {
		// What if this is nil? clusterimpl has an explicit check for this
		b.child = bb.Build(b, b.bOpts)
	}

	b.newMu.Lock()
	b.odCfg = lbCfg

	// When the outlier_detection LB policy receives an address update, it will
	// create a map entry for each subchannel address in the list, and remove
	// each map entry for a subchannel address not in the list.
	addrs := make(map[resolver.Address]bool)
	for _, addr := range s.ResolverState.Addresses {
		addrs[addr] = true
		b.odAddrs.Set(addr, newObject())
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
			obj.callCounter.clear()
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
		if b.intervalTimer != nil {
			b.intervalTimer.Stop()
		}
		b.intervalTimer = time.AfterFunc(interval, func() {
			b.intervalTimerAlgorithm()
		})
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
	// FULL MU here unlock I think - how does this work with time***? Also how do you even trigger the interval timer algo
	b.newMu.Unlock()
	b.pickerUpdateCh.Put(lbCfg)

	// then pass the address list along to the child policy.
	return b.child.UpdateClientConnState(balancer.ClientConnState{
		ResolverState: s.ResolverState,
		BalancerConfig: b.odCfg.ChildPolicy.Config,
	})
}

func (b *outlierDetectionBalancer) ResolverError(err error) {
	// This operation isn't defined in the gRFC. Pass through? If so, I don't think you need to sync.
	// If not pass through, and it actually does something you'd need to sync. (See other balancers)

	// Does this have any effect, should this stop counting? I think transient
	// failure with err picker will come from a lower level balancer, this
	// replaces cluster impl which just forwarded, cds balancer actually sends
	// up an err picker, maybe this should send a no-op picker. If so, need to
	// sync data accesses. But the picker won't do unnecessary work/counting,
	// will simply return the error out. Unless you can create theis one, but
	// then will always have a child. This comes coupled with having a child.
	// Thus, this can't be a leaf node, as it will always have a child, so no
	// need to send ErrPicker upward.

	// If you don't nil it in run(), I still think you need the close mu if you nil it in Close(), no close event in graceful switch...
	if b.child != nil { // If you sync Close() in run() can't have the nil write be in run(), you need to nil it in CLose() which is guaranteed to be sync and then do w/e in run()
		b.child.ResolverError(err) // I think
	}
}

func (b *outlierDetectionBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	// This gets called from the parent, which has no knowledge of scw (gets
	// called NewSubConn(), that returned sc gets put into a scw in this
	// balancer), thus need to convert sc to scw. This can either be done
	// through typecasting (I don't think would work) or the map thing. Cluster
	// Impl persists a map that maps sc -> scw...see how it's done in other
	// parts of codebase.
	b.newMu.Lock()
	defer b.newMu.Unlock()
	scw, ok := b.scWrappers[sc]
	if !ok {
		// Return, shouldn't happen if passed up scw
		// Because you wrap subconn always, clusterimpl i think only some
		return
	}
	if state.ConnectivityState == connectivity.Shutdown {
		// Remove this SubConn from the map on Shutdown.
		delete(b.scWrappers, scw.SubConn) // Just deletes from map doesn't touch underlying heap memory
	}

	b.scUpdateCh.Put(&scUpdate{
		scw: scw,
		state: state,
	})

}

func (b *outlierDetectionBalancer) Close() {
	// This operation isn't defined in the gRFC. Def requires synchronization points,
	// stuff in the balancer shouldn't happen after Close().

	// Don't call other methods on the child once it is closed. I think this is handled, nil checks
	// in other operations that are guaranteed to be called concurrently. Also CloseMu sync with UpdateSubConStates from eject (which can come from child balancers).

	b.closed.Fire()
	if b.child != nil {
		b.closeMu.Lock()
		child := b.child
		b.child = nil
		b.closeMu.Unlock()
		child.Close()
	}

	// Any other cleanup needs to happen (subconns, other resources?)?
	if b.intervalTimer != nil {
		b.intervalTimer.Stop()
	}
}

func (b *outlierDetectionBalancer) ExitIdle() {
	if b.child == nil {
		return
	}
	if ei, ok := b.child.(balancer.ExitIdler); ok {
		ei.ExitIdle()
		return
	}
	// Fallback for children handled in clusterimpl balancer - do we ever validate that it's a clusterimpl child for the config?
}


// The outlier_detection LB policy will provide a picker that delegates to
// the child policy's picker, and when the request finishes, increment the
// corresponding counter in the map entry referenced by the subchannel
// wrapper that was picked.
type wrappedPicker struct {
	childPicker balancer.Picker
	noopPicker bool
}

func (wp *wrappedPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	pr, err := wp.childPicker.Pick(info)
	if err != nil {
		return balancer.PickResult{}, err // Err picker will return out of here, done will never be called anyway
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
	scw, ok := sc.(*subConnWrapper)
	if !ok {
		// Shouldn't happen, as comes from child
		return
	}

	obj := (*object)(atomic.LoadPointer(&scw.obj))
	if obj == nil {
		return
	}

	// even here obj can become deprecated, so will race but again like activeBucket simply
	// write to deprecated heap memory (or another bucket) no harm done

	// if it hits here this won't be nil
	// these two pointers in whole data dereference are only ones that can
	// race (be written to concurrently), why these specifically in the tree are the LoadPointer
	ab := (*bucket)(atomic.LoadPointer(&obj.callCounter.activeBucket))

	if info.Err != nil { // s/queue that aggregates stats for you, other locks aren't
		// Is there anything else that is required to make this a successful RPC?
		// This access will require a read lock, so instead switch this whole thing to access it here
		// (remember, scw.obj can be nil, need to add that check around this)

		// Overall problem: when this done callback finishes, the scw is pointing to a certain object.
		// In between writing this to the countingUpdateCh, an UpdateAddresses call can come in and move what this scw
		// was pointing to, and this writes to the new address even though it's for the old address

		// state := (*balancer.State)(atomic.LoadPointer(&cpw.state)) the read of the pointer into memory like this, now you have
		// a local var which points to the data on the heap
		// this write will not have a concurrent read or write, you are good here
		// race if activeBucket gets swapped after being read, this simply writes to memory that won't be accessed again, that is fine
		// is this the thing that needs to protected by the mutex? Any wonky behavior that can come from ordering of this? Can this obj/scw be deleted concurrently
		atomic.AddInt64(&ab.numSuccesses, 1)
	} else {
		// this deref is dangerous - see other examples for a multi dimensional data structure that has atomics
		// high level problem: mutex is slow, but can solve with atomic read or pushing onto channel
		atomic.AddInt64(&ab.numFailures, 1)
	}
	atomic.AddInt64(&ab.requestVolume, 1)
}


func (b *outlierDetectionBalancer) UpdateState(s balancer.State) {
	b.pickerUpdateCh.Put(s)

}

func (b *outlierDetectionBalancer) NewSubConn(addrs []resolver.Address, opts balancer.NewSubConnOptions) (balancer.SubConn, error) {
	// "When the child policy asks for a subchannel, the outlier_detection will
	// wrap the subchannel with a wrapper." - A50
	sc, err := b.cc.NewSubConn(addrs, opts)
	if err != nil {
		return nil, err
	}
	scw := &subConnWrapper{
		SubConn: sc,
		addresses: addrs,
		scUpdateCh: b.scUpdateCh,
	}
	b.newMu.Lock()
	defer b.newMu.Unlock()
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
	atomic.StorePointer(&scw.obj, unsafe.Pointer(obj))

	// "If that address is currently ejected, that subchannel wrapper's eject
	// method will be called." - A50
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
	// Remove the wrapped SubConn from the parent Client Conn. We don't remove
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
		obj = newObject()
		b.odAddrs.Set(addr, obj)
	} else {
		obj , ok = val.(*object)
		if !ok {
			// shouldn't happen, logical no-op
			obj = newObject() // to protect against nil panic
		}
	}
	return obj
}

// 2a. Remove Subchannel from Addresses map entry (if singular address changed).
// 2b. Remove the map entry if only subchannel for that address.
func (b *outlierDetectionBalancer) removeSubConnFromAddressesMapEntry(scw *subConnWrapper) {
	obj := (*object)(atomic.LoadPointer(&scw.obj))
	if obj == nil { // This shouldn't happen, but include it for defensive programming.
		return
	}
	for i, sw := range obj.sws {
		if scw == sw {
			obj.sws = append(obj.sws[:i], obj.sws[i+1:]...)
			if len(obj.sws) == 0 {
				b.odAddrs.Delete(scw.addresses[0])
				obj = nil
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
	scw, ok := sc.(*subConnWrapper)
	if !ok {
		// Return, shouldn't happen if passed up scw
		return
	}

	b.cc.UpdateAddresses(scw.SubConn, addrs)
	b.newMu.Lock()
	defer b.newMu.Unlock()

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



			// 1a. Forward the update to the Client Conn. (handled earlier)

			// 1b. Update (create/delete map entries) the map of addresses if applicable.
			// There's a lot to this ^^^, draw out each step and what goes on in each step, check it for correctness and try to break

			// Remove Subchannel from Addresses map entry
			// Remove the map entry if only subchannel for that address
			b.removeSubConnFromAddressesMapEntry(scw)
			obj := b.addMapEntryIfNeeded(addrs[0])

			// 1c. Relay state with eject() recalculated
			if obj.latestEjectionTimestamp.IsZero() {
				scw.eject() // will send down correct SubConnState and set bool, eventually consistent event though race, write to beginning of queue and it will eventually happen...the right UpdateSubConnState update forwarded to child, last update will eventually send it
			} else {
				scw.uneject() // will send down correct SubConnState and set bool - the one where it can race with the latest state from client conn vs. persisted state, but this race is ok
			}
			atomic.StorePointer(&scw.obj, unsafe.Pointer(obj))
			obj.sws = append(obj.sws, scw)
		} else { // single address to multiple addresses
			// 2a. Remove Subchannel from Addresses map entry.
			// 2b. Remove the map entry if only subchannel for that address
			b.removeSubConnFromAddressesMapEntry(scw)

			// 2c. Clear the Subchannel wrapper's Call Counter entry - this might not be tied to an Address you want to count, as you potentially
			// delete the whole map entry - wait, but only a single SubConn left this address list, are we sure we want to clear in this situation?
			obj := (*object)(atomic.LoadPointer(&scw.obj))
			if obj != nil {
				obj.callCounter.clear()
				/*atomic.StorePointer(&obj.callCounter.activeBucket, unsafe.Pointer(&bucket{}))
				obj.callCounter.inactiveBucket = &bucket{}*/
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
			atomic.StorePointer(&scw.obj /*protecting this memory and only this memory - 64? bit pointer to an arbitrary data type*/, unsafe.Pointer(obj))
			obj.sws = append(obj.sws, scw) // ^^ because of this, this should be fine in regards to concurrency
		} // else is multiple to multiple - no op, continued to be ignored by outlier detection load balancer.
	}

	scw.addresses = addrs
}

func (b *outlierDetectionBalancer) ResolveNow(opts resolver.ResolveNowOptions) {
	b.cc.ResolveNow(opts)
}

func (b *outlierDetectionBalancer) Target() string {
	return b.cc.Target()
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
	return objs
}

func (b *outlierDetectionBalancer) intervalTimerAlgorithm() {
	// Grounded blob: look over/cleanup/make sure this algorithm is correct.
	// **Do this, because I already found a few correctness issues with it. If
	// you wrap this whole algo with a mutex, can you still get wonky races?
	b.newMu.Lock()
	defer b.newMu.Unlock()
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
	}
}

func (b *outlierDetectionBalancer) run() {
	for {
		select {
		// try and break these two sync points
		case update := <-b.scUpdateCh.Get():
			b.scUpdateCh.Load()
			switch u := update.(type) {
			case scUpdate:
				// scw := u.scw (would this make it cleaner?)
				u.scw.latestState = u.state
				b.closeMu.Lock()
				if !u.scw.ejected && b.child != nil {
					b.child.UpdateSubConnState(u.scw, u.state) // can this call back and close, no close comes from higher level...unless UpdateSubConnState -> UpdateState -> Close()
				}
				b.closeMu.Unlock()
			case ejectedUpdate:
				u.scw.ejected = u.ejected
				var stateToUpdate balancer.SubConnState
				if u.ejected {
					stateToUpdate = balancer.SubConnState{
						ConnectivityState: connectivity.TransientFailure,
					}
				} else {
					stateToUpdate = u.scw.latestState // wait what if this is nil? (uneject a SubConn initially that has never been ejected, will this be a possibility of hitting?)
				}
				b.closeMu.Lock()
				if b.child != nil {
					b.child.UpdateSubConnState(u.scw, stateToUpdate)
				}
				b.closeMu.Unlock()
			}
		case update := <-b.pickerUpdateCh.Get():
			b.pickerUpdateCh.Load()
			// Do you need to protect this closed event read with a mutex? I think you might have to...just protect with closed mu?
			if b.closed.HasFired() { // don't send picker updates to grpc after the balancer has been closed
				return
			}
			switch u := update.(type) {
			case balancer.State:
				b.childState = u
				b.newMu.Lock() // Could make another mu that only protect the config to prevent this from blocking, but I think this is cleaner
				noopCfg := b.noopConfig()
				b.newMu.Unlock()
				b.recentPickerNoop = noopCfg
				b.cc.UpdateState(balancer.State{
					ConnectivityState: b.childState.ConnectivityState,
					// The outlier_detection LB policy will provide a picker that delegates to
					// the child policy's picker, and when the request finishes, increment the
					// corresponding counter in the map entry referenced by the subchannel
					// wrapper that was picked.
					Picker: &wrappedPicker{
						childPicker: b.childState.Picker,
						// If both the `success_rate_ejection` and
						// `failure_percentage_ejection` fields are unset in the
						// configuration, the picker should not do that counting.
						noopPicker: noopCfg,
					},
				})
			case *LBConfig:
				noopCfg := u.SuccessRateEjection == nil && u.FailurePercentageEjection == nil
				if b.childState.Picker != nil && noopCfg != b.recentPickerNoop {
					b.recentPickerNoop = noopCfg
					b.cc.UpdateState(balancer.State{
						ConnectivityState: b.childState.ConnectivityState,
						// The outlier_detection LB policy will provide a picker that delegates to
						// the child policy's picker, and when the request finishes, increment the
						// corresponding counter in the map entry referenced by the subchannel
						// wrapper that was picked.
						Picker: &wrappedPicker{
							childPicker: b.childState.Picker,
							// If both the `success_rate_ejection` and
							// `failure_percentage_ejection` fields are unset in the
							// configuration, the picker should not do that counting.
							noopPicker: noopCfg,
						},
					})
				}
			}
		// reset or newtimer should not be done concurrent to this receive

		// this starts nil, gets written to in update client conn state....
		case <-b.closed.Done():
			return
		}
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
		failurePercentage := (float64(ccb.numFailures) / float64(ccb.requestVolume)) * 100
		if failurePercentage > float64(b.odCfg.FailurePercentageEjection.Threshold) {
			// then choose a random integer in [0, 100). If that number is less
			// than failiure_percentage_ejection.enforcement_percentage, eject
			// that address.
			if uint32(rand.Int31n(100)) < b.odCfg.FailurePercentageEjection.EnforcementPercentage {
				b.ejectAddress(addr)
			}
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
	callCounter *callCounter

	// The latest ejection timestamp, or null if the address is currently not ejected
	latestEjectionTimestamp time.Time // We represent the branching logic on the null with a time.Zero() value

	// The current ejection time multiplier, starting at 0
	ejectionTimeMultiplier int64

	// A list of subchannel wrapper objects that correspond to this address
	sws []*subConnWrapper
}

func newObject() *object {
	return &object{
		callCounter: newCallCounter(),
		sws: make([]*subConnWrapper, 0),
	}
}






// Tests (add to testing file)
//
// Balancer test
// Unit tests
// E2E test




// Should I write a document for sync stuff wrt Outlier Detection? Take what I wrote in notebook and put in a google doc ***
