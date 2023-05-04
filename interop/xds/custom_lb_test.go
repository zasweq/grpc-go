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

package xds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
	"google.golang.org/grpc/serviceconfig"
	"testing"
	"time"

	"google.golang.org/grpc/internal/grpctest"
	testpb "google.golang.org/grpc/interop/grpc_testing"
)

var defaultTestTimeout = 5 * time.Second

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// configure rr as top level - through this wrapper - assert it has correct metadata on client/server side


// TestCustomLB tests the custom lb for the interop client. It configures the
// custom lb as the top level load balancing policy of the channel, then asserts
// it can successfully make an RPC and also that the rpc behavior the custom lb
// is configured with makes it's way to the server in metadata.
func (s) TestCustomLB(t *testing.T) {
	// setup backends, get their addresses
	// setup one backend which verifies the metadata...

	mr := manual.NewBuilderWithScheme("customlb-e2e")
	defer mr.Close()
	// either err chan or send it and verify

	// timeout or error received from this channel fail out
	errCh := testutils.NewChannel()

	// where is the md? the context right? see o11y tests for example

	// Setup a backend which verifies metadata is present in the response.

	backend := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			// make sure has
			// rpc-behavior, if not send error

			// see o11y tests for how to get metadata

			md, ok := metadata.FromIncomingContext(ctx) // check if this works, if not maybe use another helper?
			if !ok {
				errCh.Send(errors.New("failed to receive metadata"))
				return &testpb.SimpleResponse{}, nil
			}
			rpcBMD := md.Get("rpc-behavior")
			if len(rpcBMD) != 1 {
				errCh.Send(errors.New("not one value for metadata key rpc-behavior"))
				return &testpb.SimpleResponse{}, nil
			}
			wantVal := "error-code-0"
			if rpcBMD[0] != wantVal {
				errCh.Send(fmt.Errorf("metadata val for key \"rpc-behavior\": got val %v, want val %v", rpcBMD[0], wantVal))
				return &testpb.SimpleResponse{}, nil
			}
			// Success.
			errCh.Send(nil) // sync concerns for this send? or does it spawn a goroutine and just work, well just try out...
			return &testpb.SimpleResponse{}, nil
		},
	}
	if err := backend.StartServer(); err != nil {
		t.Fatalf("Failed to start backend: %v", err)
	}
	t.Logf("Started good TestService backend at: %q", backend.Address)
	defer backend.Stop()

	// I think error-code-0 = status.OK?

	lbCfg := &rpcBehaviorLBConfig{
		RPCBehavior: "error-code-0",
	}
	m, err := json.Marshal(lbCfg)
	if err != nil {
		t.Fatalf("Error marshaling JSON %v: %v", lbCfg, err)
	}
	lbCfgJSON := fmt.Sprintf(`{"loadBalancingConfig": [ {%q:%v} ] }`, name, string(m))

	/*lbCfgJSON := `{
  "loadBalancingConfig": [
    {
      "test.RpcBehaviorLoadBalancer": {
        "rpcBehavior": "error-code-0"
      }
    }
  ]
}`*/

	// could even move to testutils (this balancer + test) or something if this
	// doesn't make sense
	sc := internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(lbCfgJSON)
	mr.InitialState(resolver.State{
		Addresses: []resolver.Address{
			{Addr: backend.Address},
		},
		ServiceConfig: sc,
	})

	cc, err := grpc.Dial(mr.Scheme()+":///", grpc.WithResolvers(mr), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.Dial() failed: %v", err)
	}
	defer cc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	testServiceClient := testgrpc.NewTestServiceClient(cc)

	// make a unary rpc, verify server side gets metadata
	if _, err := testServiceClient.UnaryCall(ctx, &testpb.SimpleRequest{}); err != nil {
		t.Fatalf("EmptyCall() failed: %v", err)
	}


	// recv on channel here to verify it received metadata.
	// "rpc-behavior": metadata object specified - status-code-n
	// ctx time out
	val, err := errCh.Receive(ctx)
	if err != nil {
		t.Fatalf("error receiving from errCh: %v", err)
	}


	// err received
	if err, ok := val.(error); ok { // check this assertion
		t.Fatalf("error received from errCh: %v", err)
	} // should be nil
	// nil is success
}
