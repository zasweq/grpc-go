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
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/buffer"
	"google.golang.org/grpc/resolver"
	"unsafe"
)

type subConnWrapper struct {
	// Are we embedding a SubConn or implementing a SubConn?
	balancer.SubConn

	// The subchannel wrappers created by the outlier_detection LB policy will
	// hold a reference to its map entry in the LB policy, if that map entry
	// exists. Used to update call counter per RPC "when the request finishes" from picker.
	// obj *object // this can be nil - add nil checks

	obj unsafe.Pointer // *object

	// If you make this an unsafe.Pointer and have it Loaded every time, can do a nil check then

	// The subchannel wrapper will track the latest state update from the
	// underlying subchannel. By default, it will simply pass those updates
	// along. Problem: state updates come from Client Conn in grpc-go?

	// written in UpdateSubConnState(), can this race?
	// uneject() which can get called from UpdateAddresses/interval timer
	latestState balancer.SubConnState // This can either be stored in the wrapper or the balancer...

	// ejected gets written in eject() and uneject() which can get called from UpdateAddresses/interval timer

	// NewSubConn and gets written in UpdateSubConnState, eventually will reach
	// consistency, if you simply protect with a mutex

	ejected bool // Read by od balancer to...not send updates downward if ejected in UpdateSubConnState()...I guess the balancer will persist it as well.
	// Yup, in clusterimpl.go the balancer writes to this field (atomically since it can be read and written concurrently)

	addresses []resolver.Address // For use in plurality checks in UpdateAddresses()
	// gets written to at heap construction time, read in a happens before algorithm in UpdateAddresses(),
	// you're good no need to sync

	scUpdateCh *buffer.Unbounded
	// No need for sync
}

// In regards to synchronization, this eject/uneject method
// gets called from the event of triggering the interval timer only.
// Thus, I don't think you need any mutexes...

// The wrapper will have the following methods:

// eject(): The wrapper will report a state update (which way?) with the TRANSIENT_FAILURE
// state, and will stop passing along updates from the underlying subchannel.
func (scw *subConnWrapper) eject() { // mutex protecting this call?
	// Report a TRANSIENT_FAILIURE state
	// scw.cc.UpdateSubConnState(sc, connectivity.State) // <- will need to hold a reference to sc (itself?) and also cc

	// s/putting something on a update channel
	//     ejectedUpdate(scw, ejected = true)

	// stop passing along updates from the underlying subchannel...bool?
	scw.ejected = true

	// if we send down update here instead of in od balancer,
	// this needs to hold onto balancer field.
	scw.scUpdateCh.Put(&scUpdate{
		subConn: scw,
		state:  balancer.SubConnState{
			ConnectivityState: connectivity.TransientFailure,
		},
	})
	/*scw.childPolicy.UpdateSubConnState(scw, balancer.SubConnState{ // this needs to ref to the child policy in the od balancer..., not store it here, this can get closed etc.
		ConnectivityState: connectivity.TransientFailure,
	})*/
}

// uneject(): The wrapper will report a state update with the latest update from
// the underlying subchannel, and resume passing along updates from the
// underlying subchannel.
func (scw *subConnWrapper) uneject() {
	// The wrapper will report a state update with the latest update from
	// the underlying subchannel. (Downward toward Client Conn?)

	// Resume passing along updates from the underlying subchannel (upward
	// toward grpc?)

	// scw.cc.UpdateSubConnState(sc, scw.recentState) <- this is balancer.Balancer so...? lol

	// s/putting something on a update channel
	//      ejectedUpate(scw, ejected = false)

	scw.ejected = false
	scw.scUpdateCh.Put(&scUpdate{
		subConn: scw,
		state:  scw.latestState,
	})
	// scw.childPolicy.UpdateSubConnState(scw, scw.latestState) // latestState is synced with UpdateSubConnState calls right (if there in same run() goroutine) no, need to sync with a read mu
}

// intercept update state to persist most recent and don't forward


// How the balancer uses this object:

// "When the child policy asks for a subchannel, the outlier_detection will wrap
// the subchannel with a wrapper" (NewSubConn())

// "Then, the subchannel wrapper will be added to the list in the map entry for
// its address, if that map entry exists."

// "If there is no map entry, or if the subchannel is created with multiple
// addresses, the subchannel will be ignored for outlier detection. If that
// address is currently ejected, that subchannel wrapper's eject method will be
// called."

// "Passing along updates from an underlying subchannel..." what does this mean?
// The SubConn API is:
// type SubConn interface
// UpdateAddresses([]resolver.Address) <- deprecated
// Connect()
// There is nothing in relation to "updates from an underlying subchannel", I think it is related to VVV
// Maybe with regards to doward flow - updates from an underlying subchannel

// Update Addresses...upward flow that's tied to a SubConn

// Maybe we're talking about the Client Conn's API that corresponds to SubConn?

// balancer.ClientConn and subconns...

// balancer.Balancer and subconns...

// Only thing is UpdateSubConnState()

// balancer wants to delete a SubConn:

// removeSubConn() call upward
// Client Conn removes it, switches state to shutdown
// UpdateSubConnState with shutdown sent downward
// hits lowest level, balancer sees that SubConn is now
// shutdown, can update picker accordingly.


// "The LB policy will eject endpoints by having their subchannels report TRANSIENT_FAILURE to the child policy."
// This def implies going downward - "child policy"


// State that gets interfaced with in balancer.

// ejected T | F

// UpdateState reads this to determine whether to forward down...

// UpdateAddresses






// TODO:
// List of questions for Menghan
// I do have some about "forwarding"...both ways...?

// Fix import