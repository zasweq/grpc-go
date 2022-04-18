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

type bucket struct {
	// A call finishing increments one of these two numbers...the wrapped picker updates this
	// I don't get how a picker will know whether a request succeeds or fails? (whether err == nil or not in DoneInfo in Picker once call is complete :D)!
	numSuccesses int64 // "The active bucket is updated each time a call finishes" - can either read this directly or have a method with synchronization guarantees
	numFailures int64
	requestVolume int64 // numSuccesses + numFailures, needed because this number will always be used
}

type callCounter struct {
	// Is any of this state accessed synchronosly?

	// The object contains two buckets, and each bucket has a number counting
	// successes, and another counting failures.
	activeBucket bucket // updates every time a call finishes (from picker passed to Client Conn)
	inactiveBucket bucket // active/inactiveBucket get swapped every interval (timer going off). this is two things writing to it, and a race condition, need to sync. "Used by picker" in design docs and cluster impl balancer scWrapper field.

	// The active bucket is updated each time a call finishes. When the timer
	// triggers, the inactive bucket is zeroed and swapped with the active
	// bucket. Then the inactive bucket contains the number of successes and
	// failures since the last time the timer triggered. Those numbers are used
	// to evaluate the ejection criteria.

	// What is a bucket...
}

// How are we swapping state? Does caller do it or do we define methods here?
func (cc *callCounter) swap() { // Called when the interval timer triggers in outlierdetection/balancer.go
	// inactive bucket is zeroed and swapped with the active bucket
	cc.activeBucket = cc.inactiveBucket
	cc.inactiveBucket = bucket{} // implicitly zero's due to default int values in golang

	// result: the inactive bucket contains the number of successes and failures since the last time the timer
	// triggered.
}

// Can this be called concurrently or are we guaranteed synchronization before calling here? i.e. for that swap ^^^ function
// unit test?

// The call into swap() happens on the triggering of the interval timer in
// balancer. Thus, two calls can not happen concurrently I believe.