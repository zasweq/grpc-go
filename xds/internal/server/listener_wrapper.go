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
	// switch name to activeFilterChainManager
	activeFilterChainManager *xdsresource.FilterChainManager // maybe activeFilterChainManager
	// Data structure to update correct heap memory representing rds
	// configuration. It's literally a list of filter chains, which do contain a pointer
	// switch name to activeFilterChainManager
	activeFilterChains []xdsresource.FilterChain // honestly, could even have this be part of filter chains...call a func on it...

	pendingFilterChainManager *xdsresource.FilterChainManager // switch to filter chains if ready and set

	// Still a ref to rds data (already built out) if rds watcher closed
	// updateRDS doesn't get called updating fc but rds data is still being
	// pointed to and used by fc



	// rdsHandler is used for any dynamic RDS resources specified in a LDS
	// update.
	rdsHandler *rdsHandler // accesses to map I think sync - just to build fc data but that's an atomic write to a pointer
} // new lis wrapper and rds handler

// lds update updates route names to watch (requires certain logic)
// and also top level fcm becomes pending (I think build out route configs for all?)
func (lw *listenerWrapper) ldsFlow(update xdsresource.ListenerUpdate) {
	ilc := update.InboundListenerCfg

	if ilc.Address != lw.addr || ilc.Port != lw.port {
		lw.mu.Lock()
		lw.switchMode2Locked(connectivity.ServingModeNotServing, fmt.Errorf("address (%s:%s) in Listener update does not match listening address: (%s:%s)", ilc.Address, ilc.Port, lw.addr, lw.port)) // not serving
		lw.mu.Unlock()
		return // It's like resource not found...just returns, and drains transports, I think makes sense (or can eat error if working), I think this is fine though.
	}

	// if a pending one hasn't gone active hasn't received all it's config,
	// update watch there so I think this uncondtional write to
	// pendingFilterChains is ok
	lw.pendingFilterChainManager = ilc.FilterChains
	lw.rdsHandler.updateRouteNamesToWatch(ilc.FilterChains.RouteConfigNames)

	if lw.rdsHandler.determineRDSReady() { // (or have this below)
		lw.maybeUpdateFilterChains() // either in this or in if conditioanl check if pending is not nil. Checks in this function call (need to make sure I don't nil panic from conns to close read in test) ugh that's the issue wrt creating server transport...
	}
	// write to pending, always even on first update once it's ready switch to current (need to switch to serving...)
	// what happens if route names == 0, I think rds handler will take care of this
}


func (lw *listenerWrapper) afterLDSandRDS() {
	// Is this operation always correct? I.e. does this actually work? test it
	if lw.rdsHandler.determineRDSReady() {
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
	if l.pendingFilterChainManager == nil {
		// Nothing to update, return early.
		return
	}

	// unconditionally write serving (log serving -> serving transitions I think so)
	// just writes it for accept
	l.mu.Lock()
	l.goodUpdate.Fire() // keep same flow of only serving on this good update, makes sense with respect to language for both cases
	l.switchMode2Locked(connectivity.ServingModeServing, nil) // either drains it from resource not found, or here, won't trigger since switches to serving mode not serving

	// drain right here, since you know you will swap (this replaces connsToClose)

	// needs to persist just the server transports for old lis, but this layer doesn't have access
	// to that object, comes after Accept()

	// conn either needs to accept and shut down or get new, can't race (related to problem I wrote in listener_wrapper Accept())
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

	// If I don't persist this here, will need to wait for it to clear, this scopes it appropriately.
	connsToClose := l.activeFilterChainManager.Conns() // switch this to connWrapper, export perhaps. store these conns in the fcm
	// or gracefully close?
	// called when new lds goes ready

	l.activeFilterChainManager = l.pendingFilterChainManager // Could I give this filter chain same thing - server transport?
	// Maybe call it instantiate new filter chain configuration (have Easwar review this too)
	l.instantiateFilterChainRoutingConfigurations()
	l.mu.Unlock() // happens atomically within this mu, I think this doesn't race, any new RPC's will now get configuration because needs this mu in Accept() for new full configuration...


	// perhaps wrap this in a go func(), blocking on each conn is fine to drain transport
	for _, conn := range connsToClose { // (perhaps async - sometime in future?)

		// is this Close() a graceful close? (see Drain callback?) rework Drain
		// callback and also communication/triggering of server states...affects mutex grab

		// This handles cleaning up all the resources of the object, including all of it's state.

		// need to typecast this and maybe handle ok
		conn.(*connWrapper).drain() // you need this, since there is extra memory and things to clean up on top of the net.Conn
	}
	// ()


	// Now where do I store this? and if I do need some sync
	// Accept() adds to conns to close, this gets called on a new LDS update races with sync
	// sync access in the filter chain manager?
}

// rdsUpdate rebuilds any routing configuration server side for any filter
// chains that point to this RDS.
func (l *listenerWrapper) rdsUpdate(routeName string, rcu rdsWatcherUpdate) { // Logically sort of a swap.
	// rds update updates filter chain corresponding (trigger from helper), and also caches it
	// Update any corresponding filter chains active routing configuration
	for _, fc := range l.activeFilterChains { // only written to from lds being ready (need to trigger that from here too)
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


			vhswi, _ := fc.ConstructUsableRouteConfiguration(*rcu.update) // or read from map...needs to not mess with heap
			/*if err != nil {
				// what to do...also what triggers it? should this also cause an error?
				// yes store it as nil and then yeah
			}*/
			// vhswi is nil in error case, pointer to nil works
			atomic.StorePointer(fc.VHS, unsafe.Pointer(&vhswi)) // unsafe pointer to a pointer, &vhswi is the address (i.e. pointer) to newly construct []vhswi (escapes to heap)
		}
	}
	// RDS Update above could finish RDS tree, and make pending go ready so always do it (at end of rds after persisting, etc.)

	if l.rdsHandler.determineRDSReady() { // (or have this below)
		l.maybeUpdateFilterChains() // either in this or in if conditioanl check if pending is not nil. Checks in this function call (need to make sure I don't nil panic from conns to close read in test)
	}
}

// yesterday plumbed VirtualHosts and L7 error conditions through the stack (rdsHandler -> lisWrapper -> Accept() -> Server using this Conn)
// yesterday I did nil update and vhswi (l7 level)


// called on a lds going fully ready (I think including the first one, if zero I
// think isready will determine that, cancels while clearing rds update cache
// seemsssss to work)

// Another thing is the inverse of what Mark wrote as places it goes ready
// trigger serving change once lds goes ready, automatically trigger all the time switch to READY?

// instantiateFilterChainRoutingConfigurations instantiates all of the routing
// configuration for the newly active filter chains. For any inline route
// configurations, uses that, otherwise uses cached rdsHandler updates.
func (l *listenerWrapper) instantiateFilterChainRoutingConfigurations() { // instantiateFilterChainRoutingConfigurations to instantiate fcs rcu, loops through slice (either builds using inline or dynamic rds data)

	// So swap call this operation? Whenever we have a new current I think, 1:1 with tryThisSlice...
	// this slice is one to one with serving fcs
	// "throw those refs away, garbage collection" (give up ref in fcm too)

	// New Filter Chains based off new serving one (when is this called, swap I think)

	// Construct all filter chains route configuration neded
	l.activeFilterChains = nil // zero this out, only read on an rds update, which is sync (this just stores pointers to synced memory, so no issue here)
	for _, fc := range l.activeFilterChainManager.FilterChains() {
		// could alsooooo make this is a pointer if I wanted
		l.activeFilterChains = append(l.activeFilterChains, *fc) // can update nil...I think pointing to same fc in memory is fine, never gets updated
		if fc.InlineRouteConfig != nil {
			vhswi, _ := fc.ConstructUsableRouteConfiguration(*fc.InlineRouteConfig) // typed as array, but returns nil, so equivalent to 379
			/*if err != nil { // This really shouldn't happen, but how to handle errors in this case?
				// error combining lds and rds, fail at l7 level? Is this right?
			}*/
			uPtr := unsafe.Pointer(&vhswi)
			atomic.StorePointer(fc.VHS, uPtr)
			continue
		} // Inline configuration constructed once...
		rcu := l.rdsHandler.updates[fc.RouteConfigName] // right here persists old cached RDS Data in order to rebuild configuration with
		if rcu.err != nil && rcu.update == nil {
			// stick nil as the value atomically of the route configuration.
			var vhswi *[]xdsresource.VirtualHostWithInterceptors
			atomic.StorePointer(fc.VHS, unsafe.Pointer(vhswi)) // not a nil pointer a pointer of type that points to nil
			continue
		}
		vhswi, err := fc.ConstructUsableRouteConfiguration(*rcu.update)
		if err != nil {
			// how to handle errors in this case?
			// errors look like they really shouldn't happen if lds and rds passed xDS Client validation.
			// I think that logically these can be treated like resource not found...
			uPtr := unsafe.Pointer(&vhswi)
			atomic.StorePointer(fc.VHS, uPtr)
			continue
		}
		uPtr := unsafe.Pointer(&vhswi)
		atomic.StorePointer(fc.VHS, uPtr)
		// build config and also construct this slice to persist for rds data (simplicity vs. pure 0(n) efficiency)
		// I don't need to persist anything with respect to route names
	}
}
// try and get these 5 files done (cleanup and see if logic makes sense) fcm is done

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
		fc, err := l.activeFilterChainManager.Lookup(xdsresource.FilterChainLookupParams{
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

		l.activeFilterChainManager.AddConn(cw) // synced at this layer with Drain() operation...

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


	// is this even possible to hit...I think keep this
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
	} // when does lw even get instantiated?



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
		l.goodUpdate.Fire() // in master, this triggers on essentially first lds going ready
	}
}

// is the rds conditional for it being ready correct?

// List all this stuff out for discussion with Doug...

// server creates lw, blocks on good update
// for it to Serve()

/*
// I honestly think can fire good update and keep flow, aligns with language,
// Doesn't serve until first good update, which aligns with language below.
// If goes not serving after, Accept() then Close().

To serve RPCs, the XdsServer must have its xDS configuration, provided via a
Listener resource and potentialy other related resources. When its xDS
configuration does not exist or has not yet been received the server must be in
a "not serving" mode. This is ideally implemented by not listen()ing on the
port. If that is impractical an implementation may be listen()ing on the port,
but it must also accept() and immediately close() connections, making sure to
not send any data to the client.
*/


// switchMode updates the value of serving mode and filter chains stored in the
// listenerWrapper. And if the serving mode has changed, it invokes the
// registered mode change callback.
func (l *listenerWrapper) switchMode(fcs *xdsresource.FilterChainManager, newMode connectivity.ServingMode, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.activeFilterChainManager = fcs
	if l.mode == newMode && l.mode == connectivity.ServingModeServing { // suppressed redundant updates - do I want this?
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

func (l *listenerWrapper) switchMode2Locked(newMode connectivity.ServingMode, err error) { // handled inline rather than in a goroutine
	// can only trigger sync with xDS, races with new conns and RPC's coming in though
	// l.mu.Lock() // don't need this I think...
	// defer l.mu.Unlock()

	// It also persists the mode...does this still need to happen? yes to gate
	// Accept(0 with immediately closing)...update state, no problem with
	// eventual consistency... the callback calls and puts on a channel

	// racing with respect to drain callback reading transport in map...need
	// some sync point wrt drain callback...

	l.mu.Lock()
	l.mode = newMode // this races with accept...how to protect the racy operations?

	// not serving not serving will close all them twice? Gates only one call in http2Server Drain()...
	if l.mode == connectivity.ServingModeNotServing {
		// Drain All Server Transports...trigger xDS server -> all server transports with respect to goaway (http/2 on top of tcp)
		/*if gs, ok := s.gs.(*grpc.Server); ok {
			drainServerTransports(gs, args.addr.String()) // keep this...why does this put address...switch to drain all server transports...
		}*/

		// does this race with anything...?

		for _, conn := range l.activeFilterChainManager.Conns() {
			conn.(*connWrapper).drain()
		}

		// what happens if it gets accepted here
	}
	l.mu.Unlock() // either it gets accepted before mode switch and we immediately close, or we do it after and it immediately closes...

	// The XdsServer API will allow applications to register a "serving state"
	// callback to be invoked when the server begins serving and when the
	// server encounters errors that force it to be "not serving". If "not
	// serving", the callback must be provided error information, for
	// debugging use by developers - A36.
	if l.modeCallback != nil {
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
	lw.parent.ldsFlow(update.Resource) // is this guaranteed to not be nil? I think so
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
	if lw.parent.closed.HasFired() { // can this race?
		lw.logger.Warningf("Resource %q received resource-not-found-error after listener was closed", lw.name) // can this race?
		return
	}
	if lw.logger.V(2) {
		lw.logger.Infof("LDS watch for resource %q reported resource-does-not-exist error: %v", lw.name)
	}

	// should this clear out all the lds state including active filter chains? Not serving blocks it active from being read
	// I think if goes serving not serving serving it will need to wait to swap() pending in, before goes serving, so
	// will not use old active filter chains, but also won't hurt

	// and also all the rds state...update watchers to no watchers...

	// what happens here? Mark listed out when to get to certain server states...filters out serving -> serving transitions
	err := xdsresource.NewErrorf(xdsresource.ErrorTypeResourceNotFound, "resource name %q of type Listener not found in received response", lw.name)
	// lw.parent.switchMode(nil, connectivity.ServingModeNotServing, err) (suppresses any new updates...)

	lw.parent.mu.Lock()
	defer lw.parent.mu.Unlock()
	// gracefully shutdown conns, happens already in processing of mode
	lw.parent.pendingFilterChainManager = nil
	lw.parent.activeFilterChainManager = nil

	// clear rds handlers watch
	lw.parent.rdsHandler.updateRouteNamesToWatch(make(map[string]bool))

	// lw.parent.mu.Lock()
	// defer lw.parent.mu.Unlock()
	// resource not found triggers to go serving mode not serving - happens inline
	lw.parent.switchMode2Locked(connectivity.ServingModeNotServing, err) // inline, and sync as part of lds and rds

} // switch mode api change? Any other behaviors need to happen?

// graceful close or strong close? But is there even really a different
// what happens when you switch mode?
