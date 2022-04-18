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
	"google.golang.org/grpc/resolver"
	"math"
	"math/rand"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/serviceconfig"
)

// Name is the name of the outlier detection balancer.
const Name = "outlier_detection_experimental"

func init() {
	balancer.Register(bb{})
}

type bb struct{}

func (bb) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {

	// go b.run() <- way of synchronizing
	return &outlierDetectionBalancer{}
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

// Priority parent update client conn state with what

// child lb is cluster impl

type outlierDetectionBalancer struct {
	// What state do we need?
	// TODO: Switch this resolver.Address to a string - this needs to be documented in gRFC as well
	// This gets populated in UpdateClientConnState...
	// When the outlier_detection LB policy receives an address update, it will
	// create a map entry for each subchannel address in the list, and remove
	// each map entry for a subchannel address not in the list.
	odAddrs map[string]/*pointer or not pointer*/*object // This is the central data structure to everything // Now that this is a pointer, does this break anything?

	numAddrsEjected int // For fast calculations of percentage of addrs ejected

	ejectionTime time.Time // timestamp used ejecting addresses per iteration

	// wil get read a lot for the actual Outlier Detection algorithm itself
	odCfg *LBConfig

	cc balancer.ClientConn

	// child policy balancer.Balancer (cluster impl)
	child balancer.Balancer // cluster impl - when is this built?

	// I plan to sync all operations with a run() goroutine - so no need for mutexes?
}

// What operations do we need? What can talk to this in regards to other parts of the system?
// I plan to sync all operations with a run() goroutine - so no need for mutexes?

func (b *outlierDetectionBalancer) UpdateClientConnState(s balancer.ClientConnState) error {

	// when is this originally built/updated/what happens on config changes
	// that change the knobs configuring this policy?

	// Simply persist the config? Do this first, everything is dependent on this
	lbCfg, ok := s.BalancerConfig.(*LBConfig)
	if !ok {
		// b.logger.Warningf("xds: unexpected LoadBalancingConfig type: %T", s.BalancerConfig)
		return balancer.ErrBadResolverState
	}
	b.odCfg = lbCfg

	// Create a new interval timer here - with Mark's thing of 5 + (3), not (8)

	// Everything that gets read from the config is knobs on the algorithm, which gets run every interval timer
	// Persist the config and read fields from those.
	// The only question about what to do is if the interval changes? My gut tells me to reset the whole countdown
	// even if in between an iteration, and construct a whole new timer.


	// I think persisting the config and reading from that in regards to
	// algorithms etc.

	// What to do about an already running interval timer, clear and make a new
	// one? I think I'll do that. Yeah I think resetting and making a whole
	// interval timer makes the most sense, especially in regards to
	// synchronization guarantees on the two algorithms running.



	// As the system is running, a whole new configuration can be specified to
	// configure the system. Parts of the system that are running concurrently
	// (i.e. success rate/failure percentage algorithms) that are dependent on
	// the config can be running as new configuration comes in.

	// Thus, wrap each operation with a mu (or I guess a run() goroutine would
	// guarantee the two operations don't happen synchronously).


	// Perhaps move to a handle function - after coding

	s.ResolverState.Addresses // []resolver.Address

	// When the outlier_detection LB policy receives an address update, it will
	// create a map entry for each subchannel address in the list, and remove
	// each map entry for a subchannel address not in the list, then pass the
	// address list along to the child policy.

	// When the outlier_detection LB policy receives an address update, it will
	// create a map entry for each subchannel address in the list, and remove
	// each map entry for a subchannel address not in the list

	// Algorithm here that I wrote in aggregate clusters
	// Figure out initialization of objects with what values etc.
	s.ResolverState.Addresses // What to do with Attributes and Service Config?

	// s.ResolverState.Addresses[0]. // this has the addr string inside it. What to do about this addr string? build a set?

	// This list of addresses is used for the algorithm


	addrs := make(map[string]bool) // for fast lookups during deletion phase

	// Create a map entry for each subchannel address in the list (create a whole new one or create new ones for new addresses?)
	// as you are iterating through, build a set for o(1) access during the remove stage using
	// the Addresses.Addr field
	for _, addr := range s.ResolverState.Addresses {
		addrs[addr.Addr] = true // use this to build a set

		// create a whole new map entry or keep what's currently there?
		// keep what's currently there
		if _, ok := b.odAddrs[addr.Addr]; ok { // Not yet present
			// create a whole new one
			b.odAddrs[addr.Addr] = &object{} // Do we need to initialize any part of this or are zero values sufficient?
		}
	}

	// remove each map entry for a subchannel address not in the list
	// iterate through addrs in map
	for addr, _ := range b.odAddrs {
		// if key not in set remove
		if !addrs[addr] {
			delete(b.odAddrs, addr)
		}
		// do we have to cleanup the object?

	}

	// cleanup for this whole component?


	// then pass the address list along to the child policy.
	b.child.UpdateClientConnState(s)

	// s.balancerConfig - configuration for this outlier detection
	return nil
}

func (b *outlierDetectionBalancer) ResolverError(err error) {

}

func (b *outlierDetectionBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	// what type is this sc? It's called from higher level so is this sc or scw?
	// Cluster Impl persists a map that maps sc -> scw...see how it's done in other parts of codebase



	// it gets to a point where local var on the stack is scw, whether it does this through typecasting (passed in as a scw
	// vs. ?) need to persist state etc. (if it is wrapped subconn, it's passed up as wrapper, unlike cluster impl,
	// which doesn't pass up wrapped subconn, it abstracts it upward as a normal.) **confirm whether you pass up
	// up wrapper in other parts of codebase, I think you do

	scw, ok := sc.(subConnWrapper)
	if !ok {
		// Return, shouldn't happen if passed up scw
	}
	if scw.ejected { // Is this all you need? Feels bare
		scw.latestState = state
	} else {
		scw.latestState = state
		b.child.UpdateSubConnState(scw, state)
	}


	// gate this call if the address is ejected
	// don't forward this down if address is ejected
	// if (scw.ejected (state determined by other operations))
	//       persist this state (in the scw)
 	// else
	//       forward this down
}

func (b *outlierDetectionBalancer) Close() {

}

func (b *outlierDetectionBalancer) ExitIdle() {

}


// The outlier_detection LB policy will provide a picker that delegates to
// the child policy's picker, and when the request finishes, increment the
// corresponding counter in the map entry referenced by the subchannel
// wrapper that was picked.
type wrappedPicker struct {
	// childPicker, embedded or wrapped
	childPicker balancer.Picker // written to/constructed in UpdateState
}

func (wp *wrappedPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	// How does the picker work logically?

	pr, err := wp.childPicker.Pick(info)
	if err != nil {
		return balancer.PickResult{}, err
	}
	pr.SubConn // balancer.SubConn - should be wrapped SubConn type, wraps SubConn before sending it down to child balancer, so the child balancer's Pick result should be a SubConnWrapper, implicitly deals with a wrapped subconn (i.e. not dependent on...) because it comes from child
	pr.Done // this comes back, wrap it with yours?, this function gets passed data from grpc that you need, need to plumb both into same function, needs both pieces of data
	// Dealing with the scw, not the sc, whether you get that through map or something,
	// needs to be scw

	done := func(di balancer.DoneInfo) {
		incrementCounter(pr.SubConn, di)
		pr.Done(di)
	}

	return balancer.PickResult{
		SubConn: pr.SubConn, // Should this be scw or sc? If sc, look reverse in map?
		Done: done,
	}, nil

	// Done is providing the balancer info
	// grpc (info about RPC)-> balancer
	// almost like a handle function, needs both data about RPC (successful || error)...to me determined by the data passed down to balancer about RPC in DoneInfo...err being nil vs. not nil
	// and also the scw to get the ref to the map entry

	// grpc asks picker what SubConn to use, gives it info about RPC
	// Picker sends back a SubConn and a Done function
	// Once the RPC is complete, the Done function is called with data about the RPC
}

// done
// wraps done - calls child's done
// if you use an inline function, it can take a closure on the scw that was picked, and send it to increment counter
// calls incrementCounter...but how do you know SubConn that was picked...capture it (closure)...since it's per RPC and on a single SubConn
// "pick a SubConn to send an RPC"...***per rpc***, tied to a SubConn, capture it and send it to increment counter

func incrementCounter(sc balancer.SubConn, info balancer.DoneInfo) {
	scw, ok := sc.(subConnWrapper)
	if !ok {
		// Shouldn't happen, as comes from child
		return
	}
	if info.Err != nil {
		scw.obj.callCounter.activeBucket.numSuccesses++ // is this the thing that needs to protected by the mutex?
	} else {
		scw.obj.callCounter.activeBucket.numFailures++
	}
}


func (b *outlierDetectionBalancer) UpdateState(s balancer.State) {
	b.cc.UpdateState(balancer.State{ // Is this all you need?
		ConnectivityState: s.ConnectivityState,
		Picker: &wrappedPicker{
			childPicker: s.Picker,
		},
	})
	// The outlier_detection LB policy will provide a picker that delegates to
	// the child policy's picker, and when the request finishes, increment the
	// corresponding counter in the map entry referenced by the subchannel
	// wrapper that was picked.

	// Picker: Pick(info PickInfo) (PickResult, error)
	// What to do with connectivity state?
	// wraps child picker
	s.Picker // child picker
	// type PickResult struct {
	//   SubConn // wrapped SubConn, main wrapped picker
	//   Done // "increment the corresponding counter in the map entry referenced by scw that was picker, but only if one of the algorithms is set
	// }

	// related to algorithms: is no-op logically treated same as any config with both unset, if both are unset
	// other parts of config can be set that aren't maxing interval

	// get scw out of the picker somehow
	s.Picker // wrap this
}

func (b *outlierDetectionBalancer) NewSubConn(addrs []resolver.Address, opts balancer.NewSubConnOptions) (balancer.SubConn, error) { // Intercepts call
	// RELOOK OVER THIS AFTER DONE. DRAFT

	// When the child policy asks for a subchannel, the outlier_detection will
	// wrap the subchannel with a wrapper (see Subchannel Wrapper section).
	sc, err := b.cc.NewSubConn(addrs, opts)
	// Wait, the SubConn passed downward from parent policy is a sc constructed
	// in the parent policy. Thus, it has no knowledge of this types scw. like
	// cluster impl, need a map from sc -> scw (how is this keyed?), Eric says
	// wrap every single SubConn, UpdateAddrs to single needs to keep wrapper or
	// if it switches to something in map

	// ** Ask Eric and Menghan about this in person. Should this still be done?
	// I.e. Wrap SubConn for every sc, because of special logic with regards to UpdateAddrs

	// Also clairfy with Menghan about what we decided to do for Agg. Clusters tomorrow in person

	if err != nil {
		return nil, err
	}
	scw := &subConnWrapper{SubConn: sc}
	// Then, the subchannel wrapper will be added to the list in the map entry
	// for its address, if that map entry exists. If there is no map entry, or
	// if the subchannel is created with multiple addresses, the subchannel will
	// be ignored for outlier detection. (duh, so you keep SubConn's normal
	// functioning outside of the functionality of the outlier detection, why
	// there is "if one exists" clause in SubChannel Wrapper section.)



	// Wrap regardless
	// If one of these conditionals hits VVV
	//		still wrap just don't persist in map
	//		early return
	// else (implicit from early return)
	//		persist in map
	// 		eject if needed

	// If we "ignore" subchannel - do we still wrap it? Is it just a logical no-op at this level (i.e. just pass the subconns both ways alone?)
	// From discussion with Eric and UpdateAddresses...it seems like you'll need to wrap anyway for UpdateAddresses...
	if len(addrs) != 1 {
		// "Ignore subchannel for outlier detection"
		// What
		return scw, nil
	}

	if _, ok := b.odAddrs[addrs[0].Addr]; !ok {
		// "Ignore subchannel for outlier detection" - what does this mean now that
		// we wrap everything anyway...wrap it but don't persist in map. ** Take this and actually implement it
		// map persistence will come later on UpdateAddress call.
		return scw, nil
	}

	// AFTER FINISH THIS FUNCTION CLEANUP WHOLE PROJECT TO SEE WHERE I'M AT I feel like I'm close

	// scw // All this data you can either interact with here or do it somewhere else


	b.odAddrs[addrs[0].Addr].sws = append(b.odAddrs[addrs[0].Addr].sws, *scw) // or do we make this an array of pointers to scw and don't dereference?

	// If that address is currently ejected, that subchannel wrapper's eject
	// method will be called.

	// Logical invaraint of the type - "The latest ejection timestamp, or null if the address is currently not ejected"
	// if mapentry...latest ejection timestamp != null
	//      scw.eject()
	if !b.odAddrs[addrs[0].Addr/*could also read this into a seperate variable*/].latestEjectionTimestamp.IsZero() {
		scw.eject()
	}
	return scw, nil
}

// don't reuse same cluster/target, have a max depth

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

func (b *outlierDetectionBalancer) run() {

	// interval timer receive (need some way of representing interval timer with a trigger) this logic can either be inline or in a handle function
	// Every interval trigger (determined by config):

	// 1. Record the timestamp for use when ejecting addresses in this iteration. "timestamp that was recorded when the timer fired"
	b.ejectionTime = time.Now() // I can write it here - is it because it's not a value..."referenced by the subchannel wrapper that was picked" - so object value needs to be a pointer?
	// ejectionTime could be a field plumbed through methods defined - but writing to a field
	// makes more sense?

	// 2. For each address, swap the call counter's buckets in that address's map entry. // Question, so we're using the inactive bucket then for the data to read - I'm pretty sure
	for _, obj := range b.odAddrs {
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

	// For each address in the map:
	for addr, obj := range b.odAddrs {
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
func (b *outlierDetectionBalancer) numAddrsWithAtLeastRequestVolume(/*requestVolume uint32 - you don't need this, you can pull this from config stored in balancer*/) uint32 { // either have this return a boolean or just return the number
	var numAddrs uint32 // will probably need to change this type
	for _, obj := range b.odAddrs { // "at least"
		if uint32(obj.callCounter.inactiveBucket.requestVolume) >= b.odCfg.SuccessRateEjection.RequestVolume {
			numAddrs++
		}
	}
	return numAddrs
}

// meanAndStdDevOfSucceseesAtLeastRequestVolume returns the mean and std dev of the number of requests
// of addresses that have at least requestVolume.
func (b *outlierDetectionBalancer) meanAndStdDevOfSuccessesAtLeastRequestVolume(/*requestVolume uint32 - you don't need this, you can pull this from config stored in balancer*/) (float64, float64) {
	// 2. Calculate the mean and standard deviation of the fractions of
	// successful requests among addresses with total request volume of at least
	// success_rate_ejection.request_volume.
	var totalFractionOfSuccessfulRequests float64 // could also pull this out into a helper for cleanliness
	var mean float64 // Right type?
	// var stddev float64 // Right type?

	for _, obj := range b.odAddrs {
		// "of at least success_rate_ejection.request_volume"
		if uint32(obj.callCounter.inactiveBucket.requestVolume) >= b.odCfg.SuccessRateEjection.RequestVolume { // Is inactive bucket the right one to look at?
			totalFractionOfSuccessfulRequests += float64(obj.callCounter.inactiveBucket.numSuccesses)/float64(obj.callCounter.inactiveBucket.requestVolume) // Does this cause any problems...? This shouldn't, just adds 000000000 decimals to the end of it
		}
	}
	mean = totalFractionOfSuccessfulRequests / float64(len(b.odAddrs)) // TODO: Figure out types - should this be a float and what's on the left? I feel like with means/std dev you need float for decimal


	// to calculate std dev:
	// Find each scores deviation from the mean - makes sense for me to use decimal points as well, but to what precision

	// Square each deviation from the mean

	// Find the sum of squares

	var sumOfSquares float64

	for _, obj := range b.odAddrs { // Comparing the fractions of successful requests
		// either calculate it inline or store a list and divide
		devFromMean := float64(obj.callCounter.inactiveBucket.numSuccesses) / float64(obj.callCounter.inactiveBucket.requestVolume)
		sumOfSquares += devFromMean * devFromMean
	}

	variance := sumOfSquares / float64(len(b.odAddrs))

	// Find the variance - divide the sum of the squares by n (it's population because you use every data point)
	// Take square root of the variance - you now have std dev
	return mean, math.Sqrt(variance)

}

func (b *outlierDetectionBalancer) successRateAlgorithm() {
	// b.config.fields


	// request volume I think is num of success + num of failures, either have
	// this as a field of call counter or calculate it every time.

	// request volume is used in a lot of places in this algorithm, seems like
	// it should be precalculated within the counter object. Also, it's used as
	// the denominator for a lot of parts of the algorithm (successes + failures).

	// ^^^ vvv field of call counter or calculate it every time?

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



	if b.numAddrsWithAtLeastRequestVolume()/*number of addresses with request volume of at least success_rate_ejection.request volume*/ < b.odCfg.SuccessRateEjection.MinimumHosts {
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


	// 3. For each address:
	for addr, obj := range b.odAddrs {
		// If the percentage of ejected addresses is greater than max_ejection_percent, stop.
		//
		if float64(b.numAddrsEjected) / float64(len(b.odAddrs)) * 100 > float64(b.odCfg.MaxEjectionPercent) {
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

	// 	3a. If the percentage of ejected addresses is greater than max_ejection_percent, stop.
	//  3b. If the address's total request volume is less than success_rate_ejection.request_volume, continue to the next address.
	//  3c. If the address's success rate is less than (mean - stdev * (success_rate_ejection.stdev_factor / 1000)), then choose a random integer in [0, 100). If that number is less than success_rate_ejection.enforcement_percentage, eject that address.

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

	// Request volume (num successes + num failures) <- per address



	// 1. If the number of addresses (len(map)) is less than failure_percentage_ejection.minimum_hosts, stop.
	if uint32(len(b.odAddrs)) < b.odCfg.FailurePercentageEjection.MinimumHosts {
		return
	}

	// 2. for each address
	for addr, obj := range b.odAddrs {
		// If the percentage of ejected addresses is greater than max_ejection_percent, stop.

		// ejected address int as state, add 1 to it when ejected, subtract 1 from it when unejected

		// for loop through addrs, count number of ejected addresses/ len(b.odAddrs)

		// Needs to be a float * 100 VVV
		// float64(b.numAddrsEjected) / float64(len(b.odAddrs)) * 100 > float64(b.odCfg.MaxEjectionPercent)
		if float64(b.numAddrsEjected) / float64(len(b.odAddrs)) * 100 > float64(b.odCfg.MaxEjectionPercent) {
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

func (b *outlierDetectionBalancer) ejectAddress(addr string/*key entry of map - o(1) access*/) { // I think it makes more sense to do this inline
	b.numAddrsEjected++
	// To eject an address (i.e. a single map entry in the map this balancer holds):
	// obj := b.odAddrs[key] (to remove verbosity)

	// set the current ejection timestamp to the timestamp that was recorded when the timer fired
	b.odAddrs[addr].latestEjectionTimestamp = b.ejectionTime/*timestamp that was recorded when the timer fired - need to plumb this in somehow*/
	// increase the ejection time multiplier by 1
	b.odAddrs[addr].ejectionTimeMultiplier++
	// call eject() on each subchannel wrapper in that address's subchannel wrapper list. (plural! on all the subchannels that connect to that upstream)
	for _, sbw := range b.odAddrs[addr].sws {
		sbw.eject()
	}
}

func (b *outlierDetectionBalancer) unejectAddress(addr string/*key entry of map - o(1) access*/) {
	b.numAddrsEjected--
	// set the current ejection timestamp to null (doesn't he mean latest ejection timestamp?)
	b.odAddrs[addr].latestEjectionTimestamp = time.Time{} // or nil if not currently ejected - logically equivalent to time zero value

	// call uneject() on each subchannel wrapper in that address's subchannel wrapper list
	for _, sbw := range b.odAddrs[addr].sws {
		sbw.uneject()
	}
}

type object struct { // Now that this is a pointer, does this break anything?
	// The call result counter object
	callCounter callCounter

	// The latest ejection timestamp, or null if the address is currently not ejected
	// time.Time?
	latestEjectionTimestamp time.Time // Pointer allows branching logic on the null

	// The current ejection time multiplier, starting at 0
	ejectionTimeMultiplier int64

	// A list of subchannel wrapper objects that correspond to this address - [] plural!
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


// Figure out linkage between a wrapped SubConn and the balancer...the SubConn API doesn't do anything.
// The SubConn is part of the balancer.Balancer API, and is an expression in some of the return/arguments.
// See example somewhere in codebase?


// Wrapped picker that increments corresponding counter in map entry <- this I don't know if it's an operation or part of an existing part of UpdateState!

// Interval timer going off and triggering eject/uneject behavior (also synced with others based on run() operation? Does that sound right?)


// I don't think any of these operations has sync issues if we put it on the run goroutine
// I should write out the logic for each operation, and then see if any weird racey things pop up