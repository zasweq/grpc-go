/*
 *
 * Copyright 2024 gRPC authors.
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
	"testing"

	v3clusterpb "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/stats/opentelemetry"
)

const serviceNameValue = "grpc-service"
const serviceNamespaceValue = "grpc-service-namespace"

// TestTelemetryLabels tests that telemetry labels from CDS make their way to
// the stats handler. The stats handler sets the mutable context value that the
// cluster impl picker will write telemetry labels to, and then the stats
// handler asserts that subsequent HandleRPC calls from the RPC lifecycle
// contain telemetry labels that it can see.

func (s) TestTelemetryLabels(t *testing.T) {
	// e2e.SetupManagementServer(t, e2e.ManagementServerOptions{}) // do I need to set something for management server?
	managementServer, nodeID, _, resolver, cleanup1 := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup1()

	// I think you just need one
	server := stubserver.StartTestService(t, nil)
	defer server.Stop()

	// serviceName same as label?

	// need the xDS configuration that's needed to work...
	// basic xDS configuration that will make it work + CDS with correct labels plumbed in
	// (see unit tests in client for the two labels I want)

	// this is client side configuration...what's the server just the stub server...

	const xdsServiceName = "my-service-client-side-xds"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: xdsServiceName, // use this for service name label?
		NodeID:     nodeID,
		Host:       "localhost",
		Port:       testutils.ParsePort(t, server.Address),
		SecLevel:   e2e.SecurityLevelNone,
	})

	/*
	clusterName := "cluster-" + params.DialTarget
		endpointsName := "endpoints-" + params.DialTarget
	*/
	// set the telemetry labels on the cds - end goal is default cluster with
	// telemetry labels, plumb through options...
	resources.Clusters = []*v3clusterpb.Cluster{e2e.ClusterResourceWithOptions(e2e.ClusterOptions{
		// What other options do I need to configure here?
		ClusterName:   "cluster-" + xdsServiceName,
		ServiceName:   "endpoint-" + xdsServiceName, // change this
		Policy:        e2e.LoadBalancingPolicyRoundRobin,
		SecurityLevel: e2e.SecurityLevelNone,
		// Default..

		TelemetryLabels: map[string]string{
			"service_name":      serviceNameValue, // you might have to use the service name that works
			"service_namespace": serviceNamespaceValue,
		},
	} /*eventually default + telemetry configured...*/),
	}

	// resources.Cluster // append to this the telemetry labels...plumb it as part of options


	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}





	fsh := &fakeStatsHandler{
		// do I need to construct labels?
		t: t,
	}
	// then make an RPC
	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(resolver), grpc.WithStatsHandler(fsh))
	if err != nil {
		t.Fatalf("failed to create a new client to local test server: %v", err)
	}
	defer cc.Close()
	// stats handler plumbed in ^^^

	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("rpc EmptyCall() failed: %v", err)
	}

	// stats handler asserts it can see telemetry labels...

}

type fakeStatsHandler struct {
	// immutable pointer
	labels *opentelemetry.Labels // does this work?

	// is this a valid pattern holding onto it?
	t *testing.T
}

// TagConn is on the connection context - do it at the connection scope or RPC scope...
// telemetry labels...TagConn, I think TagRPC...
func (fsh *fakeStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

func (fsh *fakeStatsHandler) HandleConn(context.Context, stats.ConnStats) {}

// define an interceptor on it, have it plumb into context the labels needed
// when would opentelemetry set it - interceptor or stats handler?
func (fsh *fakeStatsHandler) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context {
	// use helper to set mutable thing in context value...
	labels := &opentelemetry.Labels{}
	fsh.labels = labels // does this write work?
	// this is currently in xds/internal...move it to stats/opentelemetry...
	ctx = opentelemetry.SetLabels(ctx, labels)
	return ctx
}


func (fsh *fakeStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	// for the events that we know can get it (i.e. not client started
	// metrics)...

	switch /*st := */ rs.(type) {
	// in headers etc. are ignored
	case *stats.Begin:
		// this records started RPC metrics, which we've established won't have access to telemetry labels...
	case *stats.OutPayload:
	case *stats.InPayload:
	case *stats.End:
		// These all record the metrics we want...we want access to the labels
		// all the metrics but started defined in A68

		// could make these wants consts...
		if label, ok := fsh.labels.TelemetryLabels["service_name"]; !ok || label != serviceName { // these wanted vals (make const) will be plumbed through cds
			// persist t? it's in same goroutine as test?
			fsh.t.Fatalf() // what to log as error message?
		}
		if label, ok := fsh.labels.TelemetryLabels["service_namespace"]; !ok || label != serviceNamespace {
			fsh.t.Fatalf() // what to log as error message?
		}

	case /*all the stats handler events we want here...that correspond to metrics we want i.e. !started*/
	}

	// could make these wants consts...
	if label, ok := fsh.labels.TelemetryLabels[""/*first telemetry label wanted here...*/]; !ok || label != "val wanted here..." {
		// persist t? it's in same goroutine as test?
		t.Fatalf() // what to log as error message?
	}
	if label, ok := fsh.labels.TelemetryLabels[""/*second telemetry label wanted here...*/]; !ok || label != "val wanted here..." {
		t.Fatalf() // what to log as error message?
	}

}
