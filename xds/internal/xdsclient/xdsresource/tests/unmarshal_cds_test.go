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

// Package tests_test contains test cases for unmarshalling of CDS resources.
package tests_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	_ "google.golang.org/grpc/balancer/roundrobin" // To register round_robin load balancer.
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/grpctest"
	iserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/serviceconfig"
	_ "google.golang.org/grpc/xds" // Register the xDS LB Registry Converters.
	"google.golang.org/grpc/xds/internal/balancer/outlierdetection"
	"google.golang.org/grpc/xds/internal/balancer/ringhash"
	"google.golang.org/grpc/xds/internal/balancer/wrrlocality"
	"google.golang.org/grpc/xds/internal/xdsclient/xdsresource"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	v3xdsxdstypepb "github.com/cncf/xds/go/xds/type/v3"
	v3clusterpb "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3endpointpb "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	v3aggregateclusterpb "github.com/envoyproxy/go-control-plane/envoy/extensions/clusters/aggregate/v3"
	v3ringhashpb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/ring_hash/v3"
	v3roundrobinpb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/round_robin/v3"
	v3wrrlocalitypb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/wrr_locality/v3"
	"github.com/golang/protobuf/proto"
	anypb "github.com/golang/protobuf/ptypes/any"
	structpb "github.com/golang/protobuf/ptypes/struct"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

const (
	clusterName = "clusterName"
	serviceName = "service"
)

var emptyUpdate = xdsresource.ClusterUpdate{ClusterName: clusterName, LRSServerConfig: xdsresource.ClusterLRSOff}

func wrrLocality(m proto.Message) *v3wrrlocalitypb.WrrLocality {
	return &v3wrrlocalitypb.WrrLocality{
		EndpointPickingPolicy: &v3clusterpb.LoadBalancingPolicy{
			Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
				{
					TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
						TypedConfig: testutils.MarshalAny(m),
					},
				},
			},
		},
	}
}

func wrrLocalityAny(m proto.Message) *anypb.Any {
	return testutils.MarshalAny(wrrLocality(m))
}

type customLBConfig struct {
	serviceconfig.LoadBalancingConfig
}

// We have this test in a separate test package in order to not take a
// dependency on the internal xDS balancer packages within the xDS Client.
func (s) TestValidateCluster_Success(t *testing.T) {
	const customLBPolicyName = "myorg.MyCustomLeastRequestPolicy"
	stub.Register(customLBPolicyName, stub.BalancerFuncs{
		ParseConfig: func(json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
			return customLBConfig{}, nil
		},
	})

	origCustomLBSupport := envconfig.XDSCustomLBPolicy
	envconfig.XDSCustomLBPolicy = true
	defer func() {
		envconfig.XDSCustomLBPolicy = origCustomLBSupport
	}()
	tests := []struct {
		name             string
		cluster          *v3clusterpb.Cluster
		wantUpdate       xdsresource.ClusterUpdate
		wantLBConfig     *iserviceconfig.BalancerConfig
		customLBDisabled bool
	}{
		{
			name: "happy-case-logical-dns",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_LOGICAL_DNS},
				LbPolicy:             v3clusterpb.Cluster_ROUND_ROBIN,
				LoadAssignment: &v3endpointpb.ClusterLoadAssignment{
					Endpoints: []*v3endpointpb.LocalityLbEndpoints{{
						LbEndpoints: []*v3endpointpb.LbEndpoint{{
							HostIdentifier: &v3endpointpb.LbEndpoint_Endpoint{
								Endpoint: &v3endpointpb.Endpoint{
									Address: &v3corepb.Address{
										Address: &v3corepb.Address_SocketAddress{
											SocketAddress: &v3corepb.SocketAddress{
												Address: "dns_host",
												PortSpecifier: &v3corepb.SocketAddress_PortValue{
													PortValue: 8080,
												},
											},
										},
									},
								},
							},
						}},
					}},
				},
			},
			wantUpdate: xdsresource.ClusterUpdate{
				ClusterName: clusterName,
				ClusterType: xdsresource.ClusterTypeLogicalDNS,
				DNSHostName: "dns_host:8080",
			},
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &iserviceconfig.BalancerConfig{
						Name: "round_robin",
					},
				},
			},
		},
		{
			name: "happy-case-aggregate-v3",
			cluster: &v3clusterpb.Cluster{
				Name: clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_ClusterType{
					ClusterType: &v3clusterpb.Cluster_CustomClusterType{
						Name: "envoy.clusters.aggregate",
						TypedConfig: testutils.MarshalAny(&v3aggregateclusterpb.ClusterConfig{
							Clusters: []string{"a", "b", "c"},
						}),
					},
				},
				LbPolicy: v3clusterpb.Cluster_ROUND_ROBIN,
			},
			wantUpdate: xdsresource.ClusterUpdate{
				ClusterName: clusterName, LRSServerConfig: xdsresource.ClusterLRSOff, ClusterType: xdsresource.ClusterTypeAggregate,
				PrioritizedClusterNames: []string{"a", "b", "c"},
			},
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &iserviceconfig.BalancerConfig{
						Name: "round_robin",
					},
				},
			},
		},
		{
			name: "happy-case-no-service-name-no-lrs",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
				},
				LbPolicy: v3clusterpb.Cluster_ROUND_ROBIN,
			},
			wantUpdate: emptyUpdate,
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &iserviceconfig.BalancerConfig{
						Name: "round_robin",
					},
				},
			},
		},
		{
			name: "happy-case-no-lrs",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
					ServiceName: serviceName,
				},
				LbPolicy: v3clusterpb.Cluster_ROUND_ROBIN,
			},
			wantUpdate: xdsresource.ClusterUpdate{ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: xdsresource.ClusterLRSOff},
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &iserviceconfig.BalancerConfig{
						Name: "round_robin",
					},
				},
			},
		},
		{
			name: "happiest-case",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
					ServiceName: serviceName,
				},
				LbPolicy: v3clusterpb.Cluster_ROUND_ROBIN,
				LrsServer: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Self{
						Self: &v3corepb.SelfConfigSource{},
					},
				},
			},
			wantUpdate: xdsresource.ClusterUpdate{ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: xdsresource.ClusterLRSServerSelf},
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &iserviceconfig.BalancerConfig{
						Name: "round_robin",
					},
				},
			},
		},
		{
			name: "happiest-case-with-circuitbreakers",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
					ServiceName: serviceName,
				},
				LbPolicy: v3clusterpb.Cluster_ROUND_ROBIN,
				CircuitBreakers: &v3clusterpb.CircuitBreakers{
					Thresholds: []*v3clusterpb.CircuitBreakers_Thresholds{
						{
							Priority:    v3corepb.RoutingPriority_DEFAULT,
							MaxRequests: wrapperspb.UInt32(512),
						},
						{
							Priority:    v3corepb.RoutingPriority_HIGH,
							MaxRequests: nil,
						},
					},
				},
				LrsServer: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Self{
						Self: &v3corepb.SelfConfigSource{},
					},
				},
			},
			wantUpdate: xdsresource.ClusterUpdate{ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: xdsresource.ClusterLRSServerSelf, MaxRequests: func() *uint32 { i := uint32(512); return &i }()},
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &iserviceconfig.BalancerConfig{
						Name: "round_robin",
					},
				},
			},
		},
		{
			name: "happiest-case-with-ring-hash-lb-policy-with-default-config",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
					ServiceName: serviceName,
				},
				LbPolicy: v3clusterpb.Cluster_RING_HASH,
				LrsServer: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Self{
						Self: &v3corepb.SelfConfigSource{},
					},
				},
			},
			wantUpdate: xdsresource.ClusterUpdate{
				ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: xdsresource.ClusterLRSServerSelf,
			},
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: "ring_hash_experimental",
				Config: &ringhash.LBConfig{
					MinRingSize: 1024,
					MaxRingSize: 4096,
				},
			},
		},
		{
			name: "happiest-case-with-ring-hash-lb-policy-with-none-default-config",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
					ServiceName: serviceName,
				},
				LbPolicy: v3clusterpb.Cluster_RING_HASH,
				LbConfig: &v3clusterpb.Cluster_RingHashLbConfig_{
					RingHashLbConfig: &v3clusterpb.Cluster_RingHashLbConfig{
						MinimumRingSize: wrapperspb.UInt64(10),
						MaximumRingSize: wrapperspb.UInt64(100),
					},
				},
				LrsServer: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Self{
						Self: &v3corepb.SelfConfigSource{},
					},
				},
			},
			wantUpdate: xdsresource.ClusterUpdate{
				ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: xdsresource.ClusterLRSServerSelf,
			},
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: "ring_hash_experimental",
				Config: &ringhash.LBConfig{
					MinRingSize: 10,
					MaxRingSize: 100,
				},
			},
		},
		{
			name: "happiest-case-with-ring-hash-lb-policy-configured-through-LoadBalancingPolicy",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
					ServiceName: serviceName,
				},
				LoadBalancingPolicy: &v3clusterpb.LoadBalancingPolicy{
					Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
						{
							TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
								TypedConfig: testutils.MarshalAny(&v3ringhashpb.RingHash{
									HashFunction:    v3ringhashpb.RingHash_XX_HASH,
									MinimumRingSize: wrapperspb.UInt64(10),
									MaximumRingSize: wrapperspb.UInt64(100),
								}),
							},
						},
					},
				},
			},
			wantUpdate: xdsresource.ClusterUpdate{
				ClusterName: clusterName, EDSServiceName: serviceName,
			},
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: "ring_hash_experimental",
				Config: &ringhash.LBConfig{
					MinRingSize: 10,
					MaxRingSize: 100,
				},
			},
		},
		{
			name: "happiest-case-with-wrrlocality-rr-child-configured-through-LoadBalancingPolicy",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
					ServiceName: serviceName,
				},
				LoadBalancingPolicy: &v3clusterpb.LoadBalancingPolicy{
					Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
						{
							TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
								TypedConfig: wrrLocalityAny(&v3roundrobinpb.RoundRobin{}),
							},
						},
					},
				},
			},
			wantUpdate: xdsresource.ClusterUpdate{
				ClusterName: clusterName, EDSServiceName: serviceName,
			},
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &iserviceconfig.BalancerConfig{
						Name: "round_robin",
					},
				},
			},
		},
		{
			name: "happiest-case-with-custom-lb-configured-through-LoadBalancingPolicy",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
					ServiceName: serviceName,
				},
				LoadBalancingPolicy: &v3clusterpb.LoadBalancingPolicy{
					Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
						{
							TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
								TypedConfig: wrrLocalityAny(&v3xdsxdstypepb.TypedStruct{
									TypeUrl: "type.googleapis.com/myorg.MyCustomLeastRequestPolicy",
									Value:   &structpb.Struct{},
								}),
							},
						},
					},
				},
			},
			wantUpdate: xdsresource.ClusterUpdate{
				ClusterName: clusterName, EDSServiceName: serviceName,
			},
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &iserviceconfig.BalancerConfig{
						Name:   "myorg.MyCustomLeastRequestPolicy",
						Config: customLBConfig{},
					},
				},
			},
		},
		{
			name: "custom-lb-env-var-not-set-ignore-load-balancing-policy-use-lb-policy-and-enum",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
					ServiceName: serviceName,
				},
				LbPolicy: v3clusterpb.Cluster_RING_HASH,
				LbConfig: &v3clusterpb.Cluster_RingHashLbConfig_{
					RingHashLbConfig: &v3clusterpb.Cluster_RingHashLbConfig{
						MinimumRingSize: wrapperspb.UInt64(20),
						MaximumRingSize: wrapperspb.UInt64(200),
					},
				},
				LoadBalancingPolicy: &v3clusterpb.LoadBalancingPolicy{
					Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
						{
							TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
								TypedConfig: testutils.MarshalAny(&v3ringhashpb.RingHash{
									HashFunction:    v3ringhashpb.RingHash_XX_HASH,
									MinimumRingSize: wrapperspb.UInt64(10),
									MaximumRingSize: wrapperspb.UInt64(100),
								}),
							},
						},
					},
				},
			},
			wantUpdate: xdsresource.ClusterUpdate{
				ClusterName: clusterName, EDSServiceName: serviceName,
			},
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: "ring_hash_experimental",
				Config: &ringhash.LBConfig{
					MinRingSize: 20,
					MaxRingSize: 200,
				},
			},
			customLBDisabled: true,
		},
		{
			name: "load-balancing-policy-takes-precedence-over-lb-policy-and-enum",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
					ServiceName: serviceName,
				},
				LbPolicy: v3clusterpb.Cluster_RING_HASH,
				LbConfig: &v3clusterpb.Cluster_RingHashLbConfig_{
					RingHashLbConfig: &v3clusterpb.Cluster_RingHashLbConfig{
						MinimumRingSize: wrapperspb.UInt64(20),
						MaximumRingSize: wrapperspb.UInt64(200),
					},
				},
				LoadBalancingPolicy: &v3clusterpb.LoadBalancingPolicy{
					Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
						{
							TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
								TypedConfig: testutils.MarshalAny(&v3ringhashpb.RingHash{
									HashFunction:    v3ringhashpb.RingHash_XX_HASH,
									MinimumRingSize: wrapperspb.UInt64(10),
									MaximumRingSize: wrapperspb.UInt64(100),
								}),
							},
						},
					},
				},
			},
			wantUpdate: xdsresource.ClusterUpdate{
				ClusterName: clusterName, EDSServiceName: serviceName,
			},
			wantLBConfig: &iserviceconfig.BalancerConfig{
				Name: "ring_hash_experimental",
				Config: &ringhash.LBConfig{
					MinRingSize: 10,
					MaxRingSize: 100,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.customLBDisabled {
				envconfig.XDSCustomLBPolicy = false
				defer func() {
					envconfig.XDSCustomLBPolicy = true
				}()
			}
			update, err := xdsresource.ValidateClusterAndConstructClusterUpdateForTesting(test.cluster)
			if err != nil {
				t.Errorf("validateClusterAndConstructClusterUpdate(%+v) failed: %v", test.cluster, err)
			}
			// Ignore the raw JSON string into the cluster update. JSON bytes
			// are nondeterministic (whitespace etc.) so we cannot reliably
			// compare JSON bytes in a test. Thus, marshal into a Balancer
			// Config struct and compare on that. Only need to test this JSON
			// emission here, as this covers the possible output space.
			if diff := cmp.Diff(update, test.wantUpdate, cmpopts.EquateEmpty(), cmpopts.IgnoreFields(xdsresource.ClusterUpdate{}, "LBPolicy")); diff != "" {
				t.Errorf("validateClusterAndConstructClusterUpdate(%+v) got diff: %v (-got, +want)", test.cluster, diff)
			}
			bc := &iserviceconfig.BalancerConfig{}
			if err := json.Unmarshal(update.LBPolicy, bc); err != nil {
				t.Fatalf("failed to unmarshal JSON: %v", err)
			}
			if diff := cmp.Diff(bc, test.wantLBConfig); diff != "" {
				t.Fatalf("update.LBConfig got unexpected output, diff (-got +want): %v", diff)
			}
		})
	}
}

func (s) TestOutlierDetectionUnmarshaling(t *testing.T) { // this is all you need right, cds balancer just ParseConfig with emitted JSON, this gets all the logic
	// default CDS proto (the minimum you need to have it pass unmarshaling)

	// stick this thingy in it (the Knob)

	// For certain test cases:
	/*od := &v3clusterpb.OutlierDetection{
		// Don't even set top level fields, should still pickup defaults
	}

	// For last test case:
	// Set every single field, and make sure it 1:1 maps with new field
	od2 := &v3clusterpb.OutlierDetection{
		// interval 10 * time.Second in proto field here

		// BaseEjectionTime 30 * time.Second in proto field here (I'm sure I defined this somewhere in a test)
	}*/

	odToClusterProto := func(od *v3clusterpb.OutlierDetection) *v3clusterpb.Cluster {
		return &v3clusterpb.Cluster{
			Name:                 clusterName,
			ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
			EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
				EdsConfig: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
						Ads: &v3corepb.AggregatedConfigSource{},
					},
				},
			},
			LbPolicy:         v3clusterpb.Cluster_ROUND_ROBIN,
			OutlierDetection: od,
		}
	}

	// I don't think you need this. The only thing you need is to read the JSON emitted
	// and unmarshal it into an OD config and see if it matches.
	/*odToClusterUpdate := func(od json.RawMessage) xdsresource.ClusterUpdate {
		return xdsresource.ClusterUpdate{
			ClusterName:      clusterName,
			LRSServerConfig:  xdsresource.ClusterLRSOff,
			OutlierDetection: od,
		}
	}*/

	tests := []struct{
		name string
		// either a. od resource declared plumbed into a larger cluster resource
		// or b. the full cluster resource, or could do something like defaultODHelper(od)
		// odProto *v3clusterpb.OutlierDetection
		clusterWithOD *v3clusterpb.Cluster
		odCfgWant outlierdetection.LBConfig // read the field in the cluster update and unmarshal into this
	}{

		// nil emissions get checked in other cds tests right?
		/*{ // will trigger a no-op OD config to be produced in the CDS balancer
			name: "od-cfg-null",
			clusterWithOD: odToClusterProto(nil),
			// will this assertion work as intended? marshalling into odCfgWant?
			// but can't technically unmarshal this into nil...because this is json.Unarmshal(nil) after the step
			odCfgWant: nil, // or what does it unmarshal to, not a no-op, can I even test it?
		},*/
		// Now takes the logic from CDS test

		{
			name: "enforcing-success-rate-zero",
			clusterWithOD: odToClusterProto(&v3clusterpb.OutlierDetection{
				EnforcingSuccessRate: &wrapperspb.UInt32Value{Value: 0},
				EnforcingFailurePercentage: &wrapperspb.UInt32Value{Value: 0},
			}),
			odCfgWant: outlierdetection.LBConfig{
				Interval:           iserviceconfig.Duration(10 * time.Second),
				BaseEjectionTime:   iserviceconfig.Duration(30 * time.Second),
				MaxEjectionTime:    iserviceconfig.Duration(300 * time.Second),
				MaxEjectionPercent: 10,
				// ^^^ Should be defaults...
				SuccessRateEjection: nil, // no I want this to be unset
				FailurePercentageEjection: nil,
			},
		},
		// Default behavior of xDS - Outlier Detection Success Rate Ejection
		// turned on by default.
		{
			name: "enforcing-success-rate-null",
			// Nothing is set, should create sre and get defaults for everything
			clusterWithOD: odToClusterProto(&v3clusterpb.OutlierDetection{}),
			// parse into this? ParseConfig on balancer type?
			odCfgWant: outlierdetection.LBConfig{
				Interval:            iserviceconfig.Duration(10 * time.Second),
				BaseEjectionTime:    iserviceconfig.Duration(30 * time.Second),
				MaxEjectionTime:     iserviceconfig.Duration(300 * time.Second),
				MaxEjectionPercent:  10,
				// SuccessRateEjection will be configured if
				// EnforcingSuccessRate is nil, EnforcementPercentage will thus
				// pick up default enforcement percentage thus turning Outlier
				// Detection on by default in the xDS Flow.
				SuccessRateEjection: &outlierdetection.SuccessRateEjection{
					StdevFactor: 1900,
					EnforcementPercentage: 100,
					MinimumHosts: 5,
					RequestVolume: 100,
				}/*Corresponding Success rate ejection - wait if after ParseConfig this will get 100 value yup after*/,
			},
		},
		{
			name: "enforcing-failure-percentage-zero",
			// if I set success rate percent explicitly to zero should focus on fpe
			clusterWithOD: odToClusterProto(&v3clusterpb.OutlierDetection{
				EnforcingSuccessRate: &wrapperspb.UInt32Value{Value: 0}, // Thus doesn't create sre
				EnforcingFailurePercentage: &wrapperspb.UInt32Value{Value: 0},
			}),
			// defaults otherwise, could even make those fixed...
			odCfgWant: outlierdetection.LBConfig{
				Interval:           iserviceconfig.Duration(10 * time.Second),
				BaseEjectionTime:   iserviceconfig.Duration(30 * time.Second),
				MaxEjectionTime:    iserviceconfig.Duration(300 * time.Second),
				MaxEjectionPercent: 10,
				FailurePercentageEjection: nil,
			},
		},
		{
			name: "enforcing-failure-percentage-null", // thus is kinda checked above
			// od set here (I think xDS protos with all their knobs/options) (need to plumb through helpers to xDS resources
			// if I set success rate percent explicitly to zero should focus on sre

			odCfgWant: outlierdetection.LBConfig{
				Interval:           iserviceconfig.Duration(10 * time.Second),
				BaseEjectionTime:   iserviceconfig.Duration(30 * time.Second),
				MaxEjectionTime:    iserviceconfig.Duration(300 * time.Second),
				MaxEjectionPercent: 10,
				FailurePercentageEjection: nil,
			},
		},
		{
			name: "normal-conversion-both-ejection-set-non-zero",
			// set all fields to non defaults, shpuld use those.
			// If I unmarshal JSON using ParseConfig doesn't that test ParseConfig on the OD balancer too?
			clusterWithOD: odToClusterProto(&v3clusterpb.OutlierDetection{
				// all fields set (including ones that will be layered) to non default
				// should pick up that/those too.
				Interval:                       &durationpb.Duration{Seconds: 1},
				BaseEjectionTime:               &durationpb.Duration{Seconds: 2},
				MaxEjectionTime:                &durationpb.Duration{Seconds: 3},
				MaxEjectionPercent:             &wrapperspb.UInt32Value{Value: 1},
				SuccessRateStdevFactor:         &wrapperspb.UInt32Value{Value: 2},
				EnforcingSuccessRate:           &wrapperspb.UInt32Value{Value: 3},
				SuccessRateMinimumHosts:        &wrapperspb.UInt32Value{Value: 4},
				SuccessRateRequestVolume:       &wrapperspb.UInt32Value{Value: 5},
				FailurePercentageThreshold:     &wrapperspb.UInt32Value{Value: 6},
				EnforcingFailurePercentage:     &wrapperspb.UInt32Value{Value: 7},
				FailurePercentageMinimumHosts:  &wrapperspb.UInt32Value{Value: 8},
				FailurePercentageRequestVolume: &wrapperspb.UInt32Value{Value: 9},
			}),
			odCfgWant: outlierdetection.LBConfig{
				Interval:            iserviceconfig.Duration(time.Second),
				BaseEjectionTime:    iserviceconfig.Duration(2 * time.Second),
				MaxEjectionTime:     iserviceconfig.Duration(3 * time.Second),
				MaxEjectionPercent:  1,
				// SuccessRateEjection will be configred if EnforcingSuccessRate
				// is nil, EnforcementPercentage will thus pick up default.
				SuccessRateEjection: &outlierdetection.SuccessRateEjection{
					StdevFactor: 2,
					EnforcementPercentage: 3,
					MinimumHosts: 4,
					RequestVolume: 5,
				}/*Corresponding Success rate ejection - wait if after ParseConfig this will get 100 value yup after*/,
				FailurePercentageEjection: &outlierdetection.FailurePercentageEjection{
					Threshold:             6,
					EnforcementPercentage: 7,
					MinimumHosts:          8,
					RequestVolume:         9,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			update, err := xdsresource.ValidateClusterAndConstructClusterUpdateForTesting(test.clusterWithOD)
			if err != nil {
				t.Errorf("validateClusterAndConstructClusterUpdate(%+v) failed: %v", test.clusterWithOD, err)
			}
			// In order to Marshal into OD Config then assert the diff ParseConfig() needs to work correctly
			var odCfgGot *outlierdetection.LBConfig
			// This should emit valid JSON, and also with a certain structure. Unmarshal into od
			// either do map and not also test defaults
			// or od cfg want with defaults (I think this becuase tests layering flattening and also the ternary ness of each field)

			// Oh wait this does have dependency on Outlier Detection through ParseConfig()...
			// Could also unmarshal into declared maps
			if err := json.Unmarshal(update.OutlierDetection, odCfgGot); err != nil {
				t.Fatalf("failed to unmarshal JSON: %v", err)
			}
			if diff := cmp.Diff(odCfgGot, test.odCfgWant); diff != "" {
				t.Fatalf("update.OutlierDetection got unexpected output, diff (-got +want): %v", diff)
			}
		})
	}
}
// this layer already checks emissions/conversions

// Next layer is CDS layer...layer
// test nil == no-op (noop should not have anything set not even max int that got changed)
// now pass it JSON, check emitted OD config is expected? this is what this is testing here though (just through ParseConfig)
// need to have a clean way of


// e2e test of it off, I think default behavior of it turned on when OD
// is set but unspecified is a valid test case, that is the big correctness issue
// that I'm fixing with this change that wasn't there previous.
