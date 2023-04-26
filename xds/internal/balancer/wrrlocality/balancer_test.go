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

func setup(t *testing.T) (*wrrLocalityBalancer, func()) {
	t.Helper()
	builder := balancer.Get(Name)
	if builder == nil {
		t.Fatalf("balancer.Get(%q) returned nil", Name)
	}
	tcc := testutils.NewTestClientConn(t)
	wrrL := builder.Build(tcc, balancer.BuildOptions{})
	return wrrL.(*wrrLocalityBalancer), wrrL.Close
}

// TestUpdateClientConnState tests the UpdateClientConnState method of the
// wrr_locality_experimental balancer. This UpdateClientConn operation should
// take the localities and their weights in the addresses passed in, alongside
// the endpoint picking policy defined in the Balancer Config specified and
// construct a weighted target configuration corresponding to these inputs.
func (s) TestUpdateClientConnState(t *testing.T) {
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
				return errors.New("child received config that was not a weighted target config")
			}
			defer cfgCh.Send(wtCfg)
			return nil
		},
	})

	wrrL, close := setup(t)
	defer close()

	// Create the addresses with two localities with certain locality weights.
	// This represents what the addresses the wrr_locality balancer will receive
	// in UpdateClientConnState.
	var addrs []resolver.Address
	addr := resolver.Address{
		Addr: "locality-1",
	}

	lID := internal.LocalityID{
		Region:  "region-1",
		Zone:    "zone-1",
		SubZone: "subzone-1",
	}
	addr = internal.SetLocalityID(addr, lID)
	addr = SetAddrInfo(addr, AddrInfo{LocalityWeight: 2})
	addrs = append(addrs, addr)

	addr2 := resolver.Address{
		Addr: "locality-2",
	}
	lID2 := internal.LocalityID{
		Region:  "region-2",
		Zone:    "zone-2",
		SubZone: "subzone-2",
	}
	addr2 = internal.SetLocalityID(addr2, lID2)
	addr2 = SetAddrInfo(addr2, AddrInfo{LocalityWeight: 1})
	addrs = append(addrs, addr2)

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
		t.Fatalf("unexpected error from UpdateClientConnState: %v", err)
	}

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
