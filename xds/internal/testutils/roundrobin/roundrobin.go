/*
 *
 * Copyright 2022 gRPC authors.
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

// Package roundrobin contains helper functions to check for roundrobin
// loadbalancing of RPCs in tests.
package roundrobin

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/resolver"

	testgrpc "google.golang.org/grpc/test/grpc_testing"
	testpb "google.golang.org/grpc/test/grpc_testing"
)

var logger = grpclog.Component("testutils-roundrobin")

// waitForTrafficToReachBackends repeatedly makes RPCs using the provided
// TestServiceClient until RPCs reach all backends specified in addrs, or the
// context expires, in which case a non-nil error is returned.
func waitForTrafficToReachBackends(ctx context.Context, client testgrpc.TestServiceClient, addrs []resolver.Address) error {
	// Make sure connections to all backends are up. We need to do this two
	// times (to be sure that round_robin has kicked in) because the channel
	// could have been configured with a different LB policy before the switch
	// to round_robin. And the previous LB policy could be sharing backends with
	// round_robin, and therefore in the first iteration of this loop, RPCs
	// could land on backends owned by the previous LB policy.
	for j := 0; j < 2; j++ {
		for i := 0; i < len(addrs); i++ {
			for {
				time.Sleep(time.Millisecond)
				if ctx.Err() != nil {
					return fmt.Errorf("timeout waiting for connection to %q to be up", addrs[i].Addr)
				}
				var peer peer.Peer
				if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(&peer)); err != nil {
					// Some tests remove backends and check if round robin is
					// happening across the remaining backends. In such cases,
					// RPCs can initially fail on the connection using the
					// removed backend. Just keep retrying and eventually the
					// connection using the removed backend will shutdown and
					// will be removed.
					continue
				}
				if peer.Addr.String() == addrs[i].Addr {
					break
				}
			}
		}
	}
	return nil
}

// CheckRoundRobinRPCs verifies that EmptyCall RPCs on the given ClientConn,
// connected to a server exposing the test.grpc_testing.TestService, are
// roundrobin-ed across the given backend addresses.
//
// Returns a non-nil error if context deadline expires before RPCs start to get
// roundrobin-ed across the given backends.
func CheckRoundRobinRPCs(ctx context.Context, client testgrpc.TestServiceClient, addrs []resolver.Address) error {
	if err := waitForTrafficToReachBackends(ctx, client, addrs); err != nil {
		return err
	}

	// At this point, RPCs are getting successfully executed at the backends
	// that we care about. To support duplicate addresses (in addrs) and
	// backends being removed from the list of addresses passed to the
	// roundrobin LB, we do the following:
	// 1. Determine the count of RPCs that we expect each of our backends to
	//    receive per iteration.
	// 2. Wait until the same pattern repeats a few times, or the context
	//    deadline expires.
	wantAddrCount := make(map[string]int)
	for _, addr := range addrs {
		wantAddrCount[addr.Addr]++
	}
	for ; ctx.Err() == nil; <-time.After(time.Millisecond) {
		// Perform 3 more iterations.
		var iterations [][]string
		for i := 0; i < 3; i++ {
			iteration := make([]string, len(addrs))
			for c := 0; c < len(addrs); c++ {
				var peer peer.Peer
				if _, err := client.EmptyCall(ctx, &testpb.Empty{}, grpc.Peer(&peer)); err != nil {
					return fmt.Errorf("EmptyCall() = %v, want <nil>", err)
				}
				iteration[c] = peer.Addr.String() // fuck, does this get populated on an RPC error, peer information AFTER RPC completes
			}
			iterations = append(iterations, iteration)
		}
		// Ensure the the first iteration contains all addresses in addrs.
		gotAddrCount := make(map[string]int)
		for _, addr := range iterations[0] {
			gotAddrCount[addr]++
		}
		if diff := cmp.Diff(gotAddrCount, wantAddrCount); diff != "" {
			logger.Infof("non-roundrobin, got address count in one iteration: %v, want: %v, Diff: %s", gotAddrCount, wantAddrCount, diff)
			continue
		}
		// Ensure all three iterations contain the same addresses.
		if !cmp.Equal(iterations[0], iterations[1]) || !cmp.Equal(iterations[0], iterations[2]) {
			logger.Infof("non-roundrobin, first iter: %v, second iter: %v, third iter: %v", iterations[0], iterations[1], iterations[2])
			continue
		}
		return nil
	}
	return fmt.Errorf("Timeout when waiting for roundrobin distribution of RPCs across addresses: %v", addrs)
}

// what my verification needs to be:
// ctx deadline 5 seconds similar to his

// No RPC errors

// ^^^ calls to backends will error, just the nature of my test setup

// All I need to check for is iteration over a certain address list
// The request volume requirement gets rid of determinism for the first
// pass

// The ctx deadline of 5 seconds should get rid of the determinism with
// the addresses getting ejected

// Do you need to wait for the new picker to be sent and processed or does that
// happen implicitly?

// wb the nondeterminism between when addresses get unejected
// and the length of the interval timer algorithm

// but this will just race between
// a. getting it correctly on the first pass

// b. running it a few more passes, and ejecting the SubConn in subsequent
// iterations of the interval timer algorithm