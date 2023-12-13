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

package xds_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/xds"

	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
)

// TestServeLDSRDS tests the case where a server receives LDS resource which
// specifies RDS. LDS and RDS resources are configured on the management server,
// which the server should pick up. The server should successfully accept
// connections and RPCs should work on these accepted connections.
func (s) TestServeLDSRDS(t *testing.T) {
	managementServer, nodeID, bootstrapContents, _, cleanup := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup()
	lis, err := testutils.LocalTCPListener() // is this the type of listener I want? Also, can I generalize this?
	if err != nil {
		t.Fatalf("testutils.LocalTCPListener() failed: %v", err)
	}
	// Setup the management server to respond with a listener resource that
	// specifies a route name to watch, and a RDS resource corresponding to this
	// route name.
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}

	listener := e2e.DefaultServerListenerWithRouteConfigName(host, port, e2e.SecurityLevelNone, "routeName")
	routeConfig := e2e.RouteConfigNonForwardingTarget("routeName")

	resources := e2e.UpdateOptions{
		NodeID:    nodeID,
		Listeners: []*v3listenerpb.Listener{listener},
		Routes:    []*v3routepb.RouteConfiguration{routeConfig},
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	serving := grpcsync.NewEvent()

	// how to plumb serving mode to test, can block in the test on this event as a result
	modeChangeOpt := xds.ServingModeCallback(func(addr net.Addr, args xds.ServingModeChangeArgs) {
		t.Logf("serving mode for listener %q changed to %q, err: %v", addr.String(), args.Mode, args.Err)
		if args.Mode == connectivity.ServingModeServing {
			serving.Fire()
		}
	})

	server, err := xds.NewGRPCServer(grpc.Creds(insecure.NewCredentials()), modeChangeOpt, xds.BootstrapContentsForTesting(bootstrapContents))
	if err != nil {
		t.Fatalf("Failed to create an xDS enabled gRPC server: %v", err)
	}
	defer server.Stop()
	testgrpc.RegisterTestServiceServer(server, &testService{})
	go func() {
		if err := server.Serve(lis); err != nil {
			t.Errorf("Serve() failed: %v", err)
		}
	}()
	<-serving.Done()

	cc, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial local test server: %v", err)
	}
	defer cc.Close()

	waitForSuccessfulRPC(ctx, t, cc)

	// on the first one RDS update eats processing since not active.
	// gets handled once it goes ready...then handleRDS update called


	// either unavailable or another header matcher?

	// for some reason this only calls handleLDS update once ^^^, but this calls it in loop vvv
	// problem with management server or this?

	// is there an update *only for* rds? maybe just updating one will work...
	// managementServer.Update()

	// or just scale this up
	// how to differentiate?

	// calls handleRDS, but only updates second time

	// I only see it updating second time...
	// either a. the logic is wrong for first part (doesn't update config)
	// or b. it's not plumbing the configuration through atomic pointer correctly

	// (conn has already been established)
	// I think just setting this should propagate all the way down
	routeConfig = e2e.RouteConfigRoute("routeName")

	// this is the proto resource, it still needs to get processed...
	// maybe have the proto resource be non forwarding, should fail RPCs (will break in future right)

	//	it responded to all xDS responses in cache




	// this makes it continue to call lds update, I only want lds update to be called ^^^

	// Does this plumb in a whole new listener, or does it just do route
	// does it need to do diff

	// only calls handleLDS once correctly, client eats it. but it's cached so need all resources

	// calls handleRDS twice, weird it calls maybeUpdteaFilterChains correctly

	// moves to serving correctly on first one,
	// second one hits, but it doesn't trigger failure

	// the first one works but doesn't seem to be calling handleRouteUpdate
	// correctly...is it wai...


	// second one updates both filter chains correctly...



	resources = e2e.UpdateOptions{
		NodeID:    nodeID,
		Listeners: []*v3listenerpb.Listener{listener}, // I think updates lis, doesn't fail rpcs, but should eventually fail...argh (does this update lis?)

		Routes:    []*v3routepb.RouteConfiguration{routeConfig},

	}

	// I think this hit when no lis...
    // xds_server_test.go:125: failed to create new resource snapshot: mismatched "type.googleapis.com/envoy.config.route.v3.RouteConfiguration" reference and resource lengths: len(map[]) != 1
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	} // eventually consistent - how to plumb a happens before all the way wrt to this configuration being applied?

	// how to differentiate? plumb a !non forwarding action through proto

	// "NonForwardingAction is expected for all Routes used on server-side; a
	// route with an inappropriate action causes RPCs matching that route to
	// fail with UNAVAILABLE." - A36

	// sync point - eventual consistency at some point in the future

	// maybe it doesn't do the pointer stuff correctly...

	// I don't know why the signal doesn't plumb, but RPC's don't error (also make sure UNAVAILABLE), not just a failure,
	// make a knob?

	// techically it fails here: xds_server_test.go:154: failure when waiting for RPCs to fail: context deadline exceeded
	waitForFailedRPC(ctx, t, cc) // assertion is this doesn't timeout - how to verify?
	// make an assertion (ctx might expire)

	// check handleRDSUpdate and RoutingConfiguration (perhaps doesn't update pointer correctly)

	// RoutingConfiguraiton successfully polled

	// Does heap not propagate through?

} // woohoo it works! Wasn't updating pointer correctly

// perhaps merge these two ^^^ vvv

// wow this works, try others

// Dynamic RDS case switching: LDS + RDS + Accept() + RPC, new RDS, new RPC
// reflects that new RDS (how to test it actually reflects new RDS)? error RDS?
func (s) TestDynamicRDSReflected(t *testing.T) {
	// routeconfiguration is the knob - reflect it in xDS Server, do you have to wait for it to be reflected in the conn somehow?
	// or maybe poll until the eventual consitency is reflected in the system
}

// maybe try below and see if it works

// have to split up incoming RPC's into two routes for tests below...

/*
LDS + (Inline RDS/Dynamic RDS) should only start working once dynamic RDS comes in
RPCs matching to inline should work, RPCs matching to dynamic should also work
*/

/*
func (s) TestBothInlineAndDynamic(t *testing.T) {

	// make management server point to rds and inline in one lis resource

	// setup - pull out into helper?
	managementServer, nodeID, bootstrapContents, _, cleanup := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup()
	lis, err := testutils.LocalTCPListener() // is this the type of listener I want? Also, can I generalize this?
	if err != nil {
		t.Fatalf("testutils.LocalTCPListener() failed: %v", err)
	}
	// Setup the management server to respond with a listener resource that
	// specifies a route name to watch, and a RDS resource corresponding to this
	// route name.
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}

	// two route configs...how to differentiate ipv4 vs. ipv6 is how it does it currently, does it need two branches...
	// listener := e2e.DefaultServerListenerWithRouteConfigName(host, port, e2e.SecurityLevelNone, "routeName")
	var listener *v3listenerpb.Listener
	listener.FilterChains // needs two of these, one specifying inline, one specifying dynamic, and also needs to differentiate somehow...
	// fc holds an inline rds

	// routeConfig := e2e.RouteConfigNonForwardingTarget("routeName")
	var routeConfig *v3routepb.RouteConfiguration
	// maybe make same one, this is dynamic portion

	// See calls to NewWithConfigForTesting to make not resource not found

	resources := e2e.UpdateOptions{
		NodeID:    nodeID,
		Listeners: []*v3listenerpb.Listener{listener},
		Routes:    []*v3routepb.RouteConfiguration{routeConfig},
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	serving := grpcsync.NewEvent()

	// how to plumb serving mode to test, can block in the test on this event as a result
	modeChangeOpt := xds.ServingModeCallback(func(addr net.Addr, args xds.ServingModeChangeArgs) {
		t.Logf("serving mode for listener %q changed to %q, err: %v", addr.String(), args.Mode, args.Err)
		if args.Mode == connectivity.ServingModeServing {
			serving.Fire()
		}
	})

	server, err := xds.NewGRPCServer(grpc.Creds(insecure.NewCredentials()), modeChangeOpt, xds.BootstrapContentsForTesting(bootstrapContents))
	if err != nil {
		t.Fatalf("Failed to create an xDS enabled gRPC server: %v", err)
	}
	defer server.Stop()
	testgrpc.RegisterTestServiceServer(server, &testService{})
	go func() {
		if err := server.Serve(lis); err != nil {
			t.Errorf("Serve() failed: %v", err)
		}
	}()
	<-serving.Done()

	cc, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial local test server: %v", err)
	}
	defer cc.Close()
	// setup


	// assert rpc 1 matching to dynamic works (how to match?)


	// assert rpc 2 matching to inline works (how to match?)

}
*/


/*
LDS + (Two Dynamic RDS) with one specifying ok, one specifying error,
connections accepted to ok should work normally, specifying error should fail
*/
func (s) TestDynamicWithOkAndError(t *testing.T) {


	// either nack (does it get triggered by any validation failure?) or trigger
	// a resource not found through internal plumbing...RPC's matching should error

	// what property of route matching can you create distinct RPC's with?

	// RPC which match to first route fail

	// RPC which match to second route fail
	// Can you even distinguish these in the helpers ^^^?

}

/*
Basic multiple updates:
(LDS + Inline)
(LDS + Dynamic), should continue to use above before switching over to this (only when new RDS comes in)
*/
func (s) TestMultipleUpdatesInlineToDynamic(t *testing.T) {

}

/*
Multiple updates should immediately switch over
LDS (RDS A, RDS B, RDS C)
LDS (RDS A, RDS B) - should immediately start using this, should no longer have RDS C (How to test?)
LDS (RDS A)
*/

func (s) TestMultipleUpdatesImmediatelySwitch(t *testing.T) {

	// how to test "immediately switch", doesn't match to RDS c, then b, c, but first debug first test haha

	/*
	Make sure the client doesn't request A & B?  Or at least, don't send A&B
	from mgmt server, but expect RPCs to keep working.
	*/


}

/*
LDS with RDS that doesn’t exist, fail at L7 level after resource not found, only
way resource not found is 20 seconds, not explicit resource not found
*/

func (s) TestResourceNotFound(t *testing.T) {
	// inject resource not found somehow...

	// Definitely override this via some knob in `internal`

	// It might be nice to have a method that can be called to make a specific
	// resource timeout when called. That way we aren't flaky (or slow) because
	// of timing problems.

	// call on resource not found callback...

	// you've seen how watcher plumbs

	// either trigger a resource not found explicitly (when called)

	// or rely on timer (how does timer even go off)?

}

// got rid of a unit test before goes serving
// now, before goes serving, accept and close(), (perhaps already part of serving mode change above)



// method on the *xDS Client* that invokes a resource not found callback

// xDS Client emits sync, either block on callback serializer finishing
// or invoke directly and say you can't invoke this alongside other updates processing...




