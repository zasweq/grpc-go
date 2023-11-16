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
	"errors"
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
		goodUpdate:  grpcsync.NewEvent(),
		rdsUpdateCh: make(chan rdsHandlerUpdate, 1),
	}
	lw.logger = internalgrpclog.NewPrefixLogger(logger, fmt.Sprintf("[xds-server-listener %p] ", lw))

	// Serve() verifies that Addr() returns a valid TCPAddr. So, it is safe to
	// ignore the error from SplitHostPort().
	lisAddr := lw.Listener.Addr().String()
	lw.addr, lw.port, _ = net.SplitHostPort(lisAddr)

	lw.rdsHandler = newRDSHandler(lw.xdsC, lw.logger, lw.rdsUpdateCh)
	lw.cancelWatch = xdsresource.WatchListener(lw.xdsC, lw.name, &ldsWatcher{
		parent: lw,
		logger: lw.logger,
		name:   lw.name,
	})
	go lw.run()
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

	pendingFilterChains *xdsresource.FilterChainManager // switch to filter chains if ready and set

	// LDS needs to look at cached filter chains...when to clean this up
	// maybe corresponding entirely to current?

	// on rds update loop through to see which filter chains correspond?
	// flip this out when switching from pending to current (always overwrites pending)


	// when to build pendings []vhswi

	// Can't use filter chain as map type, it's like endpoint map...
	// new rds update updates all corresponding

	// Core data structure (has lds + rds name)
	// problem: how to make this filter chain actually be a map key
	tryThisMap map[xdsresource.FilterChain]*unsafe.Pointer // pointer to a pointer

	// maps from rds name to all the usable vhswi refs
	tryThisMap2 map[string][]*unsafe.Pointer

	tryThisSlice []xdsresource.FilterChain

	// atomic.StorePtr(val, new val)
	// unsafe pointer points to *vhswi

	// rds data has a store - updated immedaitely on an lds update
	// (lds + rds) to construct, where to store this...in a map and in filter chains, I think should be a value type in map
	// filter chain has a ref to it, need to store rds name in fc in this component

	// finish rds as a store, and call back into this. This should then work

	// Still a ref to rds data (already built out) if rds watcher closed

	// lds update updates route names to watch (requires certain logic)
	// and also top level fcm becomes pending (I think build out route configs for all?)

	// rds update updates filter chain corresponding (trigger from helper), and also caches it


	// rdsHandler is used for any dynamic RDS resources specified in a LDS
	// update.
	rdsHandler *rdsHandler // pass it portion of lds that dictates dynamic rds needs...need the logic for lds/data structures
	// rdsUpdates are the RDS resources received from the management
	// server, keyed on the RouteName of the RDS resource.
	rdsUpdates unsafe.Pointer // map[string]xdsclient.RouteConfigUpdate
	// rdsUpdateCh is a channel for XDSClient RDS updates.
	rdsUpdateCh chan rdsHandlerUpdate
}

func (lw *listenerWrapper) ldsFlow(update xdsresource.ListenerUpdate) {
	// resourcenotfound switch server into non serving, drain conns
	ilc := update.InboundListenerCfg
	ilc.Port
	ilc.Address // this switches mode into not serving, this is not defined by gRFC
	ilc.FilterChains // *FilterChainManager
	ilc.FilterChains.RouteConfigNames // [] route config names, give to rds watcher
	ilc.FilterChains.Lookup() // what is called on a Conn Accept()

	// map[FilterChain]unsafe.Ptr, where it points to vhswi, usable, how to link
	// on an accept? Doug wants me to store this in here, maybe have helpers
	// that get called in the LDS/RDS flow, that write to a map like that

	// new lds and rds need to trigger
	// if you have already pending filter chains that haven't updated (come sync) (what if an update comes after...check against cancels (persist list of all the cancels from most recent lds)?)
	// if a pending one hasn't gone active hasn't received all it's config, update watch there so I think this uncondtional write is ok

	lw.pendingFilterChains = ilc.FilterChains // doesn't race because conn accepts() read active, but not pending (if no active, just close conn right)?


	lw.rdsHandler.updateRouteNamesToWatch(ilc.FilterChains.RouteConfigNames) // should that call back?

	lw.rdsHandler.determineRDSReady() // (or have this below)
	// write to pending, always even on first update once it's ready switch to current (need to switch to serving...)
	// what happens if route names == 0, I think rds handler will take care of this
}

// at the end of processing LDS,
// and at the end of processing RDS, need to determine if pending is ready and then switch it over
// if all rds ready
//            switch filter chains
// I think is the correct flow here...

func (lw *listenerWrapper) calledFromRh(rdsName string) {
	// on an lds update - update all...what triggers build? ("") for rds name
	//      build all? (if ready)
	// on an rds update - all fcs that relate to it need to update
	//      bulild filter chains that point to rds?

	// Is this operation always correct? I.e. does this actually work?
	if lw.rdsHandler.determineRDSReady() { // // bool that determines if rds is ready.
		// pending -> current, operation involving closing conns (fcms need to keep track of Conn's, also get sync to work)
		// is closing conns logically "draining" conns?
		lw.updateFilterChains()
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

func (lw *listenerWrapper) updateFilterChains() { // swap operation is correct I think, grab mu give it up, this works...
	// or accepts a conn here with old config an immediately closes
	// this is only part of this helper
	l.mu.Lock() // right mutex, or use the sameeeee as RDS?

	// read conns to close? - this needs to persist a list of conns to close
	// Java gives the FCM a Conn on an accept, this manages the conns and holds a list
	// close the list from FCM of conns it manages (FCM Conn Manager or something)
	connsToClose := lw.filterChains.Conns() // switch this to connWrapper, export perhaps. store these conns in the fcm
	// Write new filter chains:
	l.filterChains = lw.pendingFilterChains
	l.mu.Unlock()
	// right so either gets new filter chain config on conn

	// close conns (perhaps async - sometime in future?)
	// what data do I eventually need to close?
	// var connsToClose []connWrapper
	for _, conn := range connsToClose { // async perhaps?

		// is this Close() a graceful close? (see Drain callback?)

		// This handles cleaning up all the resources of the object, including all of it's state.
		conn.Close() // you need this, since there is extra memory and things to clean up on top of the net.Conn
		// conn.virtualHostsFromInlineRDS // cleaned up from the gc no longer pointing to this heap.
		// conn.
	}

	// Now where do I store this? and if I do need some sync
	// Accept() adds to conns to close, this gets called on a new LDS update
	// sync access in the filter chain manager?
}

// builds route configuration for all on pending going ready (yeah, and store in pointer
func buildRouteConfiguration() { // Individual lds + rds build

	var fc xdsresource.FilterChain
	var rcu xdsresource.RouteConfigUpdate // where do I get these two pieces of data from?
	vhswi, err := fc.ConstructUsableRouteConfiguration(rcu)
	if err != nil { // How does this error?

	}
	vhswi // []VirtualHostWithInterceptors

	// overall map stores map[fc]* -> * (unsafe.Pointer) -> vhswi

}

// rdsUpdate rebuilds any routing configuration server side for any filter
// chains that point to this RDS.
func (l *listenerWrapper) rdsUpdate(routeName string, rcu rdsWatcherUpdate) { // The gates to this call from rds handler is what Eric explained to me

	// or access a map of route updates
	for fc, ptrToPtr := range l.tryThisMap {
		if fc.RouteConfigName == routeName { // I think this encapsulates all the logic we need wrt inline or not inline
			// needs to also fail l7 rpcs with err or not
			// I think make error be nil, if it gets to serving it means that it's
			// received a configuration at some point (Ignore errors that haven't been
			// there)

			// if update is set use that (I need to write a test for l7 failures on NACK or resource not found)
			if rcu.err != nil && rcu.update == nil { // If it calls in will have set one...
				// stick nil as the value atomically of the route configuration.
				var vhswi *[]xdsresource.VirtualHostWithInterceptors
				atomic.StorePointer(ptrToPtr, unsafe.Pointer(vhswi)) // not a nil pointer a pointer of type that points to nil
				continue
			}


			vhswi, err := fc.ConstructUsableRouteConfiguration(*rcu.update) // or read from map...needs to not mess with heap
			if err != nil {
				// what to do...also what triggers it? should this also cause an error?
			}
			// unsafe.Pointer(this is also a poitner) is essentially a typecast
			atomic.StorePointer(ptrToPtr, unsafe.Pointer(&vhswi)) // unsafe pointer to a pointer, &vhswi is the address (i.e. pointer) to newly construct []vhswi (escapes to heap)
			// unsafe.Pointer(&vhswi)
		}
	}

	// routeName
	// map[name]->[]xdsresource.FilterChain (this holds the ref to update), points to same vhswi that it gives conn on an accept (that's how it communicates, vh() on conn wrapper)
	// or honestly just []xdsresource.FilterChain (when accept, grab the * -> * into a local var)
	// xdsresource.FilterChain already has the rds name and pointer to []vhswi so just need that instead of map (rds updates are not on fast path)


	// my map way:
	for rdsName, usableRouteConfigs := range l.tryThisMap2 {
		if rdsName == routeName {

			break // no need to continue
		}
	}

	// slice way
	for _, fc := range l.tryThisSlice { // only written to from lds being ready (need to trigger that from here too)
		if fc.RouteConfigName == routeName { // I think this encapsulates all the logic we need wrt inline or not inline
			// needs to also fail l7 rpcs with err or not
			// I think make error be nil, if it gets to serving it means that it's
			// received a configuration at some point (Ignore errors that haven't been
			// there)

			// if update is set use that (I need to write a test for l7 failures on NACK or resource not found)
			if rcu.err != nil && rcu.update == nil { // If it calls in will have set one...
				// stick nil as the value atomically of the route configuration.
				var vhswi *[]xdsresource.VirtualHostWithInterceptors
				// now has pointer
				atomic.StorePointer(fc.VHS, unsafe.Pointer(vhswi)) // not a nil pointer a pointer of type that points to nil
				continue
			}


			vhswi, err := fc.ConstructUsableRouteConfiguration(*rcu.update) // or read from map...needs to not mess with heap
			if err != nil {
				// what to do...also what triggers it? should this also cause an error?
			}
			// unsafe.Pointer(this is also a poitner) is essentially a typecast
			atomic.StorePointer(fc.VHS, unsafe.Pointer(&vhswi)) // unsafe pointer to a pointer, &vhswi is the address (i.e. pointer) to newly construct []vhswi (escapes to heap)
			// unsafe.Pointer(&vhswi)
		}
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
func (l *listenerWrapper) rebuildFullMap() {
	// clear old map
	l.tryThisMap // wraps pointers to data, so ok to write to

	// Construct this on l creation too

	// Doug can help in 1:1 next week oh he's out
	// TODO: persist conns in this filter chain (export wrapped conn?) needs to clean up resources
	// filterchains() []FilterChain method?
	// could I persist in a map
	// filter chains in fcm could be:
	// (fc: pointer) and do it in fcm? or method on fcm
	// No need to translate fc chosen for an accepted conn ->
	// do it in fcm, and return an atomic Ref to rds as well
	// would need to forward route update to fcm (it persists map)
	// somewhere persist fc -> usable to rebuild on rds
	// also persist tree like structure to accept a filter chain
	// (fc, rds pointer) in both tree and map
	// when you recreate atomic ref in fc, new rds just needs to write to the right part of this heap
	// just needs to map by route name to same, map doesn't need to be looked in other than route name (fcs per route name perhaps)?

	// return a ref to usable heap from Accept()
	// <-rds comes in knows where to rebuild heap

	// l.filterChains.

	// this map is one to one with serving fcs
	// when it Accepts a Conn I think need to point to this pointer...
	l.tryThisMap = make(map[xdsresource.FilterChain]*unsafe.Pointer) // "throw those refs away, garbage collection"
	// based off pending (or current lds data)
	// reconstruct this map
	var fcs []xdsresource.FilterChain
	// as you build out fcm, store a []filterchain to return
	for fc := range l.filterChains.FilterChains() { // Figure out how to get all the filter chains from this structure and also how to use as a map key.

	}
	for _, fc := range fcs {
		fc.RouteConfigName // use this to index into a map[routeName]->rdsData
		// only fcm needs a lock (I think is sync with others)
		rcu := l.rdsHandler.updates[fc.RouteConfigName]
		// rebuild
		// lds + rds
		// if update is set use that (I need to write a test for l7 failures on NACK or resource not found)
		if rcu.err != nil && rcu.update == nil { // If it calls in will have set one...
			// stick nil as the value atomically of the route configuration.
			var vhswi *[]xdsresource.VirtualHostWithInterceptors
			atomic.StorePointer(ptrToPtr, unsafe.Pointer(vhswi)) // not a nil pointer a pointer of type that points to nil
			continue
		}
		// does this deref cause any problems?
		vhswi, err := fc.ConstructUsableRouteConfiguration(*rcu.update) // or this a can error, need to reflect that //)
		if err != nil {
			// what to do in this case?
		}
		uPtr := unsafe.Pointer(&vhswi) // address of slice?
		l.tryThisMap[fc] = &uPtr // this right here is when map gets built out
	}

	// New Full Map
	l.tryThisSlice = nil // zero this out, only read on an rds update, which is sync
	for _, fc := range fcs {
		// fc.RouteConfigName // string
		// fc.VHS // pointer to pointer to update
		rcu := l.rdsHandler.updates[fc.RouteConfigName] // fcs pointer to this pointer, and also conn's local var pointing to this pointer, atomically accessed
		if rcu.err != nil && rcu.update == nil { // If it calls in will have set one...
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
		l.tryThisSlice = append(l.tryThisSlice, fc) // can update nil...I think pointing to same fc in memory is fine, never gets updated
	}
}

// Accept blocks on an Accept() on the underlying listener, and wraps the
// returned net.connWrapper with the configured certificate providers.
func (l *listenerWrapper) Accept() (net.Conn, error) {
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

		l.mu.RLock()
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
		// after the error case, need to keep track of Conns
		fc, err := l.filterChains.Lookup(xdsresource.FilterChainLookupParams{
			IsUnspecifiedListener: l.isUnspecifiedAddr,
			DestAddr:              destAddr.IP,
			SourceAddr:            srcAddr.IP,
			SourcePort:            srcAddr.Port,
		})
		l.mu.RUnlock()
		if err != nil {
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





		// rds is already handled
		var rc xdsresource.RouteConfigUpdate
		if fc.InlineRouteConfig != nil {
			rc = *fc.InlineRouteConfig
		} else {
			rcPtr := atomic.LoadPointer(&l.rdsUpdates)
			rcuPtr := (*map[string]xdsresource.RouteConfigUpdate)(rcPtr)
			// This shouldn't happen, but this error protects against a panic.
			if rcuPtr == nil {
				return nil, errors.New("route configuration pointer is nil")
			}
			rcu := *rcuPtr
			rc = rcu[fc.RouteConfigName]
		}
		// The filter chain will construct a usuable route table on each
		// connection accept. This is done because preinstantiating every route
		// table before it is needed for a connection would potentially lead to
		// a lot of cpu time and memory allocated for route tables that will
		// never be used. There was also a thought to cache this configuration,
		// and reuse it for the next accepted connection. However, this would
		// lead to a lot of code complexity (RDS Updates for a given route name
		// can come it at any time), and connections aren't accepted too often,
		// so this reinstantation of the Route Configuration is an acceptable
		// tradeoff for simplicity.
		vhswi, err := fc.ConstructUsableRouteConfiguration(rc)
		if err != nil {
			l.logger.Warningf("Failed to construct usable route configuration: %v", err)
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



		// gets a filter chain from manager, look into map for unsafe.Pointer
		// I don't know if this linkage will work properly...
		uPtrPtr, ok := l.tryThisMap[fc]
		if !ok {
			// Shouldn't happen, already built out
		}





		cw := &connWrapper{Conn: conn, filterChain: fc, parent: l, virtualHosts: vhswi, vhs: uPtrPtr}

		l.filterChains.AddConn(cw) // need to pass this a ConnWrapper, and also this should be in mutex grab with swap (protect this whole operation?)

		// give this conn wrapper a *unsafe.Pointer that reads atomically on VirtualHosts()
		return cw, nil
	}
}

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

// run is a long running goroutine which handles all xds updates. LDS and RDS
// push updates onto a channel which is read and acted upon from this goroutine.
func (l *listenerWrapper) run() {
	for {
		select {
		case <-l.closed.Done():
			return
		case u := <-l.rdsUpdateCh:
			l.handleRDSUpdate(u)
		}
	}
}

// handleRDSUpdate handles a full rds update from rds handler. On a successful
// update, the server will switch to ServingModeServing as the full
// configuration (both LDS and RDS) has been received.
func (l *listenerWrapper) handleRDSUpdate(update rdsHandlerUpdate) {
	if l.closed.HasFired() {
		l.logger.Warningf("RDS received update: %v with error: %v, after listener was closed", update.updates, update.err)
		return
	}
	if update.err != nil {
		if xdsresource.ErrType(update.err) == xdsresource.ErrorTypeResourceNotFound {
			l.switchMode(nil, connectivity.ServingModeNotServing, update.err)
		}
		// For errors which are anything other than "resource-not-found", we
		// continue to use the old configuration.
		return
	}
	atomic.StorePointer(&l.rdsUpdates, unsafe.Pointer(&update.updates))

	l.switchMode(l.filterChains, connectivity.ServingModeServing, nil)
	l.goodUpdate.Fire()
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
}

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
}

func (lw *ldsWatcher) OnResourceDoesNotExist() {
	if lw.parent.closed.HasFired() {
		lw.logger.Warningf("Resource %q received resource-not-found-error after listener was closed", lw.name)
		return
	}
	if lw.logger.V(2) {
		lw.logger.Infof("LDS watch for resource %q reported resource-does-not-exist error: %v", lw.name)
	}
	err := xdsresource.NewErrorf(xdsresource.ErrorTypeResourceNotFound, "resource name %q of type Listener not found in received response", lw.name)
	lw.parent.switchMode(nil, connectivity.ServingModeNotServing, err)
}
