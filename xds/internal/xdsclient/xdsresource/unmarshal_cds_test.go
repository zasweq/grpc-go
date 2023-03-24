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
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/pretty"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/internal/xds/matcher"
	"google.golang.org/grpc/xds/internal/balancer/ringhash"
	"google.golang.org/grpc/xds/internal/balancer/wrrlocality"
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
		// ^^^ xds client assertions should hold true

		// vvv validations in the balancer, but they still shoulddd cause nack
		// even though we moved it at some layer, as xds clients job is to
		// validate the prepared configuration. So perhaps we need to move it to
		// some layer that represents the "validate" /nack thing of this.
		{
			name: "ring-hash-min-bound-greater-than-max", // example of an error condition - copy this?
			cluster: &v3clusterpb.Cluster{
				LbPolicy: v3clusterpb.Cluster_RING_HASH,
				LbConfig: &v3clusterpb.Cluster_RingHashLbConfig_{
					RingHashLbConfig: &v3clusterpb.Cluster_RingHashLbConfig{
						MinimumRingSize: wrapperspb.UInt64(100),
						MaximumRingSize: wrapperspb.UInt64(10),
					},
				},
			},
			wantUpdate: emptyUpdate,
			wantErr:    true,
		},
		{
			name: "ring-hash-min-bound-greater-than-upper-bound",
			cluster: &v3clusterpb.Cluster{
				LbPolicy: v3clusterpb.Cluster_RING_HASH,
				LbConfig: &v3clusterpb.Cluster_RingHashLbConfig_{
					RingHashLbConfig: &v3clusterpb.Cluster_RingHashLbConfig{
						MinimumRingSize: wrapperspb.UInt64(ringHashSizeUpperBound + 1),
					},
				},
			},
			wantUpdate: emptyUpdate,
			wantErr:    true,
		},
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

func (s) TestValidateCluster_Success(t *testing.T) {
	tests := []struct {
		name       string
		cluster    *v3clusterpb.Cluster
		wantUpdate ClusterUpdate
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
			// and got:
			// proto -> balancer config I prepare -> (marshal) -> json

			// (in test) json.Unmarshal (takes away the non determinism) into balancer config

			// easwars point: declare the want as a balancer config
			wantUpdate: ClusterUpdate{


				// irrregardless of what the struct you use to marshal

				// explicitly just set/populated in xds_wrr_locality_experimental
				// create a rh config and stick it as value of internalserviceconfig.BalancerConfig
				// json ends up being fmt.Sprintf(`[{%q: %s}]`, bc.Name, c)

				// Thus, unmarshal into a balancer config and declare name and config
				// config has to be the correct type - i.e. a round robin config
				// or xds_wrr_locality_config - should I add this?

				// balancer.Get will get the type and builder and parse config - so I think you need
				// to a. add xds_wrr_locality_config and also ParseConfig() method (then add tests for that) as part of this PR
				// and b. for the custom lb proto, need to declare a mock builder and balancer
				// that the any proto will point to



				//

				// wb testing converters?

				// will test validations - but should be a valid config regardless


				// represents emissions anyway...and that is how it will be used


				ClusterName: clusterName, EDSServiceName: serviceName, LRSServerConfig: ClusterLRSServerSelf,
				// Oh right because it's nil otherwise...so this is only place ring hash json
				//

				// got (initial -> json) 1 2 are done

				// for want we have 1 2 3 (need to prepare on both codepaths) and stick data at 1 2 or 3
				// into the want

				// 3 step process for each got and want

				// all the other places it's nil - weighted_target_experimental: round robin child
				// switch this to ring hash json (through preparation (or struct)) what do I put here for
				// type now that it's a two step process

				// Yeah and def the emission of JSON from the client should be valid - one of it's responsibiities is to validate.

				// for all, because also needed for round robin emissions so no check here
				// Thus if err := json.Unmarshal(balancer config object); err != nil {
				//   "emitted json from the xds client should be valid configuration: %v", err
				// }
				LBPolicy: &ClusterLBPolicyRingHash{MinimumRingSize: defaultRingHashMinSize, MaximumRingSize: defaultRingHashMaxSize},

				// unmarshal and compare those with a different struct
				// declare balancer config inline and compare it to that json.Unmarshal from emitted


				// What is the type of go struct we want to unmarshal the arbitrary json string into?
				// internalserviceconfig.BalancerConfig?

				// marshal it into same thing you prepare?
				// is there an overarching go struct that handles and encapsualtes all the possibilites here?

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
			// LBPolicy above - ring hash, not set to nil
			wantLBPolicy2: internalserviceconfig.BalancerConfig{
				Name: "ring_hash", // is it _experimental?
				Config: ringhash.LBConfig{
					MinRingSize: 10,
					MaxRingSize: 100,
				},
			},

			// this is for the normal case where LBPolicy used to be nil - put this everywhere it used to be nil
			wantLBPolicy: internalserviceconfig.BalancerConfig{
				Name: "xds_wrr_locality_experimental", // gets this type, unmarshals/verifies config
				// ChildPolicy can be arbitrary type
				Config: wrrlocality.LBConfig{
					// what is the round robin policy type? Can I just declare it as such
					ChildPolicy: internalserviceconfig.BalancerConfig{
						Name: "round_robin", // in the unmarshaling/verification phase above, calls into wrrlocality ParseConfig(), gets this name, verifies
						// No configuration - an empty json object is returned, just leave as such? this will be used as below too.
						/*Config: roundrobin.LBConfig{

						},*/
					},
				},
			},

			// for the wants: declare a internalserviceconfig.BalancerConfig,
			// than MarshalJSON into that struct this will trigger it to marshal
			// that type


			// I think it's better to do this first to see if this flow works well with the unmarshaling...
			// for the converter (separate test):
			// what's the api you're testing at?
			// ring hash same thing as above

			// round robin - an empty json object is returned, perhaps as above, excpet don't have a config

			// wrr_locality - child rr - perhaps point it to round robin and also any struct/json

			// custom_lb lmao

			// wrr_locality with child as the custom_lb ^^^ (need to declare a mock balancer builder in test to pull it off what the any corresponds to)

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
			if diff := cmp.Diff(update, test.wantUpdate, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("validateClusterAndConstructClusterUpdate(%+v) got diff: %v (-got, +want)", test.cluster, diff)
			}
		})
	}
}

// error cases for validation:
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
	if diff := cmp.Diff(wantUpdate, gotUpdate); diff != "" {
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
			if diff := cmp.Diff(test.wantUpdate, update, cmpopts.EquateEmpty(), cmp.AllowUnexported(regexp.Regexp{})); diff != "" {
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
			if diff := cmp.Diff(update, test.wantUpdate, cmpOpts); diff != "" {
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
			LbPolicy:         v3clusterpb.Cluster_ROUND_ROBIN, // proto is the branch conditional, we want to set the new field
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
			if diff := cmp.Diff(test.wantUpdate, update, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("validateClusterAndConstructClusterUpdate() returned unexpected diff (-want, +got):\n%s", diff)
			}
		})
	}
}

// this will break all the tests even outside this package...just fix the ones in this package

// validating emitted = inline (created)

// json doesn't match 1:1 (serialization format like proto)

// unmarshal (takes away the non determinism of the exact bytes of json)

// unmarshaling produces same output - the expected output from the unit test -
// but who knows if this is "correct" until e2e


// "deserialize"
// emitted json from client
// json -> struct


// Note: the marshaling to produce this json has knobs plumbed in through xDS protos,
// either set these knobs inline in tests or set to be configurable
// inline declared json want - "valid json string and you can always unmarshal it"
// json -> struct

// perhaps a helper to plumb in json and build it out with arbitrary child type?
// var for json representation of wrr locality: round robin

// var for ringhash json configuration


// prepares same thing as registry?


// emissions from validateClusterAndConstructClusterUpdate - all these switch to JSON
// switch all tests to have the internal struct (any others in codebase)?
// Fix tests already there: internal struct inline config -> json.RawMessage (wrr_locality: round_robin for nil), ringhash config in json for ringhash

// converter tests orthogonal - test all at that layer to but test a single
// converter called from validateCluster - basic

// (newField || field) ->    lbConfig switch to JSON here
// proto               ->           internal struct

// the population of the JSON is where we want to call into converter library

// ^^^ oh test all with env var set to true


func helper() json.RawMessage {
	// knobs for this first thing
	bc := internalserviceconfig.BalancerConfig{
		Name: roundrobin.Name,
	}

	// rr doesn't need a knob is a constant
	rrLBCfgJSON, err := json.Marshal(bc)
	if err != nil { // Shouldn't happen, prepared now.
		// Fail test...?
	}
	// test created json - rr
	lbCfgJSON := []byte(fmt.Sprintf(`[{%q: %s}]`, "xds_wrr_locality_experimental", rrLBCfgJSON))


	// test created json - ring hash
	// knob from management server are the min and max size in proto


	// need knobs on minSize and maxSize
	rhLBCfg := ringhash.LBConfig{
		MinRingSize: /*knob for min ring size here*/,
		MaxRingSize: /*knob for max ring size here*/,
	}

	bc := internalserviceconfig.BalancerConfig{
		Name: "ring_hash_experimental",
		Config: rhLBCfg,
	}

	// preparing it same way will validate, same with cluster specifier test
	// this is to get rid of the inline json needed to prepare
	rhLBCfgJSON, err := json.Marshal(bc)
	if err != nil {
		// Fail test...?
	}


	// emitted from the client:
	// []byte -> marshal into struct? Balancer config?

	// marshal []byte into a struct just like the other side
	// if you marshal into go struct and take away the non determinism for
	// the byte preparation, what does it marshal into


	// the rest - the primitives represented by the root of interface{}
	// bool for json booleans
	// float64 for json numbers
	// string for json strings

	// so the config layering
	// {rr_hash: primitive primitive primitive}
	// map[
	//      "rr_hash": {}
	// ]

	// but if you unmarshal and compare this is a black box
	// and if you reflect.DeepEqual on it with cmp.Diff it's also a black box

	// nil for null
	// []interface{} json array
	// map[string]interface{} json object
	// unmarshal stores json key value pairs into the map?
	// The map's key type must either be any string type, an integer, implement
	// json.Unmarshaler, or implement encoding.TextUnmarshaler.
	json.Unmarshal()

	// honestly just treat this as a black box,
	// unmarshal and cmp.Diff
	// you can a. see if it works and b. see what the map output is

}
