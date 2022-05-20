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
 */

package outlierdetection

import (
	"sync/atomic"
	"unsafe"
)

type bucket struct {
	// A call finishing increments one of these two numbers...the wrapped picker updates this
	// I don't get how a picker will know whether a request succeeds or fails? (whether err == nil or not in DoneInfo in Picker once call is complete :D)!
	numSuccesses int64 // "The active bucket is updated each time a call finishes" - can either read this directly or have a method with synchronization guarantees
	numFailures int64
	requestVolume int64 // numSuccesses + numFailures, needed because this number will always be used
}

func newCallCounter() *callCounter {
	return &callCounter{
		activeBucket: unsafe.Pointer(&bucket{}),
		inactiveBucket: &bucket{},
	}
}

// do we want clear to be a method on callCounter?

type callCounter struct {
	// Is any of this state accessed synchronosly?

	// The object contains two buckets, and each bucket has a number counting
	// successes, and another counting failures.

	// activeBucket updates every time a call finishes (from picker passed to Client Conn).
	// unsafe.Pointer so picker does not have to grab a mutex per RPC.
	activeBucket unsafe.Pointer // *bucket updates every time a call finishes (from picker passed to Client Conn)
	inactiveBucket *bucket // active/inactiveBucket get swapped every interval (timer going off). this is two things writing to it, and a race condition, need to sync. "Used by picker" in design docs and cluster impl balancer scWrapper field.

	// Read throughout interval timer algo
	// written to in swap - happens non concurrently with ^^^
	// UpdateClientConnState() can clear
	// UpdateAddresses() can also clear



	// The active bucket is updated each time a call finishes. When the timer
	// triggers, the inactive bucket is zeroed and swapped with the active
	// bucket. Then the inactive bucket contains the number of successes and
	// failures since the last time the timer triggered. Those numbers are used
	// to evaluate the ejection criteria.

	// What is a bucket...
}

func (cc *callCounter) clear() {
	atomic.StorePointer(&cc.activeBucket, unsafe.Pointer(&bucket{}))
	// inactive mu lock? - but then all the reads will be super slow but that's fine?
	cc.inactiveBucket = &bucket{}
	// inactive mu unlock?
}

// When the timer triggers, the inactive bucket is zeroed and swapped with the active bucket.

// How are we swapping state? Does caller do it or do we define methods here?
func (cc *callCounter) swap() { // Called when the interval timer triggers in outlierdetection/balancer.go
	ab := (*bucket)(atomic.LoadPointer(&cc.activeBucket)) // or...we can make this swap and have atomic reads in interval timer algo, write the algorithm as written in Michael's document
	// Don't do it exactly like defined but the same logically, as picker reads
	// ref to active bucket so instead of swapping the pointers (inducing race
	// conditions where picker writes to inactive bucket which is being used for
	// outlier detection algorithm, copy active bucket to new memory on heap)

	// inactive mu lock?
	cc.inactiveBucket = &bucket{
		numSuccesses: atomic.LoadInt64(&ab.numSuccesses),
		numFailures: atomic.LoadInt64(&ab.numFailures),
		requestVolume: atomic.LoadInt64(&ab.requestVolume),
	}
	// inactive mu unlock?
	atomic.StorePointer(&cc.activeBucket, unsafe.Pointer(&bucket{}))

	// result: the inactive bucket contains the number of successes and failures since the last time the timer
	// triggered.
}

// Can this be called concurrently or are we guaranteed synchronization before calling here? i.e. for that swap ^^^ function
// unit test?

// The call into swap() happens on the triggering of the interval timer in
// balancer. Thus, two calls can not happen concurrently I believe.