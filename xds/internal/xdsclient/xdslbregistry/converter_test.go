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

package xdslbregistry

import (
	"encoding/json"
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/serviceconfig"
	"strings"
	"testing"

	v1 "github.com/cncf/xds/go/udpa/type/v1"
	v3 "github.com/cncf/xds/go/xds/type/v3"
	v3clusterpb "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	v3corepb "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	least_requestv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/least_request/v3"
	v3ringhashpb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/ring_hash/v3"
	v3roundrobinpb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/round_robin/v3"
	wrr_localityv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/wrr_locality/v3"
	"github.com/golang/protobuf/proto"
	structpb "github.com/golang/protobuf/ptypes/struct"
	"github.com/google/go-cmp/cmp"
	_ "google.golang.org/grpc/balancer/roundrobin"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/xds/internal/balancer/ringhash"
	"google.golang.org/grpc/xds/internal/balancer/wrrlocality"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// I feel like I need to scale up these tests

// run this test within this package first, then continue

// Error details - with details proto message attached to message
// details from server

// ConvertToServiceConfig(policy *v3clusterpb.LoadBalancingPolicy, depth int) (json.RawMessage, error)

// same concept of unmarshaling into a balancer config

type customLBConfig struct {
	serviceconfig.LoadBalancingConfig
} // just marshals into empty json - if you put a field here could use it for equality check in unmarshal JSON...

func (s) TestConvertToServiceConfigSuccess(t *testing.T) {

	const customLBPolicyName = "myorg.MyCustomLeastRequestPolicy"
	stub.Register(customLBPolicyName, stub.BalancerFuncs{
		ParseConfig: func(json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
			return customLBConfig{}, nil
		},
	})

	tests := []struct {
		name string
		policy *v3clusterpb.LoadBalancingPolicy
		wantConfig *internalserviceconfig.BalancerConfig // json marshaled into this - see if flow works
	}{
		// ring hash same thing as above
		{
			name: "ring_hash",
			policy: &v3clusterpb.LoadBalancingPolicy{
				Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
					{
						TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
							Name: "what is this used for?", // This typed_config field is used to determine both the name of a policy, do we need validations on this or is this used somewhere?
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
			wantConfig: &internalserviceconfig.BalancerConfig{
				Name: "ring_hash_experimental",
				Config: &ringhash.LBConfig{
					MinRingSize: 10,
					MaxRingSize: 100,
				},
			}, // moved ring hash validation you can check in client unmarshaling tests
		},
		{
			name: "round_robin",
			policy: &v3clusterpb.LoadBalancingPolicy{
				Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
					{
						TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
							// I thinnnnkkkkk this is just ignored...
							Name: "what is this used for?", // This typed_config field is used to determine both the name of a policy, do we need validations on this or is this used somewhere?
							TypedConfig: testutils.MarshalAny(&v3roundrobinpb.RoundRobin{}),
						},
					},

					// should I test that the next node in list doesn't get hit, I don't think I should, maybe just like test to the end of recursion...
				},
			},
			wantConfig: &internalserviceconfig.BalancerConfig{
				Name: "round_robin",
			}, // json is {round robin: {}} - what does that unmarshal into, I think
		},
		// custom_lb:
		// custom lb configured through xds.type.v3.TypedStruct
		{
			name: "custom_lb_type_v3_struct",
			policy: &v3clusterpb.LoadBalancingPolicy{
				Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
					{
						TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
							// I thinnnnkkkkk this is just ignored...
							Name: "what is this used for?", // This typed_config field is used to determine both the name of a policy, do we need validations on this or is this used somewhere?

							// The gRPC policy name will be the "type name" part
							// of the value of the type_url field in the
							// TypedStruct. We get this by using the part after
							// the last / character.

							// I also need to mock this balancer registered -
							// wait this doesn't need to be part of the registry
							// right wait does this need to validate?

							// nooooo...all this does is prepare the

							// "config1": json

							// and maybe an error case that the balancer isn't found>

							// The Struct contained in the TypedStruct will be
							// returned as-is as the configuration JSON object.

							TypedConfig: testutils.MarshalAny(&v3.TypedStruct{
								TypeUrl: "type.googleapis.com/myorg.MyCustomLeastRequestPolicy",
								Value: &structpb.Struct{}, // probably just marshals into empty json, does go struct -> json
							}),
							// {"myorg.MyCustomLeastRequestPolicy": {}}
						},
					},
					// should I test that the next node in list doesn't get hit, I don't think I should, not found case is tested, but maybe I should test just to be sure
				},
			},
			wantConfig: &internalserviceconfig.BalancerConfig{
				Name: "myorg.MyCustomLeastRequestPolicy",
				Config: customLBConfig{},
			},
		},
		// for the name , use the type name part of the value of the type_url field, after the last / character - so rewrite the names
		// marshals struct value into json
		// for ^^^ and vvv, prepares a json name: value

		// custom lb configured through udpa.type.v1.TypedStruct
		{
			name: "custom_lb_type_v1_struct",
			policy: &v3clusterpb.LoadBalancingPolicy{
				Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
					{
						TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
							Name: "not supported",
							TypedConfig: testutils.MarshalAny(&v1.TypedStruct{
								TypeUrl: "type.googleapis.com/myorg.MyCustomLeastRequestPolicy",
								Value: &structpb.Struct{},
							}),
						},
					},
				},
			},
			wantConfig: &internalserviceconfig.BalancerConfig{
				Name: "myorg.MyCustomLeastRequestPolicy", // I think if we use internal balancer config type this needs to be registered otherwise validation will fail
				// leave empty (from {}? empty struct) and see how it works...if not fix later
				// config: what you return from parse config here
				Config: customLBConfig{},
			},
		}, // this gets prepares it? validated in client (including presence?) yes presence validated in client
		// this is howwww it should be used best practice as per Mark's chat vvv
		// wrr_locality with child as the custom_lb ^^^ (need to declare a mock balancer builder in test to pull it off what the any corresponds to)
		// wrr_locality - child rr - perhaps point it to round robin and also any struct/json
		{
			name: "wrr_locality_child_round_robin",
			policy: &v3clusterpb.LoadBalancingPolicy{
				Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
					{
						TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
							Name: "what is this used for?",
							TypedConfig: wrrLocalityAny(&v3roundrobinpb.RoundRobin{}),
						},
					},
				},
			},
			wantConfig: &internalserviceconfig.BalancerConfig{
				Name: "xds_wrr_locality_experimental",
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &internalserviceconfig.BalancerConfig{
						Name: "round_robin",
					},
				},
			},
		},
		// custom lb configured as a child of wrr_locality experimental - this is the normal case of how custom lbs are deployed
		{
			name: "wrr_locality_child_custom_lb_type_v3_struct", // ^^^ dependent on the v3 type struct thingy correctly working, as this uses as a child
			policy: &v3clusterpb.LoadBalancingPolicy{
				Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
					{
						TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
							Name: "what is this used for?",
							TypedConfig: wrrLocalityAny(&v3.TypedStruct{
								TypeUrl: "type.googleapis.com/myorg.MyCustomLeastRequestPolicy",
								Value: &structpb.Struct{}, // probably just marshals into empty json, does go struct -> json
							}),
						},
					},
				},
			},
			wantConfig: &internalserviceconfig.BalancerConfig{
				Name: "xds_wrr_locality_experimental",
				Config: &wrrlocality.LBConfig{
					ChildPolicy: &internalserviceconfig.BalancerConfig{
						Name: "myorg.MyCustomLeastRequestPolicy", // I think this needs to be registered in balancer registry to be pulled off stack
						// empty config like above empty struct
						Config: customLBConfig{},
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rawJSON, err := ConvertToServiceConfig(test.policy, 0)
			if err != nil { // all of these tests are passing tests
				t.Fatalf("unwanted error in ConvertToServiceConfig: %v", err)
			}
			bc := &internalserviceconfig.BalancerConfig{}
			// json.Marshal into above
			// argh this will a. unmarshal it
			// and b. check if the unmarshaled config is valid
			// this just prepares the config, so this is unrobust over time (
			// will fail if valid config changes)

			// it doesn't have a guarantee that it prepares a valid config - just that it
			// prepares a config or fails conversion

			// but definitely json emitted from the xDS client needs to be valid
			// - it's already validated using this function for this the
			// guarantee is just that it converts it into json, fails to find a
			// type or fails to convert (i.e. wrong hash algorithm)

			// maybe unmarshal into an intermediate balancer config?
			// something that doesn't also validate, but what things do we need out of the config at this layer?
			// map[string]json.RawMessage
			// unmarshal into immediate balancer config like unmarshal json on balancer config

			// json.RawMessage needs to contain the converted shit from the proto
			// what is the scope? a mapping from xDS LB Policy message to internal gRPC JSON Format:

			// This new registry will maintain a set of converters that are able
			// to map from the xDS LoadBalancingPolicy message to the internal
			// gRPC JSON format.

			// Literally just a mapping
			// [proto xDS LB Policy message] -> internal gRPC JSON format

			// is there a type that can represent "internal gRPC JSON format?"


			// This unmarshaling into a balancer config also unfortunately performs validations on
			// the name: configuration pairing and a. checking if the balancer that the name is pointed to is found and
			// b. checking if the configuration for that balancer is a valid configuration.

			// The converter registry is not guaranteed to emit json that is
			// valid, it's role is to simply convert from a proto message to
			// internal gRPC JSON format. Thus, I have emitted valid JSON in the
			// tests, but this leaves this test brittle over time in case
			// balancer validations fail. I think this simplicitly of using this
			// type outweighs this brittleness, and also the team plans on
			// decoupling the unmarshalling and validation step both present in
			// this function in the future. Thus, I have added two TODOs for
			// this: TODO: Fix any configurations in this test that aren't valid (need to make sure emissions above are valid configuration)
			// in case we switch validations over time. Also, once we partion
			// this Unmarshal into Unmarshal vs. Validation in seperate
			// operations, the brittleness of this will go away.
			if err := json.Unmarshal(rawJSON, bc); err != nil { // converter_test.go:295: failed to unmarshal JSON: json: cannot unmarshal object into Go value of type serviceconfig.intermediateBalancerConfig - why can't I json.Unmarshal, this has a function on it UnmarshalJSON...
				// as per the todo above, shouldn't happen
				t.Fatalf("failed to unmarshal JSON: %v", err)
			}
			// Unmarshalling into balancer config makes it easier to validate
			// returns an opaque type representing parsed json - externalserviceconfig.LoadBalancingConfig
			/*
			{
			   "xds_wrr_locality_experimental": {
			     "child_policy": [{
			          "myorg.MyCustomLeastRequestPolicy" : {
			            "choiceCount" : 2
			          },
			      }]
			   }
			}
			*/
			// This is I think verbatim what you get emitted ^^^
			// On the equals do I need to typecast downward?

			// I don't know if equals() methods are defined on this balancer config type, try it and see what happens
			// I don't know if this assertion is working in the slightest
			if diff := cmp.Diff(bc, test.wantConfig); diff != "" {
				t.Fatalf("ConvertToServiceConfig() got unexpected output, diff (-got +want): %v", diff)
			}

			// example emission:

		})
	}
}

func (s) TestConvertToServiceConfigFailure(t *testing.T) {
	tests := []struct {
		name string
		policy *v3clusterpb.LoadBalancingPolicy
		// Tightly couple this with
		//
		wantErr string // json marshaled into this - see if flow works
	}{
		// what other error cases can trigger here?

		// this doesn't do validations

		// 1. The function finds a supported policy but fails to convert the config to gRPC form
		// How does this even map to ConvertToServiceConfig? I don't even know how this will hit?
		// xxHash algorithm
		{
			name: "not xx_hash function",
			policy: &v3clusterpb.LoadBalancingPolicy{
				Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
					{
						TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
							Name: "what is this used for?",
							TypedConfig: testutils.MarshalAny(&v3ringhashpb.RingHash{ // this shouldddd correctly unmarshal as expected
								HashFunction: v3ringhashpb.RingHash_MURMUR_HASH_2,
								MinimumRingSize: wrapperspb.UInt64(10),
								MaximumRingSize: wrapperspb.UInt64(100),
							}),
						},
					},
				},
			},
			wantErr: "unsupported ring_hash hash function",

		}, // you need the error string, how else would you partition the error message

		// 2. The function does not find any supported policy in the list
		{
			name: "no-supported-policy-in-list",
			policy: &v3clusterpb.LoadBalancingPolicy{
				Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
					{
						TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
							Name: "unsupported proto type",
							// Branches on the type url string, so can break the switch by arbitarty proto type - maybe something like least request?
							TypedConfig: testutils.MarshalAny(&least_requestv3.LeastRequest{}), // might be because of the any string
						},
					},
					/*{
						TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
							Name: "unsupported proto type",
							TypedConfig: testutils.MarshalAny(/*second type of proto that isn't part of the list),
						},
					},*/
				},
			},
			wantErr: "no supported policy found in policy list",
		},
		// I could also test right on the boundary of recursion - i.e. 15 levels or w/e
		{
			name: "too much recursion",
			policy: &v3clusterpb.LoadBalancingPolicy{
				Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
					{
						TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
							Name: "first level",
							// wrrLocality(&v3roundrobinpb.RoundRobin{})
							// the only any is the typed config, the next parameters in the chain get converted to any in helper
							// and get marshaled as a child
							TypedConfig: wrrLocalityAny(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(wrrLocality(&v3roundrobinpb.RoundRobin{}))))))))))))))))))))))),
						},
					},
				},
			},
			wantErr: "exceeds max depth",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, gotErr := ConvertToServiceConfig(test.policy, 0)
			if gotErr == nil || !strings.Contains(gotErr.Error(), test.wantErr) {
				t.Fatalf("ConvertToServiceConfig() = %v, wantErr %v", gotErr, test.wantErr)
			}
		})
	}
}

// wrrLocality is a helper that takes a proto message, marshals it into an any
// proto, and returns a wrr locality proto (marshaled into an any) with it's
// child as the marshaled passed in proto message. *** Split comments into 2

func wrrLocality(m proto.Message) *wrr_localityv3.WrrLocality {
	return &wrr_localityv3.WrrLocality{
		EndpointPickingPolicy: &v3clusterpb.LoadBalancingPolicy{
			Policies: []*v3clusterpb.LoadBalancingPolicy_Policy{
				{
					TypedExtensionConfig: &v3corepb.TypedExtensionConfig{
						Name: "what is this used for?",
						TypedConfig: testutils.MarshalAny(m), // does it not keep the type_url?
					},
				},
			},
		},
	}
}

func wrrLocalityAny(m proto.Message) *anypb.Any {
	return testutils.MarshalAny(wrrLocality(m))
}
