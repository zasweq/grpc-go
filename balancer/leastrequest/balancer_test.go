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
 */

package leastrequest

import (
	"context"
	"encoding/json"
	"fmt"
	"google.golang.org/grpc/peer"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/stubserver"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
	"google.golang.org/grpc/serviceconfig"
)

const (
	defaultTestTimeout = 5 * time.Second
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

func (s) TestParseConfig(t *testing.T) {
	parser := bb{}
	tests := []struct {
		name    string
		input   string
		wantCfg serviceconfig.LoadBalancingConfig
		wantErr string
	}{
		{
			name:  "happy-case-default",
			input: `{}`,
			wantCfg: &LBConfig{
				ChoiceCount: 2,
			},
		},
		{
			name:  "happy-case-choice-count-set",
			input: `{"choiceCount": 3}`,
			wantCfg: &LBConfig{
				ChoiceCount: 3,
			},
		},
		{
			name:  "happy-case-choice-count-greater-than-ten",
			input: `{"choiceCount": 11}`,
			wantCfg: &LBConfig{
				ChoiceCount: 10,
			},
		},
		{
			name:    "choice-count-less-than-2",
			input:   `{"choiceCount": 1}`,
			wantErr: "must be >= 2",
		},
		{
			name:    "invalid-json",
			input:   "{{invalidjson{{",
			wantErr: "invalid character",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotCfg, gotErr := parser.ParseConfig(json.RawMessage(test.input))
			// Substring match makes this very tightly coupled to the
			// internalserviceconfig.BalancerConfig error strings. However, it
			// is important to distinguish the different types of error messages
			// possible as the parser has a few defined buckets of ways it can
			// error out.
			if (gotErr != nil) != (test.wantErr != "") {
				t.Fatalf("ParseConfig(%v) = %v, wantErr %v", test.input, gotErr, test.wantErr)
			}
			if gotErr != nil && !strings.Contains(gotErr.Error(), test.wantErr) {
				t.Fatalf("ParseConfig(%v) = %v, wantErr %v", test.input, gotErr, test.wantErr)
			}
			if test.wantErr != "" {
				return
			}
			if diff := cmp.Diff(gotCfg, test.wantCfg); diff != "" {
				t.Fatalf("ParseConfig(%v) got unexpected output, diff (-got +want): %v", test.input, diff)
			}
		})
	}
}

// Setup spins up four test backends, each listening on a port on localhost. The
// four backends always reply with an empty response with no error.
func setupBackends(t *testing.T) ([]string, func()) {
	t.Helper()

	backends := make([]*stubserver.StubServer, 4)
	addresses := make([]string, 4)
	// Construct and start 4 working backends.
	for i := 0; i < 4; i++ {
		backend := &stubserver.StubServer{
			EmptyCallF: func(ctx context.Context, in *testpb.Empty) (*testpb.Empty, error) {
				return &testpb.Empty{}, nil
			},
			FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
				for {
					_, err := stream.Recv()
					if err == io.EOF {
						return nil
					}
				}
			},
		}
		if err := backend.StartServer(); err != nil {
			t.Fatalf("Failed to start backend: %v", err)
		}
		t.Logf("Started good TestService backend at: %q", backend.Address)
		backends[i] = backend
		addresses[i] = backend.Address
	}
	cancel := func() {
		for _, backend := range backends {
			backend.Stop()
		}
	}
	return addresses, cancel
}

// e2e test in another package to only use exported symbols?
// checkRoundRobinRPCs verifies that EmptyCall RPCs on the given ClientConn,
// connected to a server exposing the test.grpc_testing.TestService, are
// roundrobined across the given backend addresses.
//
// Returns a non-nil error if context deadline expires before RPCs start to get
// roundrobined across the given backends.
func checkRoundRobinRPCs(ctx context.Context, client testgrpc.TestServiceClient, addrs []resolver.Address) error {
	wantAddrCount := make(map[string]int)
	for _, addr := range addrs {
		wantAddrCount[addr.Addr]++
	}
	gotAddrCount := make(map[string]int)
	for ; ctx.Err() == nil; <-time.After(time.Millisecond) {
		gotAddrCount = make(map[string]int)
		// Perform 3 iterations.
		var iterations [][]string
		for i := 0; i < 3; i++ {
			iteration := make([]string, len(addrs))
			for c := 0; c < len(addrs); c++ {
				var peer peer.Peer
				client.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(&peer))
				if peer.Addr != nil {
					iteration[c] = peer.Addr.String()
				}
			}
			iterations = append(iterations, iteration)
		}
		// Ensure the the first iteration contains all addresses in addrs.
		for _, addr := range iterations[0] {
			gotAddrCount[addr]++
		}
		if diff := cmp.Diff(gotAddrCount, wantAddrCount); diff != "" {
			continue
		}
		// Ensure all three iterations contain the same addresses.
		if !cmp.Equal(iterations[0], iterations[1]) || !cmp.Equal(iterations[0], iterations[2]) {
			continue
		}
		return nil
	}
	return fmt.Errorf("timeout when waiting for roundrobin distribution of RPCs across addresses: %v; got: %v", addrs, gotAddrCount)
}

// scenario: 3 backends, default 2 subconns sampled, see what happens there
// random sample always chooses least, detemrinistic because overwrite random
// first one 11 so route to 1
// second pick 12 so route to 2
// second pick 13 so route to 3 (if you do this pattern can check round robin :) but than couples test)
// 12 (route to how algo first - I think it's first hit)

// TestLeastRequestE2E tests the Least Request LB policy in an e2e style.
// The Least Request balancer is configured as the top level balancer of the channel,
// and connects to 3 upstreams.
// The randomness in the picker is injected in the test to be determinstic,
// allowing the test to make assertions on the distribution.
func (s) TestLeastRequestE2E(t *testing.T) { // same connection management logic as rr should be the same...
	defer func(u func() uint32) {
		grpcranduint32 = u
	}(grpcranduint32)
	var index int
	indexes := []uint32{ // will cause round robin behavior
		0, 0, 0, 1, 0, 2,
	} // 00 11 22 gets map back. Finish RPCs. RR is very generic
	// map address : rpc in flight


	// create a stream and see what address it
	// randomness in any order...check if stream are sent
	// 2 is used (not generic rr)


	// individual streams go to fourth address after creation. 03 13 23 33.
	// oh that fourth address will also create non determinism in map - so need to build out this map thingy of RPCs in flight


	grpcranduint32 = func() uint32 {
		ret := indexes[index % len(indexes)]
		index++
		return ret
	} // should cause continuous rr

	// setup 4 backends
	addresses, cancel := setupBackends(t)
	defer cancel()

	mr := manual.NewBuilderWithScheme("lr-e2e")
	defer mr.Close()

	// configure least request as top level balancer of channel (explicitly set to 2)
	lrscJSON := `
{
  "loadBalancingConfig": [
    {
      "least_request_experimental": {
        "choiceCount": 2
      }
    }
  ]
}`
	sc := internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(lrscJSON)
	// send down three addresses in a certain order...
	firstThreeAddresses := []resolver.Address{
		{Addr: addresses[0]},
		{Addr: addresses[1]},
		{Addr: addresses[2]},
	}
	mr.InitialState(resolver.State{
		Addresses:     firstThreeAddresses,
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

	// deterministic randomness causing RPCs to go to certain backends (see above for algorithm)
	// declare determnstc randomness here
	// declare backends expected th hit here

	// maybe look at Easwar's version of this...and see what he did
	/*var peer peer.Peer
	if _, err := testServiceClient.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(&peer)); err != nil {
		t.Fatalf("RPC failed with unexpected error %v", err)
	}
	if peer.Addr != nil {
		print("peer.Addr:", peer.Addr)
		// iteration[c] = peer.Addr.String()
	}*/

	// two things:
	// active RPCs are going to be decremented once RPC is done - need to create streams
	// could make distribution eventually still be RPCs

	stream, err := testServiceClient.FullDuplexCall(ctx) // make the backend return a certain sequence that blocks on the closeSend case...
	if err != nil {
		t.Fatalf("testServiceClient.FullDuplexCall failed: %v", err)
	}
	/*
	corresponding thing server side for stub server:
	FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
				for {
					_, err := stream.Recv()
					if err == io.EOF {
						return nil
					}
				}
			},
	*/
	stream.CloseSend() // see others for how I used this. I just this to actually trigger stream closure...I think this causes EOF server side
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}

	if err = checkRoundRobinRPCs(ctx, testServiceClient, firstThreeAddresses); err != nil {
		t.Fatalf("error in expected round robin: %v", err)
	}

	// non deterministic map ordering, so really just make it hit rr,
	// This really should be rr with indexes, log and see what's up?

	// and below compare fourth since has less requests.




	// after sending the 3 or 4 addresses, is there a way to wait for
	// the whole list of READY scs send back? I don't know OD just...worked.





	/*fullAddresses := []resolver.Address{
		{Addr: addresses[0]},
		{Addr: addresses[1]},
		{Addr: addresses[2]},
		{Addr: addresses[3]},
	}
	mr.UpdateState()*/ // update the full addresses

	// overwrite random to compare 1 4 2 4 3 4 in a loop

	// next three RPCs should go to 4 since first 3 have 3 RPCs
}


// maybe rebase onto pr in flight - write xDS e2e sanity test
// configure it as locality + endpoint picking, make a single RPC

func (s) TestLeastRequestE2E2(t *testing.T) {
	// Make 3 rpcs with detemrinistic ordering. Get the Peer Info for the three (this will take some wrangling to figure out what's going on)
	defer func(u func() uint32) {
		grpcranduint32 = u
	}(grpcranduint32)
	var index int
	indexes := []uint32{
		0, 0, 1, 1, 2, 2, // or 00 11 22
	}
	grpcranduint32 = func() uint32 {
		ret := indexes[index % len(indexes)]
		index++
		return ret
	}
	// setup 4 backends
	addresses, cancel := setupBackends(t)
	defer cancel()

	mr := manual.NewBuilderWithScheme("lr-e2e")
	defer mr.Close()

	// configure least request as top level balancer of channel (explicitly set to 2)
	lrscJSON := `
{
  "loadBalancingConfig": [
    {
      "least_request_experimental": {
        "choiceCount": 2
      }
    }
  ]
}`
	sc := internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(lrscJSON)
	// send down three addresses in a certain order...but ready scs is non deterministic (but I triaged the address update does get sent down)
	firstThreeAddresses := []resolver.Address{
		{Addr: addresses[0]},
		{Addr: addresses[1]},
		{Addr: addresses[2]},
	}
	mr.InitialState(resolver.State{
		Addresses:     firstThreeAddresses,
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
	// the problem is (and why it's racy) is Build is called 0 1 2 3 times

	// I need to wait for all of them to become READY
	// four picker updates UpdateSCS (or state listener) pushes 4 pickers can do it

	// send RPCs until 4 backends show up...wait for backends.

	// and make this e2e sanity check - even or uneven balance.
	// Test cc and test sc. Pick() unit test. "this part of code works correctly"

	// Things that are hard to stimulate unit test
	// e2e test if easy just do it - don't have to change.
	// if you want to do it e2e - test harness around what you want to happen e2e func test() test harness/setup
	// functions that can occur.

	// Wait for all 3 backends to round robin across. The happens because the ready SubConns are updated every time one SubConn becomes ready.
	if err := checkRoundRobinRPCs(ctx, testServiceClient, firstThreeAddresses); err != nil {
		t.Fatalf("error in expected round robin: %v", err)
	}

	// make 3 rpcs, get the peer info from all (doesn't need to be streaming in this case - pure rr anyway)
	// use that peer info to fill out map...this maps the indexes into actual addresses
	// can actually do this every rpc for future assertions
	// honestly for sanity and to debug just get these three working

	// The map ordering and subsequent iteration of the READY scs is non deterministic.
	// Thus, make three rpcs knowing the index to be selected to learn about what backends
	// correspond to each index in the sc list built out at picker build time.



	// rewset index to 0 for determinism reasons
	index = 0
	peerAtIndex := make([]string, 3)
	var peer0 peer.Peer
	if _, err := testServiceClient.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(&peer0)); err != nil {
		t.Fatalf("testServiceClient.EmptyCall failed: %v", err)
	} // ah because rr takes into affect start of RPC
	peerAtIndex[0] = peer0.Addr.String()
	print("peer1: ", peer0.Addr.String())

	// gets the next 2 right mocked random indexes
	if _, err := testServiceClient.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(&peer0)); err != nil {
		t.Fatalf("testServiceClient.EmptyCall failed: %v", err)
	}
	peerAtIndex[1] = peer0.Addr.String()
	print("peer2: ", peer0.Addr.String())

	if _, err := testServiceClient.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(&peer0)); err != nil {
		t.Fatalf("testServiceClient.EmptyCall failed: %v", err)
	}
	peerAtIndex[2] = peer0.Addr.String()
	print("peer3: ", peer0.Addr.String())


	// make RPCs. The peer populated should correpsond to the peerAtIndex()
	// built out previously.
	// make RPCs but don't finish them, counts should be taken into account

	// overwrite again to get index at 0
	index = 0
	indexes = []uint32{
		0, 0, 0, 1, 1, 2, // or 00 11 22
	}
	// start a streaming call on first (through overwriting random numbers)
	var peer1 peer.Peer
	stream1, err := testServiceClient.FullDuplexCall(ctx, grpc.Peer(&peer1)) // oh afterrr the rpc completes
	if err != nil {
		t.Fatalf("testServiceClient.FullDuplexCall failed: %v", err)
	}


	// compare first and second (through overwriting random number), (through 2)
	// stream should be on second
	var peer2 peer.Peer
	stream2, err := testServiceClient.FullDuplexCall(ctx, grpc.Peer(&peer2))
	if err != nil {
		t.Fatalf("testServiceClient.FullDuplexCall failed: %v", err)
	}

	// second has an rpc, compare second and third, stream should be on second
	var peer3 peer.Peer
	stream3, err := testServiceClient.FullDuplexCall(ctx, grpc.Peer(&peer3))
	if err != nil {
		t.Fatalf("testServiceClient.FullDuplexCall failed: %v", err)
	}

	stream1.CloseSend()
	if _, err = stream1.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}
	stream2.CloseSend()
	if _, err = stream2.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}
	stream3.CloseSend()
	if _, err = stream3.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}
	if peer1.Addr.String() != peerAtIndex[0] {
		t.Fatalf("got: %v, want: %v", peer1.Addr.String(), peerAtIndex[0])
	}
	if peer2.Addr.String() != peerAtIndex[1] {
		t.Fatalf("got: %v, want: %v", peer2.Addr.String(), peerAtIndex[1])
	}
	if peer3.Addr.String() != peerAtIndex[2] {
		t.Fatalf("got: %v, want: %v", peer3.Addr.String(), peerAtIndex[2])
	}
	// get this test out and try and figure out another test for state

	// start three streams again. then, update addresses, choice count to 4
	// random 0 1 2 3, should pick new address for stream.
	// needs to wait here for new picker with new READY though.

	// Write comment about persisting state
	// send fourth address, (need to figure out which address)
	// make choice count 4...it should iterate over all 4 and chose fourth address
	// overwrite indexes to be 0 1 2 3 to iterate over all


	// weird: same same same
	// same same same
	// same same2 same2
	// same same same (because need to wait for four picker updates


	// Overwrite randomness again and play with it a little amongst just the 3. To persist RPC
	// to persist the sc counts in stream:

	// Doug says can cancel the ctx to end all of them (then create a new one...for new RPCs)
	/*stream, err = testServiceClient.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("testServiceClient.FullDuplexCall failed: %v", err)
	}
	// Right here there's active RPCs
	stream.CloseSend() // see others for how I used this. I just this to actually trigger stream closure...I think this causes EOF server side
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}
	 */














	// Update picker, then wait? for client conn to process it
	// streams started ^^^ persist and continue to count for the total from the old picker executing callback





	// Update addresses with fourth one:

	// The fourth one will need to be derived, could do same loop as above. Then
	// compare that fourth one to always send it. (Get above working first)
}
