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

package wrrlocality

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/balancer/weightedtarget"
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/internal/grpctest"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
	"google.golang.org/grpc/xds/internal"
)

var (
	defaultTestTimeout      = 5 * time.Second
	defaultTestShortTimeout = 10 * time.Millisecond
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

func (s) TestParseConfig(t *testing.T) {
	const errParseConfigName = "errParseConfigBalancer"
	stub.Register(errParseConfigName, stub.BalancerFuncs{
		ParseConfig: func(json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
			return nil, errors.New("some error")
		},
	})

	parser := bb{}
	tests := []struct {
		name    string
		input   string
		wantCfg serviceconfig.LoadBalancingConfig
		wantErr string
	}{
		{
			name:  "happy-case-round robin-child",
			input: `{"childPolicy": [{"round_robin": {}}]}`,
			wantCfg: &LBConfig{
				ChildPolicy: &internalserviceconfig.BalancerConfig{
					Name: roundrobin.Name,
				},
			},
		},
		{
			name:    "invalid-json",
			input:   "{{invalidjson{{",
			wantErr: "invalid character",
		},

		{
			name:    "child-policy-field-isn't-set",
			input:   `{}`,
			wantErr: "child policy field must be set",
		},
		{
			name:    "child-policy-type-is-empty",
			input:   `{"childPolicy": []}`,
			wantErr: "invalid loadBalancingConfig: no supported policies found in []",
		},
		{
			name:    "child-policy-empty-config",
			input:   `{"childPolicy": [{"": {}}]}`,
			wantErr: "invalid loadBalancingConfig: no supported policies found in []",
		},
		{
			name:    "child-policy-type-isn't-registered",
			input:   `{"childPolicy": [{"doesNotExistBalancer": {"cluster": "test_cluster"}}]}`,
			wantErr: "invalid loadBalancingConfig: no supported policies found in [doesNotExistBalancer]",
		},
		{
			name:    "child-policy-config-is-invalid",
			input:   `{"childPolicy": [{"errParseConfigBalancer": {"cluster": "test_cluster"}}]}`,
			wantErr: "error parsing loadBalancingConfig for policy \"errParseConfigBalancer\"",
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
			if gotErr != nil && !strings.Contains(gotErr.Error(), test.wantErr) {
				t.Fatalf("ParseConfig(%v) = %v, wantErr %v", test.input, gotErr, test.wantErr)
			}
			if (gotErr != nil) != (test.wantErr != "") {
				t.Fatalf("ParseConfig(%v) = %v, wantErr %v", test.input, gotErr, test.wantErr)
			}
			if test.wantErr != "" {
				return
			}
			if diff := cmp.Diff(gotCfg, test.wantCfg); diff != "" {
				t.Fatalf("parseConfig(%v) got unexpected output, diff (-got +want): %v", string(test.input), diff)
			}
		})
	}
}

// e2e tests implicitly test pass through behavior of this though...
type mockClientConn struct {}

func setup(t *testing.T) (*wrrLocalityBalancer, func()) {
	t.Helper()
	builder := balancer.Get(Name) // does do flow of getting builder, and using it to build lb struct on heap with all corresponding threads
	if builder == nil {
		t.Fatalf("balancer.Get(%q) returned nil", Name)
	}
	tcc := testutils.NewTestClientConn(t) // what happens if nothing is called, lower level doesn't call right
	wrrL := builder.Build(tcc, balancer.BuildOptions{})
	return wrrL.(*wrrLocalityBalancer), wrrL.Close
}

// TestUpdateClientConnState tests the UpdateClientConnState method of the
// wrr_locality_experimental balancer. This UpdateClientConn operation should
// take the localities and their weights in the addresses passed in, alongside
// the endpoint picking policy defined in the Balancer Config and construct a
// weighted target configuration corresponding to these inputs.
func (s) TestUpdateClientConnState(t *testing.T) { // this is all Java has
	cfgCh := testutils.NewChannel()
	oldWeightedTargetName := weightedTargetName
	defer func() {
		weightedTargetName = oldWeightedTargetName
	}()
	// Overwrite the weighted target name to have wrrLocalityBalancer to pull
	// the mock balancer defined below from the balancer registry to be it's
	// child.
	weightedTargetName = "mock_weighted_target"
	stub.Register("mock_weighted_target", stub.BalancerFuncs{
		UpdateClientConnState: func(bd *stub.BalancerData, ccs balancer.ClientConnState) error {
			wtCfg, ok := ccs.BalancerConfig.(*weightedtarget.LBConfig)
			if !ok {
				/*
				UpdateClientConnState can error from:
				1. this call can print
				2. receiving a config with bad type
				*/
				return errors.New("child received config that was not a weighted target config") // could also just return an error
			}
			defer cfgCh.Send(wtCfg)
			return nil
		},
	})

	wrrL, close := setup(t)
	defer close()

	var addrs []resolver.Address
	addr := resolver.Address{
		Addr: "locality-1",
	}

	lID := internal.LocalityID{
		Region: "region-1",
		Zone: "zone-1",
		SubZone: "subzone-1",
	}
	addr = internal.SetLocalityID(addr, lID)
	addr = SetAddrInfo(addr, AddrInfo{LocalityWeight: 2})
	addrs = append(addrs, addr)

	addr2 := resolver.Address{
		Addr: "locality-2",
	}

	lID2 := internal.LocalityID{
		Region: "region-2",
		Zone: "zone-2",
		SubZone: "subzone-2",
	}
	addr2 = internal.SetLocalityID(addr2, lID2)
	addr2 = SetAddrInfo(addr2, AddrInfo{LocalityWeight: 1})
	addrs = append(addrs, addr2)

	// The parsed load balancing configuration returned by the builder's
	// ParseConfig method, if implemented.

	// func (bb) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error)
	// so the only difference is you fill out a JSON string and then build out this BalancerConfig

	// I think declaring this here inline is fine it's just a unit test

	err := wrrL.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: &LBConfig{
			ChildPolicy: &internalserviceconfig.BalancerConfig{
				Name: "round_robin",
			},
		},
		ResolverState: resolver.State{
			Addresses: addrs,
		},
	})
	if err != nil {
		// two things:
		// error from child's UpdateClientConnState
		// receive a bad config type (maybe try that as a unit test lol)
		// localityID issue can never happen
		t.Fatalf("unexpected error from UpdateClientConnState(maybe log ccs): %v", err)
	}

	// Three tasks:
	// 1. Get rid of non determinism? Is there non determinism? Run 100 times and figure out (no, why)
	// 2. Cleanup this file
	// 3. Does this need another test case or is this single sanity check fine? My gut tells me the latter. that's all it needs
	// (see tests for wt builder in cluster_resolver?) the latter chosen* one sanity check

	// Note that these inline strings declared as the key in Targets built from
	// Locality ID are not exactly what is shown in the example in the gRFC.
	// However, this is an implementation detail that does not affect
	// correctness (confirmed with Java team). The important thing is to get
	// those three pieces of information region, zone, and subzone down to the
	// child layer.
	wantWtCfg := &weightedtarget.LBConfig{
		Targets: map[string]weightedtarget.Target{
			"{\"region\":\"region-1\",\"zone\":\"zone-1\",\"subZone\":\"subzone-1\"}": {
				Weight: 2,
				ChildPolicy: &internalserviceconfig.BalancerConfig{
					Name: "round_robin",
				},
			},
			"{\"region\":\"region-2\",\"zone\":\"zone-2\",\"subZone\":\"subzone-2\"}": {
				Weight: 1,
				ChildPolicy: &internalserviceconfig.BalancerConfig{
					Name: "round_robin",
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	cfg, err := cfgCh.Receive(ctx)
	if err != nil {
		// This means UpdateClientConnState on the child never got called.
		t.Fatalf("error from cfgOrErrCh: %v", err)
	}

	gotWtCfg, ok := cfg.(*weightedtarget.LBConfig)
	if !ok {
		// Shouldn't happen - only sends a config on this channel.
		t.Fatalf("Unexpected config type received from channel %T", gotWtCfg)
	}

	if diff := cmp.Diff(gotWtCfg, wantWtCfg); diff != "" {
		t.Fatalf("child received unexpected wtCfg, diff (-got, +want): %v", diff)
	}
}

// know not to delete the dependencies in internal/

// unit tests are good when test function - prime number prime number

// unit tests setting up state
// expect exact behavior
// copy 50 times, 100 times, each 40 liner changes to 27 liner
// functionality tests happen at package level balancer (API balancer)
// behavior baked too hard into tests, priority lb policy tests gave Doug problems

// Tests are often non software engineered in terms of factoring out into helpers

// nothing factored out into every test - factoring things into logical operations
// such as helpers...

// testing at API level

// Major concern is testing at API level

// helper function in code well defined, unit test for that that's great

// good test helpers if fine in Easwar's

// 8 things to stimulate behavior in 100 times

// tests need to be debuggable - if a test fails, how do you know what is wrong
// failure - but why it failed, checking endpoint weights themselves
// logical scope of the emissions is debugged
// test multiple scenarios with ew, all scenarios get expected output

// distribution simpler test check whole thing
