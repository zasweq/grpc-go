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
}

// NewListenerWrapper creates a new listenerWrapper with params. It returns a
// net.Listener and a channel which is written to, indicating that the former is
// ready to be passed to grpc.Serve().
//
// Only TCP listeners are supported.
func NewListenerWrapper(params ListenerWrapperParams) net.Listener {
	lw := &listenerWrapper{
		Listener:          params.Listener,
		name:              params.ListenerResourceName,
		xdsCredsInUse:     params.XDSCredsInUse,
		xdsC:              params.XDSClient,
		isUnspecifiedAddr: params.Listener.Addr().(*net.TCPAddr).IP.IsUnspecified(),

		mode:   connectivity.ServingModeNotServing, // triggers Accept() + Close() on any incoming connections.
		closed: grpcsync.NewEvent(),
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
	return lw
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

	// Set to true if the listener is bound to the IP_ANY address (which is
	// "0.0.0.0" for IPv4 and "::" for IPv6).
	isUnspecifiedAddr bool
	// Listening address and port. Used to validate the socket address in the
	// Listener resource received from the control plane.
	addr, port string

	// A small race exists in the XDSClient code between the receipt of an xDS
	// response and the user cancelling the associated watch. In this window,
	// the registered callback may be invoked after the watch is canceled, and
	// the user is expected to work around this. This event signifies that the
	// listener is closed (and hence the watch is cancelled), and we drop any
	// updates received in the callback if this event has fired.
	closed *grpcsync.Event

	// mu guards access to the current serving mode and the active filter chain
	// manager.
	mu sync.RWMutex
	// Current serving mode.
	mode connectivity.ServingMode
	// Filter chain manager currently serving.
	activeFilterChainManager *xdsresource.FilterChainManager

	// These fields are read/written to in the context of xDS updates, which are
	// guaranteed to be emitted synchrously from the xDS Client. Thus, they do
	// not need further synchronization. Active filter chains serving, used to
	// update routing configuration for any RDS updates.
	activeFilterChains []xdsresource.FilterChain
	// Pending filter chain manager. Will go active once rdsHandler has received
	// all the RDS resources this filter chain manager needs.
	pendingFilterChainManager *xdsresource.FilterChainManager

	// rdsHandler is used for any dynamic RDS resources specified in a LDS
	// update.
	rdsHandler *rdsHandler
}

func (lw *listenerWrapper) handleLDSUpdate(update xdsresource.ListenerUpdate) {
	ilc := update.InboundListenerCfg
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
	// what we have decided to do.
	if ilc.Address != lw.addr || ilc.Port != lw.port {
		lw.mu.Lock()
		lw.switchModeLocked(connectivity.ServingModeNotServing, fmt.Errorf("address (%s:%s) in Listener update does not match listening address: (%s:%s)", ilc.Address, ilc.Port, lw.addr, lw.port))
		lw.mu.Unlock()
		return
	}

	lw.pendingFilterChainManager = ilc.FilterChains
	lw.rdsHandler.updateRouteNamesToWatch(ilc.FilterChains.RouteConfigNames)

	if lw.rdsHandler.determineRDSReady() {
		lw.maybeUpdateFilterChains()
	}
}

// maybeUpdateFilterChains swaps in the pending filter chain manager to the
// active one if the pending filter chain manager is present. It also Drains
// (gracefully stops) any Connections that were accepted on the old one. It also
// puts the server in state SERVING.
func (l *listenerWrapper) maybeUpdateFilterChains() {
	if l.pendingFilterChainManager == nil {
		// Nothing to update, return early.
		return
	}

	l.mu.Lock()
	l.switchModeLocked(connectivity.ServingModeServing, nil)
	// "Updates to a Listener cause all older connections on that Listener to be
	// gracefully shut down with a grace period of 10 minutes for long-lived
	// RPC's, such that clients will reconnect and have the updated
	// configuration apply." - A36
	connsToClose := l.activeFilterChainManager.Conns()
	l.activeFilterChainManager = l.pendingFilterChainManager
	l.pendingFilterChainManager = nil
	l.instantiateFilterChainRoutingConfigurations()
	l.mu.Unlock()
	for _, conn := range connsToClose {
		conn.(*connWrapper).drain()
	}
}

type RoutingConfiguration struct {
	VHS []xdsresource.VirtualHostWithInterceptors
	Err error
}

// handleRDSUpdate rebuilds any routing configuration server side for any filter
// chains that point to this RDS, and potentially makes pending lds
// configuration to swap to be active.
func (l *listenerWrapper) handleRDSUpdate(routeName string, rcu rdsWatcherUpdate) {
	// Update any filter chains that point to this route configuration.
	for _, fc := range l.activeFilterChains {
		if fc.RouteConfigName == routeName {
			if rcu.err != nil && rcu.update == nil { // Either NACK before update, or resource not found triggers this conditional.
				atomic.StorePointer(fc.RC, unsafe.Pointer(&RoutingConfiguration{
					Err: rcu.err,
				}))
				continue
			}
			vhswi, err := fc.ConstructUsableRouteConfiguration(*rcu.update)
			atomic.StorePointer(fc.RC, unsafe.Pointer(&RoutingConfiguration{
				VHS: vhswi,
				Err: err, // Non nil if (lds + rds) fails, shouldn't happen since validated by xDS Client, treat as L7 error but shouldn't happen.
			}))
		}
	}

	if l.rdsHandler.determineRDSReady() {
		l.maybeUpdateFilterChains()
	}
}

// instantiateFilterChainRoutingConfigurations instantiates all of the routing
// configuration for the newly active filter chains. For any inline route
// configurations, uses that, otherwise uses cached rdsHandler updates.
func (l *listenerWrapper) instantiateFilterChainRoutingConfigurations() {
	l.activeFilterChains = nil
	for _, fc := range l.activeFilterChainManager.FilterChains() {
		l.activeFilterChains = append(l.activeFilterChains, *fc)
		if fc.InlineRouteConfig != nil {
			vhswi, err := fc.ConstructUsableRouteConfiguration(*fc.InlineRouteConfig)
			atomic.StorePointer(fc.RC, unsafe.Pointer(&RoutingConfiguration{
				VHS: vhswi,
				Err: err, // Non nil if (lds + rds) fails, shouldn't happen since validated by xDS Client, treat as L7 error but shouldn't happen.
			})) // Can't race with an RPC coming in but no harm making atomic.
			continue
		} // Inline configuration constructed once here, will remain for lifetime of filter chain.
		rcu := l.rdsHandler.updates[fc.RouteConfigName]
		if rcu.err != nil && rcu.update == nil {
			atomic.StorePointer(fc.RC, unsafe.Pointer(&RoutingConfiguration{
				Err: rcu.err,
			}))
			continue
		}
		vhswi, err := fc.ConstructUsableRouteConfiguration(*rcu.update)
		atomic.StorePointer(fc.RC, unsafe.Pointer(&RoutingConfiguration{
			VHS: vhswi,
			Err: err, // Non nil if (lds + rds) fails, shouldn't happen since validated by xDS Client, treat as L7 error but shouldn't happen.
		})) // Can't race with an RPC coming in but no harm making atomic.
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
		cw := &connWrapper{Conn: conn, filterChain: fc, parent: l, rc: fc.RC}
		l.activeFilterChainManager.AddConn(cw)
		l.mu.RUnlock()
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

// switchModeLocked switches the current mode of the listener wrapper. It also
// gracefully closes any connections if the listener wrapper transitions into
// not serving. If the serving mode has changed, it invokes the registered mode
// change callback.
func (l *listenerWrapper) switchModeLocked(newMode connectivity.ServingMode, err error) {
	if l.mode == newMode && l.mode == connectivity.ServingModeServing {
		// Redundant updates are suppressed only when we are SERVING and the new
		// mode is also SERVING. In the other case (where we are NOT_SERVING and the
		// new mode is also NOT_SERVING), the update is not suppressed as:
		//   1. the error may have change
		//   2. it provides a timestamp of the last backoff attempt
		return
	}
	l.mode = newMode
	if l.mode == connectivity.ServingModeNotServing {
		for _, conn := range l.activeFilterChainManager.Conns() {
			conn.(*connWrapper).drain()
		}
	}
	// The XdsServer API will allow applications to register a "serving state"
	// callback to be invoked when the server begins serving and when the
	// server encounters errors that force it to be "not serving". If "not
	// serving", the callback must be provided error information, for
	// debugging use by developers - A36.
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
	lw.parent.mu.Lock()
	defer lw.parent.mu.Unlock()
	lw.parent.switchModeLocked(connectivity.ServingModeNotServing, err)
	lw.parent.activeFilterChainManager = nil
	lw.parent.pendingFilterChainManager = nil
	lw.parent.activeFilterChains = nil
	lw.parent.rdsHandler.updateRouteNamesToWatch(make(map[string]bool))
}
