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
	"testing"

	"github.com/google/go-cmp/cmp"
)

func (b1 *bucket) Equal(b2 *bucket) bool {
	if b1 == nil && b2 == nil {
		return true
	}
	if (b1 != nil) != (b2 != nil) {
		return false
	}
	if b1.numSuccesses != b2.numSuccesses {
		return false
	}
	if b1.numFailures != b2.numFailures {
		return false
	}
	return b1.requestVolume == b2.requestVolume
}

func (cc1 *callCounter) Equal(cc2 *callCounter) bool { // Equal in values
	if cc1 == nil && cc2 == nil {
		return true
	}
	if (cc1 != nil) != (cc2 != nil) {
		return false
	}
	ab1 := (*bucket)(atomic.LoadPointer(&cc1.activeBucket))
	ab2 := (*bucket)(atomic.LoadPointer(&cc2.activeBucket))
	if !ab1.Equal(ab2) {
		return false
	}
	return cc1.inactiveBucket.Equal(cc2.inactiveBucket)
}

// TestClear tests that clear on the call counter clears (everything set to 0)
// the active and inactive buckets.
func (s) TestClear(t *testing.T) {
	cc := newCallCounter()
	ab := (*bucket)(atomic.LoadPointer(&cc.activeBucket))
	ab.numSuccesses = 1
	ab.numFailures = 2
	ab.requestVolume = 3
	cc.inactiveBucket.numSuccesses = 4
	cc.inactiveBucket.numFailures = 5
	cc.inactiveBucket.requestVolume = 9
	cc.clear()
	// Both the active and inactive buckets should be cleared.
	ccWant := newCallCounter()
	if diff := cmp.Diff(cc, ccWant); diff != "" {
		t.Fatalf("callCounter is different than expected, diff (-got +want): %v", diff)
	}
}

// TestSwap tests that swap() on the callCounter successfully has the desired
// end result of inactive bucket containing the previous active buckets data,
// and the active bucket being cleared.
func (s) TestSwap(t *testing.T) {
	cc := newCallCounter()
	ab := (*bucket)(atomic.LoadPointer(&cc.activeBucket))
	ab.numSuccesses = 1
	ab.numFailures = 2
	ab.requestVolume = 3
	cc.inactiveBucket.numSuccesses = 4
	cc.inactiveBucket.numFailures = 5
	cc.inactiveBucket.requestVolume = 9

	cc.swap()
	// Inactive should pick up active's data, active should be cleared.
	ccWant := newCallCounter()
	ccWant.inactiveBucket.numSuccesses = 1
	ccWant.inactiveBucket.numFailures = 2
	ccWant.inactiveBucket.requestVolume = 3
	if diff := cmp.Diff(cc, ccWant); diff != "" {
		t.Fatalf("callCounter is different than expected, diff (-got +want): %v", diff)
	}
}
