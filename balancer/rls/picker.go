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

package rls

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/rls/internal/keys"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	internalgrpclog "google.golang.org/grpc/internal/grpclog"
	rlspb "google.golang.org/grpc/internal/proto/grpc_lookup_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	errRLSThrottled = errors.New("RLS call throttled at client side")

	// Function to compute data cache entry size.
	computeDataCacheEntrySize = dcEntrySize
)

// exitIdler wraps the only method on the BalancerGroup that the picker calls.
type exitIdler interface {
	ExitIdleOne(id string)
}

// rlsPicker selects the subConn to be used for a particular RPC. It does not
// manage subConns directly and delegates to pickers provided by child policies.
type rlsPicker struct {
	// The keyBuilder map used to generate RLS keys for the RPC. This is built
	// by the LB policy based on the received ServiceConfig.
	kbm keys.BuilderMap
	// Endpoint from the user's original dial target. Used to set the `host_key`
	// field in `extra_keys`.
	origEndpoint string

	lb *rlsBalancer // this already has access to balancer for recording points...

	// The picker is given its own copy of the below fields from the RLS LB policy
	// to avoid having to grab the mutex on the latter.

	// uses old ctrl ch, so just record on old rls server target
	rlsServerTarget     string
	defaultPolicy *childPolicyWrapper // Child policy for the default target.
	ctrlCh        *controlChannel     // Control channel to the RLS server.
	maxAge        time.Duration       // Cache max age from LB config.
	staleAge      time.Duration       // Cache stale age from LB config.
	bg            exitIdler
	logger        *internalgrpclog.PrefixLogger
}

// isFullMethodNameValid return true if name is of the form `/service/method`.
func isFullMethodNameValid(name string) bool {
	return strings.HasPrefix(name, "/") && strings.Count(name, "/") == 2
}

// Pick makes the routing decision for every outbound RPC.
func (p *rlsPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	// What does "drop" mean semantically with respect to pick

	// Child failures no need to record, no need to record on the delegation
	// downward...

	// RPC will queue from err no sub conn available...

	// control plane rpc fails, causes a pick failure...

	// is dropped vs. failed queue vs. not

	// errRLSThrottled fails the pick...
	// line 129 = failed RPC../




	// Things to figure out here:
	// default target vs. something returned from the RLS server...

	// something returned from rls server - target used by RLS for routing data
	// plane traffic...

	// pick result three buckets - "complete", "fail", "drop"

	// When LB picks fail due to a failed RLS request or
	// RLS channel being throttled...specifically for
	// RLS request or RLS channel being throttled...so can't just defer something here...





	// Number of LB picks sent to the default target.
	// Just emit once...

	// Number of LB picks sent to each RLS target.  Note that if the default
	// target is also returned by the RLS server, RPCs sent to that target from
	// the cache will be counted in this metric, not in
	// grpc.rls.default_target_picks.

	// I have no idea wtf this means ^^^

	// Number of LB picks failed due to either a failed RLS request or the RLS
	// channel being throttled.

	// This is self explanatory...? First and last picker metric are as written...

	// target, rls server target, uuid to here that's what's needed for these
	// metrics...alongside data plane target...

	// Looks like data plane target and pick result are
	// dynamically derived by the picker...

	// Can I have an atomic mu
	// p.lb.stateMu.Lock() // does this cause any problems...run tests and find out, I don't think we want this on the fast path but what can you do?


	failedPicksMetric.Record(p.lb.bopts.MetricsRecorder, 1, p.lb.bopts.Target.String(), p.rlsServerTarget)

	// enum for weights here...maybe just copy and paste into new operations...

	// Send cache metrics including up to picker plumbing, and then figure this out...


	// for WRR - set up SubConn's

	// and test scheduler...

	// checkWeights...

	// unit test scheduler rebuild...
	// orca data, subchannel time...

	// addresses need both to hit both backends...but that goes keep making
	// RPC's for 5 seconds...


	// Unit tests for scheduler updates...
	// delete the two I scaled up with Doug

	// e2e for OTel things...


	// oob recording...stop orca service from recording
	// stop reporting for expiration...


	// wrr in memory, fake scs, call scheduler...
	// etc...mock up state...specific values emitted

	// Delete the two scaled up, keep the first one

	// Unit test



	// spawn goroutine make RPC's

	// metrics are as written...make sure one has a weight? that means first
	// backend hits orrrr just set up both to have metrics...then no matter what
	// hits you know has weight


	// Fix e2e tests...

	// Get this PR out implementation wise so Frank can build off it

	// Review Easwar's PR


	// calls are concurrent...keep on making RPC's
	// sleep a ms between the three...

	// make sure metrics get emitted for it...
	// 3 you want (don't fail if 4th shows up...)

	// metrics a

	// poll for metrics b, should expire and weight should show up...
	// metrics b

	// How to derive the data plane target and map different things happening to different pick results occurring...



	if name := info.FullMethodName; !isFullMethodNameValid(name) {
		return balancer.PickResult{}, fmt.Errorf("rls: method name %q is not of the form '/service/method", name)
	}

	// Build the request's keys using the key builders from LB config.
	md, _ := metadata.FromOutgoingContext(info.Ctx)
	reqKeys := p.kbm.RLSKey(md, p.origEndpoint, info.FullMethodName)

	p.lb.cacheMu.Lock() // there's a cache mu...grabbed on picker...?
	defer p.lb.cacheMu.Unlock()

	p.lb.dataCache // accesses this, but guarded by cache mu...
	p.lb.bopts.Target // ok, read only after init time...I think? yup
	p.lb.bopts.MetricsRecorder

	// This is dynamic and needs to be read here...how to not induce deadlock? grab that mutex?
	p.lb.lbCfg.lookupService // target

	// Where to define this enum? And how does it map to operations here?
	targetPicksMetric.Record(p.lb.bopts.MetricsRecorder, 1, p.lb.bopts.Target.String(), p.lb.lbCfg.lookupService, /*data plane target - defer to end?*/, /*enum for pick result possibility...*/)

	// defer something? How to derive these?

	// grpc.lb.rls.data_plane_target - A target string used by RLS for routing
	// data plane traffic.  The value is either returned by the RLS server for a
	// particular key or configured as the default target in the RLS config.


	// grpc.lb.pick_result - The result of an LB pick.  This will be one of the
	// values "complete", "fail", or "drop".

	// "complete", "fail", "drop"
	// Ask Easwar what's going on here?


	// data plane target, pick result derived from picker...

	// Lookup data cache and pending request map using request path and keys.
	cacheKey := cacheKey{path: info.FullMethodName, keys: reqKeys.Str}
	dcEntry := p.lb.dataCache.getEntry(cacheKey)
	pendingEntry := p.lb.pendingMap[cacheKey]
	now := time.Now()

	// I think useDefaultPickIfPossible corresponds to
	// "Number of LB picks sent to the default target."

	// Failing RLS Request = backoffState...
	// when an RLS request succeds, backoffState is set...

	// What is drop?
	// Three buckets - (picks a target || falls back to default || error (fails RPC)) - but it can also trigger a queue through ErrNoSubConnAvailable...what should it log in that case?
	// throttled could fail

	switch {
	// No data cache entry. No pending request.
	case dcEntry == nil && pendingEntry == nil:
		throttled := p.sendRouteLookupRequestLocked(cacheKey, &backoffState{bs: defaultBackoffStrategy}, reqKeys.Map, rlspb.RouteLookupRequest_REASON_MISS, "")
		if throttled {
			// I think this becomes...
			/*pr, err := p.useDefaultPickIfPossible(info, errRLSThrottled) // fallback or fail rpc to error
			if err != nil { // or do this in helper below...
				// Emit
			}*/

			return p.useDefaultPickIfPossible(info, errRLSThrottled) // or do it in this function...
		}
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable // queues RPC

	// No data cache entry. Pending request exits.
	case dcEntry == nil && pendingEntry != nil:
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable // queues RPC - what to log in this case...

	// Data cache hit. No pending request.
	case dcEntry != nil && pendingEntry == nil:
		if dcEntry.expiryTime.After(now) {
			if !dcEntry.staleTime.IsZero() && dcEntry.staleTime.Before(now) && dcEntry.backoffTime.Before(now) {
				// Doesn't read bool?
				p.sendRouteLookupRequestLocked(cacheKey, dcEntry.backoffState, reqKeys.Map, rlspb.RouteLookupRequest_REASON_STALE, dcEntry.headerData)
			}

			// this is choosing a child to delegate to...
			// so I think this is a "target pick" metric...

			// Delegate to child policies.
			res, err := p.delegateToChildPoliciesLocked(dcEntry, info)
			return res, err
		}

		// We get here only if the data cache entry has expired. If entry is in
		// backoff, delegate to default target or fail the pick.
		if dcEntry.backoffState != nil && dcEntry.backoffTime.After(now) {
			// Avoid propagating the status code received on control plane RPCs to the
			// data plane which can lead to unexpected outcomes as we do not control
			// the status code sent by the control plane. Propagating the status
			// message received from the control plane is still fine, as it could be
			// useful for debugging purposes.
			st := dcEntry.status
			// same here - log the (fallback || fail pick) in helper?
			return p.useDefaultPickIfPossible(info, status.Error(codes.Unavailable, fmt.Sprintf("most recent error from RLS server: %v", st.Error())))
		}

		// We get here only if the entry has expired and is not in backoff.
		throttled := p.sendRouteLookupRequestLocked(cacheKey, dcEntry.backoffState, reqKeys.Map, rlspb.RouteLookupRequest_REASON_MISS, "")
		if throttled {
			// same here - log the (fallback || fail pick) in helper? could fallback or fail pick...
			return p.useDefaultPickIfPossible(info, errRLSThrottled) // this handles bucket 2 and 3 fallback/error out...only log erroring RPC's (and how does this map to fail or drop)
		}
		// queue RPC - what to log here?
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable

	// Data cache hit. Pending request exists.
	default:
		if dcEntry.expiryTime.After(now) {
			// log target picks...chooses a target
			res, err := p.delegateToChildPoliciesLocked(dcEntry, info)
			return res, err
		}
		// Data cache entry has expired and pending request exists. Queue pick.
		return balancer.PickResult{}, balancer.ErrNoSubConnAvailable // what to log in picker case?
	}

	// intercept error from child for normal pick/default pick

	// two operations:
	// delegateToChildPoliciesLocked (determined from RLS Server - normal picking (default target))
	// useDefaultPickIfPossible (pick default or error)

	// from both operations, can error or be successful, need to attach that as an attribute...



	// A lot of returns with errors from this picker...derive complete fail or drop

	// And there's a metric for failed due to failed RLS request or RLS channel
	// being throttled.


	// I don't see any code for failing RLS request failing pick...

	// RLS channel being throttled...can choose default or fail so what should it choose?

	// While waiting for response can send out just cache metrics and review Doug's comments...
} // This is done, save progress and cleanup, assign both Easwar and Doug and post it in o11y chat

// delegateToChildPoliciesLocked is a helper function which iterates through the
// list of child policy wrappers in a cache entry and attempts to find a child
// policy to which this RPC can be routed to. If all child policies are in
// TRANSIENT_FAILURE, we delegate to the last child policy arbitrarily.
func (p *rlsPicker) delegateToChildPoliciesLocked(dcEntry *cacheEntry, info balancer.PickInfo) (balancer.PickResult, error) {
	const rlsDataHeaderName = "x-google-rls-data"
	for i, cpw := range dcEntry.childPolicyWrappers {
		state := (*balancer.State)(atomic.LoadPointer(&cpw.state))
		// Delegate to the child policy if it is not in TRANSIENT_FAILURE, or if
		// it is the last one (which handles the case of delegating to the last
		// child picker if all child polcies are in TRANSIENT_FAILURE).
		if state.ConnectivityState != connectivity.TransientFailure || i == len(dcEntry.childPolicyWrappers)-1 {
			// Any header data received from the RLS server is stored in the
			// cache entry and needs to be sent to the actual backend in the
			// X-Google-RLS-Data header.
			res, err := state.Picker.Pick(info)

			// set attr to success
			if err != nil {
				// set attr to fail
				targetPicksMetric.Record(p.lb.metricsRecorder, 1, p.lb.bopts.Target.String(), p.rlsServerTarget, cpw.target, "fail")
				return res, err
			}

			// log here - merge what's here with appends to errors, maybe set attr to success inline to keep early return
			targetPicksMetric.Record(p.lb.metricsRecorder, 1, p.lb.bopts.Target.String(), p.rlsServerTarget, cpw.target, "complete")

			if res.Metadata == nil {
				res.Metadata = metadata.Pairs(rlsDataHeaderName, dcEntry.headerData)
			} else {
				res.Metadata.Append(rlsDataHeaderName, dcEntry.headerData)
			}
			return res, nil

			// Delegate to child and return pick here...


		}
	}

	// Or fail with an error...

	// Log here failing case...no attr
	failedPicksMetric.Record(p.lb.metricsRecorder, 1, p.lb.bopts.Target.String(), p.rlsServerTarget)
	// In the unlikely event that we have a cache entry with no targets, we end up
	// queueing the RPC.
	return balancer.PickResult{}, balancer.ErrNoSubConnAvailable
}

// both of these are returned directly...

// so do the recording, it has access to picker state...

// enum here?

// I think I'm good - do any codepaths from Pick not go through these helpers?

// useDefaultPickIfPossible is a helper method which delegates to the default
// target if one is configured, or fails the pick with the given error.
func (p *rlsPicker) useDefaultPickIfPossible(info balancer.PickInfo, errOnNoDefault error) (balancer.PickResult, error) {
	if p.defaultPolicy != nil {
		state := (*balancer.State)(atomic.LoadPointer(&p.defaultPolicy.state))
		// need to read error and do same branch
		res, err := state.Picker.Pick(info)
		pr := "complete"
		if err != nil {
			pr = "fail"
		}
		// attr = success
		// if err != nil {
		//      attr = fail
		// }
		// emit with addr
		defaultTargetPicksMetric.Record(p.lb.bopts.MetricsRecorder, 1, p.lb.bopts.Target.String(), p.rlsServerTarget, p.defaultPolicy.target, pr)

		return res, err  // Delegate to child and return picker here
	}

	// it's on picker so I can do it here...that makes more sense since called numerous times 4 times
	// should I record in helpers or

	// log here failing case...no attr
	failedPicksMetric.Record(p.lb.metricsRecorder, 1, p.lb.bopts.Target.String(), p.rlsServerTarget)
	return balancer.PickResult{}, errOnNoDefault // or fail if default not set
}

// sendRouteLookupRequestLocked adds an entry to the pending request map and
// sends out an RLS request using the passed in arguments. Returns a value
// indicating if the request was throttled by the client-side adaptive
// throttler.
func (p *rlsPicker) sendRouteLookupRequestLocked(cacheKey cacheKey, bs *backoffState, reqKeys map[string]string, reason rlspb.RouteLookupRequest_Reason, staleHeaders string) bool {
	if p.lb.pendingMap[cacheKey] != nil {
		return false
	}

	p.lb.pendingMap[cacheKey] = bs
	throttled := p.ctrlCh.lookup(reqKeys, reason, staleHeaders, func(targets []string, headerData string, err error) {
		p.handleRouteLookupResponse(cacheKey, targets, headerData, err)
	})
	if throttled {
		delete(p.lb.pendingMap, cacheKey)
	}
	return throttled
}

// handleRouteLookupResponse is the callback invoked by the control channel upon
// receipt of an RLS response. Modifies the data cache and pending requests map
// and sends a new picker.
//
// Acquires the write-lock on the cache. Caller must not hold p.lb.cacheMu.
func (p *rlsPicker) handleRouteLookupResponse(cacheKey cacheKey, targets []string, headerData string, err error) {

	// Lifecycle of picker object?, emits the right labels as WRR did?

	// Is this the only thing that can do cache operations?

	// On the whole cache:

	// If the new config decreases the size of the Data Cache, then the LB
	// policy will perform LRU Eviction to get the cache size down to the newly
	// specified size...

	// And capped out at a maximum size from LB policy configuration...

	// LRU eviction - config comes that reduces cache size
	// and also RLS response happens and needs to add a new entry to cache...



	// On a cache entry: (that affects size and number of entries), lru iterator field doesn't...

	// When an RLS Response is received...

	// When an entry's Backoff Timer fires...

	// which both the data_expiration_time_ and backoff_expiration_time_ fields
	// are in the past.  The timer callback will sweep through the cache and
	// delete any expired entry.  The timer will fire every 1 minute (by
	// default; may be configurable).




	// This and the backoff timer may affect *an entry*, is there a field that
	// tracks entry size/cache size... cache size is needed for lru logic, also
	// from a config update...



	// Send implementation PR to Easwar...




	p.logger.Infof("Received RLS response for key %+v with targets %+v, headerData %q, err: %v", cacheKey, targets, headerData, err)

	p.lb.cacheMu.Lock()
	defer func() {
		// state mu lock
		// read rls target...does this change picker?
		// state mu unlock

		// How to read number of entries and current size of RLS cache...?
		/*p.lb.stateMu.Lock()
		rlsLookupService := p.lb.lbCfg.lookupService
		p.lb.stateMu.Unlock()*/
		// size and number of entries
		p.lb.dataCache.currentSize // int64, is this set correctly?

		grpcTarget := p.lb.bopts.Target.String()
		// I need the LB Config rls target, just grab it in a mu see what Easwar says about it...

		// could do this outside the mutex like other records for cache
		// operations and just read properties in this function...but the send
		// new picker inside lock worries me so idkkk

		cacheSizeMetric.Record(p.lb.bopts.MetricsRecorder, p.lb.dataCache.currentSize, grpcTarget, p.rlsServerTarget, p.lb.uuid)
		// Any sync concerns here...? cache mu is ok, rls server target determined by config so needs a mu, should this read at creation time?

		// if server target changes does it create a whole new picker or what? so don't need sync concerns?
		// if I grab mu will it deadlock? if not I don't think concern above matters unless it builds picker and doesn't need sync...
		// can this change over the lifetime?

		cacheEntriesMetric.Record(p.lb.bopts.MetricsRecorder, int64(len(p.lb.dataCache.entries)), grpcTarget, p.rlsServerTarget, p.lb.uuid)
		len(p.lb.dataCache.entries) // len of map works...is this correct? map length (number of defined keys)

		// gauge so OTel will eat, jsut emit unconditionally and that should work...


		// besides this I think it's just backoff timer and config update...
		// send just these two metrics to Easwar?



		// record it here...? still holds the mu...so can read size/entries

		// then it's clean, operations are guarded by mu and so are writes...

		// Pending request map entry is unconditionally deleted since the request is
		// no longer pending.
		p.logger.Infof("Removing pending request entry for key %+v", cacheKey)
		delete(p.lb.pendingMap, cacheKey)
		p.lb.sendNewPicker() // Do picker operations affect cache or is that at a higher layer?
		p.lb.cacheMu.Unlock()
	}()

	// Lookup the data cache entry or create a new one.
	dcEntry := p.lb.dataCache.getEntry(cacheKey)
	if dcEntry == nil {
		dcEntry = &cacheEntry{}
		if _, ok := p.lb.dataCache.addEntry(cacheKey, dcEntry); !ok {
			// This is a very unlikely case where we are unable to add a
			// data cache entry. Log and leave.
			p.logger.Warningf("Failed to add data cache entry for %+v", cacheKey)
			return
		}
	}

	// For failed requests, the data cache entry is modified as follows:
	// - status is set to error returned from the control channel
	// - current backoff state is available in the pending entry
	//   - `retries` field is incremented and
	//   - backoff state is moved to the data cache
	// - backoffTime is set to the time indicated by the backoff state
	// - backoffExpirationTime is set to twice the backoff time
	// - backoffTimer is set to fire after backoffTime
	//
	// When a proactive cache refresh fails, this would leave the targets and the
	// expiry time from the old entry unchanged. And this mean that the old valid
	// entry would be used until expiration, and a new picker would be sent upon
	// backoff expiry.
	now := time.Now()

	// "An RLS request is considered to have failed if it returns a non-OK
	// status or the RLS response's targets list is non-empty." - RLS LB Policy
	// design.
	if len(targets) == 0 && err == nil {
		err = fmt.Errorf("RLS response's target list does not contain any entries for key %+v", cacheKey)
		// If err is set, rpc error from the control plane and no control plane
		// configuration is why no targets were passed into this helper, no need
		// to specify and tell the user this information.
	}
	if err != nil {
		dcEntry.status = err
		pendingEntry := p.lb.pendingMap[cacheKey]
		pendingEntry.retries++
		backoffTime := pendingEntry.bs.Backoff(pendingEntry.retries)
		dcEntry.backoffState = pendingEntry
		dcEntry.backoffTime = now.Add(backoffTime)
		dcEntry.backoffExpiryTime = now.Add(2 * backoffTime)
		if dcEntry.backoffState.timer != nil {
			dcEntry.backoffState.timer.Stop()
		}
		dcEntry.backoffState.timer = time.AfterFunc(backoffTime, p.lb.sendNewPicker)
		return
	}

	// For successful requests, the cache entry is modified as follows:
	// - childPolicyWrappers is set to point to the child policy wrappers
	//   associated with the targets specified in the received response
	// - headerData is set to the value received in the response
	// - expiryTime, stateTime and earliestEvictionTime are set
	// - status is set to nil (OK status)
	// - backoff state is cleared
	p.setChildPolicyWrappersInCacheEntry(dcEntry, targets)
	dcEntry.headerData = headerData
	dcEntry.expiryTime = now.Add(p.maxAge)
	if p.staleAge != 0 {
		dcEntry.staleTime = now.Add(p.staleAge)
	}
	dcEntry.earliestEvictTime = now.Add(minEvictDuration)
	dcEntry.status = nil
	dcEntry.backoffState = &backoffState{bs: defaultBackoffStrategy}
	dcEntry.backoffTime = time.Time{}
	dcEntry.backoffExpiryTime = time.Time{}
	p.lb.dataCache.updateEntrySize(dcEntry, computeDataCacheEntrySize(cacheKey, dcEntry))
}

// setChildPolicyWrappersInCacheEntry sets up the childPolicyWrappers field in
// the cache entry to point to the child policy wrappers for the targets
// specified in the RLS response.
//
// Caller must hold a write-lock on p.lb.cacheMu.
func (p *rlsPicker) setChildPolicyWrappersInCacheEntry(dcEntry *cacheEntry, newTargets []string) {
	// If the childPolicyWrappers field is already pointing to the right targets,
	// then the field's value does not need to change.
	targetsChanged := true
	func() {
		if cpws := dcEntry.childPolicyWrappers; cpws != nil {
			if len(newTargets) != len(cpws) {
				return
			}
			for i, target := range newTargets {
				if cpws[i].target != target {
					return
				}
			}
			targetsChanged = false
		}
	}()
	if !targetsChanged {
		return
	}

	// If the childPolicyWrappers field is not already set to the right targets,
	// then it must be reset. We construct a new list of child policies and
	// then swap out the old list for the new one.
	newChildPolicies := p.lb.acquireChildPolicyReferences(newTargets)
	oldChildPolicyTargets := make([]string, len(dcEntry.childPolicyWrappers))
	for i, cpw := range dcEntry.childPolicyWrappers {
		oldChildPolicyTargets[i] = cpw.target
	}
	p.lb.releaseChildPolicyReferences(oldChildPolicyTargets)
	dcEntry.childPolicyWrappers = newChildPolicies
}

func dcEntrySize(key cacheKey, entry *cacheEntry) int64 {
	return int64(len(key.path) + len(key.keys) + len(entry.headerData))
}
