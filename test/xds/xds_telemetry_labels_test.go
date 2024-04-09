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

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	istats "google.golang.org/grpc/internal/stats"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/stats"

	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

// cleanup, then rebase on in flight PR...

const serviceNameKey = "service_name"
const serviceNamespaceKey = "service_namespace"
const serviceNameValue = "grpc-service"
const serviceNamespaceValue = "grpc-service-namespace"

// TestTelemetryLabels tests that telemetry labels from CDS make their way to
// the stats handler. The stats handler sets the mutable context value that the
// cluster impl picker will write telemetry labels to, and then the stats
// handler asserts that subsequent HandleRPC calls from the RPC lifecycle
// contain telemetry labels that it can see.

func (s) TestTelemetryLabels(t *testing.T) {
	managementServer, nodeID, _, resolver, cleanup1 := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup1()

	server := stubserver.StartTestService(t, nil)
	defer server.Stop()

	// serviceName same as label?

	// need the xDS configuration that's needed to work...
	// basic xDS configuration that will make it work + CDS with correct labels plumbed in
	// (see unit tests in client for the two labels I want)

	const xdsServiceName = "my-service-client-side-xds"
	resources := e2e.DefaultClientResources(e2e.ResourceParams{
		DialTarget: xdsServiceName,
		NodeID:     nodeID,
		Host:       "localhost",
		Port:       testutils.ParsePort(t, server.Address),
		SecLevel:   e2e.SecurityLevelNone,
	})

	resources.Clusters[0].Metadata = &v3corepb.Metadata{
		FilterMetadata: map[string]*structpb.Struct{
			"com.google.csm.telemetry_labels": {
				Fields: map[string]*structpb.Value{
					serviceNameKey: structpb.NewStringValue(serviceNameValue),
					serviceNamespaceKey: structpb.NewStringValue(serviceNamespaceValue),
				},
			},
		},
	}

	/*resources.Clusters = []*v3clusterpb.Cluster{e2e.ClusterResourceWithOptions(e2e.ClusterOptions{
		// What other options do I need to configure here?
		ClusterName:   "cluster-" + xdsServiceName, // can I not add it to a const?
		ServiceName:   "endpoint-" + xdsServiceName,
		Policy:        e2e.LoadBalancingPolicyRoundRobin,
		SecurityLevel: e2e.SecurityLevelNone,
		// Default..

		TelemetryLabels: map[string]string{
			serviceNameKey:      serviceNameValue, // you might have to use the service name that works
			serviceNamespaceKey: serviceNamespaceValue,
		}, // this doesn't work
	}), // eventually default + telemetry configured...
	}*/ // replace cluster with default + telemetry labels
	//resources.SkipValidation = true // what happens if this gets set?


	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	fsh := &fakeStatsHandler{
		// do I need to construct labels? map or is it constructed on TagRPC and
		// stored in fakeStatsHandler...
		t: t,
	}

	cc, err := grpc.NewClient(fmt.Sprintf("xds:///%s", xdsServiceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(resolver), grpc.WithStatsHandler(fsh))
	if err != nil {
		t.Fatalf("failed to create a new client to local test server: %v", err)
	}
	defer cc.Close()

	client := testgrpc.NewTestServiceClient(cc)
	if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.WaitForReady(true)); err != nil {
		t.Fatalf("rpc EmptyCall() failed: %v", err)
	}

	// stats handler asserts it can see telemetry labels...will this actually
	// fail?

}

type fakeStatsHandler struct {
	// immutable pointer
	labels *istats.Labels // does this work?

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
	labels := &istats.Labels{
		TelemetryLabels: make(map[string]string),
	}
	fsh.labels = labels // does this write work? tests the mutablity
	// this is currently in xds/internal...move it to stats/opentelemetry...
	ctx = istats.SetLabels(ctx, labels)
	return ctx
}


func (fsh *fakeStatsHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	// for the events that we know can get it (i.e. not client started
	// metrics)...write a comment about it

	switch /*st := */ rs.(type) {
	// in headers etc. are ignored, don't record metrics on sh...but theoritically which events should get it?
	case *stats.Begin:
		// this records started RPC metrics, which we've established won't have access to telemetry labels...
	case *stats.OutPayload:
	case *stats.InPayload:
	case *stats.End:

		// GetLabels happens in cluster_impl, that happens on heap so it's
		// observable by this... same heap as what's pointed to by this map,
		// doesn't race because pick comes before.



		// These all record the metrics we want...we want access to the labels
		// all the metrics but started defined in A68

		// could make these wants consts...
		if label, ok := fsh.labels.TelemetryLabels[serviceNameKey]; !ok || label != serviceNameValue { // these wanted vals (make const) will be plumbed through cds
			// persist t? it's in same goroutine as test?
			fsh.t.Fatalf("for telemetry label %v, want: %v, got: %v", serviceNameKey, serviceNameValue, label) // what to log as error message?
		}
		if label, ok := fsh.labels.TelemetryLabels[serviceNamespaceKey]; !ok || label != serviceNamespaceValue {
			fsh.t.Fatalf("for telemetry label %v, want: %v, got: %v", serviceNamespaceKey, serviceNamespaceValue, label) // what to log as error message?
		}

	default:
		// Unknown sh event?
	}

}
