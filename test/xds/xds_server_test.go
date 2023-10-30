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
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/xds"

	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
)

// TestServeLDSRDS tests the case where you get the LDS resource which specifies RDS,
// and then accept a conn.
func (s) TestServeLDSRDS(t *testing.T) {
	// Do I even need server side credentials? What about resolver builder?
	managementServer, nodeID, bootstrapContents, resolverBuilder, cleanup := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup()

	// creds, err := xdscreds.NewServerCredentials()

	lis, err := testutils.LocalTCPListener()
	if err != nil {
		t.Fatalf("testutils.LocalTCPListener() failed: %v", err)
	}

	// what does the stub server do?



	// same test, but rather than LDS (inline route config)


	// Setup the management server to respond with the listener resources.
	host, port, err := hostPortFromListener(lis)
	if err != nil {
		t.Fatalf("failed to retrieve host and port of server: %v", err)
	}

	// just need to split this up into default server listener
	// and inline route config

	listener := e2e.DefaultServerListener(host, port, e2e.SecurityLevelNone, "routeName")

	listener2 := e2e.DefaultServerListener2(host, port, e2e.SecurityLevelNone, "routeName")

	// Oh duh this is how listener wrapper alone tested it...

	// make routeName a const?
	// lol they already have it
	listener3 := e2e.DefaultServerListenerWithRouteConfigName(host, port, e2e.SecurityLevelNone, "routeName")

	routeConfig := e2e.DefaultRouteConfig("routeName", "*", "cluster") // clusterName = "cluster", is this actually used or is it nfa?

	resources := e2e.UpdateOptions{
		NodeID:    nodeID,
		Listeners: []*v3listenerpb.Listener{listener3},
		// I don't think I want to skip validation...
		// or could pull this out into a seperate update step, but I think this is fine...should tirgger another rds request which will hit management server
		Routes: []*v3routepb.RouteConfiguration{routeConfig},
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	// might need mode change option for a sync point, although the server could eat
	// the mode change and block on it
	server, err := xds.NewGRPCServer(grpc.Creds(insecure.NewCredentials()), /*modeChangeOpt,*/ xds.BootstrapContentsForTesting(bootstrapContents))
	if err != nil {
		t.Fatalf("Failed to create an xDS enabled gRPC server: %v", err)
	}
	defer server.Stop()
	testgrpc.RegisterTestServiceServer(server, &testService{})

	// split it out into lds + rds

	// what type of server is this type?
	// Is it the wrapped one or the wrappper, and do you stub the inside?

	go func() {
		if err := server.Serve(lis); err != nil {
			t.Errorf("Serve() failed: %v", err)
		}
	}()

	// do I need to wait for it to switch to serving and have a sync point?
	// if it doens't wait for serving, dojn't know if lookup will hit that nil problem

	// Dialing should trigger it, don't even have to make an rpc (but can make rpc if you want)
	// Dial on the listeners address, that encodes the information you need
	cc, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial local test server: %v", err)
	}
	defer cc.Close()

	// maybe make rpc on the dialed code
	waitForSuccessfulRPC(ctx, t, cc)

}

// Move this to release branches and that should trigger it.


// Cleanup this test
// Run it on master
// cleanup this branch
// run it on this branch, it should fix it
