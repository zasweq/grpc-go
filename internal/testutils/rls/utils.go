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

package rls

import (
	"context"
	"errors"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/internal"
	rlspb "google.golang.org/grpc/internal/proto/grpc_lookup_v1"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
	"google.golang.org/grpc/serviceconfig"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/durationpb"
	"strings"
	"testing"
)

// MakeTestRPCAndExpectItToReachBackend is a test helper function which makes
// the EmptyCall RPC on the given ClientConn and verifies that it reaches a
// backend. The latter is accomplished by listening on the provided channel
// which gets pushed to whenever the backend in question gets an RPC.
//
// There are many instances where it can take a while before the attempted RPC
// reaches the expected backend. Examples include, but are not limited to:
//   - control channel is changed in a config update. The RLS LB policy creates a
//     new control channel, and sends a new picker to gRPC. But it takes a while
//     before gRPC actually starts using the new picker.
//   - test is waiting for a cache entry to expire after which we expect a
//     different behavior because we have configured the fake RLS server to return
//     different backends.
//
// Therefore, we do not return an error when the RPC fails. Instead, we wait for
// the context to expire before failing.
func MakeTestRPCAndExpectItToReachBackend(ctx context.Context, t *testing.T, cc *grpc.ClientConn, ch chan struct{}) {
	t.Helper()

	// Drain the backend channel before performing the RPC to remove any
	// notifications from previous RPCs.
	select {
	case <-ch:
	default:
	}

	for {
		if err := ctx.Err(); err != nil {
			t.Fatalf("Timeout when waiting for RPCs to be routed to the given target: %v", err)
		}
		sCtx, sCancel := context.WithTimeout(ctx, defaultTestShortTimeout)
		client := testgrpc.NewTestServiceClient(cc)
		client.EmptyCall(sCtx, &testpb.Empty{})

		select {
		case <-sCtx.Done():
		case <-ch:
			sCancel()
			return
		}
	}
}

// MakeTestRPCAndVerifyError is a test helper function which makes the EmptyCall
// RPC on the given ClientConn and verifies that the RPC fails with the given
// status code and error.
//
// Similar to makeTestRPCAndExpectItToReachBackend, retries until expected
// outcome is reached or the provided context has expired.
func MakeTestRPCAndVerifyError(ctx context.Context, t *testing.T, cc *grpc.ClientConn, wantCode codes.Code, wantErr error) {
	t.Helper()

	for {
		if err := ctx.Err(); err != nil {
			t.Fatalf("Timeout when waiting for RPCs to fail with given error: %v", err)
		}
		sCtx, sCancel := context.WithTimeout(ctx, defaultTestShortTimeout)
		client := testgrpc.NewTestServiceClient(cc)
		_, err := client.EmptyCall(sCtx, &testpb.Empty{})

		// err: rpc error: code = Unavailable desc = name resolver error: produced zero addresses
		// the name resolver produced no addresses?
		print("err: ", err.Error(), "/n")

		// If the RPC fails with the expected code and expected error message (if
		// one was provided), we return. Else we retry after blocking for a little
		// while to ensure that we don't keep blasting away with RPCs.
		if code := status.Code(err); code == wantCode {
			if wantErr == nil || strings.Contains(err.Error(), wantErr.Error()) {
				sCancel()
				return
			}
		}
		<-sCtx.Done()
	}
}

// Takes a testing dependency already so I'm good here...
// SetupRLSServer is already present!!! :)

// Move all the symbols needed here and also export...

// BuildBasicRLSConfigWithChildPolicy constructs a very basic service config for
// the RLS LB policy. It also registers a test LB policy which is capable of
// being a child of the RLS LB policy.
func BuildBasicRLSConfigWithChildPolicy(t *testing.T, childPolicyName, rlsServerAddress string) *RLSConfig { // need to move these helpers as well...how far does it go down?
	childPolicyName = "test-child-policy" + childPolicyName
	RegisterRLSChildPolicy(childPolicyName, nil) // this also registers a balancer builder that can serve as a child - so RPC's continue to work and will actually get routed to server...
	t.Logf("Registered child policy with name %q", childPolicyName)

	return &RLSConfig{
		RouteLookupConfig: &rlspb.RouteLookupConfig{
			GrpcKeybuilders:      []*rlspb.GrpcKeyBuilder{{Names: []*rlspb.GrpcKeyBuilder_Name{{Service: "grpc.testing.TestService"}}}},
			LookupService:        rlsServerAddress,
			LookupServiceTimeout: durationpb.New(defaultTestTimeout),
			CacheSizeBytes:       1024,
		},
		RouteLookupChannelServiceConfig:  `{"loadBalancingConfig": [{"pick_first": {}}]}`,
		ChildPolicy:                      &internalserviceconfig.BalancerConfig{Name: childPolicyName},
		ChildPolicyConfigTargetFieldName: rlsChildPolicyTargetNameField,
	}
}

// call below setup helpers or something...

// startBackend is trivial enough to just copy I thinkkk...

// If want to set it to default: rlsConfig.RouteLookupConfig.DefaultTarget = defBackendAddress

// r := startManualResolverWithConfig(t, rlsConfig)

// It's just copy pasting symbols lol...
// startManualResolverWithConfig registers and returns a manual resolver which
// pushes the RLS LB policy's service config on the channel.
func StartManualResolverWithConfig(t *testing.T, rlsConfig *RLSConfig) *manual.Resolver {
	t.Helper()

	scJSON, err := rlsConfig.ServiceConfigJSON() // This is how it links it - creates a JSON
	if err != nil {
		t.Fatal(err)
	}

	sc := internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(scJSON)
	r := manual.NewBuilderWithScheme("rls-e2e")
	r.InitialState(resolver.State{ServiceConfig: sc})
	t.Cleanup(r.Close)
	return r
} // All these symbols work expect this manual resolver thingy...

// RLSConfig is a utility type to build service config for the RLS LB policy.
type RLSConfig struct { // rls/internal/test/e2e/rls_lb_config...what would be useful for the test to fully run...? Figure out list of symbols...I think I need this though for keybuilders...
	RouteLookupConfig                *rlspb.RouteLookupConfig
	RouteLookupChannelServiceConfig  string
	ChildPolicy                      *internalserviceconfig.BalancerConfig
	ChildPolicyConfigTargetFieldName string
}

// ServiceConfigJSON generates service config with a load balancing config
// corresponding to the RLS LB policy.
func (c *RLSConfig) ServiceConfigJSON() (string, error) {
	m := protojson.MarshalOptions{
		Multiline:     true,
		Indent:        "  ",
		UseProtoNames: true,
	}
	routeLookupCfg, err := m.Marshal(c.RouteLookupConfig)
	if err != nil {
		return "", err
	}
	childPolicy, err := c.ChildPolicy.MarshalJSON()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`
{
  "loadBalancingConfig": [
    {
      "rls_experimental": {
        "routeLookupConfig": %s,
				"routeLookupChannelServiceConfig": %s,
        "childPolicy": %s,
        "childPolicyConfigTargetFieldName": %q
      }
    }
  ]
}`, string(routeLookupCfg), c.RouteLookupChannelServiceConfig, string(childPolicy), c.ChildPolicyConfigTargetFieldName), nil
}

// LoadBalancingConfig generates load balancing config which can used as part of
// a ClientConnState update to the RLS LB policy.
func (c *RLSConfig) LoadBalancingConfig() (serviceconfig.LoadBalancingConfig, error) {
	m := protojson.MarshalOptions{
		Multiline:     true,
		Indent:        "  ",
		UseProtoNames: true,
	}
	routeLookupCfg, err := m.Marshal(c.RouteLookupConfig)
	if err != nil {
		return nil, err
	}
	childPolicy, err := c.ChildPolicy.MarshalJSON()
	if err != nil {
		return nil, err
	}
	lbConfigJSON := fmt.Sprintf(`
{
  "routeLookupConfig": %s,
  "routeLookupChannelServiceConfig": %s,
  "childPolicy": %s,
  "childPolicyConfigTargetFieldName": %q
}`, string(routeLookupCfg), c.RouteLookupChannelServiceConfig, string(childPolicy), c.ChildPolicyConfigTargetFieldName)

	builder := balancer.Get("rls_experimental")
	if builder == nil {
		return nil, errors.New("balancer builder not found for RLS LB policy")
	}
	parser := builder.(balancer.ConfigParser)
	return parser.ParseConfig([]byte(lbConfigJSON))
}
