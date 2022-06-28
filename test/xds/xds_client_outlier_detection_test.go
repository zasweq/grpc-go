/*
 *
 * Copyright 2022 gRPC authors.
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
	"fmt"
	v3clusterpb "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	v3endpointpb "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"testing"

	testgrpc "google.golang.org/grpc/test/grpc_testing"
	testpb "google.golang.org/grpc/test/grpc_testing"
)

func (s) TestOutlierDetection(t *testing.T) {
	oldOD := envconfig.XDSOutlierDetection
	envconfig.XDSOutlierDetection = true
	internal.RegisterOutlierDetectionBalancerForTesting()
	defer func() {
		envconfig.XDSOutlierDetection = oldOD
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

	managementServer, nodeID, _, resolver, cleanup1 := e2e.SetupManagementServer(t)
	defer cleanup1()

	port, cleanup2 := startTestService(t, nil)
	defer cleanup2()

	const serviceName = "my-service-client-side-xds"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       "localhost",
		Port:       port,
		SecLevel:   e2e.SecurityLevelNone,
	})
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	// Create a ClientConn and make a successful RPC.
	cc, err := grpc.Dial(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(resolver))
	if err != nil {
		t.Fatalf("failed to dial local test server: %v", err)
	}
	defer cc.Close()

	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("rpc EmptyCall() failed: %v", err)
	}
}

func defaultClientResourcesSpecifyingMultipleBackends(params e2e.ResourceParams) e2e.UpdateOptions {
	routeConfigName := "route-" + params.DialTarget
	clusterName := "cluster-" + params.DialTarget
	endpointsName := "endpoints-" + params.DialTarget
	return e2e.UpdateOptions{
		NodeID:    params.NodeID,
		Listeners: []*v3listenerpb.Listener{e2e.DefaultClientListener(params.DialTarget, routeConfigName)},
		Routes:    []*v3routepb.RouteConfiguration{e2e.DefaultRouteConfig(routeConfigName, params.DialTarget, clusterName)},
		Clusters:  []*v3clusterpb.Cluster{defaultClusterWithOutlierDetection(clusterName, endpointsName, params.SecLevel)},
		Endpoints: []*v3endpointpb.ClusterLoadAssignment{e2e.DefaultEndpoint(endpointsName, params.Host, []uint32{10, 11, 12})}, // this is the delta
	}
}

func defaultClusterWithOutlierDetection(clusterName, edsServiceName string, secLevel e2e.SecurityLevel) *v3clusterpb.Cluster {
	// either call other file with same args or prepare it inline
	cluster := e2e.DefaultCluster(clusterName, edsServiceName, secLevel)
	/*Outlier Detection Config from unit tests that ejects*/
	// call that function and then if it returns, append
	cluster.OutlierDetection = &v3clusterpb.OutlierDetection{
		Interval: &durationpb.Duration{Seconds: 1<<63 - 1}, // is this right? just needs to not trigger
		BaseEjectionTime:               &durationpb.Duration{Seconds: 30},
		MaxEjectionTime:                &durationpb.Duration{Seconds: 300},
		MaxEjectionPercent:             &wrapperspb.UInt32Value{Value: 1},
		SuccessRateStdevFactor:         &wrapperspb.UInt32Value{Value: 100},
		SuccessRateMinimumHosts:        &wrapperspb.UInt32Value{Value: 3},
		SuccessRateRequestVolume:       &wrapperspb.UInt32Value{Value: 3},
	}

	// wait we need to trigger the interval
	 	/*BalancerConfig: &LBConfig{
		Interval:           1<<63 - 1, // so the interval will never run unless called manually in test.
		BaseEjectionTime:   30 * time.Second,
		MaxEjectionTime:    300 * time.Second,
		MaxEjectionPercent: 10,
		SuccessRateEjection: &SuccessRateEjection{
			StdevFactor:           500,
			EnforcementPercentage: 100,
			MinimumHosts:          3,
			RequestVolume:         3,
		},
		ChildPolicy: &internalserviceconfig.BalancerConfig{
			Name:   tcibname,
			Config: testClusterImplBalancerConfig{},
		},
	},/*Outlier Detection Config from unit tests that ejects*/
	return cluster
}

// backend 1 - normal
// backend 2 - normal
// backend 3 - always return error

// Need to RR over these 3 ^^^ setup in same priority?
// Spin up some backends

// Something that actually has an Outlier upstream - how I do setup this system
// can't be a no-op lb config...has to actually do counting...
func (s) TestOutlierDetectionWithOutlier(t *testing.T) {
	// Also need e2e resources with Outlier Detection configuration that actually counts inside it...
	oldOD := envconfig.XDSOutlierDetection
	envconfig.XDSOutlierDetection = true
	internal.RegisterOutlierDetectionBalancerForTesting()
	defer func() {
		envconfig.XDSOutlierDetection = oldOD
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

	managementServer, nodeID, _, resolver, cleanup1 := e2e.SetupManagementServer(t)
	defer cleanup1()

	const serviceName = "my-service-client-side-xds"
	resources := defaultClientResourcesSpecifyingMultipleBackends(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       "localhost",
		SecLevel:   e2e.SecurityLevelNone,
	}) // ****, I just remembered you have to plumb Outlier Detection configuration, this comes from CDS
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}
	// What is the relation of a "backend" to a "service" to an "address"

	// cluster - explicit set of hosts/ports (can remain unchanged, can be
	// configured further with EDS, etc.)

	// what is the 3 backends, they have to be specific on address + attributes

	// Setup the system with 3 backends (same service? - see example) need a knob on one of the backends
	// to always return an error...how does the service always return true ^^^

	// spin up service 1 - stubserver with empty call that returns empty nil

	/*
	ss := &stubserver.StubServer{
			EmptyCallF: func(ctx context.Context, _ *testpb.Empty) (*testpb.Empty, error) {
				return authorityChecker(ctx, expectedAuthority)
			},
			Network: "unix",
			Address: address,
			Target:  target,
		}
	*/
	ss1 := &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) {
			// 1 make sure something is written here
			return &testpb.Empty{}, nil
		},
		Address: "localhost:10", // connections to this server need to be unique for this address, the EDS resp needs to stick this in it's locality list of endpoints
	}
	// _, p, err := net.SplitHostPort(server.Address)
	ss1.StartServer(/*Any important server options you need to configure this with?*/)
	defer ss1.Stop()

	// spin up service 2 - stubserver with empty call that returns empty nil
	ss2 := &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) {
			// 2 make sure something is written here
			return &testpb.Empty{}, nil
		},
		Address: "localhost:11", // connections to this server need to be unique for this address, the EDS resp needs to stick this in it's locality list of endpoints
	}
	// _, p, err := net.SplitHostPort(server.Address)
	ss2.StartServer()
	defer ss2.Stop()


	// spin up service 3 - stubserver with empty call that returns nil, error
	ss3 := &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) {
			// 3 (make sure nothing is written here)
			return &testpb.Empty{}, nil
		},
		Address: "localhost:12", // connections to this server need to be unique for this address, the EDS resp needs to stick this in it's locality list of endpoints
	}
	// _, p, err := net.SplitHostPort(server.Address)
	ss3.StartServer(/*Any important server options you need to configure this with?*/)
	defer ss3.Stop()

	// Outlier Detection ejects endpoints across a single priority (cluster).

	// I guess three seperate services listening on different ports on local host can logically
	// be a part of the same xDS "Cluster"?

	// Cluster Type EDS -> EDS Specifying those three addresses spun up by server
	// cluster specifies EDS -> EDS specifies three addresses spun up by server (do these addresses need port)
	//e2e.DefaultClientResources() // need to scale this up with Outlier Detection as part of cluster_impl

	// ok so this has 3 endpoints, how does it round robin across them?
	// Some balancer does round robin across them - the rr balancer itself does.
	// weighted target with one locality?

	// Make a bunch of RPC's (15 5 success 5 success 5 failure with my same
	// config), Round Robining across each of the backends. This should cause
	// one of the backends to get ejected

	// 15 times, even if one of them is ejected, it's fine just will go 0, 1, 0, 1, 0, 1 wait won't get ejected no intervalTimerAlgorithm
	// Create a ClientConn and make a successful RPC.
	cc, err := grpc.Dial(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(resolver))
	if err != nil {
		t.Fatalf("failed to dial local test server: %v", err)
	}
	defer cc.Close()

	client := testgrpc.NewTestServiceClient(cc)
	for i := 0; i < 15; i++ {
		if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
			t.Fatalf("rpc EmptyCall() failed: %v", err)
		}
	}

	// cleanup and get it compiling/working, then do this...

	// TriggerIntervalTimeForTesting (will eject)

	// 1 (buffer)

	// 2 (buffer)

	// 3 (make sure nothing is written here)


	// How to assert that RPC's only go RR across two of the backends?

	// Service 1 and 2 need to assert they got called (with a channel perhaps?)

	// Service 3 needs to assert it didn't get called (make sure a channel receive errors?)
}

/*
Outlier Detection E2E tests
Spin up a few backends, have one of them always return error and see what outlier detection does
E2E test which lives in our repo (5 seconds) is much faster than interop tests, spins up everything
*/