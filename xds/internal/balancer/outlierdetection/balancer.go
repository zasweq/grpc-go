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

// Package outlierdetection provides an implementation of the outlier detection
// LB policy, as defined in
// https://github.com/grpc/proposal/blob/master/A50-xds-outlier-detection.md.
package outlierdetection

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/buffer"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/grpcrand"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

// Globals to stub out in tests.
var (
	afterFunc = time.AfterFunc
	now       = time.Now
)

// Name is the name of the outlier detection balancer.
const Name = "outlier_detection_experimental"

func init() {
	if envconfig.XDSOutlierDetection {
		balancer.Register(bb{})
	}
	// TODO: Remove these once the Outlier Detection env var is removed.
	internal.RegisterOutlierDetectionBalancerForTesting = func() {
		balancer.Register(bb{})
	}
	internal.UnregisterOutlierDetectionBalancerForTesting = func() {
		internal.BalancerUnregister(Name)
	}
}

type bb struct{}

func (bb) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	b := &outlierDetectionBalancer{
		cc:             cc,
		bOpts:          bOpts,
		closed:         grpcsync.NewEvent(),
		odAddrs:        make(map[string]*object),
		scWrappers:     make(map[balancer.SubConn]*subConnWrapper),
		scUpdateCh:     buffer.NewUnbounded(),
		pickerUpdateCh: buffer.NewUnbounded(),
	}
	go b.run()
	return b
}

func (bb) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	var lbCfg *LBConfig
	if err := json.Unmarshal(s, &lbCfg); err != nil { // Validates child config if present as well.
		return nil, fmt.Errorf("xds: unable to unmarshal LBconfig: %s, error: %v", string(s), err)
	}

	// Note: in the xds flow, these validations will never fail. The xdsclient
	// performs the same validations as here on the xds Outlier Detection
	// resource before parsing into the internal struct which gets marshaled
	// into JSON before calling this function. A50 defines two separate places
	// for these validations to take place, the xdsclient and this ParseConfig
	// method. "When parsing a config from JSON, if any of these requirements is
	// violated, that should be treated as a parsing error." - A50

	switch {
	// "The google.protobuf.Duration fields interval, base_ejection_time, and
	// max_ejection_time must obey the restrictions in the
	// google.protobuf.Duration documentation and they must have non-negative
	// values." - A50
	// Approximately 290 years is the maximum time that time.Duration (int64)
	// can represent. The restrictions on the protobuf.Duration field are to be
	// within +-10000 years. Thus, just check for negative values.
	case lbCfg.Interval < 0:
		return nil, fmt.Errorf("OutlierDetectionLoadBalancingConfig.interval = %s; must be >= 0", lbCfg.Interval)
	case lbCfg.BaseEjectionTime < 0:
		return nil, fmt.Errorf("OutlierDetectionLoadBalancingConfig.base_ejection_time = %s; must be >= 0", lbCfg.BaseEjectionTime)
	case lbCfg.MaxEjectionTime < 0:
		return nil, fmt.Errorf("OutlierDetectionLoadBalancingConfig.max_ejection_time = %s; must be >= 0", lbCfg.MaxEjectionTime)

	// "The fields max_ejection_percent,
	// success_rate_ejection.enforcement_percentage,
	// failure_percentage_ejection.threshold, and
	// failure_percentage.enforcement_percentage must have values less than or
	// equal to 100." - A50
	case lbCfg.MaxEjectionPercent > 100:
		return nil, fmt.Errorf("OutlierDetectionLoadBalancingConfig.max_ejection_percent = %v; must be <= 100", lbCfg.MaxEjectionPercent)
	case lbCfg.SuccessRateEjection != nil && lbCfg.SuccessRateEjection.EnforcementPercentage > 100:
		return nil, fmt.Errorf("OutlierDetectionLoadBalancingConfig.SuccessRateEjection.enforcement_percentage = %v; must be <= 100", lbCfg.SuccessRateEjection.EnforcementPercentage)
	case lbCfg.FailurePercentageEjection != nil && lbCfg.FailurePercentageEjection.Threshold > 100:
		return nil, fmt.Errorf("OutlierDetectionLoadBalancingConfig.FailurePercentageEjection.threshold = %v; must be <= 100", lbCfg.FailurePercentageEjection.Threshold)
	case lbCfg.FailurePercentageEjection != nil && lbCfg.FailurePercentageEjection.EnforcementPercentage > 100:
		return nil, fmt.Errorf("OutlierDetectionLoadBalancingConfig.FailurePercentageEjection.enforcement_percentage = %v; must be <= 100", lbCfg.FailurePercentageEjection.EnforcementPercentage)
	case lbCfg.ChildPolicy == nil:
		return nil, errors.New("OutlierDetectionLoadBalancingConfig.child_policy must be present")
	}

	return lbCfg, nil
}

func (bb) Name() string {
	return Name
}

// scUpdate wraps a subConn update to be sent to the child balancer.
type scUpdate struct {
	scw   *subConnWrapper
	state balancer.SubConnState
}

type ejectedUpdate struct {
	scw     *subConnWrapper
	ejected bool // true for ejected, false for unejected
}

type outlierDetectionBalancer struct {
	numAddrsEjected int // For fast calculations of percentage of addrs ejected

	childState       balancer.State
	recentPickerNoop bool

	closed *grpcsync.Event
	cc     balancer.ClientConn
	bOpts  balancer.BuildOptions

	// childMu protects child and also updates to the child (to uphold the
	// balancer.Balancer API guarantee of synchronous calls). It also protects
	// against run() reading that the child is not nil for SubConn updates, and
	// then UpdateClientConnState or Close writing to the the child.
	childMu sync.Mutex
	child   balancer.Balancer

	// mu guards access to a lot of the core LB Policy State. It also prevents
	// intersplicing certain operations.
	//
	// ex 1: interval timer goes off, outlier detection algorithm starts running
	// based on knobs in odCfg. in the middle of running the algorithm, a
	// ClientConn update comes in and writes to odCfg. This causes undefined
	// behavior for the interval timer algorithm.
	//
	// ex 2: Updating the odAddrs map from UpdateAddresses in the middle of
	// running the interval timer algorithm which uses odAddrs heavily. This
	// will cause undefined behavior for the interval timer algorithm.
	mu             sync.Mutex
	odAddrs        map[string]*object
	odCfg          *LBConfig
	scWrappers     map[balancer.SubConn]*subConnWrapper
	timerStartTime time.Time
	intervalTimer  *time.Timer

	scUpdateCh     *buffer.Unbounded
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
		return balancer.ErrBadResolverState
	}

	// Reject whole config if any errors, don't persist it for later
	bb := balancer.Get(lbCfg.ChildPolicy.Name)
	if bb == nil {
		return fmt.Errorf("balancer %q not registered", lbCfg.ChildPolicy.Name)
	}

	if b.child == nil || b.odCfg.ChildPolicy.Name != lbCfg.ChildPolicy.Name {
		b.childMu.Lock()
		if b.child != nil {
			b.child.Close()
		}
		// What if this is nil? Seems fine
		b.child = bb.Build(b, b.bOpts)
		b.childMu.Unlock()
	}

	b.mu.Lock()
	b.odCfg = lbCfg

	// When the outlier_detection LB policy receives an address update, it will
	// create a map entry for each subchannel address in the list, and remove
	// each map entry for a subchannel address not in the list.
	addrs := make(map[string]bool, len(s.ResolverState.Addresses))
	for _, addr := range s.ResolverState.Addresses {
		addrs[addr.Addr] = true
		b.odAddrs[addr.Addr] = newObject()
	}
	for addr := range b.odAddrs {
		if !addrs[addr] {
			delete(b.odAddrs, addr)
		}
	}

	// When a new config is provided, if the timer start timestamp is unset, set
	// it to the current time and start the timer for the configured interval,
	// then for each address, reset the call counters.
	var interval time.Duration
	if b.timerStartTime.IsZero() {
		b.timerStartTime = time.Now()
		for _, obj := range b.odAddrs {
			obj.callCounter.clear()
		}
		interval = b.odCfg.Interval
	} else {
		// If the timer start timestamp is set, instead cancel the existing
		// timer and start the timer for the configured interval minus the
		// difference between the current time and the previous start timestamp,
		// or 0 if that would be negative.
		interval = b.odCfg.Interval - (now().Sub(b.timerStartTime))
		if interval < 0 {
			interval = 0
		}
	}

	if !b.noopConfig() {
		if b.intervalTimer != nil {
			b.intervalTimer.Stop()
		}
		b.intervalTimer = afterFunc(interval, func() {
			b.intervalTimerAlgorithm()
		})
	} else {
		// "If a config is provided with both the `success_rate_ejection` and
		// `failure_percentage_ejection` fields unset, skip starting the timer and
		// unset the timer start timestamp."
		b.timerStartTime = time.Time{}
		// Should we stop the timer here as well? Not defined in gRFC but I feel
		// like it might make sense as you don't want to eject addresses. Also
		// how will addresses eventually get unejected in this case if only one
		// more pass of the interval timer after no-op configuration comes in?
	}
	b.mu.Unlock()
	b.pickerUpdateCh.Put(lbCfg)

	// then pass the address list along to the child policy.
	b.childMu.Lock()
	defer b.childMu.Unlock()
	return b.child.UpdateClientConnState(balancer.ClientConnState{
		ResolverState:  s.ResolverState,
		BalancerConfig: b.odCfg.ChildPolicy.Config,
	})
}

func (b *outlierDetectionBalancer) ResolverError(err error) {
	if b.child != nil {
		b.childMu.Lock()
		defer b.childMu.Unlock()
		b.child.ResolverError(err)
	}
}

func (b *outlierDetectionBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	scw, ok := b.scWrappers[sc]
	if !ok {
		// Return, shouldn't happen if passed up scw
		return
	}
	if state.ConnectivityState == connectivity.Shutdown {
		delete(b.scWrappers, scw.SubConn)
	}
	b.scUpdateCh.Put(&scUpdate{
		scw:   scw,
		state: state,
	})

}

func (b *outlierDetectionBalancer) Close() {
	b.closed.Fire()
	if b.child != nil {
		b.childMu.Lock()
		b.child.Close()
		b.child = nil
		b.childMu.Unlock()
	}

	// Any other cleanup needs to happen (subconns, other resources?)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.intervalTimer != nil {
		b.intervalTimer.Stop()
	}
}

func (b *outlierDetectionBalancer) ExitIdle() {
	if b.child == nil {
		return
	}
	if ei, ok := b.child.(balancer.ExitIdler); ok {
		b.childMu.Lock()
		defer b.childMu.Unlock()
		ei.ExitIdle()
		return
	}

	// Fallback for children handled in clusterimpl balancer Removing SubConns
	// is defined in API and also in graceful switch balancer, but already done
	// in ClusterImpl. I guess we should do that here?
}

// "The outlier_detection LB policy will provide a picker that delegates to the
// child policy's picker, and when the request finishes, increment the
// corresponding counter in the map entry referenced by the subchannel wrapper
// that was picked." - A50
type wrappedPicker struct {
	childPicker balancer.Picker
	noopPicker  bool
}

func (wp *wrappedPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	pr, err := wp.childPicker.Pick(info)
	if err != nil {
		return balancer.PickResult{}, err
	}

	done := func(di balancer.DoneInfo) {
		if !wp.noopPicker {
			incrementCounter(pr.SubConn, di)
		}
		if pr.Done != nil {
			pr.Done(di)
		}
	}
	// Shouldn't happen, defensive programming.
	scw, ok := pr.SubConn.(*subConnWrapper)
	if !ok {
		return balancer.PickResult{
			SubConn: pr.SubConn,
			Done:    done,
		}, nil
	}
	return balancer.PickResult{
		SubConn: scw.SubConn,
		Done:    done,
	}, nil
}

func incrementCounter(sc balancer.SubConn, info balancer.DoneInfo) {
	scw, ok := sc.(*subConnWrapper)
	if !ok {
		// Shouldn't happen, as comes from child
		return
	}

	// scw.obj and callCounter.activeBucket can be written to concurrently (the
	// pointers themselves). Thus, protect the reads here with atomics to
	// prevent data corruption. There exists a race in which you read the object
	// or active bucket pointer and then that pointer points to deprecated
	// memory. If this goroutine yields the processor, in between reading the
	// object pointer and writing to the active bucket, UpdateAddresses can
	// switch the obj the scw points to. Writing to an outdated addresses is a
	// very small race and tolerable. After reading callCounter.activeBucket in
	// this picker a swap call can concurrently change what activeBucket points
	// to. A50 says to swap the pointer, but I decided to make create new memory
	// for both active and inactive bucket, and have this race instead write to
	// deprecated memory. If you swap the pointers, this write would write to
	// the inactive buckets memory, which is read throughout in the interval
	// timers algorithm.
	obj := (*object)(atomic.LoadPointer(&scw.obj))
	if obj == nil {
		return
	}
	ab := (*bucket)(atomic.LoadPointer(&obj.callCounter.activeBucket))

	if info.Err == nil {
		atomic.AddInt64(&ab.numSuccesses, 1)
	} else {
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
		SubConn:    sc,
		addresses:  addrs,
		scUpdateCh: b.scUpdateCh,
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.scWrappers[sc] = scw
	if len(addrs) != 1 {
		return scw, nil
	}
	obj, ok := b.odAddrs[addrs[0].Addr]
	if !ok {
		return scw, nil
	}
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

// appendIfPresent appends the scw to the address, if the address is present in
// the Outlier Detection balancers address map. Returns nil if not present, and
// the map entry if present.
func (b *outlierDetectionBalancer) appendIfPresent(addr string, scw *subConnWrapper) *object {
	obj, ok := b.odAddrs[addr]
	if !ok {
		return nil
	}

	obj.sws = append(obj.sws, scw)
	atomic.StorePointer(&scw.obj, unsafe.Pointer(obj))
	return obj
}

// removeSubConnFromAddressesMapEntry removes the scw from it's map entry if
// present.
func (b *outlierDetectionBalancer) removeSubConnFromAddressesMapEntry(scw *subConnWrapper) {
	obj := (*object)(atomic.LoadPointer(&scw.obj))
	if obj == nil {
		return
	}
	for i, sw := range obj.sws {
		if scw == sw {
			obj.sws = append(obj.sws[:i], obj.sws[i+1:]...)
			break
		}
	}
}

func (b *outlierDetectionBalancer) UpdateAddresses(sc balancer.SubConn, addrs []resolver.Address) {
	scw, ok := sc.(*subConnWrapper)
	if !ok {
		// Return, shouldn't happen if passed up scw
		return
	}

	b.cc.UpdateAddresses(scw.SubConn, addrs)
	b.mu.Lock()
	defer b.mu.Unlock()

	// Note that 0 addresses is a valid update/state for a SubConn to be in.
	// This is correctly handled by this algorithm (handled as part of a non singular
	// old address/new address).
	if len(scw.addresses) == 1 {
		if len(addrs) == 1 { // single address to single address
			// If the updated address is the same, then there is nothing to do
			// past this point.
			if scw.addresses[0].Addr == addrs[0].Addr {
				return
			}
			// 1. Remove Subchannel from Addresses map entry if present in Addresses map.
			b.removeSubConnFromAddressesMapEntry(scw)
			// 2. Add Subchannel to Addresses map entry if new address present in map.
			obj := b.appendIfPresent(addrs[0].Addr, scw)
			// 3. Relay state with eject() recalculated (using the corresponding
			// map entry to see if it's currently ejected).
			if obj == nil { // uneject unconditionally because could have come from an ejected address
				scw.eject()
			} else {
				if obj.latestEjectionTimestamp.IsZero() { // relay new updated subconn state
					scw.uneject()
				} else {
					scw.eject()
				}
			}
		} else { // single address to multiple addresses
			// 1. Remove Subchannel from Addresses map entry if present in Addresses map.
			b.removeSubConnFromAddressesMapEntry(scw)
			// 2. Clear the Subchannel wrapper's Call Counter entry.
			obj := (*object)(atomic.LoadPointer(&scw.obj))
			if obj != nil {
				obj.callCounter.clear()
			}
			// 3. Uneject the Subchannel in case it was previously ejected.
			scw.uneject()
		}
	} else {
		if len(addrs) == 1 { // multiple addresses to single address
			// 1. Add Subchannel to Addresses map entry if new address present in map.
			obj := b.appendIfPresent(addrs[0].Addr, scw)
			if obj != nil && !obj.latestEjectionTimestamp.IsZero() {
				scw.eject()
			}
		} // else is multiple to multiple - no op, continued to be ignored by outlier detection.
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

func (b *outlierDetectionBalancer) intervalTimerAlgorithm() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.timerStartTime = time.Now()

	// 2. For each address, swap the call counter's buckets in that address's
	// map entry.
	for _, obj := range b.odAddrs {
		obj.callCounter.swap()
	}

	// 3. If the success_rate_ejection configuration field is set, run the
	// success rate algorithm.
	if b.odCfg.SuccessRateEjection != nil {
		b.successRateAlgorithm()
	}

	// 4. If the failure_percentage_ejection configuration field is set, run the
	// failure percentage algorithm.
	if b.odCfg.FailurePercentageEjection != nil {
		b.failurePercentageAlgorithm()
	}

	// 5. For each address in the map:
	for addr, obj := range b.odAddrs {
		// If the address is not ejected and the multiplier is greater than 0,
		// decrease the multiplier by 1.
		if obj.latestEjectionTimestamp.IsZero() && obj.ejectionTimeMultiplier > 0 {
			obj.ejectionTimeMultiplier--
			continue
		}
		// If the address is ejected, and the current time is after
		// ejection_timestamp + min(base_ejection_time (type: time.Time) *
		// multiplier (type: int), max(base_ejection_time (type: time.Time),
		// max_ejection_time (type: time.Time))), un-eject the address.
		if !obj.latestEjectionTimestamp.IsZero() && now().After(obj.latestEjectionTimestamp.Add(time.Duration(min(b.odCfg.BaseEjectionTime.Nanoseconds()*obj.ejectionTimeMultiplier, max(b.odCfg.BaseEjectionTime.Nanoseconds(), b.odCfg.MaxEjectionTime.Nanoseconds()))))) { // need to way to inject a desired bool here at a certain point in tests, mock time.Now to return a late time, mock time.After to always return true...
			b.unejectAddress(addr)
		}
	}

	// This conditional only for testing (since the interval timer algorithm is
	// called manually), will never hit in production.
	if b.intervalTimer != nil {
		b.intervalTimer.Stop()
	}
	b.intervalTimer = afterFunc(b.odCfg.Interval, func() {
		b.intervalTimerAlgorithm()
	})
}

func (b *outlierDetectionBalancer) run() {
	for {
		select {
		case update := <-b.scUpdateCh.Get():
			b.scUpdateCh.Load()
			switch u := update.(type) {
			case *scUpdate:
				scw := u.scw
				scw.latestState = u.state
				b.childMu.Lock()
				if !scw.ejected && b.child != nil {
					b.child.UpdateSubConnState(scw, u.state)
				}
				b.childMu.Unlock()
			case *ejectedUpdate:
				scw := u.scw
				scw.ejected = u.ejected
				var stateToUpdate balancer.SubConnState
				if u.ejected {
					// "The wrapper will report a state update with the
					// TRANSIENT_FAILURE state, and will stop passing along
					// updates from the underlying subchannel."
					stateToUpdate = balancer.SubConnState{
						ConnectivityState: connectivity.TransientFailure,
					}
				} else {
					// "The wrapper will report a state update with the latest
					// update from the underlying subchannel, and resume passing
					// along updates from the underlying subchannel."
					stateToUpdate = scw.latestState // If this has never been written to will send connectivity IDLE which seems fine to me
				}
				b.childMu.Lock()
				if b.child != nil {
					b.child.UpdateSubConnState(scw, stateToUpdate)
				}
				b.childMu.Unlock()
			}
		case update := <-b.pickerUpdateCh.Get():
			b.pickerUpdateCh.Load()
			if b.closed.HasFired() { // don't send picker updates to grpc after the balancer has been closed
				return
			}
			switch u := update.(type) {
			case balancer.State:
				b.childState = u
				b.mu.Lock()
				noopCfg := b.noopConfig()
				b.mu.Unlock()
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
		case <-b.closed.Done():
			return
		}
	}
}

// numAddrsWithAtLeastRequestVolume returns the number of addresses present in
// the map that have request volume of at least requestVolume.
func (b *outlierDetectionBalancer) numAddrsWithAtLeastRequestVolume() uint32 {
	var numAddrs uint32
	for _, obj := range b.odAddrs {
		if uint32(obj.callCounter.inactiveBucket.requestVolume) >= b.odCfg.SuccessRateEjection.RequestVolume {
			numAddrs++
		}
	}
	return numAddrs
}

// meanAndStdDevOfSucceseesAtLeastRequestVolume returns the mean and std dev of
// the number of requests of addresses that have at least requestVolume.
func (b *outlierDetectionBalancer) meanAndStdDevOfSuccessesAtLeastRequestVolume() (float64, float64) {
	// 2. Calculate the mean and standard deviation of the fractions of
	// successful requests among addresses with total request volume of at least
	// success_rate_ejection.request_volume.
	var totalFractionOfSuccessfulRequests float64
	var mean float64
	for _, obj := range b.odAddrs {
		// "of at least success_rate_ejection.request_volume"
		if uint32(obj.callCounter.inactiveBucket.requestVolume) >= b.odCfg.SuccessRateEjection.RequestVolume {
			totalFractionOfSuccessfulRequests += float64(obj.callCounter.inactiveBucket.numSuccesses) / float64(obj.callCounter.inactiveBucket.requestVolume)
		}
	}
	mean = totalFractionOfSuccessfulRequests / float64(len(b.odAddrs))
	var sumOfSquares float64
	for _, obj := range b.odAddrs {
		devFromMean := (float64(obj.callCounter.inactiveBucket.numSuccesses) / float64(obj.callCounter.inactiveBucket.requestVolume)) - mean
		sumOfSquares += devFromMean * devFromMean
	}

	variance := sumOfSquares / float64(len(b.odAddrs))
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
	for addr, obj := range b.odAddrs {
		ccb := obj.callCounter.inactiveBucket
		sre := b.odCfg.SuccessRateEjection
		// i. If the percentage of ejected addresses is greater than
		// max_ejection_percent, stop.
		if float64(b.numAddrsEjected)/float64(len(b.odAddrs))*100 > float64(b.odCfg.MaxEjectionPercent) {
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
		if successRate < (mean - stddev*(float64(sre.StdevFactor)/1000)) {
			// then choose a random integer in [0, 100). If that number is less
			// than success_rate_ejection.enforcement_percentage, eject that
			// address.
			if uint32(grpcrand.Int31n(100)) < sre.EnforcementPercentage {
				b.ejectAddress(addr)
			}
		}
	}
}

func (b *outlierDetectionBalancer) failurePercentageAlgorithm() {
	// 1. If the number of addresses is less than
	// failure_percentage_ejection.minimum_hosts, stop.
	if uint32(len(b.odAddrs)) < b.odCfg.FailurePercentageEjection.MinimumHosts {
		return
	}

	// 2. For each address:
	for addr, obj := range b.odAddrs {
		ccb := obj.callCounter.inactiveBucket
		fpe := b.odCfg.FailurePercentageEjection
		// i. If the percentage of ejected addresses is greater than
		// max_ejection_percent, stop.
		if float64(b.numAddrsEjected)/float64(len(b.odAddrs))*100 > float64(b.odCfg.MaxEjectionPercent) {
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
			if uint32(grpcrand.Int31n(100)) < b.odCfg.FailurePercentageEjection.EnforcementPercentage {
				b.ejectAddress(addr)
			}
		}
	}
}

func (b *outlierDetectionBalancer) ejectAddress(addr string) {
	obj, ok := b.odAddrs[addr]
	if !ok { // Shouldn't happen
		return
	}
	b.numAddrsEjected++

	// To eject an address, set the current ejection timestamp to the timestamp
	// that was recorded when the timer fired, increase the ejection time
	// multiplier by 1, and call eject() on each subchannel wrapper in that
	// address's subchannel wrapper list.
	obj.latestEjectionTimestamp = b.timerStartTime
	obj.ejectionTimeMultiplier++
	for _, sbw := range obj.sws {
		sbw.eject()
	}
}

func (b *outlierDetectionBalancer) unejectAddress(addr string) {
	obj, ok := b.odAddrs[addr]
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

type object struct {
	// The call result counter object
	callCounter *callCounter

	// The latest ejection timestamp, or null if the address is currently not
	// ejected
	latestEjectionTimestamp time.Time // We represent the branching logic on the null with a time.Zero() value

	// The current ejection time multiplier, starting at 0
	ejectionTimeMultiplier int64

	// A list of subchannel wrapper objects that correspond to this address
	sws []*subConnWrapper
}

func newObject() *object {
	return &object{
		callCounter: newCallCounter(),
		sws:         make([]*subConnWrapper, 0),
	}
}
