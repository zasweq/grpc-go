/*
 *
 * Copyright 2021 gRPC authors.
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
 */

package xdsresource

import (
	"encoding/json"
	v3 "github.com/cncf/xds/go/xds/type/v3"
	least_requestv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/least_request/v3"
	v3ringhashpb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/ring_hash/v3"
	v3roundrobinpb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/round_robin/v3"
	wrr_localityv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/wrr_locality/v3"
	"github.com/golang/protobuf/proto"
	structpb "github.com/golang/protobuf/ptypes/struct"
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/serviceconfig"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	_ "google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/pretty"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/matcher"
	"google.golang.org/grpc/xds/internal/balancer/ringhash"
	"google.golang.org/grpc/xds/internal/balancer/wrrlocality"
	_ "google.golang.org/grpc/xds/internal/balancer/wrrlocality"
	"google.golang.org/grpc/xds/internal/xdsclient/xdsresource/version"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	v3clusterpb "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	v3endpointpb "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	v3aggregateclusterpb "github.com/envoyproxy/go-control-plane/envoy/extensions/clusters/aggregate/v3"
	v3tlspb "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	v3discoverypb "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	v3matcherpb "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	anypb "github.com/golang/protobuf/ptypes/any"
)

const (
	clusterName = "clusterName"
	serviceName = "service"
)

var emptyUpdate = ClusterUpdate{ClusterName: clusterName, LRSServerConfig: ClusterLRSOff}

func (s) TestValidateCluster_Failure(t *testing.T) {
	oldCustomLBSupport := envconfig.XDSCustomLBPolicy // gates the client layer kasi
	envconfig.XDSCustomLBPolicy = true
	defer func() {
		envconfig.XDSCustomLBPolicy = oldCustomLBSupport
	}()
	tests := []struct {
		name       string
		cluster    *v3clusterpb.Cluster
		wantUpdate ClusterUpdate
		wantErr    bool
	}{
		{
			name: "non-supported-cluster-type-static",
			cluster: &v3clusterpb.Cluster{
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_STATIC},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
				},
				LbPolicy: v3clusterpb.Cluster_LEAST_REQUEST,
			},
			wantUpdate: emptyUpdate,
			wantErr:    true,
		},
		{
			name: "non-supported-cluster-type-original-dst",
			cluster: &v3clusterpb.Cluster{
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_ORIGINAL_DST},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
				},
				LbPolicy: v3clusterpb.Cluster_LEAST_REQUEST,
			},
			wantUpdate: emptyUpdate,
			wantErr:    true,
		},
		{
			name: "no-eds-config",
			cluster: &v3clusterpb.Cluster{
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				LbPolicy:             v3clusterpb.Cluster_ROUND_ROBIN,
			},
			wantUpdate: emptyUpdate,
			wantErr:    true,
		},
		{
			name: "no-ads-config-source",
			cluster: &v3clusterpb.Cluster{
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig:     &v3clusterpb.Cluster_EdsClusterConfig{},
				LbPolicy:             v3clusterpb.Cluster_ROUND_ROBIN,
			},
			wantUpdate: emptyUpdate,
			wantErr:    true,
		},
		{
			name: "non-round-robin-or-ring-hash-lb-policy",
			cluster: &v3clusterpb.Cluster{
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
				},
				LbPolicy: v3clusterpb.Cluster_LEAST_REQUEST,
			},
			wantUpdate: emptyUpdate,
			wantErr:    true,
		},
		{
			name: "logical-dns-multiple-localities",
			cluster: &v3clusterpb.Cluster{
				Name:                 clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_LOGICAL_DNS},
				LbPolicy:             v3clusterpb.Cluster_ROUND_ROBIN,
				LoadAssignment: &v3endpointpb.ClusterLoadAssignment{
					Endpoints: []*v3endpointpb.LocalityLbEndpoints{
						// Invalid if there are more than one locality.
						{LbEndpoints: nil},
						{LbEndpoints: nil},
					},
				},
			},
			wantUpdate: emptyUpdate,
			wantErr:    true,
		},



		// should still error, just (make note in pr?) due to ParseConfig
		{
			name: "ring-hash-hash-function-not-xx-hash",
			cluster: &v3clusterpb.Cluster{
				LbPolicy: v3clusterpb.Cluster_RING_HASH,
				LbConfig: &v3clusterpb.Cluster_RingHashLbConfig_{
					RingHashLbConfig: &v3clusterpb.Cluster_RingHashLbConfig{
						HashFunction: v3clusterpb.Cluster_RingHashLbConfig_MURMUR_HASH_2,
					},
				},
			},
			wantUpdate: emptyUpdate,
			wantErr:    true,
		},
		// ^^^ xds client assertions should hold true (should I readd back the
		// validation failure, except have it at the nack level still errors but
		// at the validation step in the client calling into lb policy registry.

		// comment below vvv

		// vvv validations in the balancer, but they still shoulddd cause nack
		// even though we moved it at some layer, as xds clients job is to
		// validate the prepared configuration. So perhaps we need to move it to
		// some layer that represents the "validate" /nack thing of this.
		{
			name: "ring-hash-max-bound-greater-than-upper-bound",
			cluster: &v3clusterpb.Cluster{
				LbPolicy: v3clusterpb.Cluster_RING_HASH,
				LbConfig: &v3clusterpb.Cluster_RingHashLbConfig_{
					RingHashLbConfig: &v3clusterpb.Cluster_RingHashLbConfig{
						MaximumRingSize: wrapperspb.UInt64(ringHashSizeUpperBound + 1),
					},
				},
			},
			wantUpdate: emptyUpdate,
			wantErr:    true,
		},

		// ring hash configured as above through new field which breaks validation
		{
			name: "ring-hash-max-bound-greater-than-upper-bound-load-balancing-policy",
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
				// the value of this gets passed verbatim to my converter library
				LoadBalancingPolicy: &v3clusterpb.LoadBalancingPolicy{
					Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
						{
							TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
								Name: "what happens if this isn't set?", // This typed_config field is used to determine both the name of a policy, do we need validations on this or is this used somewhere?
								TypedConfig: testutils.MarshalAny(&v3ringhashpb.RingHash{ // this shouldddd correctly unmarshal as expected
									HashFunction: v3ringhashpb.RingHash_XX_HASH,
									MinimumRingSize: wrapperspb.UInt64(10),
									MaximumRingSize: wrapperspb.UInt64(ringHashSizeUpperBound + 1),
								}),
							},
						},
						// should I test that the next node in list doesn't get hit, I don't think I should
					},
				},
				LrsServer: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Self{
						Self: &v3corepb.SelfConfigSource{},
					},
				},
			},
			wantUpdate: emptyUpdate,
			wantErr: true,
		},
		// Least request configured - should not find it in policy registry
		{
			name: "least-request-unsupported-in-converter",
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
				// the value of this gets passed verbatim to my converter library
				LoadBalancingPolicy: &v3clusterpb.LoadBalancingPolicy{
					Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
						{
							TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
								Name: "unsupported proto type",
								TypedConfig: testutils.MarshalAny(&least_requestv3.LeastRequest{}),
							},
						},
						// should I test that the next node in list doesn't get hit, I don't think I should
					},
				},
				LrsServer: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Self{
						Self: &v3corepb.SelfConfigSource{},
					},
				},
			},
			wantUpdate: emptyUpdate,
			wantErr: true,
		},

		{
			name: "aggregate-nil-clusters",
			cluster: &v3clusterpb.Cluster{
				Name: clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_ClusterType{
					ClusterType: &v3clusterpb.Cluster_CustomClusterType{
						Name:        "envoy.clusters.aggregate",
						TypedConfig: testutils.MarshalAny(&v3aggregateclusterpb.ClusterConfig{}),
					},
				},
				LbPolicy: v3clusterpb.Cluster_ROUND_ROBIN,
			},
			wantUpdate: emptyUpdate,
			wantErr:    true,
		},
		{
			name: "aggregate-empty-clusters",
			cluster: &v3clusterpb.Cluster{
				Name: clusterName,
				ClusterDiscoveryType: &v3clusterpb.Cluster_ClusterType{
					ClusterType: &v3clusterpb.Cluster_CustomClusterType{
						Name: "envoy.clusters.aggregate",
						TypedConfig: testutils.MarshalAny(&v3aggregateclusterpb.ClusterConfig{
							Clusters: []string{},
						}),
					},
				},
				LbPolicy: v3clusterpb.Cluster_ROUND_ROBIN,
			},
			wantUpdate: emptyUpdate,
			wantErr:    true,
		},
	}

	oldAggregateAndDNSSupportEnv := envconfig.XDSAggregateAndDNS
	envconfig.XDSAggregateAndDNS = true
	defer func() { envconfig.XDSAggregateAndDNS = oldAggregateAndDNSSupportEnv }()
	oldRingHashSupport := envconfig.XDSRingHash
	envconfig.XDSRingHash = true
	defer func() { envconfig.XDSRingHash = oldRingHashSupport }()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if update, err := validateClusterAndConstructClusterUpdate(test.cluster); err == nil {
				t.Errorf("validateClusterAndConstructClusterUpdate(%+v) = %v, wanted error", test.cluster, update)
			}
		})
	}
}

func wrrLocality(m proto.Message) *wrr_localityv3.WrrLocality {
	return &wrr_localityv3.WrrLocality{
		EndpointPickingPolicy: &v3clusterpb.LoadBalancingPolicy{
			Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
				{
					TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
						Name: "what is this used for?",
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

func (s) TestValidateCluster_Success(t *testing.T) {
	const customLBPolicyName = "myorg.MyCustomLeastRequestPolicy"
	stub.Register(customLBPolicyName, stub.BalancerFuncs{
		ParseConfig: func(json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
			return customLBConfig{}, nil
		},
	})

	oldCustomLBSupport := envconfig.XDSCustomLBPolicy // gates the client layer kasi
	envconfig.XDSCustomLBPolicy = true
	defer func() {
		envconfig.XDSCustomLBPolicy = oldCustomLBSupport
	}()
	tests := []struct {
		name       string
		cluster    *v3clusterpb.Cluster
		wantUpdate ClusterUpdate
		wantLBConfig *internalserviceconfig.BalancerConfig // take json in cluster update unmarshal into wantLBConfig - write comment explaining reasoning here
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
			wantUpdate: ClusterUpdate{
				ClusterName: clusterName,
				ClusterType: ClusterTypeLogicalDNS,
				DNSHostName: "dns_host:8080",
			},
			wantLBConfig: &internalserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &internalserviceconfig.BalancerConfig{
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
			wantUpdate: ClusterUpdate{
				ClusterName: clusterName, LRSServerConfig: ClusterLRSOff, ClusterType: ClusterTypeAggregate,
				PrioritizedClusterNames: []string{"a", "b", "c"},
			},
			wantLBConfig: &internalserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &internalserviceconfig.BalancerConfig{
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
			wantLBConfig: &internalserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &internalserviceconfig.BalancerConfig{
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
			wantUpdate: ClusterUpdate{ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: ClusterLRSOff},
			wantLBConfig: &internalserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &internalserviceconfig.BalancerConfig{
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
			wantUpdate: ClusterUpdate{ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: ClusterLRSServerSelf},
			wantLBConfig: &internalserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &internalserviceconfig.BalancerConfig{
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
			wantUpdate: ClusterUpdate{ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: ClusterLRSServerSelf, MaxRequests: func() *uint32 { i := uint32(512); return &i }()},
			wantLBConfig: &internalserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &internalserviceconfig.BalancerConfig{
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
			wantUpdate: ClusterUpdate{
				ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: ClusterLRSServerSelf,
				LBPolicy: &ClusterLBPolicyRingHash{MinimumRingSize: defaultRingHashMinSize, MaximumRingSize: defaultRingHashMaxSize},
			},
			wantLBConfig: &internalserviceconfig.BalancerConfig{
				Name: "ring_hash_experimental",
				Config: &ringhash.LBConfig{
					MinRingSize: 1024, // perhaps check if this follows gRFCs. This feels off wrt validations and numbers at each layer?
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
			wantUpdate: ClusterUpdate{
				ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: ClusterLRSServerSelf,
				LBPolicy: &ClusterLBPolicyRingHash{MinimumRingSize: 10, MaximumRingSize: 100},
			},
			wantLBConfig: &internalserviceconfig.BalancerConfig{
				Name: "ring_hash_experimental",
				Config: &ringhash.LBConfig{
					MinRingSize: 10,
					MaxRingSize: 100,
				},
			},
		},
		// *** Load Balancing Policy field tests
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
				// the value of this gets passed verbatim to my converter library
				LoadBalancingPolicy: &v3clusterpb.LoadBalancingPolicy{
					Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
						{
							TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
								Name: "what happens if this isn't set?", // This typed_config field is used to determine both the name of a policy, do we need validations on this or is this used somewhere?
								TypedConfig: testutils.MarshalAny(&v3ringhashpb.RingHash{ // this shouldddd correctly unmarshal as expected
									HashFunction: v3ringhashpb.RingHash_XX_HASH,
									MinimumRingSize: wrapperspb.UInt64(10),
									MaximumRingSize: wrapperspb.UInt64(100),
								}),
							},
						},
						// should I test that the next node in list doesn't get hit, I don't think I should
					},
				},
				LrsServer: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Self{
						Self: &v3corepb.SelfConfigSource{},
					},
				},
			},
			wantUpdate: ClusterUpdate{
				ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: ClusterLRSServerSelf,
			},
			wantLBConfig: &internalserviceconfig.BalancerConfig{
				Name: "ring_hash_experimental",
				Config: &ringhash.LBConfig{
					MinRingSize: 10,
					MaxRingSize: 100,
				},
			},
		},
		// maybe test the wrr_locality with a custom lb child
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
				// the value of this gets passed verbatim to my converter library
				LoadBalancingPolicy: &v3clusterpb.LoadBalancingPolicy{
					Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
						{
							TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
								Name: "what is this used for?",
								TypedConfig: wrrLocalityAny(&v3roundrobinpb.RoundRobin{}),
							},
						},
					},
				},
				LrsServer: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Self{
						Self: &v3corepb.SelfConfigSource{},
					},
				},
			},
			wantUpdate: ClusterUpdate{
				ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: ClusterLRSServerSelf,
			},
			wantLBConfig: &internalserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &internalserviceconfig.BalancerConfig{
						Name: "round_robin",
					},
				},
			},
		},
		// Custom LB:
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
				// the value of this gets passed verbatim to my converter library
				LoadBalancingPolicy: &v3clusterpb.LoadBalancingPolicy{
					Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
						{
							TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
								Name: "what is this used for?",
								// nah we don't want to populate exported name space, perhaps use internal test utils helpers?
								TypedConfig: wrrLocalityAny(&v3.TypedStruct{ // need to add the two helpers here too
									TypeUrl: "type.googleapis.com/myorg.MyCustomLeastRequestPolicy",
									Value: &structpb.Struct{}, // probably just marshals into empty json, does go struct -> json
								}),
							},
						},
					},
				},
				LrsServer: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Self{
						Self: &v3corepb.SelfConfigSource{},
					},
				},
			},
			wantUpdate: ClusterUpdate{
				ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: ClusterLRSServerSelf,
			},
			wantLBConfig: &internalserviceconfig.BalancerConfig{
				Name: wrrlocality.Name,
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &internalserviceconfig.BalancerConfig{
						Name: "myorg.MyCustomLeastRequestPolicy",
						Config: customLBConfig{}, // declare this type as a stub balancer and also return this config from the stub
					},
				},
			},
		},
	}

	oldAggregateAndDNSSupportEnv := envconfig.XDSAggregateAndDNS
	envconfig.XDSAggregateAndDNS = true
	defer func() { envconfig.XDSAggregateAndDNS = oldAggregateAndDNSSupportEnv }()
	oldRingHashSupport := envconfig.XDSRingHash
	envconfig.XDSRingHash = true
	defer func() { envconfig.XDSRingHash = oldRingHashSupport }()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			update, err := validateClusterAndConstructClusterUpdate(test.cluster)
			if err != nil {
				t.Errorf("validateClusterAndConstructClusterUpdate(%+v) failed: %v", test.cluster, err)
			}
			// mention why we ignore json, ignore due to stuff like non
			// deterministic white spaces etc. and also how the tests of this
			// json -> LB Config are sufficient and map to whole testing file
			// since all round robin set elsewhere, and this sufficient tests
			// the json through the lb config below.
			if diff := cmp.Diff(update, test.wantUpdate, cmpopts.EquateEmpty(), cmpopts.IgnoreFields(ClusterUpdate{}, "LBPolicyJSON")); diff != "" { // maybe make this top level helper if failing a lot
				t.Errorf("validateClusterAndConstructClusterUpdate(%+v) got diff: %v (-got, +want)", test.cluster, diff)
			}
			// Same flow, read update.JSON (will always be set anyway)
			bc := &internalserviceconfig.BalancerConfig{}
			if err := json.Unmarshal(update.LBPolicyJSON, bc); err != nil { // could be nil, but this is a test so we're good? does cmp.Diff handle nils?
				t.Fatalf("failed to unmarshal JSON: %v", err)
			}
			if diff := cmp.Diff(bc, test.wantLBConfig); diff != "" {
				t.Fatalf("update.LBConfig got unexpected output, diff (-got +want): %v", diff)
			} // child policy field must be set
		})
	}
}

// error cases for validation: need to add support for these cases
/*

xDS LB policy registry cannot convert the configuration and returns an error
so same error cases as the xDS LB policy registry

gRPC LB policy registry cannot parse the converted configuration.
(invalid balancer configuration - again, need a mock type here)

move test for that ring hash validation I moved to the balancer to there


Configurations that require more than 16 levels of recursion are considered
invalid and should result in a NACK response.

Should result in a NACK response from the highest layer logically - so test at this layer



*/

func (s) TestValidateClusterWithSecurityConfig_EnvVarOff(t *testing.T) {
	// Turn off the env var protection for client-side security.
	origClientSideSecurityEnvVar := envconfig.XDSClientSideSecurity
	envconfig.XDSClientSideSecurity = false
	defer func() { envconfig.XDSClientSideSecurity = origClientSideSecurityEnvVar }()

	cluster := &v3clusterpb.Cluster{
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
		TransportSocket: &v3corepb.TransportSocket{
			Name: "envoy.transport_sockets.tls",
			ConfigType: &v3corepb.TransportSocket_TypedConfig{
				TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
					CommonTlsContext: &v3tlspb.CommonTlsContext{
						ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContextCertificateProviderInstance{
							ValidationContextCertificateProviderInstance: &v3tlspb.CommonTlsContext_CertificateProviderInstance{
								InstanceName:    "rootInstance",
								CertificateName: "rootCert",
							},
						},
					},
				}),
			},
		},
	}
	wantUpdate := ClusterUpdate{
		ClusterName:     clusterName,
		EDSServiceName:  serviceName,
		LRSServerConfig: ClusterLRSOff,
	}
	gotUpdate, err := validateClusterAndConstructClusterUpdate(cluster)
	if err != nil {
		t.Errorf("validateClusterAndConstructClusterUpdate() failed: %v", err)
	}
	if diff := cmp.Diff(wantUpdate, gotUpdate, cmpopts.IgnoreFields(ClusterUpdate{}, "LBPolicyJSON")); diff != "" {
		t.Errorf("validateClusterAndConstructClusterUpdate() returned unexpected diff (-want, got):\n%s", diff)
	}
}

func (s) TestSecurityConfigFromCommonTLSContextUsingNewFields_ErrorCases(t *testing.T) {
	tests := []struct {
		name    string
		common  *v3tlspb.CommonTlsContext
		server  bool
		wantErr string
	}{
		{
			name: "unsupported-tls_certificates-field-for-identity-certs",
			common: &v3tlspb.CommonTlsContext{
				TlsCertificates: []*v3tlspb.TlsCertificate{
					{CertificateChain: &v3corepb.DataSource{}},
				},
			},
			wantErr: "unsupported field tls_certificates is set in CommonTlsContext message",
		},
		{
			name: "unsupported-tls_certificates_sds_secret_configs-field-for-identity-certs",
			common: &v3tlspb.CommonTlsContext{
				TlsCertificateSdsSecretConfigs: []*v3tlspb.SdsSecretConfig{
					{Name: "sds-secrets-config"},
				},
			},
			wantErr: "unsupported field tls_certificate_sds_secret_configs is set in CommonTlsContext message",
		},
		{
			name: "unsupported-sds-validation-context",
			common: &v3tlspb.CommonTlsContext{
				ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContextSdsSecretConfig{
					ValidationContextSdsSecretConfig: &v3tlspb.SdsSecretConfig{
						Name: "foo-sds-secret",
					},
				},
			},
			wantErr: "validation context contains unexpected type",
		},
		{
			name: "missing-ca_certificate_provider_instance-in-validation-context",
			common: &v3tlspb.CommonTlsContext{
				ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContext{
					ValidationContext: &v3tlspb.CertificateValidationContext{},
				},
			},
			wantErr: "expected field ca_certificate_provider_instance is missing in CommonTlsContext message",
		},
		{
			name: "unsupported-field-verify_certificate_spki-in-validation-context",
			common: &v3tlspb.CommonTlsContext{
				ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContext{
					ValidationContext: &v3tlspb.CertificateValidationContext{
						CaCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
							InstanceName:    "rootPluginInstance",
							CertificateName: "rootCertName",
						},
						VerifyCertificateSpki: []string{"spki"},
					},
				},
			},
			wantErr: "unsupported verify_certificate_spki field in CommonTlsContext message",
		},
		{
			name: "unsupported-field-verify_certificate_hash-in-validation-context",
			common: &v3tlspb.CommonTlsContext{
				ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContext{
					ValidationContext: &v3tlspb.CertificateValidationContext{
						CaCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
							InstanceName:    "rootPluginInstance",
							CertificateName: "rootCertName",
						},
						VerifyCertificateHash: []string{"hash"},
					},
				},
			},
			wantErr: "unsupported verify_certificate_hash field in CommonTlsContext message",
		},
		{
			name: "unsupported-field-require_signed_certificate_timestamp-in-validation-context",
			common: &v3tlspb.CommonTlsContext{
				ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContext{
					ValidationContext: &v3tlspb.CertificateValidationContext{
						CaCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
							InstanceName:    "rootPluginInstance",
							CertificateName: "rootCertName",
						},
						RequireSignedCertificateTimestamp: &wrapperspb.BoolValue{Value: true},
					},
				},
			},
			wantErr: "unsupported require_sugned_ceritificate_timestamp field in CommonTlsContext message",
		},
		{
			name: "unsupported-field-crl-in-validation-context",
			common: &v3tlspb.CommonTlsContext{
				ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContext{
					ValidationContext: &v3tlspb.CertificateValidationContext{
						CaCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
							InstanceName:    "rootPluginInstance",
							CertificateName: "rootCertName",
						},
						Crl: &v3corepb.DataSource{},
					},
				},
			},
			wantErr: "unsupported crl field in CommonTlsContext message",
		},
		{
			name: "unsupported-field-custom_validator_config-in-validation-context",
			common: &v3tlspb.CommonTlsContext{
				ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContext{
					ValidationContext: &v3tlspb.CertificateValidationContext{
						CaCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
							InstanceName:    "rootPluginInstance",
							CertificateName: "rootCertName",
						},
						CustomValidatorConfig: &v3corepb.TypedExtensionConfig{},
					},
				},
			},
			wantErr: "unsupported custom_validator_config field in CommonTlsContext message",
		},
		{
			name: "invalid-match_subject_alt_names-field-in-validation-context",
			common: &v3tlspb.CommonTlsContext{
				ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContext{
					ValidationContext: &v3tlspb.CertificateValidationContext{
						CaCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
							InstanceName:    "rootPluginInstance",
							CertificateName: "rootCertName",
						},
						MatchSubjectAltNames: []*v3matcherpb.StringMatcher{
							{MatchPattern: &v3matcherpb.StringMatcher_Prefix{Prefix: ""}},
						},
					},
				},
			},
			wantErr: "empty prefix is not allowed in StringMatcher",
		},
		{
			name: "unsupported-field-matching-subject-alt-names-in-validation-context-of-server",
			common: &v3tlspb.CommonTlsContext{
				ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContext{
					ValidationContext: &v3tlspb.CertificateValidationContext{
						CaCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
							InstanceName:    "rootPluginInstance",
							CertificateName: "rootCertName",
						},
						MatchSubjectAltNames: []*v3matcherpb.StringMatcher{
							{MatchPattern: &v3matcherpb.StringMatcher_Prefix{Prefix: "sanPrefix"}},
						},
					},
				},
			},
			server:  true,
			wantErr: "match_subject_alt_names field in validation context is not supported on the server",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := securityConfigFromCommonTLSContextUsingNewFields(test.common, test.server)
			if err == nil {
				t.Fatal("securityConfigFromCommonTLSContextUsingNewFields() succeeded when expected to fail")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("securityConfigFromCommonTLSContextUsingNewFields() returned err: %v, wantErr: %v", err, test.wantErr)
			}
		})
	}
}

func (s) TestValidateClusterWithSecurityConfig(t *testing.T) {
	const (
		identityPluginInstance = "identityPluginInstance"
		identityCertName       = "identityCert"
		rootPluginInstance     = "rootPluginInstance"
		rootCertName           = "rootCert"
		clusterName            = "cluster"
		serviceName            = "service"
		sanExact               = "san-exact"
		sanPrefix              = "san-prefix"
		sanSuffix              = "san-suffix"
		sanRegexBad            = "??"
		sanRegexGood           = "san?regex?"
		sanContains            = "san-contains"
	)
	var sanRE = regexp.MustCompile(sanRegexGood)

	tests := []struct {
		name       string
		cluster    *v3clusterpb.Cluster
		wantUpdate ClusterUpdate
		wantErr    bool
	}{
		{
			name: "transport-socket-matches",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocketMatches: []*v3clusterpb.Cluster_TransportSocketMatch{
					{Name: "transport-socket-match-1"},
				},
			},
			wantErr: true,
		},
		{
			name: "transport-socket-unsupported-name",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocket: &v3corepb.TransportSocket{
					Name: "unsupported-foo",
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: &anypb.Any{
							TypeUrl: version.V3UpstreamTLSContextURL,
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "transport-socket-unsupported-typeURL",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocket: &v3corepb.TransportSocket{
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: &anypb.Any{
							TypeUrl: version.V3HTTPConnManagerURL,
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "transport-socket-unsupported-type",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocket: &v3corepb.TransportSocket{
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: &anypb.Any{
							TypeUrl: version.V3UpstreamTLSContextURL,
							Value:   []byte{1, 2, 3, 4},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "transport-socket-unsupported-tls-params-field",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocket: &v3corepb.TransportSocket{
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								TlsParams: &v3tlspb.TlsParameters{},
							},
						}),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "transport-socket-unsupported-custom-handshaker-field",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocket: &v3corepb.TransportSocket{
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								CustomHandshaker: &v3corepb.TypedExtensionConfig{},
							},
						}),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "transport-socket-unsupported-validation-context",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocket: &v3corepb.TransportSocket{
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContextSdsSecretConfig{
									ValidationContextSdsSecretConfig: &v3tlspb.SdsSecretConfig{
										Name: "foo-sds-secret",
									},
								},
							},
						}),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "transport-socket-without-validation-context",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocket: &v3corepb.TransportSocket{
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{},
						}),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "empty-prefix-in-matching-SAN",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocket: &v3corepb.TransportSocket{
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								ValidationContextType: &v3tlspb.CommonTlsContext_CombinedValidationContext{
									CombinedValidationContext: &v3tlspb.CommonTlsContext_CombinedCertificateValidationContext{
										DefaultValidationContext: &v3tlspb.CertificateValidationContext{
											MatchSubjectAltNames: []*v3matcherpb.StringMatcher{
												{MatchPattern: &v3matcherpb.StringMatcher_Prefix{Prefix: ""}},
											},
										},
										ValidationContextCertificateProviderInstance: &v3tlspb.CommonTlsContext_CertificateProviderInstance{
											InstanceName:    rootPluginInstance,
											CertificateName: rootCertName,
										},
									},
								},
							},
						}),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "empty-suffix-in-matching-SAN",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocket: &v3corepb.TransportSocket{
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								ValidationContextType: &v3tlspb.CommonTlsContext_CombinedValidationContext{
									CombinedValidationContext: &v3tlspb.CommonTlsContext_CombinedCertificateValidationContext{
										DefaultValidationContext: &v3tlspb.CertificateValidationContext{
											MatchSubjectAltNames: []*v3matcherpb.StringMatcher{
												{MatchPattern: &v3matcherpb.StringMatcher_Suffix{Suffix: ""}},
											},
										},
										ValidationContextCertificateProviderInstance: &v3tlspb.CommonTlsContext_CertificateProviderInstance{
											InstanceName:    rootPluginInstance,
											CertificateName: rootCertName,
										},
									},
								},
							},
						}),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "empty-contains-in-matching-SAN",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocket: &v3corepb.TransportSocket{
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								ValidationContextType: &v3tlspb.CommonTlsContext_CombinedValidationContext{
									CombinedValidationContext: &v3tlspb.CommonTlsContext_CombinedCertificateValidationContext{
										DefaultValidationContext: &v3tlspb.CertificateValidationContext{
											MatchSubjectAltNames: []*v3matcherpb.StringMatcher{
												{MatchPattern: &v3matcherpb.StringMatcher_Contains{Contains: ""}},
											},
										},
										ValidationContextCertificateProviderInstance: &v3tlspb.CommonTlsContext_CertificateProviderInstance{
											InstanceName:    rootPluginInstance,
											CertificateName: rootCertName,
										},
									},
								},
							},
						}),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid-regex-in-matching-SAN",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocket: &v3corepb.TransportSocket{
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								ValidationContextType: &v3tlspb.CommonTlsContext_CombinedValidationContext{
									CombinedValidationContext: &v3tlspb.CommonTlsContext_CombinedCertificateValidationContext{
										DefaultValidationContext: &v3tlspb.CertificateValidationContext{
											MatchSubjectAltNames: []*v3matcherpb.StringMatcher{
												{MatchPattern: &v3matcherpb.StringMatcher_SafeRegex{SafeRegex: &v3matcherpb.RegexMatcher{Regex: sanRegexBad}}},
											},
										},
										ValidationContextCertificateProviderInstance: &v3tlspb.CommonTlsContext_CertificateProviderInstance{
											InstanceName:    rootPluginInstance,
											CertificateName: rootCertName,
										},
									},
								},
							},
						}),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid-regex-in-matching-SAN-with-new-fields",
			cluster: &v3clusterpb.Cluster{
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
				TransportSocket: &v3corepb.TransportSocket{
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								ValidationContextType: &v3tlspb.CommonTlsContext_CombinedValidationContext{
									CombinedValidationContext: &v3tlspb.CommonTlsContext_CombinedCertificateValidationContext{
										DefaultValidationContext: &v3tlspb.CertificateValidationContext{
											MatchSubjectAltNames: []*v3matcherpb.StringMatcher{
												{MatchPattern: &v3matcherpb.StringMatcher_SafeRegex{SafeRegex: &v3matcherpb.RegexMatcher{Regex: sanRegexBad}}},
											},
											CaCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
												InstanceName:    rootPluginInstance,
												CertificateName: rootCertName,
											},
										},
									},
								},
							},
						}),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "happy-case-with-no-identity-certs-using-deprecated-fields",
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
				TransportSocket: &v3corepb.TransportSocket{
					Name: "envoy.transport_sockets.tls",
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContextCertificateProviderInstance{
									ValidationContextCertificateProviderInstance: &v3tlspb.CommonTlsContext_CertificateProviderInstance{
										InstanceName:    rootPluginInstance,
										CertificateName: rootCertName,
									},
								},
							},
						}),
					},
				},
			},
			wantUpdate: ClusterUpdate{
				ClusterName:     clusterName,
				EDSServiceName:  serviceName,
				LRSServerConfig: ClusterLRSOff,
				SecurityCfg: &SecurityConfig{
					RootInstanceName: rootPluginInstance,
					RootCertName:     rootCertName,
				},
			},
		},
		{
			name: "happy-case-with-no-identity-certs-using-new-fields",
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
				TransportSocket: &v3corepb.TransportSocket{
					Name: "envoy.transport_sockets.tls",
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContext{
									ValidationContext: &v3tlspb.CertificateValidationContext{
										CaCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
											InstanceName:    rootPluginInstance,
											CertificateName: rootCertName,
										},
									},
								},
							},
						}),
					},
				},
			},
			wantUpdate: ClusterUpdate{
				ClusterName:     clusterName,
				EDSServiceName:  serviceName,
				LRSServerConfig: ClusterLRSOff,
				SecurityCfg: &SecurityConfig{
					RootInstanceName: rootPluginInstance,
					RootCertName:     rootCertName,
				},
			},
		},
		{
			name: "happy-case-with-validation-context-provider-instance-using-deprecated-fields",
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
				TransportSocket: &v3corepb.TransportSocket{
					Name: "envoy.transport_sockets.tls",
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								TlsCertificateCertificateProviderInstance: &v3tlspb.CommonTlsContext_CertificateProviderInstance{
									InstanceName:    identityPluginInstance,
									CertificateName: identityCertName,
								},
								ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContextCertificateProviderInstance{
									ValidationContextCertificateProviderInstance: &v3tlspb.CommonTlsContext_CertificateProviderInstance{
										InstanceName:    rootPluginInstance,
										CertificateName: rootCertName,
									},
								},
							},
						}),
					},
				},
			},
			wantUpdate: ClusterUpdate{
				ClusterName:     clusterName,
				EDSServiceName:  serviceName,
				LRSServerConfig: ClusterLRSOff,
				SecurityCfg: &SecurityConfig{
					RootInstanceName:     rootPluginInstance,
					RootCertName:         rootCertName,
					IdentityInstanceName: identityPluginInstance,
					IdentityCertName:     identityCertName,
				},
			},
		},
		{
			name: "happy-case-with-validation-context-provider-instance-using-new-fields",
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
				TransportSocket: &v3corepb.TransportSocket{
					Name: "envoy.transport_sockets.tls",
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								TlsCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
									InstanceName:    identityPluginInstance,
									CertificateName: identityCertName,
								},
								ValidationContextType: &v3tlspb.CommonTlsContext_ValidationContext{
									ValidationContext: &v3tlspb.CertificateValidationContext{
										CaCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
											InstanceName:    rootPluginInstance,
											CertificateName: rootCertName,
										},
									},
								},
							},
						}),
					},
				},
			},
			wantUpdate: ClusterUpdate{
				ClusterName:     clusterName,
				EDSServiceName:  serviceName,
				LRSServerConfig: ClusterLRSOff,
				SecurityCfg: &SecurityConfig{
					RootInstanceName:     rootPluginInstance,
					RootCertName:         rootCertName,
					IdentityInstanceName: identityPluginInstance,
					IdentityCertName:     identityCertName,
				},
			},
		},
		{
			name: "happy-case-with-combined-validation-context-using-deprecated-fields",
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
				TransportSocket: &v3corepb.TransportSocket{
					Name: "envoy.transport_sockets.tls",
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								TlsCertificateCertificateProviderInstance: &v3tlspb.CommonTlsContext_CertificateProviderInstance{
									InstanceName:    identityPluginInstance,
									CertificateName: identityCertName,
								},
								ValidationContextType: &v3tlspb.CommonTlsContext_CombinedValidationContext{
									CombinedValidationContext: &v3tlspb.CommonTlsContext_CombinedCertificateValidationContext{
										DefaultValidationContext: &v3tlspb.CertificateValidationContext{
											MatchSubjectAltNames: []*v3matcherpb.StringMatcher{
												{
													MatchPattern: &v3matcherpb.StringMatcher_Exact{Exact: sanExact},
													IgnoreCase:   true,
												},
												{MatchPattern: &v3matcherpb.StringMatcher_Prefix{Prefix: sanPrefix}},
												{MatchPattern: &v3matcherpb.StringMatcher_Suffix{Suffix: sanSuffix}},
												{MatchPattern: &v3matcherpb.StringMatcher_SafeRegex{SafeRegex: &v3matcherpb.RegexMatcher{Regex: sanRegexGood}}},
												{MatchPattern: &v3matcherpb.StringMatcher_Contains{Contains: sanContains}},
											},
										},
										ValidationContextCertificateProviderInstance: &v3tlspb.CommonTlsContext_CertificateProviderInstance{
											InstanceName:    rootPluginInstance,
											CertificateName: rootCertName,
										},
									},
								},
							},
						}),
					},
				},
			},
			wantUpdate: ClusterUpdate{
				ClusterName:     clusterName,
				EDSServiceName:  serviceName,
				LRSServerConfig: ClusterLRSOff,
				SecurityCfg: &SecurityConfig{
					RootInstanceName:     rootPluginInstance,
					RootCertName:         rootCertName,
					IdentityInstanceName: identityPluginInstance,
					IdentityCertName:     identityCertName,
					SubjectAltNameMatchers: []matcher.StringMatcher{
						matcher.StringMatcherForTesting(newStringP(sanExact), nil, nil, nil, nil, true),
						matcher.StringMatcherForTesting(nil, newStringP(sanPrefix), nil, nil, nil, false),
						matcher.StringMatcherForTesting(nil, nil, newStringP(sanSuffix), nil, nil, false),
						matcher.StringMatcherForTesting(nil, nil, nil, nil, sanRE, false),
						matcher.StringMatcherForTesting(nil, nil, nil, newStringP(sanContains), nil, false),
					},
				},
			},
		},
		{
			name: "happy-case-with-combined-validation-context-using-new-fields",
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
				TransportSocket: &v3corepb.TransportSocket{
					Name: "envoy.transport_sockets.tls",
					ConfigType: &v3corepb.TransportSocket_TypedConfig{
						TypedConfig: testutils.MarshalAny(&v3tlspb.UpstreamTlsContext{
							CommonTlsContext: &v3tlspb.CommonTlsContext{
								TlsCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
									InstanceName:    identityPluginInstance,
									CertificateName: identityCertName,
								},
								ValidationContextType: &v3tlspb.CommonTlsContext_CombinedValidationContext{
									CombinedValidationContext: &v3tlspb.CommonTlsContext_CombinedCertificateValidationContext{
										DefaultValidationContext: &v3tlspb.CertificateValidationContext{
											MatchSubjectAltNames: []*v3matcherpb.StringMatcher{
												{
													MatchPattern: &v3matcherpb.StringMatcher_Exact{Exact: sanExact},
													IgnoreCase:   true,
												},
												{MatchPattern: &v3matcherpb.StringMatcher_Prefix{Prefix: sanPrefix}},
												{MatchPattern: &v3matcherpb.StringMatcher_Suffix{Suffix: sanSuffix}},
												{MatchPattern: &v3matcherpb.StringMatcher_SafeRegex{SafeRegex: &v3matcherpb.RegexMatcher{Regex: sanRegexGood}}},
												{MatchPattern: &v3matcherpb.StringMatcher_Contains{Contains: sanContains}},
											},
											CaCertificateProviderInstance: &v3tlspb.CertificateProviderPluginInstance{
												InstanceName:    rootPluginInstance,
												CertificateName: rootCertName,
											},
										},
									},
								},
							},
						}),
					},
				},
			},
			wantUpdate: ClusterUpdate{
				ClusterName:     clusterName,
				EDSServiceName:  serviceName,
				LRSServerConfig: ClusterLRSOff,
				SecurityCfg: &SecurityConfig{
					RootInstanceName:     rootPluginInstance,
					RootCertName:         rootCertName,
					IdentityInstanceName: identityPluginInstance,
					IdentityCertName:     identityCertName,
					SubjectAltNameMatchers: []matcher.StringMatcher{
						matcher.StringMatcherForTesting(newStringP(sanExact), nil, nil, nil, nil, true),
						matcher.StringMatcherForTesting(nil, newStringP(sanPrefix), nil, nil, nil, false),
						matcher.StringMatcherForTesting(nil, nil, newStringP(sanSuffix), nil, nil, false),
						matcher.StringMatcherForTesting(nil, nil, nil, nil, sanRE, false),
						matcher.StringMatcherForTesting(nil, nil, nil, newStringP(sanContains), nil, false),
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			update, err := validateClusterAndConstructClusterUpdate(test.cluster)
			if (err != nil) != test.wantErr {
				t.Errorf("validateClusterAndConstructClusterUpdate() returned err %v wantErr %v)", err, test.wantErr)
			}
			if diff := cmp.Diff(test.wantUpdate, update, cmpopts.EquateEmpty(), cmp.AllowUnexported(regexp.Regexp{}), cmpopts.IgnoreFields(ClusterUpdate{}, "LBPolicyJSON")); diff != "" {
				t.Errorf("validateClusterAndConstructClusterUpdate() returned unexpected diff (-want, +got):\n%s", diff)
			}
		})
	}
}

func (s) TestUnmarshalCluster(t *testing.T) {
	const (
		v3ClusterName = "v3clusterName"
		v3Service     = "v3Service"
	)
	var (
		v3ClusterAny = testutils.MarshalAny(&v3clusterpb.Cluster{
			Name:                 v3ClusterName,
			ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
			EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
				EdsConfig: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
						Ads: &v3corepb.AggregatedConfigSource{},
					},
				},
				ServiceName: v3Service,
			},
			LbPolicy: v3clusterpb.Cluster_ROUND_ROBIN,
			LrsServer: &v3corepb.ConfigSource{
				ConfigSourceSpecifier: &v3corepb.ConfigSource_Self{
					Self: &v3corepb.SelfConfigSource{},
				},
			},
		})

		v3ClusterAnyWithEDSConfigSourceSelf = testutils.MarshalAny(&v3clusterpb.Cluster{
			Name:                 v3ClusterName,
			ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
			EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
				EdsConfig: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Self{},
				},
				ServiceName: v3Service,
			},
			LbPolicy: v3clusterpb.Cluster_ROUND_ROBIN,
			LrsServer: &v3corepb.ConfigSource{
				ConfigSourceSpecifier: &v3corepb.ConfigSource_Self{
					Self: &v3corepb.SelfConfigSource{},
				},
			},
		})
	)

	tests := []struct {
		name       string
		resource   *anypb.Any
		wantName   string
		wantUpdate ClusterUpdate
		wantErr    bool
	}{
		{
			name:     "non-cluster resource type",
			resource: &anypb.Any{TypeUrl: version.V3HTTPConnManagerURL},
			wantErr:  true,
		},
		{
			name: "badly marshaled cluster resource",
			resource: &anypb.Any{
				TypeUrl: version.V3ClusterURL,
				Value:   []byte{1, 2, 3, 4},
			},
			wantErr: true,
		},
		{
			name: "bad cluster resource",
			resource: testutils.MarshalAny(&v3clusterpb.Cluster{
				Name:                 "test",
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_STATIC},
			}),
			wantName: "test",
			wantErr:  true,
		},
		{
			name: "cluster resource with non-self lrs_server field",
			resource: testutils.MarshalAny(&v3clusterpb.Cluster{
				Name:                 "test",
				ClusterDiscoveryType: &v3clusterpb.Cluster_Type{Type: v3clusterpb.Cluster_EDS},
				EdsClusterConfig: &v3clusterpb.Cluster_EdsClusterConfig{
					EdsConfig: &v3corepb.ConfigSource{
						ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
							Ads: &v3corepb.AggregatedConfigSource{},
						},
					},
					ServiceName: v3Service,
				},
				LbPolicy: v3clusterpb.Cluster_ROUND_ROBIN,
				LrsServer: &v3corepb.ConfigSource{
					ConfigSourceSpecifier: &v3corepb.ConfigSource_Ads{
						Ads: &v3corepb.AggregatedConfigSource{},
					},
				},
			}),
			wantName: "test",
			wantErr:  true,
		},
		{
			name:     "v3 cluster",
			resource: v3ClusterAny,
			wantName: v3ClusterName,
			wantUpdate: ClusterUpdate{
				ClusterName:    v3ClusterName,
				EDSServiceName: v3Service, LRSServerConfig: ClusterLRSServerSelf,
				Raw: v3ClusterAny,
			},
		},
		{
			name:     "v3 cluster wrapped",
			resource: testutils.MarshalAny(&v3discoverypb.Resource{Resource: v3ClusterAny}),
			wantName: v3ClusterName,
			wantUpdate: ClusterUpdate{
				ClusterName:    v3ClusterName,
				EDSServiceName: v3Service, LRSServerConfig: ClusterLRSServerSelf,
				Raw: v3ClusterAny,
			},
		},
		{
			name:     "v3 cluster with EDS config source self",
			resource: v3ClusterAnyWithEDSConfigSourceSelf,
			wantName: v3ClusterName,
			wantUpdate: ClusterUpdate{
				ClusterName:    v3ClusterName,
				EDSServiceName: v3Service, LRSServerConfig: ClusterLRSServerSelf,
				Raw: v3ClusterAnyWithEDSConfigSourceSelf,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name, update, err := unmarshalClusterResource(test.resource)
			if (err != nil) != test.wantErr {
				t.Fatalf("unmarshalClusterResource(%s), got err: %v, wantErr: %v", pretty.ToJSON(test.resource), err, test.wantErr)
			}
			if name != test.wantName {
				t.Errorf("unmarshalClusterResource(%s), got name: %s, want: %s", pretty.ToJSON(test.resource), name, test.wantName)
			}
			if diff := cmp.Diff(update, test.wantUpdate, cmpOpts, cmpopts.IgnoreFields(ClusterUpdate{}, "LBPolicyJSON")); diff != "" {
				t.Errorf("unmarshalClusterResource(%s), got unexpected update, diff (-got +want): %v", pretty.ToJSON(test.resource), diff)
			}
		})
	}
}

func (s) TestValidateClusterWithOutlierDetection(t *testing.T) {
	odToClusterProto := func(od *v3clusterpb.OutlierDetection) *v3clusterpb.Cluster {
		// Cluster parsing doesn't fail with respect to fields orthogonal to
		// outlier detection.
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
	odToClusterUpdate := func(od *OutlierDetection) ClusterUpdate {
		return ClusterUpdate{
			ClusterName:      clusterName,
			LRSServerConfig:  ClusterLRSOff,
			OutlierDetection: od,
		}
	}

	tests := []struct {
		name       string
		cluster    *v3clusterpb.Cluster
		wantUpdate ClusterUpdate
		wantErr    bool
	}{
		{
			name: "successful-case-all-defaults",
			// Outlier detection proto is present without any fields specified,
			// so should trigger all default values in the update.
			cluster: odToClusterProto(&v3clusterpb.OutlierDetection{}),
			wantUpdate: odToClusterUpdate(&OutlierDetection{
				Interval:                       10 * time.Second,
				BaseEjectionTime:               30 * time.Second,
				MaxEjectionTime:                300 * time.Second,
				MaxEjectionPercent:             10,
				SuccessRateStdevFactor:         1900,
				EnforcingSuccessRate:           100,
				SuccessRateMinimumHosts:        5,
				SuccessRateRequestVolume:       100,
				FailurePercentageThreshold:     85,
				EnforcingFailurePercentage:     0,
				FailurePercentageMinimumHosts:  5,
				FailurePercentageRequestVolume: 50,
			}),
		},
		{
			name: "successful-case-all-fields-configured-and-valid",
			cluster: odToClusterProto(&v3clusterpb.OutlierDetection{
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
			wantUpdate: odToClusterUpdate(&OutlierDetection{
				Interval:                       time.Second,
				BaseEjectionTime:               time.Second * 2,
				MaxEjectionTime:                time.Second * 3,
				MaxEjectionPercent:             1,
				SuccessRateStdevFactor:         2,
				EnforcingSuccessRate:           3,
				SuccessRateMinimumHosts:        4,
				SuccessRateRequestVolume:       5,
				FailurePercentageThreshold:     6,
				EnforcingFailurePercentage:     7,
				FailurePercentageMinimumHosts:  8,
				FailurePercentageRequestVolume: 9,
			}),
		},
		{
			name:    "interval-is-negative",
			cluster: odToClusterProto(&v3clusterpb.OutlierDetection{Interval: &durationpb.Duration{Seconds: -10}}),
			wantErr: true,
		},
		{
			name:    "interval-overflows",
			cluster: odToClusterProto(&v3clusterpb.OutlierDetection{Interval: &durationpb.Duration{Seconds: 315576000001}}),
			wantErr: true,
		},
		{
			name:    "base-ejection-time-is-negative",
			cluster: odToClusterProto(&v3clusterpb.OutlierDetection{BaseEjectionTime: &durationpb.Duration{Seconds: -10}}),
			wantErr: true,
		},
		{
			name:    "base-ejection-time-overflows",
			cluster: odToClusterProto(&v3clusterpb.OutlierDetection{BaseEjectionTime: &durationpb.Duration{Seconds: 315576000001}}),
			wantErr: true,
		},
		{
			name:    "max-ejection-time-is-negative",
			cluster: odToClusterProto(&v3clusterpb.OutlierDetection{MaxEjectionTime: &durationpb.Duration{Seconds: -10}}),
			wantErr: true,
		},
		{
			name:    "max-ejection-time-overflows",
			cluster: odToClusterProto(&v3clusterpb.OutlierDetection{MaxEjectionTime: &durationpb.Duration{Seconds: 315576000001}}),
			wantErr: true,
		},
		{
			name:    "max-ejection-percent-is-greater-than-100",
			cluster: odToClusterProto(&v3clusterpb.OutlierDetection{MaxEjectionPercent: &wrapperspb.UInt32Value{Value: 150}}),
			wantErr: true,
		},
		{
			name:    "enforcing-success-rate-is-greater-than-100",
			cluster: odToClusterProto(&v3clusterpb.OutlierDetection{EnforcingSuccessRate: &wrapperspb.UInt32Value{Value: 150}}),
			wantErr: true,
		},
		{
			name:    "failure-percentage-threshold-is-greater-than-100",
			cluster: odToClusterProto(&v3clusterpb.OutlierDetection{FailurePercentageThreshold: &wrapperspb.UInt32Value{Value: 150}}),
			wantErr: true,
		},
		{
			name:    "enforcing-failure-percentage-is-greater-than-100",
			cluster: odToClusterProto(&v3clusterpb.OutlierDetection{EnforcingFailurePercentage: &wrapperspb.UInt32Value{Value: 150}}),
			wantErr: true,
		},
		// A Outlier Detection proto not present should lead to a nil
		// OutlierDetection field in the ClusterUpdate, which is implicitly
		// tested in every other test in this file.
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			update, err := validateClusterAndConstructClusterUpdate(test.cluster)
			if (err != nil) != test.wantErr {
				t.Errorf("validateClusterAndConstructClusterUpdate() returned err %v wantErr %v)", err, test.wantErr)
			}
			if diff := cmp.Diff(test.wantUpdate, update, cmpopts.EquateEmpty(), cmpopts.IgnoreFields(ClusterUpdate{}, "LBPolicyJSON")); diff != "" {
				t.Errorf("validateClusterAndConstructClusterUpdate() returned unexpected diff (-want, +got):\n%s", diff)
			}
		})
	}
}

// json doesn't match 1:1 (serialization format like proto)

// unmarshal (takes away the non determinism of the exact bytes of json)

// unmarshaling produces same output - the expected output from the unit test -
// but who knows if this is "correct" until e2e
// put these explanations into a comment


// new system:
// env var set and new fields set
// new field sent to xDS Config registry, emission of json and then internal service config Balancer Config so logically equivalent

// so old system:
// ring hash -> ring hash JSON (do this test first)
// nil ring hash config -> wrr locality with round robin as child (pull internalbalancerconfig declaration of this from the xds registry test)


// read the possibilities of error case (still need to add)
// error case 1 - test case 1, test case 2
// error case 2 - test case 1, test case 2
/*
The CDS resource is rejected and a NACK returned to the control plane in either of the following cases:

xDS LB policy registry cannot convert the configuration and returns an error (so set up the same error conditions you had in xDS LB registry except through full proto but you just pass it in directly right so I think it's the same proto - perhaps Least Request?)
gRPC LB policy registry cannot parse the converted configuration.
	// how to trigger bad json being created from proto?
	// unregistered balancer - should this ever happen because we support all balancers emit config this should never happen
	// custom lb with bad config?
	// custom lb type that isn't there
	// ^^^ because registry simply prepares config based on name and typed struct doesn't validate name or anything
*/

// ^^^ the tests above were testing the old flow ring hash configured on old field and round robin (just not ring hash)?

// could test fallback, but that's implicitly tested in scaling up the success cases
// ^^^ leave those tests in since we will continue to support them over time

// So same test but different
// If the load_balancing_policy field is provided, it will be used instead of
// the lb_policy and lb_config fields. If load_balancing_policy is not provided,
// full backward compatibility with the old fields will be maintained.

// Need to test successful management server updates with new fields
// map the extra layer to what gets passed into proto registry
// emissions and input same, just mapped to another layer
// this should always pass when expected, fail if prepares a bad config as below
func (s) TestLoadBalancingPolicyField(t *testing.T) {
	oldCustomLBSupport := envconfig.XDSCustomLBPolicy
	envconfig.XDSCustomLBPolicy = true
	defer func() {
		envconfig.XDSCustomLBPolicy = oldCustomLBSupport
	}()
	// Don't need to test cluster update? wait can I add it to that
}

// new field in the cds update struct
// the migration really only touches this file right
// ignore the new field with equate optionss, keep the old field validations, which is literally just that one test

func (s) TestNewLBPolicyFieldCorrectlyFails(t *testing.T) {
	// Do I need to turn on ring hash?
	// Custom LB Env Var set to true
	// proto configuration set through the new field
	// Does importing the registry import all the types through transitive dependencies?
	// If so, that's good don't import

	// Same thing here, new field failjres

}
