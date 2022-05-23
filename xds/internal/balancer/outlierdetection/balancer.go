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
	// go b.run() <- way of synchronizing
	return &outlierDetectionBalancer{
		cc: cc,
		bOpts: bOpts,
		closed: grpcsync.NewEvent(),
		done: grpcsync.NewEvent(),

		odAddrs: am,
		scWrappers: make(map[balancer.SubConn]*subConnWrapper),
		scUpdateCh: buffer.NewUnbounded(),
		pickerUpdateCh: buffer.NewUnbounded(),
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
	// All 4 data points of type object need sync.

	// callCounter gets called/written to a lot (should we have mutex only for call counter - inside the object and at what level?)
	// latestEjectionTimeStamp gets read to determine if an address has been ejected...
	// ejectionTimeMultiplier is called happens before in interval timer algo going off
	// sws gets read/written to a lot


	// not really protected by newMu...although part of interval timer algo, and entirely part of interval timer algo
	numAddrsEjected int // For fast calculations of percentage of addrs ejected

	// cfgMu protects access to the config and to the picker logic for resending update
	// on UpdateClientConnState()
	// cfgMu sync.Mutex
	// odCfg *LBConfig // races with UpdateState -> add a mutex for this field (mutex ok here because not on RPC data flow)
	// More:
	// wrong, this gets read to determine no-op picker in UpdateState()...
	// that's it, "race with picker update", determines if no-op config
	// ^^^ Do I protect with same mutex or a different mutex?

	childState balancer.State
	recentPickerNoop bool

	cc balancer.ClientConn
	// Only written to at build time so no protection needed
	bOpts balancer.BuildOptions
	// Only written to at build time so no protection needed



	child balancer.Balancer // talk about sync...
	// clusterimpl config as part of the OD config, can be different enough to warrant a new creation of this child? - no, add to parsing logic?
	// built on UpdateClientConnState (i.e. written to) (already synced with entire interval timer algorithm)

	// read/called in all the other pass throughs

	// also written to in Close() - nilled (and read to bal.Close()) (sync whole operation or just write?)


	// pass through read/call:
	// mu protecting read
	// if child != nil
	// mu protecting read

	// Another event can happen right here...i.e. closing and clearing of balancer...think back to graceful switch and how that handled it

	//     child.method


	// mu lock (graceful switch did this UpdateSubConnState with another mu so balancer would
	// not be closed)

	// if child != nil
	//     child.method

	// mu unlock

	// if that child.method call calls back into this, then you have problems




	// If we sync UpdateClientConnState and interval timer algo, this doesn't need any protecting, reads
	// and writes happen happens before and never concurrently, we're good here



	// These two vvv can help with synchronization problems reasoned about ^^^

	// I plan to sync all operations with a run() goroutine - so no need for mutexes? Wrong,
	// sometimes interspliced between reading in non run() and operations synced in run() **



	// Behavior mu

	// behaviorMu guards UpdateClientConnState(), the interval timer algorithm, (UpdateAddresses(), and Close().) no longer, see ***wait below

	// It protects against these wonky behaviors: (if too verbose, put in sync design doc)

	// Closing the balancer before forwarding updates down *** wait, the scw update channel handles this for you, have the goroutine
	// read process it, need to clean up that goroutine once balancer closed

	// interval timer goes off, outlier detection algorithm starts running based on knobs in odCfg.
	// in the middle of running the algorithm, a ClientConn update comes in and writes to odCfg.
	// This causes undefined behavior for the algorithm.


	// closeMu guards against run() reading a subconn update, reading that the child is not nil,
	// and then a Close() call comes in, clears the balancer, and then run() continues to try
	// and write the SubConn update to the child.

	// The rest of the child balancer calls are guaranteed to be called concurrently with Close(),
	// as they are present in operations defined as part of the balancer.Balancer API.

	// but close() has to wait for the component to clean up right
	closeMu sync.Mutex
	// whether you need this or not is dependent on how you sync close (whether it's done in run() goroutine or not)



	// newMu prevents interspliced operations and data structures (ex. odCfg getting written to in middle of interval timer algo)
	// (ex. map getting updated in the middle of each operation)
	newMu sync.Mutex
	// move all of the state protected by this to here
	// TODO: When I get back, move all state protected by this to here.
	// triage if you need the other mutexes at all. Any behavior/state/deadlocks needed to prevent?
	// then the things defined in my commit message (finishing operations, cleanup (went halfway through on plane), testing)
	// close(), and interval timer
	odAddrs *resolver.AddressMap // this I think is successfully protected by the mutex
	odCfg *LBConfig
	scWrappers map[balancer.SubConn]*subConnWrapper
	timerStartTime time.Time // The outlier_detection LB policy will store the timestamp of the most recent timer start time. Also used for ejecting addresses per iteration

	// Any read/write mutexes here in a group they represent
	intervalTimer *time.Timer // TODO: figure out how to receive off of this to trigger algorithm
	// Again, written to in UpdateClientConnState()
	// channel receive in run()
	// No sync needed


	// run() goroutine processes both of these VVV

	// sc update ch (triage on how other balancers), desired seems to be an unbounded buffer
	// process sc updates async **do when get back (write scw to this)
	scUpdateCh *buffer.Unbounded // TODO when get back: define/figure out type to put on this channel, see cluster resolver and rls for examples


	pickerUpdateCh *buffer.Unbounded

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
	// Close guard - this is overprotective...
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

	// The child gets updated at the end of this function, thus you need to
	// build the child and return early if the child doesn't build
	if b.child == nil {
		// What if this is nil? clusterimpl has an explicit check for this
		b.child = bb.Build(b /*Again, do we want to make this a functioning ClientConn or just embed? - yes*/, b.bOpts)
	}

	b.newMu.Lock()
	// do we need this cfgMu? or do we protect noopConfig read in updatestate with
	// this newMu?
	// Closemu you don't need if close is synced in run()
	b.cfgMu.Lock()
	b.odCfg = lbCfg
	b.cfgMu.Unlock()


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

	if !b.noopConfig() { // Can't get concurrently written, guard at callsite
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
	// FULL MU here unlock I think - how does this work with time***? Also how do you even trigger the interval timer algo
	b.newMu.Unlock()
	b.pickerUpdateCh.Put(lbCfg)

	// Any weird behavior here...child can't be niled concurrently so you're good

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

	// Cleanup stage - go specific cleanup, clean up goroutines and memory? See
	// other balancers on how they implement close() and what they put in
	// close(). Ping run() <- right now has child.Close() i.e. pass through of the operation?

	// ping run() and cleanup (waiting on a done event)?
	// exit from run() <- cleaning up that goroutine, cleanup any other resources?
	// nil/close the child
	// any subconns you need to clean up?

	// fire closed event (which exits run goroutine here)
	b.closed.Fire()
	b.closeMu.Lock()
	if b.child != nil {
		b.child = nil
		b.closeMu.Unlock()
		b.child.Close()
	}
	// or exits here

	// Any other cleanup needs to happen?
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
	if b.child == nil { // implicitly wraps a close guard as well
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
		return balancer.PickResult{}, err // Err picker will return out of here, done will never be called anyway
	}

	done := func(di balancer.DoneInfo) { // This function can literally be called whenever...a callback by grpc once done, increments counter scw.obj.callCounter.activeBucket.(numSuccesses||numFailures)++
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


func (b *outlierDetectionBalancer) UpdateState(s balancer.State) { // Can this race with anything???? esp that b.noopConfig()
	b.pickerUpdateCh.Put(s)
	// cfgMu lock
	/*b.cfgMu.Lock()
	b.childState = s
	noopCfg := b.noopConfig()
	b.recentPickerNoop = noopCfg
	b.cfgMu.Unlock()
	// b.sentFirstPicker = true
	// noopCfg := b.noopConfig() this is right even if it gets pinged on calculation of diff, since it can diff either way
	// b.recentPickerNoop = true
	// persist the bool to represent the state of the most recent picker forwarded up
	// use this to determine whether to ping this

	// still need to determine whether picker has been sent - separate bool sentFirstPicker?
	// if sentFirstPicker && recentPickerNoop != newCfgNoop // WHEN GET BACK FINISH THIS IMPLEMENTATION

	// cfgMu unlock

	// Two things here - the actual read/write protection

	// and the behavior of this needing to update when received a substantially
	// different config in UpdateClientConnState()

	// how is this event communicated/synced...can't just explicitly call b.UpdateState() imo,
	// channel like cluster impl?

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
			noopPicker: noopCfg,
		},
	})*/

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
		scUpdateCh: b.scUpdateCh,
	}
	// b.scwMu.Lock() // NEW MU lock I think
	b.newMu.Lock()
	defer b.newMu.Unlock()
	// defer NEW MU unlock I think
	b.scWrappers[sc] = scw // can you protect this as well? I think so, UpdateSubConnState puts onto a channel so doesn't deadlock there
	// b.scwMu.Unlock()
	if len(addrs) != 1 {
		return scw, nil
	}

	val, ok := b.odAddrs.Get(addrs[0])
	if !ok {
		return scw, nil
	}

	obj, ok := val.(*object)
	obj.sws = append(obj.sws, scw)
	// do we even need to make this atomic, I'm going to do it for defensive sake
	// scw.obj = unsafe.Pointer(obj)
	atomic.StorePointer(&scw.obj, unsafe.Pointer(obj))

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
// 2b. Remove the map entry if only subchannel for that address
func (b *outlierDetectionBalancer) removeSubConnFromAddressesMapEntry(scw *subConnWrapper) {
	obj := (*object)(atomic.LoadPointer(&scw.obj))
	if obj == nil { // This shouldn't happen, but incldue it for defensive programming.
		return
	}
	for i, sw := range obj.sws {
		if scw == sw {
			obj.sws = append(obj.sws[:i], obj.sws[i+1:]...)
			if len(obj.sws) == 0 {
				// []resolver.Address just automatically use [0], invariant of the caller
				b.odAddrs.Delete(scw.addresses[0]) // invariant that used to be single (top level if), guarantee this never becomes nil
				// Switch to atomic load of nil into scw.obj
				obj = nil // does this cause any negative downstream effects? yes, add nil check
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

	b.cc.UpdateAddresses(scw.SubConn, addrs) // This it to cc

	// behaviorMu.Lock()
	// defer behaviorMu.Unlock() s/UpdateSubConnState here to write to a channel (i.e. in eject())

	// NEW MUTEX.LOCK I think
	b.newMu.Lock()
	// defer NEW MUTEX.UNLOCK I think
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
	// Operation to finish
	b.cc.ResolveNow(opts)
}

func (b *outlierDetectionBalancer) Target() string { // embed or just pass like this, I think this is correct.
	// Operation to finish
	return b.cc.Target()
}

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

			// closeMu lock
			b.closeMu.Lock()
			if b.child != nil {
				b.child = nil
				// either a. closeMu unlock (would reduce chance of
				// deadlock), I think this is correct, since you can't nil
				// concurrently, and it won't proceed with the operation if it
				// is niled, which means that at some point the child will be
				// Closed(), doesn't really matter when (see graceful switch),
				// jsut sometime in the future. The other operations (outside of
				// interval timer algo and UpdateAddresses) are guaranteed to be
				// called concurrently (balancer.Balancer API), so you don't
				// need to wait for this mutex.
				b.closeMu.Unlock()
				b.child.Close()
				// or b. closeMu unlock (I think A
			}

		// NewSubConn (yes for sure, adds to map - or protect map with mutex like cluster impl)
		// RemoveSubConn (removes from map, no, this is determined by address list, I think just removes scw from address list, still reading it so sync it here. Need to add functionality for this)
		// UpdateAddresses (algorithm I proposed, uses map so yes needs to be synced)
		// UpdateState (yes, reads sc...I think so?)
		// ResolveNow?
		// Target?

		// Should I finish all of these operations before figuring any of this out? I.e. ordering of operations

		// try and break these two sync points
		case update := <-b.scUpdateCh.Get(): // s this to also handle eject logic and latest state, that will sync scw.ejected and scw.latestState implicitly and keep behavior consistent?
			// update := u.(*scUpdate)
			b.scUpdateCh.Load()
			switch u := update.(type) {
			case scUpdate:
				// scw := u.scw (would this make it cleaner?)
				u.scw.latestState = u.state
				b.closeMu.Lock() // won't need this if you sync close and add it to run goroutine because then b.child won't race
				if !u.scw.ejected && b.child != nil {
					b.child.UpdateSubConnState(u.scw, u.state) // can this call back and close, no close comes from higher level...
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
		// case update := <-b.scUpdateCh.Get():

		//      case sc update:
		//           write latest state
		//				if !sc.ejected && b.child == nil {
		//				       forward latest update
		//                     b.child.UpdateSubConnState(scw, state)
		//              }
		//		case eject/uneject: (or two separate - I think one is better, can determine based on bool)
		//           write sc.ejected
		//           b.child.UpdateSubConnState(scw, TRANSIENT FAILURE || scw.latestState)

		case update := <-b.pickerUpdateCh.Get():
			b.pickerUpdateCh.Load()
			// closed guard? - how to make sure this doesn't send updates to grpc after it's been closed
			switch u := update.(type) {
			case balancer.State:
				b.childState = u
				b.newMu.Lock() // Could make another mu that only protect the config to prevent this from blocking, but I think this is cleaner
				noopCfg := b.noopConfig() // Reads a bit
				b.newMu.Unlock()
				b.recentPickerNoop = noopCfg
				b.cc.UpdateState(balancer.State{
					ConnectivityState: b.childState.ConnectivityState, // and this will have the most recent child state
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
				noopCfg := u.SuccessRateEjection == nil && u.FailurePercentageEjection == nil // By the end of processing, this is going to be the most recent config sent to UpdateClientConnState()
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

		case <-b.intervalTimer.C: // will this nil?
			b.intervalTimerAlgorithm()
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
	callCounter *callCounter // should this be a pointer? yes TODO: switch to pointer, poointer, doesn't race since doesn't get written to

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

// Syncing access to this ^^^, rather than mutex, what can we make atomic
// Obviously can't make everything atomic

// Seems like we're only making callCounter atomic, as that is the only thing on the RPC flow
// of this struct.

// scw.obj.callCounter.activeBucket.numSuccesses++

// scw.*.*.*.*.int (unsafe pointer at each level, or just at the ones we want to read atomically?)

// do we need to load this step by step

// is loadPtr(scw.obj.callCounter.activeBucket) <- that's a read right

// loadPtr(scw.obj)

// this read cannot happen concurrently with a write to this right
// loadPtr (obj.callCounter).activeBucket{} // If this gets cleared (at any level of the tree), will write to cleared memory in other places (other places don't have pointer to it) (this is only ref), ok right?

// activeBucket.numSuccess/Failures++


// then just use that, atomically update activeBucket(how do we make sure this
// doesn't get niled), seems like a race that doesn't matter according to doug
// if it counts it for previous interval and goes to next one basically a no-op

// load the atomic pointer obj from memory, have each pointer, or
// interface with a uint32




// Tests (add to testing file)
//
// Balancer test
// Unit tests
// E2E test
// Also cleanup


// This operation, rather than being defined on the SubConn, actually seems to be interfacing with a wrapped SubConn's
// state it persists from getting called and written to***, how do we get ref to child for this

// eject/uneject changes internal state

// can also persist recent update state by writing directly to it (with a mutex protecting it), what blobs write to each
// field in the scws and how do we protect - rw mutex

// Figure out linkage between a wrapped SubConn and the balancer, see example somewhere in codebase?


// why does this hold the map entry?
// the scw that was picker by picker will increase counter in map entry once picked



// 2. Figure out run() goroutine and syncing operations

// I try to avoid a run goroutine as much as possible
// But if I need a run(), I will probably try to move all the operations there
// Unless it's to just forward the update, without needing to sync any field




// Should scws[] be pointers to heap memory or value types...to map and how pointers work
// logically. Decided to make scws pointers, this same question applies to the callCounter object

// A zero value struct is simply a struct variable where each key's value is set
// to their respective zero value. So different than pointer nil vs. not nil, underlying heap memory
// vs. struct copyyyyy?

// ** Switched to [] of pointers to heap memory




// Close() event synchronization, inside run goroutine*** need to sync with others, or all in Close()

// Def a Close() guard in all the other operations (event that fires)

// Mutex locks/whether to put in run goroutine for some operations

// Do we even need some operations such as target() or resolveNow()...this is related to overall question of embedding vs. implementing
// important to think about and learn, my gut tells me to implement target and resolve now and have balancer implement ClientConn,
// and maybe have wrapped picker also wrap not embed?

// Syncing reads and writes to other declared objects...

// More cleanup logic passes on each operation





// Close, Done - why do some balancers have this protection of close before other operations
// and some don't? How do I know if I need?

// only grpc has guarantee of not calling stuff after close, others like xdsclient don't have it...look at other balancers to see examples of this

// Then sync the rest of operations - how do I go about thinking about this


// incoming and outgoing calls...keep one direction in goroutine and one in mutex/inline
// keep things out of goroutine as much as possible, can't run in parallel, causes deadlock if inline, but inline/mutex can cause deadlocks
// goroutine you don't need mutex


// Should I write a document for sync stuff wrt Outlier Detection? Take what I wrote in notebook and put in a google doc ***

// buckets, state, etc.







// Precedence:

// Reads and writes (most basic)...correctness issue for sure guaranteed, seems
// like there, two big ones to reason about is map address -> obj (including obj
// itself), scw->obj (*when get back, triage gives you a grounding point - what
// parts of obj does this access? Same as when accessed threw map or just scw),
// and the sc[scw]map (easier for reads and writes, harder/then need to think
// about what behaviors break it)

// three places need to sync state:
// scw.objref
// map.objref
// obj.each 4 field
// 2 fields in the obj have even more data that needs to be synced


// Behaviors (similar to how Close() put in between the UpdateSubConnState
// function block) would break API - see if there is undesirable things that could
// happen. I.e. can any concurrent shit happen that causes wonky behavior?

// readmu.lock
// read field
// readmu.unlock

// now...can any weird shit happen logically concurrently
// i.e. Closing() of the current balancer
// what menghan said close() after creating new subconn, not remove subconn
// which would leak it



// (guarantee of synchronous calls from grpc -> balancer.Balancer (including UpdateSubConnState(), two reads),
// but could race with UpdateState() deleting current), both reads atomic, similar to how we want whole interval timer algo
// to run atomic wrt odCfg and UpdateClientConnState

// After mutex protection - things to put in a run() goroutine
// Cons: blocking, waits for one operation to complete and doesn't do operation immediately, can't return
// Pros: prevents deadlocks...in a mutex being locked, call into another system
// can call back with mutex lock





// mutexes for reading/writing state:


// Behavior mutex - UpdateClientConnState and interval timer algo (AB BA), does any other behavior
// need to be synced here? If something else intersplices with these two behaviors, does it cause
// undefined/unwanted behavior?

// Make a precedence list of synchronization to see dependencies and if
// anything covers another

// If we sync UpdateClientConnState() and interval timer algo
// these data still need mutex (irrespective of behavior):

// 1. odAddrs and its complications

// If you decide to sync whole other operations, do these reads and writes at
// each level of the data tree implicitly get synced? (i.e. already atomic)

// AB vs BA induce any weird behavior for this od addrs tree?
// Or any weird orderings here VVV? (i.e. whether to initially eject, I think eventually the scw eject method will be called)

// Operations to sync (only add if necessary):
// UpdateClientConnState *
// Close (this is already synced with UpdateClientConnState as part of same balancer API)
// Interval Timer * Def,  what started it all
// Picker update?

// sc -> scw state mutex



// 2. odCfg - synced if sync in UpdateClientConnState (write) and
// Interval timer algo (read - can happen concurrently because timer), but picker update (read - can happen concurrently because balancer->grpc) is dependent on this
// can get written to in UpdateClientConnState, do we sync picker update alongside
// other two events?

// Noop picker is determined by odCfg

// so if odCfg comes in that changes (read old config noop vs. new one but first check would break it) noopConfig()...? or always ping?
// needs to send a new picker update, would send with counting, if a picker update
// comes in from child will send a new picker again with counting

// If you protect A B in regards to Atomicity (

// Update Client Conn State comes in, writes a config
// Picker update sends picker based on config

// Picker update sends picker (without an initial UpdateClientConnState() config
// and building of child, no picker will ever get sent up, so good there)
// UpdateClientConnState() comes in, writes a new config, pings picker
// to send a new picker, good here ***triage on how other balancer handle this






// 3. child - built on first UpdateClientConnState() received using config, then
// forwarded down, forwarded down if already there, ***this is also called in
// eject() and uneject() in the scw

// other operations after outlier detection specific logic
// if child != nil {

// can close and nil here, unless you sync Close() as a whole atomic operation
// with all the other operations, see how other balancers do it

// child.sameOperation
// }

// niled during close

// Doesn't get read in interval timer algo

// Written in UpdateClientConnState (built on first successful update)
// Written in Close (niled "cleared" on Close())
// Read (should be same pointer I think) in eject and uneject...how do we do this? (even if not same pointer, still in state closed or not closed), can't update after close

// eject() gets called in, child.UpdateSubConnState(scw, TRANSIENT_FAILURE)

// 1. NewSubConn() (synced with close since update from grpc)
// 2. UpdateAddresses (not synced with close since balancer -> grpc)
// 3. interval timer algo (for sure gonna sync with UpdateClientConnState(), should I also sync with close()), have one operation happen at once interval timer, updateClientConnState(), and Close()?

// uneject() gets called in, child.UpdateSubConnState(scw, scw.latestState)

// 1. UpdateAddresses (not synced with close since balancer -> grpc)
// 2. interval timer algo



// How graceful switch handled this child being closed problem: Had the whole
// operation be atomic with Close()...close on an updateState() call, read in
// UpdateSubConnState() (and only UpdateSubConnState() had this problem with
// Close()). Now, read in UpdateAddresses and interval timer algo, (and
// NewSubConn() - but synced with close()).

// Find examples in the codebase: forwarding an update down, with something that
// can concurrently close the balancer in between if child == nil { (child gets
// deleted) child.forward }



// Read for other forwarding operations...


// Close() is guaranteed to be called sync with other balancer methods
// if closed.HasFired() (too defensive, up to you if you want to add)

// nil check on child balancer - handles nil on close and hasn't been built
// you're good, this doesn't need sync with close because it can't be closed
// during a sync call grpc -> balancer, unlike balancerCurrent which can be deleted
// by a concurrent UpdateState() call.

// none of the balancer.Balancer calls can happen sync

// Buckets of stuff that can happen sync (balancer.Balancer API, interval timer algo, balancer.ClientConn method1, balancer.ClientConn method2, balancer.ClientConn method3)
// but you're syncing UpdateClientConnState from balancer.Balancer and interval timer



// 4. scWrappers

// read in UpdateSubConnState() to map sc (from parent) -> scw (what child knows)

// What if in between reading scw = scw[sc]

// scw gets removed...does this break it, scw can't get removed because no concurrent balancer.Balancer call

// UpdateSubConnState(scw, state) // if this call is in mutex, I feel like this can callback and get deadlocked

// written to in UpdateSubConnState() deleting any SubConns with state SHUTDOWN


// written to in NewSubConn to persist the sc -> scw relationship (has both data
// types in that function)

// My initial feeling tells me no weird behavior things if you just protect the
// reads and writes, the NewSubConn write simply appends to the the map (and only thing that can happen concurrently), not
// deletes what is currently there.


// Say you sync the three operations UpdateClientConnState, interval timer algo
// and Close(), and also scWrappers read/write, what other things would you have to sync

// Mu lock

// mu unlock

// can be closed before updating, but need to release lock earlier to
// prevent deadlock
// UpdateClientConnState has child.UpdateClientConnState (can call back inline)

// Can protect the whole operation and make each operation atomic
// if you put into a run() goroutine...but that's last resort "no right answer"



// Fields in balancer, SubConn (and methods), callcounter, etc.

// When you switch to run, need to move stuff around to handleClientConnUpdate etc.



// mutex locks/unlocks first

// then run goroutine if needed to prevent deadlock

// mutex lock/unlock don't call into other system as that has high possibility of deadlock

// a call being blocking rather than put on a run, parent waits for it to return before calling
// another operation that is a plus

// defensive programming against close() is grpc violating the balancer API, I think I want to add
// this regardless though
