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
	"errors"
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
	"strings"
	"testing"
	"time"

	testgrpc "google.golang.org/grpc/test/grpc_testing"
	testpb "google.golang.org/grpc/test/grpc_testing"
)

// TestOutlierDetection tests an xDS configured ClientConn with an Outlier
// Detection present in the system which is a logical no-op. An RPC should
// proceed as normal.
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

// defaultClientResourcesSpecifyingMultipleBackendsAndOutlierDetection returns
// xDS resources which correspond to multiple upstreams, corresponding different
// backends listening on different localhost:port combinations. The resources
// also configure an Outlier Detection Balancer set up with
// SuccessRateAlgorithm.
func defaultClientResourcesSpecifyingMultipleBackendsAndOutlierDetection(params e2e.ResourceParams, ports []uint32) e2e.UpdateOptions {
	routeConfigName := "route-" + params.DialTarget
	clusterName := "cluster-" + params.DialTarget
	endpointsName := "endpoints-" + params.DialTarget
	return e2e.UpdateOptions{
		NodeID:    params.NodeID,
		Listeners: []*v3listenerpb.Listener{e2e.DefaultClientListener(params.DialTarget, routeConfigName)},
		Routes:    []*v3routepb.RouteConfiguration{e2e.DefaultRouteConfig(routeConfigName, params.DialTarget, clusterName)},
		Clusters:  []*v3clusterpb.Cluster{defaultClusterWithOutlierDetection(clusterName, endpointsName, params.SecLevel)},
		Endpoints: []*v3endpointpb.ClusterLoadAssignment{e2e.DefaultEndpoint(endpointsName, params.Host, ports)}, // this is the delta
	}
}

func defaultClusterWithOutlierDetection(clusterName, edsServiceName string, secLevel e2e.SecurityLevel) *v3clusterpb.Cluster {
	cluster := e2e.DefaultCluster(clusterName, edsServiceName, secLevel)
	cluster.OutlierDetection = &v3clusterpb.OutlierDetection{
		Interval: &durationpb.Duration{
			Seconds: 1,
			// Seconds: 1,
			// Nanos: 500000000,
			// Nanos: 500000000,
		}, // will constantly trigger interval timer algorithm. Mimics a real configuration
		BaseEjectionTime:               &durationpb.Duration{Seconds: 30},
		MaxEjectionTime:                &durationpb.Duration{Seconds: 300},
		MaxEjectionPercent:             &wrapperspb.UInt32Value{Value: 1},
		/*SuccessRateStdevFactor:         &wrapperspb.UInt32Value{Value: 100},
		SuccessRateMinimumHosts:        &wrapperspb.UInt32Value{Value: 3},
		SuccessRateRequestVolume:       &wrapperspb.UInt32Value{Value: 5},*/
		FailurePercentageThreshold: &wrapperspb.UInt32Value{Value: 50},
		EnforcingFailurePercentage: &wrapperspb.UInt32Value{Value: 100},
		FailurePercentageRequestVolume: &wrapperspb.UInt32Value{Value: 1},
		FailurePercentageMinimumHosts: &wrapperspb.UInt32Value{Value: 1},
	}
	return cluster
}

// backend 1 - normal
// backend 2 - normal
// backend 3 - always return error

// ^^^ system it tests

// TestOutlierDetectionWithOutlier tests the Outlier Detection Balancer e2e. It
// spins up three backends, one which consistently errors, and configures the
// ClientConn using xDS to connect to all three of those backends. The Outlier
// Detection Balancer should eject the connection to the backend which
// constantly errors, meaning it no longer gets round robin requests forwarded
// to it.
func (s) TestOutlierDetectionWithOutlier(t *testing.T) {
	oldOD := envconfig.XDSOutlierDetection
	envconfig.XDSOutlierDetection = true
	internal.RegisterOutlierDetectionBalancerForTesting()
	defer func() {
		envconfig.XDSOutlierDetection = oldOD
		internal.UnregisterOutlierDetectionBalancerForTesting()
	}()

	managementServer, nodeID, _, resolver, cleanup := e2e.SetupManagementServer(t)
	defer cleanup()

	// counters for how many times backends got called
	var count1, count2, count3 int

	// Working backend 1.
	//updateCh1 := testutils.NewChannel()
	port1, cleanup1 := startTestService(t, &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) {
			// 1 make sure something is written here
			// updateCh1.Replace(struct{}{})
			count1++
			return &testpb.Empty{}, nil
		},
		Address: "localhost:0",
	})
	defer cleanup1()

	// Working backend 2.
	//updateCh2 := testutils.NewChannel()
	port2, cleanup2 := startTestService(t, &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) {
			// 1 make sure something is written here
			// updateCh2.Replace(struct{}{})
			count2++
			return &testpb.Empty{}, nil
		},
		Address: "localhost:0",
	})
	defer cleanup2()
	// Backend that will always return an error and be ejected.
	//updateCh3 := testutils.NewChannel()
	port3, cleanup3 := startTestService(t, &stubserver.StubServer{
		EmptyCallF: func(context.Context, *testpb.Empty) (*testpb.Empty, error) {
			// 3 (make sure nothing is written here)
			// updateCh3.Replace(struct{}{})
			count3++
			return nil, errors.New("some error")
		},
		Address: "localhost:0",
	})
	defer cleanup3()

	const serviceName = "my-service-client-side-xds"
	resources := defaultClientResourcesSpecifyingMultipleBackendsAndOutlierDetection(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID:     nodeID,
		Host:       "localhost",
		SecLevel:   e2e.SecurityLevelNone,
	}, []uint32{port1, port2, port3})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second * 100)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	// Make a bunch of RPC's (15 5 success 5 success 5 failure with my same
	// config), Round Robining across each of the backends. This should cause
	// one of the backends to get ejected

	// 15 times, even if one of them is ejected, it's fine just will go 0, 1, 0, 1, 0, 1 wait won't get ejected no intervalTimerAlgorithm call

	// Create a ClientConn and make 15 successful RPCs, which will round robin over the three upstreams.

	cc, err := grpc.Dial(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(resolver))
	if err != nil {
		t.Fatalf("failed to dial local test server: %v", err)
	}
	defer cc.Close()


	client := testgrpc.NewTestServiceClient(cc)
	for i := 0; i < 3000; i++ {
		// Can either error or not depending on the backend called.
		if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil && !strings.Contains(err.Error(), "some error") {
			t.Fatalf("rpc EmptyCall() failed: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	print("count1: ", count1, "count2: ", count2, "count3: ", count3)


	// cleanup and get it compiling/working, then do the stuff after...

	// TriggerIntervalTimeForTesting (will eject)

	// Two things: 1. No reference to od (part of system)
	// 2. No reference to intervalTimer (unexported function)

	// od.intervalTimerAlgorithm <- this thing is way into the system

	// the intervalTimerAlgorithm is a function on the balancer...how do we get this to trigger?
	// simulate time? keep running interval timer (with a short duration),

	// all 3 updates channels should have something there - clear them
	/*_, err = updateCh1.Receive(ctx)
	if err != nil {
		t.Fatalf("error receiving from updateCh1 (backend1 should have been requested): %v", err)
	}
	_, err = updateCh2.Receive(ctx)
	if err != nil {
		t.Fatalf("error receiving from updateCh2 (backend2 should have been requested): %v", err)
	}
	_, err = updateCh3.Receive(ctx)
	if err != nil {
		t.Fatalf("error receiving from updateCh3 (backend3 should have been requested): %v", err)
	}

	// The third backend should eject once that for loop completes and it hits
	// request volume of 5 each - deterministic from Outlier Detection
	// Algorithm.

	for i := 0; i < 15; i++ {
		// Can either error or not depending on the backend called.
		if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil && !strings.Contains(err.Error(), "some error") {
			t.Fatalf("rpc EmptyCall() failed: %v", err)
		}
	}

	// Only the first two upstreams should have received an RPC, as the third should have been ejected from the interval timer algorithm.
	_, err = updateCh1.Receive(ctx)
	if err != nil {
		t.Fatalf("error receiving from updateCh1 (backend1 should have been requested): %v", err)
	}
	_, err = updateCh2.Receive(ctx)
	if err != nil {
		t.Fatalf("error receiving from updateCh2 (backend2 should have been requested): %v", err)
	}
	sCtx, sCancel := context.WithTimeout(context.Background(), defaultTestShortTimeout)
	defer sCancel()
	_, err = updateCh3.Receive(sCtx)
	if err == nil {
		t.Fatalf("there should not have been an update from updateCh3 (backend3 should have not have been requested and should be ejected): %v", err)
	}*/

	// clear the recent call buffer of the three, and then do 3 rpc's and wait on each of the buffer reads

	// at the end


	// maybe just keep running the interval timer algorithm...and wait for it to be ejected for 5 seconds

	// Send a few more RPC's, make sure 1 and 2 are still sent to but 3 isn't

	// 1 (buffer)

	// 2 (buffer)

	// 3 (make sure nothing is written here)


	// How to assert that RPC's only go RR across two of the backends?

	// Service 1 and 2 need to assert they got called (with a channel perhaps?)

	// Service 3 needs to assert it didn't get called (make sure a channel receive errors?)
}
