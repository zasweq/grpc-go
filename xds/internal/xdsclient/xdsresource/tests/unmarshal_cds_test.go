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
	"google.golang.org/grpc/xds/internal/balancer/ringhash"
	"google.golang.org/grpc/xds/internal/balancer/wrrlocality"
	"google.golang.org/grpc/xds/internal/xdsclient/xdsresource"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"testing"

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

// this layer already checks emissions/conversions

// ^^^ Switch unit tests above to declare inline JSON and then unmarshal both into map to take away non determinism
// arbitrary layers..how JSON is represented anyway...

func (s) TestOutlierDetectionUnmarshalingJSON(t *testing.T) {
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
	// Declare defaults here to use throughout tests too?

	// declare inline JSON wants here or figure out a way to plumb JSON wants into test case

	tests := []struct{
		name string
		// either a. od resource declared plumbed into a larger cluster resource
		// or b. the full cluster resource, or could do something like defaultODHelper(od)
		// odProto *v3clusterpb.OutlierDetection
		clusterWithOD *v3clusterpb.Cluster
		// can see ParseConfig tests to see what these JSON string should be
		wantODCfg     string // then typecast to json.RawMessage later
	}{
		// Conver as written, including unset
		// unset is good to test, will get defaults from ParseConfig() in cds balancer
		/*{
			name: "enforcing-success-rate-zero",
			clusterWithOD: odToClusterProto(&v3clusterpb.OutlierDetection{
				EnforcingSuccessRate: &wrapperspb.UInt32Value{Value: 0},
				EnforcingFailurePercentage: &wrapperspb.UInt32Value{Value: 0},
			}),
			// Nothing set
			wantODCfg: `{}`,
		},
		{
			name: "enforcing-success-rate-null",
			// Nothing is set, should create sre and get defaults for everything
			clusterWithOD: odToClusterProto(&v3clusterpb.OutlierDetection{}),
			// sre set to {} that's it
			wantODCfg: `{"successRateEjection": {}}`,
		}, // present but empty, there is a distinction that comes from pointers
		{
			name: "enforcing-failure-percentage-zero",
			// if I set success rate percent explicitly to zero should focus on fpe
			// Say both are set to 0 in title
			clusterWithOD: odToClusterProto(&v3clusterpb.OutlierDetection{
				EnforcingSuccessRate: &wrapperspb.UInt32Value{Value: 0}, // Thus doesn't create sre - to focus on fpe
				EnforcingFailurePercentage: &wrapperspb.UInt32Value{Value: 0},
			}),
			wantODCfg: `{}`,
		},
		{
			name: "enforcing-failure-percentage-null",

			// JSON not set as well
		},*/
		// All of these checks ^^^ are encapsulated vvv


		{
			name: "success-and-failure-null",
			clusterWithOD: odToClusterProto(&v3clusterpb.OutlierDetection{}),
			wantODCfg: `{"successRateEjection": {}}`,
		},
		{
			name: "success-and-failure-zero",
			clusterWithOD: odToClusterProto(&v3clusterpb.OutlierDetection{
				EnforcingSuccessRate: &wrapperspb.UInt32Value{Value: 0}, // Thus doesn't create sre - to focus on fpe
				EnforcingFailurePercentage: &wrapperspb.UInt32Value{Value: 0},
			}),
			wantODCfg: `{}`,
		},
		// no defaults as well tested here ^^^ vvv
		{ // Should only set in JSON what is explicitly set here...
			name: "some-fields-set",
			clusterWithOD: odToClusterProto(&v3clusterpb.OutlierDetection{
				Interval:                       &durationpb.Duration{Seconds: 1},
				MaxEjectionTime:                &durationpb.Duration{Seconds: 3},
				EnforcingSuccessRate:           &wrapperspb.UInt32Value{Value: 3},
				SuccessRateRequestVolume:       &wrapperspb.UInt32Value{Value: 5},
				EnforcingFailurePercentage:     &wrapperspb.UInt32Value{Value: 7},
				FailurePercentageRequestVolume: &wrapperspb.UInt32Value{Value: 9},
			}),
			wantODCfg: `{
				"interval": "1s",
				"maxEjectionTime": "3s",
				"successRateEjection": {
					"enforcementPercentage": 3,
					"requestVolume": 5
				},
				"failurePercentageEjection": {
					"enforcementPercentage": 7,
					"requestVolume": 9
				}
			}`,
		},
		{
			name: "every-field-set-non-zero",
			clusterWithOD: odToClusterProto(&v3clusterpb.OutlierDetection{
				// all fields set (including ones that will be layered)
				// should pick up that/those too and explicitly set it in the JSON generated.
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
			wantODCfg: `{
				"interval": "1s",
				"baseEjectionTime": "2s",
				"maxEjectionTime": "3s",
				"maxEjectionPercent": 1,
				"successRateEjection": {
					"stdevFactor": 2,
					"enforcementPercentage": 3,
					"minimumHosts": 4,
					"requestVolume": 5
				},
				"failurePercentageEjection": {
					"threshold": 6,
					"enforcementPercentage": 7,
					"minimumHosts": 8,
					"requestVolume": 9
				}
			}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			update, err := xdsresource.ValidateClusterAndConstructClusterUpdateForTesting(test.clusterWithOD)
			if err != nil {
				t.Errorf("validateClusterAndConstructClusterUpdate(%+v) failed: %v", test.clusterWithOD, err)
			}
			// got and want must be unmarshalled since JSON strings shouldn't
			// generally be directly compared.
			var got map[string]interface{}
			if err := json.Unmarshal(update.OutlierDetection, &got); err != nil {
				t.Fatalf("Error unmarshalling update.OutlierDetection (%q): %v", update.OutlierDetection, err)
			}
			// why is this a slice rather than a map[string]interface{}
			// I don't need slice here, just map[string]interface{}
			var want map[string]interface{}
			if err := json.Unmarshal(json.RawMessage(test.wantODCfg), &want); err != nil {
				t.Fatalf("Error unmarshalling wantODCfg (%q): %v", test.wantODCfg, err)
			}
			if diff := cmp.Diff(got, want); diff != "" {
				t.Fatalf("cluster.OutlierDetection got unexpected output, diff (-got, +want): %v", diff)
			}
		})
	}

}


// So honestly just emit json with no child policy - nil or empty list are
// equivalent and both logically allowed. I think empty list will trigger the
// not omit empty and trigger parsing, so I think nothing is what we want to
// emit here.

// ParseConfig in CDS Balancer (no child policy - ParseConfig on OD needs to treat this as a valid config)

// Send this OD config down inline as is now in discovery mechanism

// OD Config + child policy in Cluster resolver
// Marshal Into JSON
// Parse again...

// So in this layer, just emit nothing for the child, which Michael says falls within the scope of correctness anyway






// Next layer is CDS layer...layer
// test nil == no-op (noop should not have anything set not even max int that got changed)
// now pass it JSON, check emitted OD config is expected? this is what this is testing here though (just through ParseConfig)
// need to have a clean way of


// e2e test of it off, I think default behavior of it turned on when OD
// is set but unspecified is a valid test case, that is the big correctness issue
// that I'm fixing with this change that wasn't there previous.



// WAIT BIG QUESTION WHAT TO DO WITH CHILD POLICY, if cds balancer calls with emission won't be there...
// Where was thsi called before, I think child policy was present
// do we really want to require child policy (if so add to emission from client)

// BEFORE CONFIG ADDING CHILD, JUST IGNORE THE CHILD SO ACCEPT EMPTY CHILD IN PARSE CONFIG OR EVEN NIL
// AND ADD IT LATER ONCE CONVERTED TO STRUCT AND ADD CHILD POLICY

// at the very least we just need to know when to add it, technically anything is valid here...
//

// I have an EqualIgnoringChildPolicy helper
