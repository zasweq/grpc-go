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
	odAddrs *resolver.AddressMap // concurrent accesses - determines behavior etc.

	// map[string]object{A, B, C, D}
	// read/write to object protect it from happening concurrently
	// map only thing with reference to value...? Scw? Can you use that
	// to protect writes

	// if you want object to be protected, if map only ref than can do it that way atomic(read + write)
	// if other things have pointer to object (on the heap part of memory), I'm pretty sure scw does then you need to gate
	// that as well with a mutex

	// scw.obj read/written to in Picker Callback when an RPC is done...
	// also read/written to in UpdateAddresses scw.obj

	// Two refs to obj - from map when you get map and also from scw which has a ref to object, both refs are used to
	// update object in this balancer, so if you sync those 3 or 4 or 5 events will this be covered?

	// event pinging picker update, needs to have a significant diff from
	// previous picker and also (picker != nil) special case first update


	// lock
	// obj := map.Get (simplified) // this access needs to be synced, multiple accesses cannot be performed concurrently
	// obj.write (write to object) // (obj.write, obj.write)
	// unlock

	// (scw.obj) = nil // pointer read/write needs to be synced
	// obj := map.Get, map.Set(obj)

	// writing to what that pointer points to also needs sync

	/*
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
	*/

	// Can you concurrently write a.b and a.c? if a is just a pointer, but write to b would displace c right?

	// All 4 data points of type object need sync.

	// callCounter gets called/written to a lot (should we have mutex only for call counter - inside the object and at what level?)
	// latestEjectionTimeStamp gets read to determine if an address has been ejected...
	// ejectionTimeMultiplier is called happens before in interval timer algo going off
	// sws gets read/written to a lot

	// only callCounter (activeBucket and inactiveBucket) and sws get written to
	// through scw...sws not the actual data type, just the array of pointers
	// appended/deleted from



	// "Multiple accesses may not be performed concurrently."

	// Most important underlying data structure:
	// Read and written to in UpdateClientConnState, updating map based on address diff, add/delete(addr, object ref), does go have implicit ref counting?

	// Read in NewSubConn to determine whether to add to scw list

	// Read/written to in UpdateAddresses (a lot) to update map with new subconn's address

	// Read a lotttttt in interval timer algo
	// also, value types are the things that interface with the object

	// mutex lock

	// read value from map
	// value.callCounter/wrappedSubConn write/read

	// mutex unlock



	// mutex lock

	// read value from map
	// value.callCounter/wrappedSubConn write/read

	// mutex unlock

	// implicitly protects callCounter/wrappedSubConn ^^^ since inside the mutex
	// (scw needs to call with dedicated mutex/sync thing such as run() or an
	// atomic protecting)


	// mutex lock

	// read value from map

	// mutex unlock

	// value.callCounter/wrappedSubConn write/read...have two things that read this pointer to the
	// heap and concurrently write to it, you now have a race condition





	// behaviors to keep consistent?


	// Avoid overtraining (cardio 1+ hour), you miss the next day anyway, so
	// it's like the same time except much more optimal an hour a day, and
	// alcohol *** please, so you don't get depressed/sick/suicidal

	// two things differentiate me: I am healthy with real food,
	// I am working a hyper intellectual job



	numAddrsEjected int // For fast calculations of percentage of addrs ejected
	// read, read, read, write, write ... etc. all happen
	// in atomic Interval timer algo...do we need to make this atomic with every other operation
	// or just UpdateClientConnState?


	odCfg *LBConfig
	// If we sync UpdateClientConnState and interval timer algo, this doesn't
	// need protecting happens before with the two...any other reads or writes
	// or usages in other operations? If not, syncing those two events will protect this

	// Synced:
	// UpdateClientConnState()
	// Interval Timer algo(), a lotttt of reads

	// More:
	// wrong, this gets read to determine no-op picker in UpdateState()...
	// that's it, "race with picker update", determines if no-op config


	cc balancer.ClientConn // Priority parent right?
	// Only written to at build time so no protection needed




	bOpts balancer.BuildOptions
	// Only written to at build time so no protection needed


	child balancer.Balancer // cluster impl - when is this built? See others for example, is it in Build()? This gets built in different places in cluster impl, so, it has nil checks on forwarding it
	// clusterimpl config as part of the OD config, can be different enough to warrant a new creation of this child?
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





	timerStartTime time.Time // The outlier_detection LB policy will store the timestamp of the most recent timer start time. Also used for ejecting addresses per iteration
	// If we sync UpdateClientConnState and interval timer algo, this doesn't need any protecting, reads
	// and writes happen happens before and never concurrently, we're good here



	intervalTimer *time.Timer
	// Again, written to in UpdateClientConnState()
	// channel receive in run()
	// No sync needed


	// map sc (all parent knows about) -> scw (all children know about, this balancer wraps scs in scw)...explain and talk about why you need this map
	// clusterimpl also has a mutex protecting this
	scWrappers map[balancer.SubConn]*subConnWrapper // concurrent accesses - determines behavior etc. think deeply about this
	// read in UpdateSubConnState to try and map subconn sent from parent
	// to subconn wrapper.
	// written to in UpdateSubConnState (deleting any SubConns with state shutdown)

	// written to in NewSubConn (duh, when it constructs the wrapper from the subconn the parent creates,
	// it needs a way of mapping that to correct SubConn)




	// These two vvv can help with synchronization problems reasoned about ^^^

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
	// mu lock
	scw, ok := b.scWrappers[sc]
	// mu unlock
	if !ok {
		// Return, shouldn't happen if passed up scw
		// Because you wrap subconn always, clusterimpl i think only some
		return
	}
	if state.ConnectivityState == connectivity.Shutdown {
		// Remove this SubConn from the map on Shutdown.
		// mu lock
		delete(b.scWrappers, scw.SubConn) // Just deletes from map doesn't touch underlying heap memory
		// mu unlock
	}
	scw.latestState = state // do we need this write if shutdown?, also this gets read in uneject()...I think interval timer can run concurrently
	// what if subconn gets deleted here? from another call to UpdateSubConnState()?, no, UpdateSubConnState()
	// calls are guaranteed to be called synchronously, only thing that can happen is NewSubConn makes
	// a new map entry
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
	// mu lock
	b.scWrappers[sc] = scw
	// mu unlock
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

	// Grounded blob: look over/cleanup/make sure this algorithm is correct. **Do this, because I already
	// found a few correctness issues with it.

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
	callCounter callCounter // should this be a pointer?

	// The latest ejection timestamp, or null if the address is currently not ejected
	latestEjectionTimestamp time.Time // We represent the branching logic on the null with a time.Zero() value

	// The current ejection time multiplier, starting at 0
	ejectionTimeMultiplier int64

	// A list of subchannel wrapper objects that correspond to this address
	sws []*subConnWrapper
}


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
// important to think about and learn, my gut tells me to implement target and resolve now and have balancer implement ClientConn,
// and maybe have wrapped picker also wrap not embed?

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




// Behavior to keep consistent:
// currentMu makes sure UpdateSubConnState() happens atomically
// with Close()

// Not an interspliced read/write

// but UpdateSubConnState()
// (
// 1 // read, still protected with read/write mutex so ok in regards to concurrent read/write
// Close interspliced here - concurrently, not protected with mutex - behavior mutex - this is wrong (looks like you can have one mutex serve both functionalities)
// 2
// ) bad behavior


// (
// 1
// 2
// ) A

// (
// Close
// ) B

// AB or BA, not interspliced...is everything else ok with AB BA or does stuff happen that causes wonky behavior?

// And then reads and writes

// Make sure you're not holding mutex when calling into another system



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
