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
	"sync"

	igrpclog "google.golang.org/grpc/internal/grpclog"
	"google.golang.org/grpc/xds/internal/xdsclient/xdsresource"
)

// rdsHandlerUpdate wraps the full RouteConfigUpdate that are dynamically
// queried for a given server side listener.
type rdsHandlerUpdate struct {
	// map from routeName to rds update/error
	// If not written in this map, no RDS update for that route name yet.
	// If update set, use that as valid route configuration for RDS,
	// otherwise treat as an error case an fail at L7 level.
	updates map[string]xdsresource.RouteConfigUpdate
	err     error
}

// rdsHandler handles any RDS queries that need to be started for a given server
// side listeners Filter Chains (i.e. not inline).
type rdsHandler struct {
	xdsC   XDSClient
	logger *igrpclog.PrefixLogger

	mu      sync.Mutex // I don't know you still need this now that everything is sync
	// map from routeName to rds update
	// If not written in this map, no RDS update for that route name yet.
	// If update set, use that as valid route configuration for RDS,
	// otherwise treat as an error case an fail at L7 level.
	updates map[string]rdsWatcherUpdate
	cancels map[string]func()

	// For a rdsHandler update, the only update wrapped listener cares about is
	// most recent one, so this channel will be opportunistically drained before
	// sending any new updates.
	updateChannel chan rdsHandlerUpdate
}

// newRDSHandler creates a new rdsHandler to watch for RDS resources.
// listenerWrapper updates the list of route names to watch by calling
// updateRouteNamesToWatch() upon receipt of new Listener configuration.
func newRDSHandler(xdsC XDSClient, logger *igrpclog.PrefixLogger, ch chan rdsHandlerUpdate) *rdsHandler {
	return &rdsHandler{
		xdsC:          xdsC,
		logger:        logger,
		updateChannel: ch,
		updates:       make(map[string]rdsWatcherUpdate),
		cancels:       make(map[string]func()),
	}
}

// updateRouteNamesToWatch handles a list of route names to watch for a given
// server side listener (if a filter chain specifies dynamic RDS configuration).
// This function handles all the logic with respect to any routes that may have
// been added or deleted as compared to what was previously present.
func (rh *rdsHandler) updateRouteNamesToWatch(routeNamesToWatch map[string]bool) {
	rh.mu.Lock()
	defer rh.mu.Unlock()
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

	// If the full list (determined by length) of updates are now successfully
	// updated, the listener is ready to be updated.
	/*if len(rh.updates) == len(rh.cancels) && len(routeNamesToWatch) != 0 {
		drainAndPush(rh.updateChannel, rdsHandlerUpdate{updates: rh.updates})
	}*/
	// determineRDSReady, or maybe call into lw operation

	// Do I want lw to have that pending to current operation on check if ready
	// if pending (written at beginning of handleLDS)
	//		checkIfReady
	//             call helper operation I've always thought about



}

// Stupid, LDS just gives this route names (checks readyness based off route names)
// and we cache updates from RDS (handleRouteUpdate) and determines READY or not
// LDS ^^^, can be ready after updating (zero always triggers ready, does it need to go through this component)


// determines if all dynamic RDS has received configuration
func (rh *rdsHandler) determineRDSReady() bool { // I think this is right, maybe trigger swap if pending is set
	// master handles the edge case where length is zero, does this need to account for it as well?
	return len(rh.updates) == len(rh.cancels) // when to call this?
	// either return and read bool or signal or signal by calling func on listener wrapper
}

// RDS VVV, can make a pending ready,
func (rh *rdsHandler) handleRouteUpdate(routeName string, update rdsWatcherUpdate) {
	rh.mu.Lock()
	defer rh.mu.Unlock() // I don't think you need this anymore since sync...
	// Usable route configuration from LDS + RDS is what gets atomically pointed to. In higher level
	rwu, ok := rh.updates[routeName] // this just persists rdsWatcherUpdate which can be ok or err (need to plumb err)
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
	} else { // either one or the other
		rwu.update = update.update
	}
	rh.updates[routeName] = rwu
	// Signal for filter chains held in lw to rebuild, happen sync after write so no sync problems.
	// pending filter chains...routeNames (always switch, same or seperate operation as below)

	// Signal to lw to rebuild filter chains,
	// in that function can determineRDSReady(), and if so pending -> current,
	// and do that dance with locking and closing (need to persist Conns in FCM)
	// that can race with a Conn Accept()

	// Also check if pending is ready?
	rh.determineRDSReady() // bool represents all dynamic RDS is ready, should this signal?
} // send a draft pr saying this is just representing error case properly.

// needs to communicate to lw that pending is ready, is the determine rds ready by cancels ok here?

// Java constructs rds names to watch and once that goes to zero signals (I think maybe only signal once)

// close() is meant to be called by wrapped listener when the wrapped listener
// is closed, and it cleans up resources by canceling all the active RDS
// watches.
func (rh *rdsHandler) close() {
	rh.mu.Lock()
	defer rh.mu.Unlock()
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
		err: err, // Can I send nil route update to represent error
	})
}

// Valid update
// Error update
// Hasn't received (Java does this by route names to watch)

func (rw *rdsWatcher) OnResourceDoesNotExist() {
	if rw.logger.V(2) {
		rw.logger.Infof("RDS watch for resource %q reported resource-does-not-exist error: %v", rw.routeName)
	}
	err := xdsresource.NewErrorf(xdsresource.ErrorTypeResourceNotFound, "resource name %q of type RouteConfiguration not found in received response", rw.routeName)
	rw.parent.handleRouteUpdate(rw.routeName, rdsWatcherUpdate{
		err: err,
	})
}
