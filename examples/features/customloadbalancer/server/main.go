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
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/examples/features/proto/echo"
)

var (
	addrs = []string{"localhost:20000", "localhost:20001"} // or do I drop localhost?
	addr1 = "localhost:20000"
	addr2 = "localhost:20001" // change later perhaps
)

type echoServer struct {
	echo.UnimplementedEchoServer
	addr string
}

func (s *echoServer) UnaryEcho(ctx context.Context, req *echo.EchoRequest) (*echo.EchoResponse, error) {
	return &echo.EchoResponse{Message: fmt.Sprintf("%s (from %s)", req.Message, s.addr)}, nil
}

func main() {
	// start two servers with hardcoded aadress, can't do wildcart port since
	// would need to communicate to client (e2e test has ref)

	// do this twice per address ***

	var wg sync.WaitGroup
	for _, addr := range addrs {
		lis, err := net.Listen("tcp", addr)
		if err != nil {
			// what to do here, fail binary?
			log.Fatalf("failed to listen: %v", err)
		}
		s := grpc.NewServer()
		echo.RegisterEchoServer(s, &echoServer{
			addr: addr,
		})
		log.Printf("serving on %s\n", addr)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Serve(lis); err != nil {
				log.Fatalf("failed to serve: %v", err)
			}
		}()
		// wait for this operation to complete before exiting the binary?
	}
	wg.Wait()
	/*lis, err := net.Listen("tcp", addr1)
	if err != nil {
		// what to do here, fail binary?
	}
	s := grpc.NewServer()
	echo.RegisterEchoServer(s, &echoServer{
		addr: addr1,
	})
	if err := s.Serve(lis); err != nil {

	}*/
	// do this twice per address ***
}
