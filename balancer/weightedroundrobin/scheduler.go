/*
 *
 * Copyright 2023 gRPC authors.
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

package weightedroundrobin

import (
	"math"
)

type scheduler interface {
	nextIndex() int
}

// newScheduler uses scWeights to create a new scheduler for selecting subconns
// in a picker.  It will return a round robin implementation if at least
// len(scWeights)-1 are zero or there is only a single subconn, otherwise it
// will return an Earliest Deadline First (EDF) scheduler implementation that
// selects the subchannels according to their weights.
func (p *picker) newScheduler() scheduler { // and the operation happens from new scheduler...so needs it here
	
	// does the new scheduler come in after the picker is built?
	// what is logically a scheduler update...
	
	// Do count as well...the rr case is the easiet, declare it at the top level balancer
	
	scWeights := p.scWeights() // only want to compute this once anyway...
	
	

	// Either scope a variable to this algorithm or just record adhoc as is...

	// Logs locality and target as labels - can change from updateccs but
	// doesn't blow up cardinality only until locality

	// For target - target hooks the top level balancer/channel, so this is fixed over time


	// Counter...can just record ad hoc...
	/*

	Number of scheduler updates in which there were not enough endpoints with
	valid weight, which caused the WRR policy to fall back to RR behavior.

	*/

	// Counter which always increases, either seperate callouts 1 2 3 4 5 or
	// persist and do 5, same from OTel point of view

	/*
	Number of endpoints from each scheduler update that don't yet have usable
	weight information (i.e., either the load report has not yet been received,
	or it is within the blackout period).
	*/


	/*

	Number of endpoints from each scheduler update whose latest weight is older
	than the expiration period.

	*/ // Count...



	// Histo:
	/*
	Weight of each endpoint, recorded on every scheduler update. Endpoints
	without usable weights will be recorded as weight 0.
	*/

	// For target I need to send it down from weighted target through resolver attr,

	// write it (protected by mutex?)...target comes in at build time and is
	// fixed and is read only after that



	n := len(scWeights)
	if n == 0 { // doesn't count
		// is the n == 0 and n == 1 case also logically fallback to rr? this is orthogonal to fallback in config...
		// fallback to rr balancer doesn't make sense because
		// no concept of a scheduler update there...

		// "When less than two subchannels have load info, all subchannels will
		// get the same weight and the policy will behave the same as
		// round_robin." so yeah this one..

		return nil
	}
	if n == 1 {
		// Vaugeness - talk to other languages

		// Doug thinks not fallback if one (and zero) - just an optimization...

		return &rrScheduler{numSCs: 1, inc: p.inc}
	}
	sum := float64(0)
	numZero := 0
	max := float64(0)
	for _, w := range scWeights {
		sum += w
		if w > max {
			max = w
		}
		if w == 0 {
			numZero++
		}
	}

	// endpoint weight not yet usable...is that just zero ones...I think so...

	// endpoint weight stale...where is this determined

	// weight of each endpoint (so defer a func that happens at each record point that loops through?)


	// less than 2...

	// ordering matters for labels...
	if numZero >= n-1 { // Not enough endpoints with valid weight, is this the only valid emission site?
		rrFallbackHandle.Record(p.metricsRecorder/*mr*/, 1, []string{p.target, p.locality}.../*[]string{target, locality}*/) // how to plumb target and locality here? Do I need to make it a method on some balancer...
		return &rrScheduler{numSCs: uint32(n), inc: p.inc} // is this "fallback" to rr?
	}
	unscaledMean := sum / float64(n-numZero)
	scalingFactor := maxWeight / max
	mean := uint16(math.Round(scalingFactor * unscaledMean))

	weights := make([]uint16, n)
	allEqual := true
	for i, w := range scWeights {
		if w == 0 {
			// Backends with weight = 0 use the mean.
			weights[i] = mean
		} else {
			scaledWeight := uint16(math.Round(scalingFactor * w))
			weights[i] = scaledWeight
			if scaledWeight != mean {
				allEqual = false
			}
		}
	}

	if allEqual { // this just happens to be all equal, doesn't count
		/* Does "all equal" count as "fall back to RR behavior" or is it not 1:1?

		Number of scheduler updates in which there were not enough endpoints
		with valid weight, which caused the WRR policy to fall back to RR
		behavior.

		*/
		return &rrScheduler{numSCs: uint32(n), inc: p.inc}
	}

	/*
	Number of endpoints from each scheduler update that don't yet have usable
	weight information (i.e., either the load report has not yet been received,
	or it is within the blackout period).
	*/
	// Rewrite the algo? Plumb in scWeights helper already present?

	logger.Infof("using edf scheduler with weights: %v", weights)
	return &edfScheduler{weights: weights, inc: p.inc} // I think ok anyway, always a pointer to function...
}

const maxWeight = math.MaxUint16

// edfScheduler implements EDF using the same algorithm as grpc-c++ here:
//
// https://github.com/grpc/grpc/blob/master/src/core/ext/filters/client_channel/lb_policy/weighted_round_robin/static_stride_scheduler.cc
type edfScheduler struct {
	inc     func() uint32
	weights []uint16
}

// Returns the index in s.weights for the picker to choose.
func (s *edfScheduler) nextIndex() int {
	const offset = maxWeight / 2

	for {
		idx := uint64(s.inc())

		// The sequence number (idx) is split in two: the lower %n gives the
		// index of the backend, and the rest gives the number of times we've
		// iterated through all backends. `generation` is used to
		// deterministically decide whether we pick or skip the backend on this
		// iteration, in proportion to the backend's weight.

		backendIndex := idx % uint64(len(s.weights))
		generation := idx / uint64(len(s.weights))
		weight := uint64(s.weights[backendIndex])

		// We pick a backend `weight` times per `maxWeight` generations. The
		// multiply and modulus ~evenly spread out the picks for a given
		// backend between different generations. The offset by `backendIndex`
		// helps to reduce the chance of multiple consecutive non-picks: if we
		// have two consecutive backends with an equal, say, 80% weight of the
		// max, with no offset we would see 1/5 generations that skipped both.
		// TODO(b/190488683): add test for offset efficacy.
		mod := uint64(weight*generation+backendIndex*offset) % maxWeight

		if mod < maxWeight-weight {
			continue
		}
		return int(backendIndex)
	}
}

// A simple RR scheduler to use for fallback when fewer than two backends have
// non-zero weights, or all backends have the same weight, or when only one
// subconn exists.
type rrScheduler struct {
	inc    func() uint32
	numSCs uint32
}

func (s *rrScheduler) nextIndex() int {
	idx := s.inc()
	return int(idx % s.numSCs)
}
