/*
 *
 * Copyright 2021 gRPC authors.
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

// Package server contains internal server-side functionality used by the public
// facing xds package.
package server

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/grpclog"
	internalbackoff "google.golang.org/grpc/internal/backoff"
	internalgrpclog "google.golang.org/grpc/internal/grpclog"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/xds/internal/xdsclient/bootstrap"
	"google.golang.org/grpc/xds/internal/xdsclient/xdsresource"
)

var (
	logger = grpclog.Component("xds")

	// Backoff strategy for temporary errors received from Accept(). If this
	// needs to be configurable, we can inject it through ListenerWrapperParams.
	bs = internalbackoff.Exponential{Config: backoff.Config{
		BaseDelay:  5 * time.Millisecond,
		Multiplier: 2.0,
		MaxDelay:   1 * time.Second,
	}}
	backoffFunc = bs.Backoff
)

// ServingModeCallback is the callback that users can register to get notified
// about the server's serving mode changes. The callback is invoked with the
// address of the listener and its new mode. The err parameter is set to a
// non-nil error if the server has transitioned into not-serving mode.
type ServingModeCallback func(addr net.Addr, mode connectivity.ServingMode, err error)

// DrainCallback is the callback that an xDS-enabled server registers to get
// notified about updates to the Listener configuration. The server is expected
// to gracefully shutdown existing connections, thereby forcing clients to
// reconnect and have the new configuration applied to the newly created
// connections.
type DrainCallback func(addr net.Addr)

// XDSClient wraps the methods on the XDSClient which are required by
// the listenerWrapper.
type XDSClient interface {
	WatchResource(rType xdsresource.Type, resourceName string, watcher xdsresource.ResourceWatcher) (cancel func())
	BootstrapConfig() *bootstrap.Config
}

// ListenerWrapperParams wraps parameters required to create a listenerWrapper.
type ListenerWrapperParams struct {
	// Listener is the net.Listener passed by the user that is to be wrapped.
	Listener net.Listener
	// ListenerResourceName is the xDS Listener resource to request.
	ListenerResourceName string
	// XDSCredsInUse specifies whether or not the user expressed interest to
	// receive security configuration from the control plane.
	XDSCredsInUse bool
	// XDSClient provides the functionality from the XDSClient required here.
	XDSClient XDSClient
	// ModeCallback is the callback to invoke when the serving mode changes.
	ModeCallback ServingModeCallback
	// DrainCallback is the callback to invoke when the Listener gets a LDS
	// update.
	DrainCallback DrainCallback
}

// NewListenerWrapper creates a new listenerWrapper with params. It returns a
// net.Listener and a channel which is written to, indicating that the former is
// ready to be passed to grpc.Serve().
//
// Only TCP listeners are supported.
func NewListenerWrapper(params ListenerWrapperParams) (net.Listener, <-chan struct{}) {
	lw := &listenerWrapper{
		Listener:          params.Listener,
		name:              params.ListenerResourceName,
		xdsCredsInUse:     params.XDSCredsInUse,
		xdsC:              params.XDSClient,
		modeCallback:      params.ModeCallback,
		drainCallback:     params.DrainCallback,
		isUnspecifiedAddr: params.Listener.Addr().(*net.TCPAddr).IP.IsUnspecified(),

		mode:        connectivity.ServingModeStarting,
		closed:      grpcsync.NewEvent(),
		goodUpdate:  grpcsync.NewEvent(), // is this the right thing to trigger serving, a singular good update?
	}
	lw.logger = internalgrpclog.NewPrefixLogger(logger, fmt.Sprintf("[xds-server-listener %p] ", lw))

	// Serve() verifies that Addr() returns a valid TCPAddr. So, it is safe to
	// ignore the error from SplitHostPort().
	lisAddr := lw.Listener.Addr().String()
	lw.addr, lw.port, _ = net.SplitHostPort(lisAddr)

	lw.rdsHandler = newRDSHandler(lw, lw.xdsC, lw.logger)
	lw.cancelWatch = xdsresource.WatchListener(lw.xdsC, lw.name, &ldsWatcher{
		parent: lw,
		logger: lw.logger,
		name:   lw.name,
	})
	return lw, lw.goodUpdate.Done()
}

// listenerWrapper wraps the net.Listener associated with the listening address
// passed to Serve(). It also contains all other state associated with this
// particular invocation of Serve().
type listenerWrapper struct {
	net.Listener
	logger *internalgrpclog.PrefixLogger

	name          string
	xdsCredsInUse bool
	xdsC          XDSClient
	cancelWatch   func()
	modeCallback  ServingModeCallback
	drainCallback DrainCallback

	// Set to true if the listener is bound to the IP_ANY address (which is
	// "0.0.0.0" for IPv4 and "::" for IPv6).
	isUnspecifiedAddr bool
	// Listening address and port. Used to validate the socket address in the
	// Listener resource received from the control plane.
	addr, port string

	// This is used to notify that a good update has been received and that
	// Serve() can be invoked on the underlying gRPC server. Using an event
	// instead of a vanilla channel simplifies the update handler as it need not
	// keep track of whether the received update is the first one or not.
	goodUpdate *grpcsync.Event
	// A small race exists in the XDSClient code between the receipt of an xDS
	// response and the user cancelling the associated watch. In this window,
	// the registered callback may be invoked after the watch is canceled, and
	// the user is expected to work around this. This event signifies that the
	// listener is closed (and hence the watch is cancelled), and we drop any
	// updates received in the callback if this event has fired.
	closed *grpcsync.Event

	// mu guards access to the current serving mode and the filter chains. The
	// reason for using an rw lock here is that these fields are read in
	// Accept() for all incoming connections, but writes happen rarely (when we
	// get a Listener resource update).
	mu sync.RWMutex
	// Current serving mode.
	mode connectivity.ServingMode
	// Filter chains received as part of the last good update.
	filterChains *xdsresource.FilterChainManager // maybe active filterChains
	// Data structure to update correct heap memory representing rds
	// configuration. It's literally a list of filter chains, which do contain a pointer
	tryThisSlice []xdsresource.FilterChain // honestly, could even have this be part of filter chains...call a func on it...


	pendingFilterChains *xdsresource.FilterChainManager // switch to filter chains if ready and set

	// Still a ref to rds data (already built out) if rds watcher closed
	// updateRDS doesn't get called updating fc but rds data is still being
	// pointed to and used by fc



	// rdsHandler is used for any dynamic RDS resources specified in a LDS
	// update.
	rdsHandler *rdsHandler // accesses to map I think sync - just to build fc data but that's an atomic write to a pointer
}

// lds update updates route names to watch (requires certain logic)
// and also top level fcm becomes pending (I think build out route configs for all?)
func (lw *listenerWrapper) ldsFlow(update xdsresource.ListenerUpdate) {

	// map[FilterChain]unsafe.Ptr, where it points to vhswi, usable, how to link
	// on an accept? Doug wants me to store this in here, maybe have helpers
	// that get called in the LDS/RDS flow, that write to a map like that
	ilc := update.InboundListenerCfg
	// new lds and rds need to trigger
	// if you have already pending filter chains that haven't updated (come sync) (what if an update comes after...check against cancels (persist list of all the cancels from most recent lds)?)
	// if a pending one hasn't gone active hasn't received all it's config, update watch there so I think this uncondtional write is ok
	// write unconditionally I think...
	lw.pendingFilterChains = ilc.FilterChains // doesn't race because conn accepts() read active, but not pending (if no active, just close conn right)?
	lw.rdsHandler.updateRouteNamesToWatch(ilc.FilterChains.RouteConfigNames) // should that call back?
	// calls back into this to determine if ready and switch if needed...

	if lw.rdsHandler.determineRDSReady() { // (or have this below)
		lw.maybeUpdateFilterChains() // either in this or in if conditioanl check if pending is not nil. Checks in this function call (need to make sure I don't nil panic from conns to close read in test)
	}
	// write to pending, always even on first update once it's ready switch to current (need to switch to serving...)
	// what happens if route names == 0, I think rds handler will take care of this
}

// at the end of processing LDS,
// and at the end of processing RDS, need to determine if pending is ready and then switch it over
// if all rds ready
//            switch filter chains
// I think is the correct flow here...

func (lw *listenerWrapper) afterLDSandRDS(rdsName string) { // call unconditionally...

	// Is this operation always correct? I.e. does this actually work? test it
	if lw.rdsHandler.determineRDSReady() { // // bool that determines if rds is ready.
		// pending -> current, operation involving closing conns (fcms need to keep track of Conn's, also get sync to work)
		// is closing conns logically "draining" conns?
		lw.maybeUpdateFilterChains()
	}
}

// map[filter chain] blob representing (lds (fc) + rds) and built on a new lds and rds update, need to pull the rds data in somehow
// how to link filter chain to pointer, easy just write it at fc instantiation time
// need to update the pointers whenever lds or rds changes
// lw holds lds + rds data and tracking of filter chains
// route watcher is dumb

// on rds data...trigger the rebuild for just the fcs that correspond and point to this rds
// on new lds data - after updating route names to watch,
// build all corresponding usable route configurations
// This requires a separate data structure ^^^

// if rds ready (call unconditionally)...
func (l *listenerWrapper) maybeUpdateFilterChains() { // swap operation is correct I think, grab mu give it up, this works...
	if l.pendingFilterChains == nil {
		// Nothing to update, return early.
		return
	}
	// drain right here, since you know you will swap (this replaces connsToClose)

	// conn either needs to accept and shut down or get new, can't race (related to problem I wrote in listener_wrapper Accept())
	l.mu.Lock()
	// "Updates to a Listener cause all older connections on that Listener to be
	// gracefully shut down with a grace period of 10 minutes for long-lived
	// RPC's, such that clients will reconnect and have the updated
	// configuration apply." - A36 Note that this is not the same as moving the
	// Server's state to ServingModeNotServing. That prevents new connections
	// from being accepted, whereas here we simply want the clients to reconnect
	// to get the updated configuration.
	if l.drainCallback != nil { // graceful close trigger...
		l.drainCallback(l.Listener.Addr())
	} // (think about eventual consistency of serving mode state, I think process inline

	// read conns to close? - this needs to persist a list of conns to close
	// Java gives the FCM a Conn on an accept, this manages the conns and holds a list
	// close the list from FCM of conns it manages (FCM Conn Manager or something)
	// connsToClose := l.filterChains.Conns() // switch this to connWrapper, export perhaps. store these conns in the fcm
	// or gracefully close?
	// called when new lds goes ready

	l.filterChains = l.pendingFilterChains
	l.mu.Unlock()

	/*for _, conn := range connsToClose { // (perhaps async - sometime in future?)

		// is this Close() a graceful close? (see Drain callback?) rework Drain
		// callback and also communication/triggering of server states...affects mutex grab

		// This handles cleaning up all the resources of the object, including all of it's state.
		conn.Close() // you need this, since there is extra memory and things to clean up on top of the net.Conn
	}*/

	// Now where do I store this? and if I do need some sync
	// Accept() adds to conns to close, this gets called on a new LDS update
	// sync access in the filter chain manager?
}

// rdsUpdate rebuilds any routing configuration server side for any filter
// chains that point to this RDS.
func (l *listenerWrapper) rdsUpdate(routeName string, rcu rdsWatcherUpdate) { // Logically sort of a swap.
	// rds update updates filter chain corresponding (trigger from helper), and also caches it

	// routeName
	// map[name]->[]xdsresource.FilterChain (this holds the ref to update), points to same vhswi that it gives conn on an accept (that's how it communicates, vh() on conn wrapper)
	// or honestly just []xdsresource.FilterChain (when accept, grab the * -> * into a local var)
	// xdsresource.FilterChain already has the rds name and pointer to []vhswi so just need that instead of map (rds updates are not on fast path)


	for _, fc := range l.tryThisSlice { // only written to from lds being ready (need to trigger that from here too)
		if fc.RouteConfigName == routeName { // I think this encapsulates all the logic we need wrt inline or not inline
			// needs to also fail l7 rpcs with err or not
			// I think make error be nil, if it gets to serving it means that it's
			// received a configuration at some point (Ignore errors that haven't been
			// there)

			// if update is set use that (I need to write a test for l7 failures on NACK or resource not found)
			if rcu.err != nil && rcu.update == nil { // If it calls in will have set one...
				// stick nil as the value atomically of the route configuration. Will trigger L7 failure
				var vhswi *[]xdsresource.VirtualHostWithInterceptors
				// now has pointer
				atomic.StorePointer(fc.VHS, unsafe.Pointer(vhswi)) // not a nil pointer a pointer of type that points to nil
				continue
			} // could make this helper...


			vhswi, err := fc.ConstructUsableRouteConfiguration(*rcu.update) // or read from map...needs to not mess with heap
			if err != nil {
				// what to do...also what triggers it? should this also cause an error?
			}
			atomic.StorePointer(fc.VHS, unsafe.Pointer(&vhswi)) // unsafe pointer to a pointer, &vhswi is the address (i.e. pointer) to newly construct []vhswi (escapes to heap)
		}
	}
	// RDS Update above could finish RDS tree, and make pending go ready so always do it (at end of rds after persisting, etc.)

	if l.rdsHandler.determineRDSReady() { // (or have this below)
		l.maybeUpdateFilterChains() // either in this or in if conditioanl check if pending is not nil. Checks in this function call (need to make sure I don't nil panic from conns to close read in test)
	}
}
// triggering swap is more logic I need to work on...

// yesterday plumbed VirtualHosts and L7 error conditions through the stack (rdsHandler -> lisWrapper -> Accept() -> Server using this Conn)
// yesterday I did nil update and vhswi

// maybe update checks if pending is not nil, and if it switches over and calls below in the case

// Throws away map once it goes ready vvv, throws away ref

// map k string v *1 -> *same -> vhswi

// map updates update pointer atomically
// conn atomically reads *same, *same is read and written to atomically (x bits)

// accepted conn has *2 -> *same -> vhswi

// called on a lds going fully ready (I think including the first one, if zero I think isready will determine that, cancels while clearing rds update cache seemsssss to work)

// Another thing is the inverse of what Mark wrote as places it goes ready
// trigger serving change once lds goes ready, automatically trigger all the time switch to READY?
func (l *listenerWrapper) instantiateFilterChainRoutingConfiguration() { // instantiateFilterChainRoutingConfiguration to instantiate fcs rcu, loops through slice (either builds using inline or dynamic rds data)

	// So swap call this operation? Whenever we have a new current I think, 1:1 with tryThisSlice...
	// this slice is one to one with serving fcs
	// "throw those refs away, garbage collection" (give up ref in fcm too)
	// New Full Map
	l.tryThisSlice = nil // zero this out, only read on an rds update, which is sync (this just stores pointers to synced memory, so no issue here)
	for _, fc := range l.filterChains.FilterChains() {
		// could alsooooo make this is a pointer if I wanted
		l.tryThisSlice = append(l.tryThisSlice, *fc) // can update nil...I think pointing to same fc in memory is fine, never gets updated
		if fc.InlineRouteConfig != nil {
			vhswi, err := fc.ConstructUsableRouteConfiguration(*fc.InlineRouteConfig)
			if err != nil { // This really shouldn't happen, but how to handle errors in this case?
				// error combining lds and rds, fail at l7 level? Is this right?
			}
			uPtr := unsafe.Pointer(&vhswi)
			atomic.StorePointer(fc.VHS, uPtr)
			continue
		}
		rcu := l.rdsHandler.updates[fc.RouteConfigName]
		if rcu.err != nil && rcu.update == nil {
			// stick nil as the value atomically of the route configuration.
			var vhswi *[]xdsresource.VirtualHostWithInterceptors
			atomic.StorePointer(fc.VHS, unsafe.Pointer(vhswi)) // not a nil pointer a pointer of type that points to nil
			continue
		}
		vhswi, err := fc.ConstructUsableRouteConfiguration(*rcu.update)
		if err != nil {
			// how to handle errors in this case?
		}
		uPtr := unsafe.Pointer(&vhswi)
		atomic.StorePointer(fc.VHS, uPtr)
		// build config and also construct this slice to persist for rds data (simplicity vs. pure 0(n) efficiency)
		// I don't need to persist anything with respect to route names
	}
}
// try and get these 5 files done (cleanup and see if logic makes sense)

// Will have to change the interface to server for mode changes for
// a. switching to ready in certain cases (and staying ready)
// b. logging l7 error server side

// Accept blocks on an Accept() on the underlying listener, and wraps the
// returned net.connWrapper with the configured certificate providers.
func (l *listenerWrapper) Accept() (net.Conn, error) { // this is ran in a go func async go server.Serve
	var retries int
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			// Temporary() method is implemented by certain error types returned
			// from the net package, and it is useful for us to not shutdown the
			// server in these conditions. The listen queue being full is one
			// such case.
			if ne, ok := err.(interface{ Temporary() bool }); !ok || !ne.Temporary() {
				return nil, err
			}
			retries++
			timer := time.NewTimer(backoffFunc(retries))
			select {
			case <-timer.C:
			case <-l.closed.Done():
				timer.Stop()
				// Continuing here will cause us to call Accept() again
				// which will return a non-temporary error.
				continue
			}
			continue
		}
		// Reset retries after a successful Accept().
		retries = 0

		// Since the net.Conn represents an incoming connection, the source and
		// destination address can be retrieved from the local address and
		// remote address of the net.Conn respectively.
		destAddr, ok1 := conn.LocalAddr().(*net.TCPAddr)
		srcAddr, ok2 := conn.RemoteAddr().(*net.TCPAddr)
		if !ok1 || !ok2 {
			// If the incoming connection is not a TCP connection, which is
			// really unexpected since we check whether the provided listener is
			// a TCP listener in Serve(), we return an error which would cause
			// us to stop serving.
			return nil, fmt.Errorf("received connection with non-TCP address (local: %T, remote %T)", conn.LocalAddr(), conn.RemoteAddr())
		}
		// It's when it gets persisted on server
		l.mu.RLock() // Make sure this doesn't cause deadlock...needs to protect whole conn accept...acutally I think this is good it's just what you wrap conn with
		if l.mode == connectivity.ServingModeNotServing {
			// Close connections as soon as we accept them when we are in
			// "not-serving" mode. Since we accept a net.Listener from the user
			// in Serve(), we cannot close the listener when we move to
			// "not-serving". Closing the connection immediately upon accepting
			// is one of the other ways to implement the "not-serving" mode as
			// outlined in gRFC A36.
			l.mu.RUnlock()
			conn.Close()
			continue
		}


		// dependening on how I do mode switch..
		// l.mu.RLock() can do this here...since mode doens't need to protected, accept and lds going ready I think
		// can happen concurrently or is there happens before relationship here?

		// after the error case, need to keep track of Conns
		fc, err := l.filterChains.Lookup(xdsresource.FilterChainLookupParams{
			IsUnspecifiedListener: l.isUnspecifiedAddr,
			DestAddr:              destAddr.IP,
			SourceAddr:            srcAddr.IP,
			SourcePort:            srcAddr.Port,
		})
		if err != nil {
			l.mu.RUnlock()
			// When a matching filter chain is not found, we close the
			// connection right away, but do not return an error back to
			// `grpc.Serve()` from where this Accept() was invoked. Returning an
			// error to `grpc.Serve()` causes the server to shutdown. If we want
			// to avoid the server from shutting down, we would need to return
			// an error type which implements the `Temporary() bool` method,
			// which is invoked by `grpc.Serve()` to see if the returned error
			// represents a temporary condition. In the case of a temporary
			// error, `grpc.Serve()` method sleeps for a small duration and
			// therefore ends up blocking all connection attempts during that
			// time frame, which is also not ideal for an error like this.
			l.logger.Warningf("Connection from %s to %s failed to find any matching filter chain", conn.RemoteAddr().String(), conn.LocalAddr().String())
			conn.Close()
			continue
		}
		// rds is already handled in map



		// return a ref that represents a * -> *

		// all this component needs is a way to map from fc1 rdsName1 -> config (to update the heap pointed to by config by atomically updating pointer (and allocating new heap memory, like Envoy))
		// know all the places to update
		// rdsName -> multiple refs perhaps? (amd update this based on LDS)
		// calls into rdsUpdate blocked by watcher diff
		// can still update correctly

		// on an lds diff can just append

		// fcm has to know the correct atomic ref somehow (why not just hold this atomic ref in the filter chain object)
		// active fcm 1:1
		// needs to know the *ref* to update from an rds update

		cw := &connWrapper{Conn: conn, filterChain: fc, parent: l, vhs: fc.VHS}

		//l.filterChains.AddConn(cw)

		// do a
		l.mu.RUnlock()
		return cw, nil
	}
} // Argh...not sync with drain callback and update **write** because drain callback touches map that gets written to
// after this accept returns, and after it wraps in server transport...

// need server transport ref to gracefully close

// Close closes the underlying listener. It also cancels the xDS watch
// registered in Serve() and closes any certificate provider instances created
// based on security configuration received in the LDS response.
func (l *listenerWrapper) Close() error {
	l.closed.Fire()
	l.Listener.Close()
	if l.cancelWatch != nil {
		l.cancelWatch()
	}
	l.rdsHandler.close()
	return nil
}

func (l *listenerWrapper) handleLDSUpdate(update xdsresource.ListenerUpdate) {
	// Make sure that the socket address on the received Listener resource
	// matches the address of the net.Listener passed to us by the user. This
	// check is done here instead of at the XDSClient layer because of the
	// following couple of reasons:
	// - XDSClient cannot know the listening address of every listener in the
	//   system, and hence cannot perform this check.
	// - this is a very context-dependent check and only the server has the
	//   appropriate context to perform this check.
	//
	// What this means is that the XDSClient has ACKed a resource which can push
	// the server into a "not serving" mode. This is not ideal, but this is
	// what we have decided to do. See gRPC A36 for more details.
	ilc := update.InboundListenerCfg
	if ilc.Address != l.addr || ilc.Port != l.port {
		l.switchMode(nil, connectivity.ServingModeNotServing, fmt.Errorf("address (%s:%s) in Listener update does not match listening address: (%s:%s)", ilc.Address, ilc.Port, l.addr, l.port))
		return
	}

	// "Updates to a Listener cause all older connections on that Listener to be
	// gracefully shut down with a grace period of 10 minutes for long-lived
	// RPC's, such that clients will reconnect and have the updated
	// configuration apply." - A36 Note that this is not the same as moving the
	// Server's state to ServingModeNotServing. That prevents new connections
	// from being accepted, whereas here we simply want the clients to reconnect
	// to get the updated configuration.
	if l.drainCallback != nil {
		l.drainCallback(l.Listener.Addr())
	}
	l.rdsHandler.updateRouteNamesToWatch(ilc.FilterChains.RouteConfigNames)
	// If there are no dynamic RDS Configurations still needed to be received
	// from the management server, this listener has all the configuration
	// needed, and is ready to serve.
	if len(ilc.FilterChains.RouteConfigNames) == 0 {
		l.switchMode(ilc.FilterChains, connectivity.ServingModeServing, nil)
		l.goodUpdate.Fire()
	}
}

// switchMode updates the value of serving mode and filter chains stored in the
// listenerWrapper. And if the serving mode has changed, it invokes the
// registered mode change callback.
func (l *listenerWrapper) switchMode(fcs *xdsresource.FilterChainManager, newMode connectivity.ServingMode, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.filterChains = fcs
	if l.mode == newMode && l.mode == connectivity.ServingModeServing {
		// Redundant updates are suppressed only when we are SERVING and the new
		// mode is also SERVING. In the other case (where we are NOT_SERVING and the
		// new mode is also NOT_SERVING), the update is not suppressed as:
		//   1. the error may have change
		//   2. it provides a timestamp of the last backoff attempt
		return
	}
	l.mode = newMode
	if l.modeCallback != nil {
		l.modeCallback(l.Listener.Addr(), newMode, err)
	}
}

func (l *listenerWrapper) switchMode2(newMode connectivity.ServingMode, err error) {
	// can only trigger sync with xDS, races with new conns and RPC's coming in though
	l.mu.Lock() // don't need this I think...
	defer l.mu.Unlock()

	// It also persists the mode...does this still need to happen?
	// the callback calls and puts on a channel

	if l.modeCallbacķ != nil {
		l.modeCallback(l.Listener.Addr(), newMode, err) // what the fuck...what behavior does this initiate...also drain server transports?
	}
}

func (l *listenerWrapper) drain() {
	// trigger this on a switch
	l.drainCallback(l.Listener.Addr()) // drains server transports, persists it
}

// ldsWatcher implements the xdsresource.ListenerWatcher interface and is
// passed to the WatchListener API.
type ldsWatcher struct {
	parent *listenerWrapper
	logger *internalgrpclog.PrefixLogger
	name   string
}

func (lw *ldsWatcher) OnUpdate(update *xdsresource.ListenerResourceData) {
	if lw.parent.closed.HasFired() {
		lw.logger.Warningf("Resource %q received update: %#v after listener was closed", lw.name, update)
		return
	}
	if lw.logger.V(2) {
		lw.logger.Infof("LDS watch for resource %q received update: %#v", lw.name, update.Resource)
	}
	lw.parent.handleLDSUpdate(update.Resource)
	lw.parent.ldsFlow(update.Resource) // is this guaranteed to not be nil?
}

// list out what Mark said here for resource not found...

// We are in NOT_SERVING in two cases: (1) before we get the initial LDS
// resource and its RDS dependencies, and (2) if we get a does-not-exist for the
// LDS resource.

func (lw *ldsWatcher) OnError(err error) {
	if lw.parent.closed.HasFired() {
		lw.logger.Warningf("Resource %q received error: %v after listener was closed", lw.name, err)
		return
	}
	if lw.logger.V(2) {
		lw.logger.Infof("LDS watch for resource %q reported error: %#v", lw.name, err)
	}
	// For errors which are anything other than "resource-not-found", we
	// continue to use the old configuration.
} // I think this is right

func (lw *ldsWatcher) OnResourceDoesNotExist() {
	if lw.parent.closed.HasFired() {
		lw.logger.Warningf("Resource %q received resource-not-found-error after listener was closed", lw.name)
		return
	}
	if lw.logger.V(2) {
		lw.logger.Infof("LDS watch for resource %q reported resource-does-not-exist error: %v", lw.name)
	}
	// should this clear out all the lds state including active filter chains?

	// and also all the rds state...update watchers to no watchers...

	// what happens here? Mark listed out when to get to certain server states...
	err := xdsresource.NewErrorf(xdsresource.ErrorTypeResourceNotFound, "resource name %q of type Listener not found in received response", lw.name)
	lw.parent.switchMode(nil, connectivity.ServingModeNotServing, err)
} // switch mode api change? Any other behaviors need to happen?

// graceful close or strong close? But is there even really a different
// what happens when you switch mode?
