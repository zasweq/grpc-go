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
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/weightedtarget"
	"google.golang.org/grpc/internal/testutils"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/xds/internal"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/internal/grpctest"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/serviceconfig"
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

// the only thing this needs to make sure is pass through

// e2e tests implicitly test pass through behavior of this though...
type mockClientConn struct {}

func setup(t *testing.T) (*wrrLocality, func()) { // rename to wrrLocalityBalancer? Also if needed return a tcc here
	t.Helper()
	builder := balancer.Get(Name) // does do flow of getting builder, and using it to build lb struct on heap with all corresponding threads
	if builder == nil {
		t.Fatalf("balancer.Get(%q) returned nil", Name)
	}
	// in unit tests verify tcc called with pass through crap
	tcc := testutils.NewTestClientConn(t) // what happens if nothing is called, lower level doesn't call right
	wrrL := builder.Build(tcc, balancer.BuildOptions{})
	return wrrL.(*wrrLocality), wrrL.Close
}




// Prepares right weighted target configuration based on inputs
// weighted target configuration want


// See weighted target test to see how they test as top level balancer - that with same configurations (used to be configured through
// inline wt, now configured with this balancer should logically be same when top level balancer of the Client Conn.)

// TestUpdateClientConnState tests the UpdateClientConnState method of the
// wrr_locality_experimental balancer. This UpdateClientConn operation should
// take the localities and their weights in the addresses passed in, alongside
// the endpoint picking policy defined in the Balancer Config and construct a
// weighted target configuration corresponding to these inputs.
func (s) TestUpdateClientConnState(t *testing.T) {
	cfgCh := testutils.NewChannel()
	oldWeightedTargetName := weightedTargetName
	defer func() {
		weightedTargetName = oldWeightedTargetName
	}()
	// overwrite the weighted target name to have wrr_locality_expiermental pull
	// the mock balancer defined below off the registry.
	weightedTargetName = "mock_weighted_target"
	stub.Register("mock_weighted_target", stub.BalancerFuncs{
		UpdateClientConnState: func(bd *stub.BalancerData, ccs balancer.ClientConnState) error {
			// ccs.ResolverState - verify this too?
			wtCfg, ok := ccs.BalancerConfig.(*weightedtarget.LBConfig)
			if !ok {
				/*
				UpdateClientConnState can error from:
				1. this call can print
				2. receiving a config with bad type
				*/
				return errors.New("child received config that was not a weighted target config") // could also just return an error
			}
			defer cfgCh.Send(wtCfg) // typecast to *weightedtarget.LBConfig
			return nil
		},
	})

	wrrL, close := setup(t)
	defer close()

	ccs := balancer.ClientConnState{}
	var addrs []resolver.Address
	addr := resolver.Address{
		Addr: "locality-1",
	}
	// "Locality{region=region_a,zone=zone_a,subZone=subZone_a}" or is this just Java JSON parsing? Does this even matter?
	// Do we want to make Marshaling of Locality ID = ^^^

	lID := internal.LocalityID{
		Region: "region-1", // actually won't the string be region/zone/subzone, including top level locality string
		Zone: "zone-1",
		SubZone: "subzone-1",
	}
	addr = internal.SetLocalityID(addr, lID)
	addr = SetAddrInfo(addr, AddrInfo{LocalityWeight: 2})
	addrs = append(addrs, addr)

	addr2 := resolver.Address{ // point to the same memory/heap memory make a whole new variable
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
	ccs.ResolverState.Addresses = addrs // if t-test this needs to be specified

	// 2. The wrrL config with child policy round robin...
	// can merge ccs into an inline declaration later

	// The parsed load balancing configuration returned by the builder's
	// ParseConfig method, if implemented.

	// func (bb) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error)
	// so the only difference is you fill out a JSON string and then build out this BalancerConfig

	// I think declaring this here inline is fine it's just a unit test


	ccs.BalancerConfig = &LBConfig{
		ChildPolicy: &internalserviceconfig.BalancerConfig{
			Name: "round_robin",
		},
	}

	err := wrrL.UpdateClientConnState(ccs)
	if err != nil {
		// two things: error from child's UpdateClientConnState
		// receive a bad config type (maybe try that as a unit test lol)
		// localityID issue can never happen
		t.Fatalf("unexpected error from UpdateClientConnState(maybe log ccs): %v", err)
	}




	/*
	lID := internal.LocalityID{
			Region: "region-1", // actually won't the string be region/zone/subzone, including top level locality string
			Zone: "zone-1",
			SubZone: "subzone-1",
		}
	*/

	// Three tasks:
	// 1. Get rid of non determinism
	// 2. Cleanup this file
	// 3. Does this need another test case or is this single sanity check fine? My gut tells me the latter. that's all it needs
	// (see tests for wt builder in cluster_resolver?)

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
	// too many knobs to make t-test


	// could defer the channel push? that would cause it to not block forever.
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	cfg, err := cfgCh.Receive(ctx)
	if err != nil {
		// Logically, this means UpdateClientConnState never got called in the first place because timeout.
		t.Fatalf("error from cfgOrErrCh: %v", err)
	}

	gotWtCfg, ok := cfg.(*weightedtarget.LBConfig)
	if !ok {
		// Shouldn't happen - only sends a config on this
		t.Fatalf("Unexpected config type received from channel %T", gotWtCfg)
	}

	if diff := cmp.Diff(gotWtCfg, wantWtCfg); diff != "" {
		t.Fatalf("child received unexpected wtCfg, diff (-got, +want): %v", diff)
	}
}

