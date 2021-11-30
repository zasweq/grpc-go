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
 *
 */

package rls

import (
	"github.com/golang/protobuf/proto"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"testing"

	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/proto/grpc_lookup_v1"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/xds/internal/clusterspecifier"
	"google.golang.org/protobuf/types/known/durationpb"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// Same setup as xdsclient tests...test it here



// Since we now scale up ParseClusterSpecifierConfig to now take the full rls lb policy, need to declare
// an rls config here that passes validation.

// First do trivial changes (in rls config), get original tests to pass.


// Then, move those tests to here, and read rls config to create a config that passes.

/*func (s) TestUnmarshalRouteConfig(t *testing.T) {
	// Maybe call into UnmarshalRouteConfig exported by xdsclient...

	// unexport lbconfig json...export RLSBalancer ParseConfig()
}*/

// how to call into unmarshal RDS...or is there another way to test this?



func (s) TestParseClusterSpecifierConfig(t *testing.T) {
	// func (rls) ParseClusterSpecifierConfig(cfg proto.Message) (clusterspecifier.BalancerConfig, error)
	tests := []struct {
		name       string
		rlcs       proto.Message
		wantConfig clusterspecifier.BalancerConfig
		wantErr  bool
	}{
		{
			name: "invalid-rls-cluster-specifier",
			rlcs: rlsClusterSpecifierConfigError, // Example from easwars tests?
			wantErr: true,
		},
		{
			name: "valid-rls-cluster-specifier",
			rlcs: rlsClusterSpecifierConfigWithoutTransformations,
			wantConfig: configWithoutTransformationsWant,
		},
	}
	for _, test := range tests{
		// test.rlcs.GetTypedCongig()?
		cs := clusterspecifier.Get("type.googleapis.com/grpc.lookup.v1.RouteLookupClusterSpecifier")
		if cs == nil {
			t.Fatal("Error getting cluster specifier")
		}
		lbCfg, err := cs.ParseClusterSpecifierConfig(test.rlcs)

		// check error first
		if (err != nil) != test.wantErr {
			t.Fatalf("ParseClusterSpecifierConfig(%+v) returned err: %v, wantErr: %v", test.rlcs, err, test.wantErr)
		}

		if !cmp.Equal(test.wantConfig, lbCfg, cmpopts.EquateEmpty()) {
			t.Fatalf("ParseClusterSpecifierConfig(%+v) returned expected, diff (-want +got):\\n%s", test.rlcs, cmp.Diff(test.wantConfig, lbCfg, cmpopts.EquateEmpty()))
		}

		// TODO: Figure out Config diff and how to check - manual?
		// then check config diff
		// BalancerConfig []map[string]interface{} vs. BalancerConfig []map[string]interface{}
	}
}


// knob (the input) is the RouteLookupClusterSpecifier proto... -- RLS ClusterSpecifier Proto

// The other two are fixed - will this not break RLS LB parsing? Is this rebased onto master to get the updated changes?

// This will either error - wantErr

// Or return a valid RLS LB Config (json with three fields) - wantUpdate





// rls config here that doesn't pass RLS validation


// rls config here that passes RLS validation - you could also probably look at
// the tests already in codebase for RLS ***

// Just the RLC proto:

// Build out rlcJSON in proto format based on the json - input, piped into json and the piped back into a proto



// Take the raw json string and put it inside the Want - output
//
/*
{"rls_experimental": &rls.LBConfigJSON{ // Switch []map[string]interface{} to lbConfig
						RouteLookupConfig:                rlcJSON,
						ChildPolicy:                      []map[string]interface{}{{"cds_experimental": nil}},
						ChildPolicyConfigTargetFieldName: "cluster",
					}}
*/


// IF NEW CONFIG, SWITCH OVER GRACEFULLY
// IF SAME CONFIG, JUST SEND OVER SAME CONFIG
// PRIORITY LB POLICY LOGIC IS SAME
// WAIT FOR NEW ONE TO BECOME READY, CLOSE THE OLD ONE
// THE C WRAPPER DOES THIS LOGIC, INJECT FOR EVERY CHILD FOR XDS CLUSTER MANAGER

// DONE BEFORE REMOVING ENV VAR PROTECTING THIS RLS CSP POLICY

// ENV VAR PROTECTION

// THIS NEW LOGIC of two children and graceful switch, have it pipe lastest config

/*var rlsClusterSpecifierConfig = testutils.MarshalAny(&grpc_lookup_v1.RouteLookupClusterSpecifier{
	// Once Easwar merges the RLS LB implementation and the rls csp actually
	// calls into it for validation, this config will have to be scaled up to
	// pass the validations present in the RLS LB implementation.
	RouteLookupConfig: &grpc_lookup_v1.RouteLookupConfig{
		HttpKeybuilders: , // No explicit check for this...

		LookupService: "rls-specifier",
	},
})*/

// bad grpc key builder because required match set
var rlsClusterSpecifierConfigError = testutils.MarshalAny(&grpc_lookup_v1.RouteLookupClusterSpecifier{
	RouteLookupConfig: &grpc_lookup_v1.RouteLookupConfig{
		GrpcKeybuilders: []*grpc_lookup_v1.GrpcKeyBuilder{
			{
				Names: []*grpc_lookup_v1.GrpcKeyBuilder_Name{
					{
						Service: "service",
						Method: "method",
					},
				},
				Headers: []*grpc_lookup_v1.NameMatcher{
					{
						Key: "k1",
						RequiredMatch: true,
						Names: []string{"v1"},
					},
				},
			},
		},
	},
})

// Corresponds to that unit test case in config_test.go
var rlsClusterSpecifierConfigWithoutTransformations = testutils.MarshalAny(&grpc_lookup_v1.RouteLookupClusterSpecifier{
	// Once Easwar merges the RLS LB implementation and the rls csp actually
	// calls into it for validation, this config will have to be scaled up to
	// pass the validations present in the RLS LB implementation.
	RouteLookupConfig: &grpc_lookup_v1.RouteLookupConfig{
		GrpcKeybuilders: []*grpc_lookup_v1.GrpcKeyBuilder{
			{
				Names: []*grpc_lookup_v1.GrpcKeyBuilder_Name{
					{
						Service: "service",
						Method: "method",
					},
				},
				Headers: []*grpc_lookup_v1.NameMatcher{
					{
						Key: "k1",
						Names: []string{"v1"},
					},
				},
			},
		},
		LookupService: "target",
		LookupServiceTimeout: &durationpb.Duration{Seconds: 100},
		MaxAge: &durationpb.Duration{Seconds: 60},
		StaleAge: &durationpb.Duration{Seconds: 50},
		CacheSizeBytes: 1000,
		DefaultTarget: "passthrough:///default",
	},
})

var configWithoutTransformationsWant = clusterspecifier.BalancerConfig{{"rls_experimental": &LBConfigJSON{
	RouteLookupConfig: []byte(`{
					"grpcKeybuilders": [{
						"names": [{"service": "service", "method": "method"}],
						"headers": [{"key": "k1", "names": ["v1"]}]
					}],
					"lookupService": "target",
					"lookupServiceTimeout" : "100s",
					"maxAge": "60s",
					"staleAge" : "50s",
					"cacheSizeBytes": 1000,
					"defaultTarget": "passthrough:///default"
				},`),
	ChildPolicy: []map[string]interface{}{
		{
			"cds_experimental": nil, // Is this right? {} in design doc
		},
	},
	ChildPolicyConfigTargetFieldName: "cluster",
}}}