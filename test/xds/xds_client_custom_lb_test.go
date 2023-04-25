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
	"fmt"
	"testing"

	v3 "github.com/cncf/xds/go/xds/type/v3"
	v3clusterpb "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3endpointpb "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3roundrobinpb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/round_robin/v3"
	v3wrrlocalitypb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/wrr_locality/v3"
	"github.com/golang/protobuf/proto"
	structpb "github.com/golang/protobuf/ptypes/struct"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/testutils/roundrobin"
	"google.golang.org/grpc/internal/testutils/xds/e2e"
	"google.golang.org/grpc/resolver"
	"google.golang.org/protobuf/types/known/anypb"
)

// pretend the pick first is a custom LB we wrote.
const customLBPickFirstName = "pick_first"

// UpdateState with a picker that does something interesting - write scenario in
// notebook below and implement it with this picker, test distribution?

// just point to pick_first - can do the 1 33 1 33 assertion
func (s) TestCustomLBWRRLocalityChild(t *testing.T) {
	oldCustomLBSupport := envconfig.XDSCustomLBPolicy
	envconfig.XDSCustomLBPolicy = true
	defer func() {
		envconfig.XDSCustomLBPolicy = oldCustomLBSupport
	}()

	// blank import rr?

	// do we need to stub anything?
	managementServer, nodeID, _, r, cleanup := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup()

	// stubserver that responds with correct behavior?
	// stubserver gives you addresses

	// we need to verify distribution somehow - perhaps do that through stub servers and counters?
	backend1 := stubserver.StartTestService(t, nil)
	port1 := testutils.ParsePort(t, backend1.Address)
	defer backend1.Stop()
	backend2 := stubserver.StartTestService(t, nil)
	port2 := testutils.ParsePort(t, backend2.Address)
	defer backend2.Stop()
	backend3 := stubserver.StartTestService(t, nil)
	port3 := testutils.ParsePort(t, backend3.Address)
	defer backend3.Stop()
	backend4 := stubserver.StartTestService(t, nil)
	port4 := testutils.ParsePort(t, backend4.Address)
	defer backend4.Stop()
	backend5 := stubserver.StartTestService(t, nil)
	port5 := testutils.ParsePort(t, backend5.Address)
	defer backend5.Stop()

	// Configure a wrr_locality balancer with a rr child as the locality picking
	// policy and endpoint picking policy, and also configure 2 localities with
	// weights (1, 2) which have backends (12, 345) respectively.
	const serviceName = "my-service-client-side-xds"
	m := &v3.TypedStruct{
		TypeUrl: "type.googleapis.com/pick_first", // have this correspond to the name of custom LB registered above
		Value:   &structpb.Struct{},
	}
	resources := clientResourcesNewFieldSpecifiedAndPortsInMultipleLocalities2(e2e.ResourceParams{ // need to maybe get the cluster and pass it in here...
		DialTarget: serviceName,
		NodeID: nodeID,
		Host: "localhost",
		SecLevel: e2e.SecurityLevelNone,
	}, []uint32{port1, port2, port3, port4, port5}, wrrLocality(m)) // how do these ports get put into localities/how to specify locality weights - "load balancer information about locality weights received from EDS" (we took these ports and I put them in EDS response)
	// how do we put addresses in each locality? *** I think it's localhost + port which I already did...but make sure

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	cc, err := grpc.Dial(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(r))
	if err != nil {
		t.Fatalf("failed to dial local test server: %v", err)
	}
	defer cc.Close()

	client := testgrpc.NewTestServiceClient(cc)
	// ping once before sending to helper?
	if _, err := client.UnaryCall(ctx, &testpb.SimpleRequest{}); err != nil {
		t.Fatalf("Unary RPC failed, error: %v", err)
	}

	// 1 2 - Locality 1 (How do we specify this in xDS Configuration? i.e. these addresses in locality 1)
	// Weight: 1 (also specify this in xDS configuration)

	// 3 4 5 - Locality 2 (How do we specify this in xDS Configuration? i.e. these addresses in locality 2)
	// Weight: 2 (also specify this in xDS configuration)

	// we don't care about response, we care about the downstream effects

	// could make custom lb least request based on data sent that would be cool

	// streaming is less important because goes through same and this is just lb
	// distributions are flaky? other way of checking distribution

	// all these ports will need to be partitioned based on localities and priorities

	// 1 2 3 p1 l1
	// 4 5   p1 l2

	// p1 l2

	// based on that what is expected distribution

	// two layers here - locality weights say 75% to l1 and 25% to l2

	// the endpoint picking policy - another mathematical rr across the 75% and 25% bucket

	// scenario 2: wrr_locality round_robin child (through new field)

	// wrr locality (locality layer)
	// locality 1: 1       locality 2: 2
	// 1 2                 3 4 5 (rr across both - endpoint layer)
	// 12 345 345 12 345 345 12 345 345 (expected distribution)
	// 1/3rds 1 2        2/3rds 3 4 5

	// Figure out how to test distribution
	fullAddresses := []resolver.Address{ // full PR with deletion of old field as well...
		// backends addresses actually require you spin up backends
		// helper counts these
		// 1 - backends addresses or localhost + port that we spin up (on stubservers or backends) or are these logically equivalent?
		{Addr: backend1.Address}, // is it deterministic in ordering pick first down hierarchy, perhaps not map
		// 3
		{Addr: backend3.Address},
		{Addr: backend3.Address},
	}
	if err := roundrobin.CheckWeightedRoundRobinRPCs(ctx, client, fullAddresses); err != nil { // to make t-test: fullAddresses = wantAddresses and make knob
		t.Fatalf("error in expeected round robin: %v", err)
	}
}

// wrrLocality is a helper that takes a proto message and returns a
// WrrLocalityProto with the proto message marshaled into a proto.Any as a
// child.
func wrrLocality(m proto.Message) *v3wrrlocalitypb.WrrLocality {
	return &v3wrrlocalitypb.WrrLocality{
		EndpointPickingPolicy: &v3clusterpb.LoadBalancingPolicy{
			Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
				{
					TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
						Name:        "what is this used for?",
						TypedConfig: testutils.MarshalAny(m),
					},
				},
			},
		},
	}
}

// wrrLocalityAny takes a proto message and returns a wrr locality proto
// marshaled as an any with an any child set to the marshaled proto message.
func wrrLocalityAny(m proto.Message) *anypb.Any {
	return testutils.MarshalAny(wrrLocality(m))
}

// wrr locality
// custom lb through type struct child

// wrr locality
// round robin child

// ring hash (implicitly tested by current test)

// clusterWithLBConfiguration returns a cluster resource with the proto message Marshaled as an any
// and specified through the load_balancing_policy field.
func clusterWithLBConfiguration(clusterName, edsServiceName string, secLevel e2e.SecurityLevel, m proto.Message) *v3clusterpb.Cluster {
	// locality knobs on weights and what addresses are in which locality? no write that it's hardcoded
	// will also test that old Field doesn't get used and new one takes precedence (should get rid of as part of this PR)
	cluster := e2e.DefaultCluster(clusterName, edsServiceName, secLevel)
	// this should take precedence over the old field still plumbed in above
	// (i.e. get the rebase with all of Easwar's comments incorporated in working - will delete old field)
	cluster.LoadBalancingPolicy = &v3clusterpb.LoadBalancingPolicy{
		Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
			{
				TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
					Name: "noop name",
					TypedConfig: testutils.MarshalAny(m),
				},
			},
		},
	}
	return cluster
}

// clientResourcesNewFieldSpecifiedAndPortsInMultipleLocalities returns default
// xDS resources with two localities, of weights 1 and 2 respectively. It must
// be passed 5 ports, and the first two ports will be put in the first locality,
// and the last three will be put in the second locality. It also configures the
// proto message passed in as the Locality + Endpoint picking policy in CDS.
func clientResourcesNewFieldSpecifiedAndPortsInMultipleLocalities2(params e2e.ResourceParams, ports []uint32, m proto.Message) e2e.UpdateOptions {
	routeConfigName := "route-" + params.DialTarget
	clusterName := "cluster-" + params.DialTarget
	endpointsName := "endpoints-" + params.DialTarget
	return e2e.UpdateOptions{
		NodeID:    params.NodeID,
		Listeners: []*v3listenerpb.Listener{e2e.DefaultClientListener(params.DialTarget, routeConfigName)},
		Routes:    []*v3routepb.RouteConfiguration{e2e.DefaultRouteConfig(routeConfigName, params.DialTarget, clusterName)},
		Clusters:  []*v3clusterpb.Cluster{clusterWithLBConfiguration(clusterName, endpointsName, params.SecLevel, m)},
		Endpoints: []*v3endpointpb.ClusterLoadAssignment{e2e.EndpointResourceWithOptionsMultipleLocalities(e2e.EndpointOptions{
			ClusterName: endpointsName,
			Host:        params.Host,
			PortsInLocalities: [][]uint32{
				{ports[0], ports[1]},
				{ports[2], ports[3], ports[4]},
			},
			// interop is only turning on Go :)
			LocalityWeights: []uint32{
				1,
				2,
			},
		})},
	}
}

// scenario 1: wrr_locality custom lb child (through new field, only way)

// locality 1: 1 locality 2: 2 (make a comment = to pick first child through custom LB)
// 12            345   (custom distribution across these endpoints) (if first one (i.e. pick first) could do 1 33 1 33 1 33)

// scenario 2: wrr_locality round_robin child (through new field)

// wrr locality (locality layer)
// locality 1: 1       locality 2: 2
// 1 2                 3 4 5 (rr across both - endpoint layer)
// 12 345 345 12 345 345 12 345 345 (expected distribution)
// 1/3rds 1 2        2/3rds 3 4 5

// cleanup this helper vvv then plumb the custom lb thing into t test (make seperate t-test)

// TestCustomLBRRChild tests scenario where you have a round robin balancer
// deployed as the child of the wrr_locality picking balancer with round robin
// configured as the endpoint picking policy. The test spins up 5 backends, and
// puts the first 2 backends into a locality, and the last 3 into a second
// locality of weight 2, double the weight of the first. The xDS balancer tree
// system should correctly configure a locality picking layer (weighted target
// in wrr_locality) which picks localities based on random and nondeterminsitic
// WRR, and the endpoint layer, which simply round robins across the backends
// present in each locality. Thus, the expected distribution should be close to
// (12 345 345).
func (s) TestCustomLBRRChild(t *testing.T) {
	oldCustomLBSupport := envconfig.XDSCustomLBPolicy
	envconfig.XDSCustomLBPolicy = true
	defer func() {
		envconfig.XDSCustomLBPolicy = oldCustomLBSupport
	}()

	managementServer, nodeID, _, r, cleanup := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup()
	backend1 := stubserver.StartTestService(t, nil)
	port1 := testutils.ParsePort(t, backend1.Address)
	defer backend1.Stop()
	backend2 := stubserver.StartTestService(t, nil)
	port2 := testutils.ParsePort(t, backend2.Address)
	defer backend2.Stop()
	backend3 := stubserver.StartTestService(t, nil)
	port3 := testutils.ParsePort(t, backend3.Address)
	defer backend3.Stop()
	backend4 := stubserver.StartTestService(t, nil)
	port4 := testutils.ParsePort(t, backend4.Address)
	defer backend4.Stop()
	backend5 := stubserver.StartTestService(t, nil)
	port5 := testutils.ParsePort(t, backend5.Address)
	defer backend5.Stop()

	// Configure a wrr_locality balancer with a rr child as the locality picking
	// policy and endpoint picking policy, and also configure 2 localities with
	// weights (1, 2) which have backends (12, 345) respectively.
	m := wrrLocality(&v3roundrobinpb.RoundRobin{})
	const serviceName = "my-service-client-side-xds"
	resources := clientResourcesNewFieldSpecifiedAndPortsInMultipleLocalities2(e2e.ResourceParams{
		DialTarget: serviceName,
		NodeID: nodeID,
		Host: "localhost",
		SecLevel: e2e.SecurityLevelNone,
	}, []uint32{port1, port2, port3, port4, port5}, m)

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := managementServer.Update(ctx, resources); err != nil {
		t.Fatal(err)
	}

	cc, err := grpc.Dial(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(r))
	if err != nil {
		t.Fatalf("failed to dial local test server: %v", err)
	}
	defer cc.Close()

	client := testgrpc.NewTestServiceClient(cc)

	// 1 2 - Locality 1 (How do we specify this in xDS Configuration? i.e. these addresses in locality 1)
	// Weight: 1 (also specify this in xDS configuration)

	// 3 4 5 - Locality 2 (How do we specify this in xDS Configuration? i.e. these addresses in locality 2)
	// Weight: 2 (also specify this in xDS configuration)

	// we don't care about response, we care about the downstream effects

	// could make custom lb least request based on data sent that would be cool

	// streaming is less important because goes through same and this is just lb
	// distributions are flaky? other way of checking distribution

	// all these ports will need to be partitioned based on localities and priorities

	// 1 2 3 p1 l1
	// 4 5   p1 l2

	// p1 l2

	// based on that what is expected distribution

	// two layers here - locality weights say 75% to l1 and 25% to l2

	// the endpoint picking policy - another mathematical rr across the 75% and 25% bucket

	// scenario 2: wrr_locality round_robin child (through new field)

	// Write comment detailing the scenario in depth
	// wrr locality (locality layer)
	// locality 1: 1       locality 2: 2
	// 1 2                 3 4 5 (rr across both - endpoint layer)
	// 12 345 345 12 345 345 12 345 345 (expected distribution)
	// 1/3rds 1 2        2/3rds 3 4 5
	fullAddresses := []resolver.Address{ // full PR with deletion of old field as well...
		{Addr: backend1.Address},
		{Addr: backend2.Address},
		{Addr: backend3.Address},
		{Addr: backend4.Address},
		{Addr: backend5.Address},
		{Addr: backend3.Address},
		{Addr: backend4.Address},
		{Addr: backend5.Address},
	}

	// this broke once :/
	// adjust jitter/give on this to actually get it to stop being flaky once you get it to reproduce it
	// or context timeout
	if err := roundrobin.CheckWeightedRoundRobinRPCs(ctx, client, fullAddresses); err != nil {
		t.Fatalf("error in expeected round robin: %v", err)
	}

	// Only knob is m passed in

	// and addresses expected, once you finish this one can scale up to a t-test for custom LB
	// to make t-test: fullAddresses = wantAddresses and make knob

}

// setup my scenario above once then can reuse for
// wrr locality rr child
// wrr locality custom lb child

// ring hash (implicitly tested from previous test and the tests in xDS Client
// that show same JSON emitted)

// ^^^ other scenarios that map to this statement as well

// Top level comment from rr child comment
func (s) TestWrrLocality(t *testing.T) {
	oldCustomLBSupport := envconfig.XDSCustomLBPolicy
	envconfig.XDSCustomLBPolicy = true
	defer func() {
		envconfig.XDSCustomLBPolicy = oldCustomLBSupport
	}()

	managementServer, nodeID, _, r, cleanup := e2e.SetupManagementServer(t, e2e.ManagementServerOptions{})
	defer cleanup()
	backend1 := stubserver.StartTestService(t, nil)
	port1 := testutils.ParsePort(t, backend1.Address)
	defer backend1.Stop()
	backend2 := stubserver.StartTestService(t, nil)
	port2 := testutils.ParsePort(t, backend2.Address)
	defer backend2.Stop()
	backend3 := stubserver.StartTestService(t, nil)
	port3 := testutils.ParsePort(t, backend3.Address)
	defer backend3.Stop()
	backend4 := stubserver.StartTestService(t, nil)
	port4 := testutils.ParsePort(t, backend4.Address)
	defer backend4.Stop()
	backend5 := stubserver.StartTestService(t, nil)
	port5 := testutils.ParsePort(t, backend5.Address)
	defer backend5.Stop()
	const serviceName = "my-service-client-side-xds"
	tests := []struct{
		name string
		// specified through load_balancing_policy field
		wrrLocalityConfiguration *v3wrrlocalitypb.WrrLocality
		addressDistributionWant []resolver.Address
	}{
		{
			name: "rr_child",
			wrrLocalityConfiguration: wrrLocality(&v3roundrobinpb.RoundRobin{}),
			addressDistributionWant: []resolver.Address{ // full PR with deletion of old field as well...
				{Addr: backend1.Address},
				{Addr: backend2.Address},
				{Addr: backend3.Address},
				{Addr: backend4.Address},
				{Addr: backend5.Address},
				{Addr: backend3.Address},
				{Addr: backend4.Address},
				{Addr: backend5.Address},
			},
		},
		// custom LB field which points to pick first
		{
			name: "custom_lb_child_pick_first",
			wrrLocalityConfiguration: wrrLocality(&v3.TypedStruct{
				TypeUrl: "type.googleapis.com/pick_first",
				Value:   &structpb.Struct{},
			}),
			addressDistributionWant: []resolver.Address{
				{Addr: backend1.Address},
				{Addr: backend3.Address},
				{Addr: backend3.Address},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resources := clientResourcesNewFieldSpecifiedAndPortsInMultipleLocalities2(e2e.ResourceParams{
				DialTarget: serviceName,
				NodeID: nodeID,
				Host: "localhost",
				SecLevel: e2e.SecurityLevelNone,
			}, []uint32{port1, port2, port3, port4, port5}, test.wrrLocalityConfiguration)

			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
			defer cancel()
			if err := managementServer.Update(ctx, resources); err != nil {
				t.Fatal(err)
			}

			cc, err := grpc.Dial(fmt.Sprintf("xds:///%s", serviceName), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithResolvers(r))
			if err != nil {
				t.Fatalf("failed to dial local test server: %v", err)
			}
			defer cc.Close()

			client := testgrpc.NewTestServiceClient(cc)
			if err := roundrobin.CheckWeightedRoundRobinRPCs(ctx, client, test.addressDistributionWant); err != nil {
				t.Fatalf("error in expeected round robin: %v", err)
			}
		})
	}
}

// wt target looks correct, this is all less though
/*
tlogger.go:116: INFO roundrobin.go:202 [testutils-roundrobin] non-weighted-roundrobin, gotRatio: map[127.0.0.1:62974:0.1672872340425532 127.0.0.1:62975:0.1672872340425532 127.0.0.1:62976:0.22180851063829787 127.0.0.1:62977:0.22180851063829787 127.0.0.1:62978:0.22180851063829787], wantRatio: map[127.0.0.1:62974:0.125 127.0.0.1:62975:0.125 127.0.0.1:62976:0.25 127.0.0.1:62977:0.25 127.0.0.1:62978:0.25]  (t=+954.042525ms)
*/

/*
tlogger.go:116: INFO roundrobin.go:203 [testutils-roundrobin] non-weighted-roundrobin, gotRatio: map[127.0.0.1:49189:0.16447368421052633 127.0.0.1:49190:0.16529605263157895 127.0.0.1:49191:0.2236842105263158 127.0.0.1:49192:0.2236842105263158 127.0.0.1:49193:0.22286184210526316], wantRatio: map[127.0.0.1:49189:0.125 127.0.0.1:49190:0.125 127.0.0.1:49191:0.25 127.0.0.1:49192:0.25 127.0.0.1:49193:0.25]  (t=+199.722691ms)


tlogger.go:116: INFO roundrobin.go:203 [testutils-roundrobin] non-weighted-roundrobin, gotRatio: map[127.0.0.1:50096:0.16866335141651598 127.0.0.1:50097:0.16870102471368295 127.0.0.1:50098:0.2208785412899337 127.0.0.1:50099:0.2208785412899337 127.0.0.1:50100:0.2208785412899337], wantRatio: map[127.0.0.1:50096:0.125 127.0.0.1:50097:0.125 127.0.0.1:50098:0.25 127.0.0.1:50099:0.25 127.0.0.1:50100:0.25]

// tlogger.go:116: INFO roundrobin.go:203 [testutils-roundrobin] non-weighted-roundrobin, gotRatio: map[127.0.0.1:50916:0.16573810321715818 127.0.0.1:50917:0.16575904825737264 127.0.0.1:50918:0.22283428284182305 127.0.0.1:50919:0.22283428284182305 127.0.0.1:50920:0.22283428284182305], wantRatio: map[127.0.0.1:50916:0.125 127.0.0.1:50917:0.125 127.0.0.1:50918:0.25 127.0.0.1:50919:0.25 127.0.0.1:50920:0.25]

seems to be something slightly off wrt psuedo randomness...how is this calculated?
is weighted target being configured right?
is weighted target being configured right?

with 2, 4:

 tlogger.go:116: INFO roundrobin.go:203 [testutils-roundrobin] non-weighted-roundrobin, gotRatio: map[127.0.0.1:51279:0.16607142857142856 127.0.0.1:51280:0.1669642857142857 127.0.0.1:51281:0.22232142857142856 127.0.0.1:51282:0.22232142857142856 127.0.0.1:51283:0.22232142857142856], wantRatio: map[127.0.0.1:51279:0.125 127.0.0.1:51280:0.125 127.0.0.1:51281:0.25 127.0.0.1:51282:0.25 127.0.0.1:51283:0.25]

generally deterministic failures, I think has something to do with weighted target logic

*/
