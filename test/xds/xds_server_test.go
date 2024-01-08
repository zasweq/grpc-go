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
	"google.golang.org/grpc/internal"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/grpcsync"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/xds"

	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

var (
	errAcceptAndClose = []*status.Status{
		status.New(codes.Unavailable, "connection error: desc = \"error reading server preface: EOF\""),
		status.New(codes.Unavailable, "write: broken pipe"),
		status.New(codes.Unavailable, "read: connection reset by peer"),
	}
)

// TestServeLDSRDS tests the case where a server receives LDS resource which
// specifies RDS. LDS and RDS resources are configured on the management server,
// which the server should pick up. The server should successfully accept
// connections and RPCs should work on these accepted connections. It then
// switches the RDS resource to match incoming RPC's to a route type of type
// that isn't non forwarding action. This should get picked up by the connection
// dynamically, and subsequent RPC's on that connection should start failing
// with status code UNAVAILABLE.
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
	routeConfig := e2e.RouteConfigNonForwardingAction("routeName")

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
	select {
	case <-ctx.Done():
		t.Fatal("timeout waiting for the xDS Server to go Serving")
	case <-serving.Done():
	}

	cc, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial local test server: %v", err)
	}
	defer cc.Close()

	waitForSuccessfulRPC(ctx, t, cc) // Eventually, the LDS and dynamic RDS get processed, work, and RPC's should work as usual.

	// Set the route config to be of type route action route, which the rpc will
	// match to. This should eventually reflect in the Conn's routing
	// configuration and fail the rpc with a status code UNAVAILABLE.
	routeConfig = e2e.RouteConfigFilterAction("routeName")
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
	waitForFailedRPCWithStatusCode(ctx, t, cc, status.New(codes.Unavailable, "the incoming RPC matched to a route that was not of action type non forwarding"))
}

// waitForFailedRPCWithStatusCode makes unary RPC's until it receives the
// expected status in a polling manner. Fails if the RPC made does not return
// the expected status before the context expires.
func waitForFailedRPCWithStatusCode(ctx context.Context, t *testing.T, cc *grpc.ClientConn, sts ...*status.Status) {
	t.Helper()

	c := testgrpc.NewTestServiceClient(cc)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var err error
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("failure when waiting for RPCs to fail with certain status %v: %v. most recent error received from RPC: %v", sts, ctx.Err(), err.Error())
		case <-ticker.C:
			_, err = c.EmptyCall(ctx, &testpb.Empty{})
			for _, st := range sts {
				if status.Code(err) == st.Code() && strings.Contains(err.Error(), st.Message()) {
					t.Logf("most recent error happy case: %v", err.Error())
					return
				}
			}
		}
	}
}

// TestResourceNotFoundRDS tests the case where an LDS points to an RDS which
// returns resource not found. Before getting the resource not found, the xDS
// Server has not received all configuration needed, so it should Accept and
// Close any new connections. After it has received the resource not found
// error, the server should move to serving, successfully Accept Connections,
// and fail at the L7 level with resource not found specified.
func (s) TestResourceNotFoundRDS(t *testing.T) {
	managementServer, nodeID, bootstrapContents, _, cleanup := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup()
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatalf("testutils.LocalTCPListener() failed: %v", err)
	}
	// Setup the management server to respond with a listener resource that
	// specifies a route name to watch, and no RDS resource corresponding to
	// this route name.
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}

	listener := e2e.DefaultServerListenerWithRouteConfigName(host, port, e2e.SecurityLevelNone, "routeName")
	resources := e2e.UpdateOptions{
		NodeID:         nodeID,
		Listeners:      []*v3listenerpb.Listener{listener},
		SkipValidation: true,
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
			//serving.Fire()
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

	cc, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial local test server: %v", err)
	}
	defer cc.Close()

	waitForFailedRPCWithStatusCode(ctx, t, cc, errAcceptAndClose...)

	// Invoke resource not found - this should result in L7 RPC error with unavailable
	// receive on serving as a result, should trigger it to go serving.
	// internal.TriggerXDSResourceNameNotFoundForTesting.(func(xdsresource.Producer, string, string) error)() // test rds resource not found here
	// internal trigger xds resource not found on xDS Server

	// Problem: you only have server ref, not client, how to plumb signal all the way down
	// typecast it to a certain, server only has a certain interface of the xDS Client

	// Need to plumb two strings downward - type and name...server only has interface
	/*singletonClient := internal.SingletonClientRef.(func() any)()

	if err := internal.TriggerXDSResourceNameNotFoundForTesting.(func (any, string, string) error)(singletonClient, "RouteConfigResource", "routeName"); err != nil {
		t.Fatalf("Failed to trigger resource name not found for testing: %v", err)
	}*/
	if err := internal.TriggerXDSResourceNameNotFoundClient.(func (string, string) error)("RouteConfigResource", "routeName"); err != nil {
		t.Fatalf("Failed to trigger resource name not found for testing: %v", err)
	}
	<-serving.Done()
	waitForFailedRPCWithStatusCode(ctx, t, cc, status.New(codes.Unavailable, "error from xDS configuration for matched route configuration"))
}

// e2e test problem: how to invoke resource not found from e2e test when is internal

// TestServingModeChanges tests the Server's logic as it transitions from Not
// Ready to Ready, then to Not Ready. Before it goes Ready, connections should
// be accepted and closed. After it goes ready, RPC's should proceed as normal
// according to matched route configuration. After it transitions back into not
// ready (through an explicit LDS Resource Not Found), previously running RPC's
// should be gracefully closed and still work, and new RPC's should fail.
func (s) TestServingModeChanges(t *testing.T) {
	managementServer, nodeID, bootstrapContents, _, cleanup := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup()
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatalf("testutils.LocalTCPListener() failed: %v", err)
	}
	// Setup the management server to respond with a listener resource that
	// specifies a route name to watch. Due to not having received the full
	// configuration, this should cause the server to be in mode Serving.
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}

	listener := e2e.DefaultServerListenerWithRouteConfigName(host, port, e2e.SecurityLevelNone, "routeName")
	resources := e2e.UpdateOptions{
		NodeID:         nodeID,
		Listeners:      []*v3listenerpb.Listener{listener},
		SkipValidation: true,
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
	cc, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial local test server: %v", err)
	}
	defer cc.Close()

	waitForFailedRPCWithStatusCode(ctx, t, cc, errAcceptAndClose...)
	routeConfig := e2e.RouteConfigNonForwardingAction("routeName")
	resources = e2e.UpdateOptions{
		NodeID:    nodeID,
		Listeners: []*v3listenerpb.Listener{listener},
		Routes:    []*v3routepb.RouteConfiguration{routeConfig},
	}
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ctx.Done():
		t.Fatal("timeout waiting for the xDS Server to go Serving")
	case <-serving.Done():
	}

	// A unary RPC should work once it transitions into serving. (need this same
	// assertion from LDS resource not found triggering it).
	waitForSuccessfulRPC(ctx, t, cc)

	// Start a stream before switching the server to not serving. Due to the
	// stream being created before the graceful stop of the underlying
	// connection, it should be able to continue even after the server switches
	// to not serving.
	c := testgrpc.NewTestServiceClient(cc)
	stream, err := c.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("cc.FullDuplexCall failed: %f", err)
	}

	// Invoke the lds resource not found - this should cause the server to
	// switch to not serving. This should gracefully drain connections, and fail
	// RPC's after. (how to assert accepted + closed) does this make it's way to
	// application layer? (should work outside of resource not found...

	// Invoke LDS Resource not found here (tests graceful close)

	// New RPCs on that connection should eventually start failing. Due to
	// Graceful Stop any started streams continue to work.
	if err = stream.Send(&testgrpc.StreamingOutputCallRequest{}); err != nil {
		t.Fatalf("stream.Send() failed: %v, should continue to work due to graceful stop", err)
	}
	if err = stream.CloseSend(); err != nil {
		t.Fatalf("stream.CloseSend() failed: %v, should continue to work due to graceful stop", err)
	}
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}

	// after asserting stream can continue - this is sort of the invariant...behavior of graceful close
	// see expected error code - could tie it into error
	/*
	   If the connection is gracefully closed (and no new connection can be made)
	   you'll just get UNAVAILABLE + connection refused or no addresses or failed to
	   handshake or something
	*/
	// eventually this will hit right...
	// waitForFailedRPC(ctx, t, cc)
	// waitForFailedRPCWithStatusCode(ctx, t, cc, status.New(codes.Unavailable, ""/*error string here as Doug was describing - is there a way to describe this?*/))

	// any new connections Accept() + Close() (triggers an error)
	// try and make an rpc, fail (see earlier for more logic...) (maybe it uses wait for failed RPC like earlier - wait for failed RPC is already a helper)

	// not serving on a specific lis and one client conn, so state changes are scoped to this singular client conn...

}

// TestMultipleUpdatesImmediatelySwitch tests the case where you get an LDS
// specifying RDS A, B, and C (with A being matched to). The Server should be in
// not serving until it receives all 3 RDS Configurations, and then transition
// into serving. RPCs will match to RDS A and work properly. Afterward, it
// receives an LDS specifying RDS A, B. The Filter Chain pointing to RDS A
// doesn't get matched, and the Default Filter Chain pointing to RDS B does get
// matched. RDS B is of the wrong route type for server side, so RPC's are
// expected to eventually fail with that information. However, any RPC's on the
// old configration should be allowed to complete due to the transition being
// graceful stop.After, it receives an LDS specifying RDS A (which incoming
// RPC's will match to). This configuration should eventually be represented in
// the Server's state, and RPCs should proceed successfully.
func (s) TestMultipleUpdatesImmediatelySwitch(t *testing.T) {
	managementServer, nodeID, bootstrapContents, _, cleanup := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup()
	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatalf("testutils.LocalTCPListener() failed: %v", err)
	}
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	// Setup the management server to respond with a listener resource that
	// specifies three route names to watch.
	ldsResource := e2e.ListenerResourceThreeRouteResources(host, port, e2e.SecurityLevelNone, "routeName")
	resources := e2e.UpdateOptions{
		NodeID:         nodeID,
		Listeners:      []*v3listenerpb.Listener{ldsResource},
		SkipValidation: true,
	}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	server, err := xds.NewGRPCServer(grpc.Creds(insecure.NewCredentials()), testModeChangeServerOption(t), xds.BootstrapContentsForTesting(bootstrapContents))
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

	cc, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial local test server: %v", err)
	}
	defer cc.Close()

	waitForFailedRPCWithStatusCode(ctx, t, cc, errAcceptAndClose...)

	routeConfig1 := e2e.RouteConfigNonForwardingAction("routeName")
	routeConfig2 := e2e.RouteConfigFilterAction("routeName2")
	routeConfig3 := e2e.RouteConfigFilterAction("routeName3")
	resources = e2e.UpdateOptions{
		NodeID:         nodeID,
		Listeners:      []*v3listenerpb.Listener{ldsResource},
		Routes:         []*v3routepb.RouteConfiguration{routeConfig1, routeConfig2, routeConfig3},
		SkipValidation: true,
	}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	pollForSuccessfulRPC(ctx, t, cc)

	c := testgrpc.NewTestServiceClient(cc)
	stream, err := c.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("cc.FullDuplexCall failed: %f", err)
	}
	if err = stream.Send(&testgrpc.StreamingOutputCallRequest{}); err != nil {
		t.Fatalf("stream.Send() failed: %v, should continue to work due to graceful stop", err)
	}

	// Configure with LDS with a filter chain that doesn't get matched to and a
	// default filter chain that matches to RDS A.
	ldsResource = e2e.ListenerResourceFallbackToDefault(host, port, e2e.SecurityLevelNone)
	resources = e2e.UpdateOptions{
		NodeID:         nodeID,
		Listeners:      []*v3listenerpb.Listener{ldsResource},
		Routes:         []*v3routepb.RouteConfiguration{routeConfig1, routeConfig2, routeConfig3},
		SkipValidation: true,
	}
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatalf("error updating management server: %v", err)
	}

	// xDS is eventually consistent. So simply poll for the new change to be
	// reflected.
	// "NonForwardingAction is expected for all Routes used on server-side; a
	// route with an inappropriate action causes RPCs matching that route to
	// fail with UNAVAILABLE." - A36
	waitForFailedRPCWithStatusCode(ctx, t, cc, status.New(codes.Unavailable, "the incoming RPC matched to a route that was not of action type non forwarding"))

	// Stream should be allowed to continue on the old working configuration -
	// as it on a connection that is gracefully closed (old FCM/LDS
	// Configuration which is allowed to continue).
	if err = stream.CloseSend(); err != nil {
		t.Fatalf("stream.CloseSend() failed: %v, should continue to work due to graceful stop", err)
	}
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}

	ldsResource = e2e.DefaultServerListener(host, port, e2e.SecurityLevelNone, "routeName")
	resources = e2e.UpdateOptions{
		NodeID:         nodeID,
		Listeners:      []*v3listenerpb.Listener{ldsResource},
		Routes:         []*v3routepb.RouteConfiguration{routeConfig1, routeConfig2, routeConfig3},
		SkipValidation: true,
	}
	if err := managementServer.Update(ctx, resources); err != nil {
		print("management server error")
		t.Fatal(err)
	}

	pollForSuccessfulRPC(ctx, t, cc)
}

func pollForSuccessfulRPC(ctx context.Context, t *testing.T, cc *grpc.ClientConn) {
	t.Helper()
	c := testgrpc.NewTestServiceClient(cc)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout waiting for RPCs to succeed")
		case <-ticker.C:
			if _, err := c.EmptyCall(ctx, &testpb.Empty{}); err == nil {
				return
			}
		}
	}
}
