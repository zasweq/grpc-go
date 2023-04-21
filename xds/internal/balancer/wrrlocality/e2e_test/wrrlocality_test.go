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

// Package e2e_test contains e2e test cases for the WRR Locality LB Policy.
package e2e_test

import (
	"testing"

	"google.golang.org/grpc/internal/grpctest"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// write the logic for this after the other test?
func (s) TestWRRLocalityE2E(t *testing.T) {
	// configure wrr locality as top level balancer of the channel

	// setup and verify a scenario where the weighted target works
	// I think it's just weighted round robin
}

// unit tests using api surface so parseconfig etc.

// so rather than migrate over, switch over, e2e tests should continue to work as normal
// so  configure


// I don't think I need this file/package...
// covered by xDS e2e tests
