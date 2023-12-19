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
	"github.com/golang/groupcache/testpb"
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
// connections and RPCs should work on these accepted connections. It then
// switches the RDS resource to match incoming RPC's to a route type of type
// that isn't non forwarding action. This should get picked up by the connection
// dynamically, and all RPC's on that connection should start failing.
func (s) TestServeLDSRDS(t *testing.T) {
	managementServer, nodeID, bootstrapContents, _, cleanup := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup()
	lis, err := testutils.LocalTCPListener()
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

	waitForSuccessfulRPC(ctx, t, cc) // Eventually, the LDS and dynamic RDS get processed, work, and RPC's should work as usual

	// Set the route config to be of type route action route, which the rpc will
	// match to. This should eventually reflect in the Conn's routing
	// configuration and fail the rpc with a status code UNAVAILABLE.
	routeConfig = e2e.RouteConfigRoute("routeName")
	resources = e2e.UpdateOptions{
		NodeID:    nodeID,
		Listeners: []*v3listenerpb.Listener{listener}, // Same lis, so will get eaten by the xDS Client.

		Routes:    []*v3routepb.RouteConfiguration{routeConfig},

	}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	// "NonForwardingAction is expected for all Routes used on server-side; a
	// route with an inappropriate action causes RPCs matching that route to
	// fail with UNAVAILABLE." - A36
	waitForFailedRPC(ctx, t, cc)

	// make it go ready again - if a different error message can just use below...

	// inject a resource not found - it should fail - invoke using Easwar's change
}

// knob on headers maybe - can attach headers to RPC's on returned client conn

// clientconn gets passed into it and then wrapped...
func setup(t *testing.T, resources e2e.UpdateOptions) (*e2e.ManagementServer, *xds.GRPCServer, *grpc.ClientConn, func()) { // also return a management server, export struct or pointer to struct (perhaps look elsewhere in codebase to see how they do)
	// t.Helper()?

	// anything funky with respect to ports or no?
	managementServer, nodeID, bootstrapContents, _, cleanup := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup()
	lis, err := testutils.LocalTCPListener()
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

	// fuck variables above are used, pass something else in? or just do it per test case?

	// listener := e2e.DefaultServerListenerWithRouteConfigName(host, port, e2e.SecurityLevelNone, "routeName")
	// routeConfig := e2e.RouteConfigNonForwardingTarget("routeName")

	/*resources := e2e.UpdateOptions{
		NodeID:    nodeID,
		Listeners: []*v3listenerpb.Listener{listener},
		Routes:    []*v3routepb.RouteConfiguration{routeConfig},
	}*/

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	serving := grpcsync.NewEvent()

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
	// defer server.Stop()
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
	// defer cc.Close()

	// also wrap the cleanups in a helper? T test no extends too far out
	return managementServer, server, cc, func() {
		// all the cleanup...
		cleanup()
		cancel()
		server.Stop()
		cc.Close()
	}
}


// *** New musings
// all this requires resource not found - clean this test up, and then rebase onto Easwar's

// If not spoofed addresses:
// one rds - either works, fails, or resource not found



// graceful close of that rds (test case written below)



// switching lds to not found causing failures
// switching lds to a failing thing causing it to not match and failures eventually (fail)

// Gets rid of the multiple rdses complicating

// Can test rds 1 rds 2 rds 3 (wait until all 3 rds have been received to successfully go serving).
// rds (fc won't match) 1 (def filter chain) 2 (should immediately serve)
// rds (fc normal) rds 1 - should go back to rds 1 immediately (is there a way to immediately check or should it poll and that's good enough?)
// should it leave stuff around in cache? nah too much effort

// *** End new musings


// I could trigger resource not found on the first test and switch that to error condition yes :)

// error with unavailable - make sure status code is correct for failure
// assert fail with unavailable? or whatever the expected error status is...

// figuring out resource not found invocation are next steps

/*
LDS + (Two Dynamic RDS) with one specifying ok, one specifying error,
connections accepted to ok should work normally, specifying error should fail
*/
func (s) TestDynamicWithOkAndError(t *testing.T) {
	// Can I pull a lot of the setup into a helper? (difference is resources) maybe write one more test and see
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

	// big deal
	// listener originally was (ipv4 and ipv6)
	// now branch on a property of client (still need to figure out)
	// fc ipv4 ipv6 + property 1 (or can property entirely take precedence) -> ok
	// fc ipv4 + ipv6 + property 2 -> ok
	listener := e2e.DefaultServerListenerWithRouteConfigName(host, port, e2e.SecurityLevelNone, "routeName")
	// corresponding to route name ok
	routeConfig := e2e.RouteConfigNonForwardingTarget("routeName")
	// the route name for filter chain 2 manually invoke resource not found

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

	// what is a successful rpc in this case?

	// waitForSuccessfulRPC(ctx, t, cc)


	// either nack (does it get triggered by any validation failure?) or trigger
	// a resource not found through internal plumbing...RPC's matching should error

	// what property of route matching can you create distinct RPC's with?

	// RPC which match to first route fail

	// RPC which match to second route fail
	// Can you even distinguish these in the helpers ^^^?

} // this could go inline inline

/*
Serving State changes: Not Serving (before RDS comes in) (Accept() + Close), ->
Serving -> Not Serving (on specific lis) (triggers a graceful close for
connections accepted on that lis) -> serving (Test LDS resource not found)
*/

// not serving test and serving can be done before this
// trigger not serving with lds resoruce not found, Accept and Close()

// graceful close checked by - "Also verify that a streaming RPC (or a very long
// running unary) on the old configuration is able to complete gracefully."

func (s) TestServingModeChanges(t *testing.T) { // already have serving mode changes, can maybe rewrite that/merge this with that...
	managementServer, nodeID, bootstrapContents, _, cleanup := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup()
	lis, err := testutils.LocalTCPListener()
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
	// routeConfig := e2e.RouteConfigNonForwardingTarget("routeName")

	resources := e2e.UpdateOptions{
		NodeID:    nodeID,
		Listeners: []*v3listenerpb.Listener{listener},
		// Routes:    []*v3routepb.RouteConfiguration{routeConfig}, (will this trigger a failure?)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	serving := grpcsync.NewEvent()
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
	// <-serving.Done()

	// Accept() and Close() here...
	cc, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		// assert status code and error message (do error message of conn closure get eaten before hitting application layer?)
		t.Fatalf("failed to dial local test server: %v", err) // is this going to fail on a conn failure?
	}
	defer cc.Close()


	routeConfig := e2e.RouteConfigNonForwardingTarget("routeName")
	resources = e2e.UpdateOptions{
		NodeID:    nodeID,
		Listeners: []*v3listenerpb.Listener{listener},
		Routes:    []*v3routepb.RouteConfiguration{routeConfig},
	}
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	<-serving.Done()

	// A unary RPC should work once it transitions into serving.
	waitForSuccessfulRPC(ctx, t, cc)

	// this can have more than one actual underlying connection right, what
	// happens on a conn failure, does it get eaten by a lower layer?

	// setup with just lds

	// any incoming connections should accept() + close()...how to test this?

	// does conn close() signal propagate to channel/app layer, or does it get eaten by gRPC and just has an error.
	// Do Conn errors propagate to app. layer




	// then trigger rds (through management server update) (will just lds setup fail if no corresponding rds?)

	// goes serving, unary rpc should work (wait for it to work - sync to serving)
	// ^^^ code below





	// streaming RPC started too vvv
	// wrap the cc in something
	// create a stream...

	// start operation on stream




	// resource not found for lds (copy Easwar's code for this)
	// plumbing for triggerResourceNotFoundForTesting...

	// streaming RPC can complete (guarantees graceful close can continue)
	// stream.Send() // operations, have arbitrary receives server side but also set it up so CloseSend() works
	// stream.Recv() // need the corresponding server side streaming logic here
	// stream.CloseSend() // triggers close

	// Start a stream before switching the server to not serving. Due to the
	// stream being created before graceful stop, it should be able to continue
	// even after the server switches to not serving.
	c := testgrpc.NewTestServiceClient(cc)
	stream, err := c.FullDuplexCall(ctx)
	if err != nil {
		// fail - see o11y for syntax
		t.Fatalf("cc.FullDuplexCall failed: %f", err)
	}

	// Invoke the lds resource not found - this should cause the server to
	// switch to not serving. This should gracefully drain connections, and fail
	// RPC's after. (how to assert accepted + closed) does this make it's way to
	// application layer?

	// I think this would error if server already gracefully closed
	if err = stream.Send(&testgrpc.StreamingOutputCallRequest{}); err != nil {
		t.Fatalf("stream.Send() failed: %v, should continue to work due to graceful stop", err)
	}
	if err = stream.CloseSend(); err != nil {
		t.Fatalf("stream.CloseSend() failed: %v, should continue to work due to graceful stop", err)
	}
	/*if _, err := c.EmptyCall(ctx, &testpb.Empty{}); err != nil { // where is the corresponding server side streaming/full duplex call logic (stubserver?)...
		return
	}*/



	// assert status code on failed rpc?
	// check that RPC's on old conn eventually start failing since gracefully closed? how do I test this? (is this new connection or just not processing new streams?)

	// rpcs on that connection eventually start failing...because graceful stop
	// started streams work but not new ones
	waitForFailedRPC(ctx, t, cc)

	// any new connections Accept() + Close() (triggers an error)
	// try and make an rpc, fail (see earlier for more logic...) (maybe it uses wait for failed RPC like earlier - wait for failed RPC is already a helper)

	// not serving on a specific lis and one client conn, so state changes are scoped to this singular client conn...


}

// see expected error code - could tie it into error
/*
If the connection is gracefully closed (and no new connection can be made)
you'll just get UNAVAILABLE + connection refused or no addresses or failed to
handshake or something
*/

// assert the correct error code (from build or error plumbing to client side)
// what would the error code be?


// unless I want to spoof the address of the client side lis I think just do everything here
// top level test works so just need to test graceful close

// resource not found for rds either do it as part of above or whatever

// Doug mentioned spoof addresses client side (only way to do it is port), take
// port put into multiple filter chains in lis resource if I want to branch it on that...i.e. only port

/*
Basic multiple updates:
(LDS + Inline) (xDS resources can be used that have already had)

// when continuing to use above...how to verify uses old configuration?


(LDS + Dynamic), should continue to use above before switching over to this (only when new RDS comes in)

// clients should reconnect and have the new configuration apply - does this ever signal an RPC error?
// maybe could plumb in something in LDS that would cause an RPC to fail, such as an LDS that doesn't match that client anymore

// if no matching filter chain, closes the conn (how does this get reported to the application layer?)
// polling for a failing RPC waits until it finishes gracefully closing right, sync point until it starts closing conns...or does it hit immediately because new rpc
// that assertion might conflate with a new stream, so coulddd use mode change no but it doesn't change mode just starts failing RPC's

// assert certain statuses in these RPC's

*/

// Easwar's PR conflicts with this, but very minor


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
	Doug's suggestion for how to immediately switch:
	Make sure the client doesn't request A & B?  Or at least, don't send A&B
	from mgmt server, but expect RPCs to keep working.

	Easwar's suggestion:

	Or you could have some header matchers specific to each of those RDS
	resources, and thereby ensure which RDS resource is used for the RPC.

	*/

	// should setup be the same with a knob on xDS Resources:

	// oh yeah I can plumb in headers client side that determine

	// ok route, not non forwarding action unavailable, l7 failure

	// three filter chains filter chain 1 route a
	// filter chain 2 route b
	// filter chain 3 route c

	// how to test immediately switch...should this be e2e or unit?



	// same update...lds update a b

	// puts it on a queue so loses sync guarantee


	// Eventually just uses two routes (how to verify?)



	// another update lds update a...

	// eventually just use one route (how to verify?)

}

// Can I merge some of these scenarios into unit tests (handleLDS and RDS update for listener_wrapper)

// if you have rds a rds b and one rpc to rds a works it synces all of it,
// because updates once received all routes



// for filter chain match:

// can either do a. two filter chains

// or b. a filter chain
// and a default filter chain (but to hit this still needs to !match to a)

// so either way properties to match on have to flip...how do others simulate properties of incoming connections
// that match to a or b?

// all my rbac tests are ipv4 and ipv6 but same lis

// maybe can branch on

// a. 3. Server name (e.g. SNI for TLS protocol) (can I override server name)?

// b. 9. Source port. (If I make another client conn I think I get a new source
// port...) multiple rds client side from multiple channels as described by
// Eric, so really do have multiple channels in this case

// maybe ask Easwar

// get this pr out with some test cases today or tomorrow

/*
LDS with RDS that doesn’t exist, fail at L7 level after resource not found, only
way resource not found is 20 seconds, not explicit resource not found
*/

func (s) TestResourceNotFound(t *testing.T) { // technically can merge with my scenario above wrt resource not found creating failure at l7 level
	// inject resource not found somehow...

	// Definitely override this via some knob in `internal`

	// It might be nice to have a method that can be called to make a specific
	// resource timeout when called. That way we aren't flaky (or slow) because
	// of timing problems.

	// call on resource not found callback...

	// you've seen how watcher plumbs

	// either trigger a resource not found explicitly (when called)

	// or rely on timer (how does timer even go off)?

	// branch on source port
	// server name (ask Easwar)
	grpc.Dial()

	// also need to plumb resource not found into this...

}

// got rid of a unit test before goes serving
// now, before goes serving, accept and close(), (perhaps already part of serving mode change above)


// for resource not found, see Easwar's helper he wrote...


// method on the *xDS Client* that invokes a resource not found callback

// xDS Client emits sync, either block on callback serializer finishing
// or invoke directly and say you can't invoke this alongside other updates processing...




