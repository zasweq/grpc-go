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

package server

import (
	igrpclog "google.golang.org/grpc/internal/grpclog"
	"google.golang.org/grpc/xds/internal/xdsclient/xdsresource"
)


// rdsHandlerUpdate wraps the full RouteConfigUpdate that are dynamically
// queried for a given server side listener.
/*type rdsHandlerUpdate struct {
	// map from routeName to rds update/error
	// If not written in this map, no RDS update for that route name yet.
	// If update set, use that as valid route configuration for RDS,
	// otherwise treat as an error case an fail at L7 level.
	updates map[string]xdsresource.RouteConfigUpdate
	err     error
}*/


// Will need to rewrite any unit tests to any interface I define

// rdsHandler handles any RDS queries that need to be started for a given server
// side listeners Filter Chains (i.e. not inline).
type rdsHandler struct {
	xdsC   XDSClient
	logger *igrpclog.PrefixLogger

	parent *listenerWrapper

	// updates is a map from routeName to rds update, including RDS resources
	// and any errors received. If not written in this map, no RDS update for
	// that route name yet. If update set in value, use that as valid route
	// configuration for RDS, otherwise treat as an error case and fail at L7
	// level.
	updates map[string]rdsWatcherUpdate
	cancels map[string]func()
}

// newRDSHandler creates a new rdsHandler to watch for RDS resources.
// listenerWrapper updates the list of route names to watch by calling
// updateRouteNamesToWatch() upon receipt of new Listener configuration.
func newRDSHandler(lw *listenerWrapper, xdsC XDSClient, logger *igrpclog.PrefixLogger) *rdsHandler {
	return &rdsHandler{
		xdsC:          xdsC,
		logger:        logger,
		parent:        lw,
		updates:       make(map[string]rdsWatcherUpdate),
		cancels:       make(map[string]func()),
	}
}

// updateRouteNamesToWatch handles a list of route names to watch for a given
// server side listener (if a filter chain specifies dynamic RDS configuration).
// This function handles all the logic with respect to any routes that may have
// been added or deleted as compared to what was previously present.
func (rh *rdsHandler) updateRouteNamesToWatch(routeNamesToWatch map[string]bool) {
	// Add and start watches for any routes for any new routes in
	// routeNamesToWatch.
	for routeName := range routeNamesToWatch {
		if _, ok := rh.cancels[routeName]; !ok {
			// The xDS client keeps a reference to the watcher until the cancel
			// func is invoked. So, we don't need to keep a reference for fear
			// of it being garbage collected.
			w := &rdsWatcher{parent: rh, routeName: routeName}
			rh.cancels[routeName] = xdsresource.WatchRouteConfig(rh.xdsC, routeName, w)
		}
	}

	// Delete and cancel watches for any routes from persisted routeNamesToWatch
	// that are no longer present.
	for routeName := range rh.cancels {
		if _, ok := routeNamesToWatch[routeName]; !ok {
			rh.cancels[routeName]()
			delete(rh.cancels, routeName) // is this the correct handling of data structures?
			delete(rh.updates, routeName)
		}
	}


	// I don't like route names left to watch like Java, I like using length of rds cancels...



}

// Stupid, LDS just gives this route names (checks readyness based off route names)
// and we cache updates from RDS (handleRouteUpdate) and determines READY or not

// determines if all dynamic RDS needed has received configuration.
func (rh *rdsHandler) determineRDSReady() bool {
	// master handles the edge case where length is zero, does this need to account for it as well?
	return len(rh.updates) == len(rh.cancels)
}

func (rh *rdsHandler) handleRouteUpdate(routeName string, update rdsWatcherUpdate) {
	// Usable route configuration from LDS + RDS is what gets atomically pointed
	// to. In higher level, this just persists route watches, determines ready,
	// and serves as a cache

	rwu, ok := rh.updates[routeName] // caches it here, should it cache at lower layer?
	if !ok {
		// Or is this already the zero value?
		rwu = rdsWatcherUpdate{}
	}

	if update.err != nil {
		if xdsresource.ErrType(update.err) == xdsresource.ErrorTypeResourceNotFound {
			// Clear update (write a top level comment on the map that explains
			// this logic). This will cause future RPCs that match to this route
			// to fail with UNAVAILABLE.
			rwu.update = nil
		}
		// Write error.
		rwu.err = update.err
	} else {
		rwu.update = update.update
	}
	rh.updates[routeName] = rwu
	// Signal for filter chains held in lw to rebuild, happen sync after write so no sync problems.
	// pending filter chains...routeNames (always switch, same or seperate operation as below)

	// Signal to lw to rebuild active filter chains that point to this,
	// in that function can determineRDSReady(), and if so pending -> current,
	// and do that dance with locking and closing (need to persist Conns in FCM)
	// that can race with a Conn Accept()

	rh.parent.handleRDSUpdate(routeName, rwu) // pass it data, persist it just for new lds to rebuild...
}

// needs to communicate to lw that pending is ready, is the determine rds ready by cancels ok here?


// close() is meant to be called by wrapped listener when the wrapped listener
// is closed, and it cleans up resources by canceling all the active RDS
// watches.
func (rh *rdsHandler) close() {
	for _, cancel := range rh.cancels {
		cancel()
	}
}

type rdsWatcherUpdate struct {
	update *xdsresource.RouteConfigUpdate
	err    error
}

// rdsWatcher implements the xdsresource.RouteConfigWatcher interface and is
// passed to the WatchRouteConfig API.
type rdsWatcher struct {
	parent    *rdsHandler
	logger    *igrpclog.PrefixLogger
	routeName string
}

func (rw *rdsWatcher) OnUpdate(update *xdsresource.RouteConfigResourceData) {
	if rw.logger.V(2) {
		rw.logger.Infof("RDS watch for resource %q received update: %#v", rw.routeName, update.Resource)
	}
	rw.parent.handleRouteUpdate(rw.routeName, rdsWatcherUpdate{
		update: &update.Resource, // does this cause any problems wrt pointing to same heap memory?
	})
}

func (rw *rdsWatcher) OnError(err error) {
	if rw.logger.V(2) {
		rw.logger.Infof("RDS watch for resource %q reported error: %v", rw.routeName, err)
	}
	rw.parent.handleRouteUpdate(rw.routeName, rdsWatcherUpdate{
		err: err,
	})
}

func (rw *rdsWatcher) OnResourceDoesNotExist() {
	if rw.logger.V(2) {
		rw.logger.Infof("RDS watch for resource %q reported resource-does-not-exist error: %v", rw.routeName)
	}
	err := xdsresource.NewErrorf(xdsresource.ErrorTypeResourceNotFound, "resource name %q of type RouteConfiguration not found in received response", rw.routeName)
	rw.parent.handleRouteUpdate(rw.routeName, rdsWatcherUpdate{
		err: err,
	})
}
