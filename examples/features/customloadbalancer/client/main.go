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

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/examples/features/proto/echo"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
	"google.golang.org/grpc/serviceconfig"
	_ "google.golang.org/grpc/examples/features/customloadbalancer/client/customroundrobin" // To register custom_round_robin.
)

var (
	// or do I drop localhost?
	addr1 = "localhost:20000"
	addr2 = "localhost:20001" // change later perhaps
)

// hardcode addresses?
func main() {
	// Load Balancing Example
	mr := manual.NewBuilderWithScheme("example") // how did Doug do it for ORCA example?
	defer mr.Close()

	// You can also plug in your own custom lb policy, which needs to be
	// configurable. This n is configurable. Try changing it and see how the
	// behavior changes.
	json := `{"loadBalancingConfig": [{"custom_round_robin":{"n": 3}}]}`
	// Make another ClientConn with round_robin policy.
	sc := internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(json)
	mr.InitialState(resolver.State{
		Endpoints: []resolver.Endpoint{
			{
				Addresses: []resolver.Address{
					{
						Addr: addr1,
					},
				},
				// need any attributes?
			},
			{
				Addresses: []resolver.Address{
					{
						Addr: addr2,
					},
				},
				// need any attributes?
			},
		},
		ServiceConfig: sc, // I think this is right place to put service config...
	})

	cc, err := grpc.Dial(mr.Scheme() + ":///", grpc.WithResolvers(mr), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to dial: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second * 5)
	defer cancel()
	ec := echo.NewEchoClient(cc)
	for i := 0; i < 20; i++ {
		r, err := ec.UnaryEcho(ctx, &echo.EchoRequest{}) // make 20 rpcs to show distribution
		if err != nil {
			log.Fatalf("UnaryEcho failed: %v", err)
		}
		fmt.Println(r)
	}

	// Outlier Detection e2e test:
	// stub server.Start()
}
