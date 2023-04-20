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
	tcc := testutils.NewTestClientConn(t)
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
	/*mb := &mockBalancer{
		cfgOrErrCh: cfgOrErrCh,
	}

	oldNewWRRLocality := newWRRLocality
	defer func() {
		newWRRLocality = oldNewWRRLocality
	}()

	newWRRLocality = func(child balancer.Balancer) *wrrLocality {
		return &wrrLocality{
			child: mb, // wtf why...
		}
	}*/
	oldWeightedTargetName := weightedTargetName
	defer func() {
		weightedTargetName = oldWeightedTargetName
	}()
	weightedTargetName = "mock_weighted_target" // have it point to balancer registered below

	// ccsCh := testutils.NewChannel()
	// hardcodes weighted target child but even if you mock you can
	// map the mock verifications anyway
	// but the balancer brings it in, so prob need to mock constructor since prebuilt
	stub.Register("mock_weighted_target", stub.BalancerFuncs{ // can't do this because it conflicts with weighted target on the stack
		UpdateClientConnState: func(bd *stub.BalancerData, ccs balancer.ClientConnState) error {
			// cfgCh.Send(ccs.BalancerConfig)
			wtCfg, ok := ccs.BalancerConfig.(*weightedtarget.LBConfig)
			if !ok {
				// send on err ch - config sent down must be weighted target, important
				// verification to make.
				// cfgCh.Send(errors.New("child received config that was not a weighted target config")) // this will block if need a recv for return, reader error chan?
				/*
				UpdateClientConnState can error from:
				1. this call can print
				2. receiving a config with bad type
				*/
				return errors.New("child received config that was not a weighted target config") // could also just return an error
			}

			// can't do verification here unless you persist want, which will be more work

			// send the wtCfg on the channel - either sends error or config
			defer cfgCh.Send(wtCfg) // typecast to *weightedtarget.LBConfig
			return nil
		},
	})

	// but plumbs in child balancer at build time...so if you want to plumb in mock
	// need to perhaps hook in a constructor?

	wrrL, close := setup(t)
	defer close()

	ccs := balancer.ClientConnState{}
	// Two things needed:
	// 1. The Address Map to be populated with the correct locality weights
	// and also the locality weight
	var addrs []resolver.Address
	addr := resolver.Address{
		Addr: "locality-1",
	}

	// "Locality{region=region_a,zone=zone_a,subZone=subZone_a}" from A52
	/*
	locality.ToString() simply just marshals it

	 */


	// just run it and see what happens

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

	// want err etc.

	// honestly easier just to overwrite name and do it this way
	// by registering stub balancer...combine with codeblock below with channels etc.
	/*ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	cfg, err := ccsCh.Receive(ctx)
	if err != nil {
		t.Fatalf("timed out waiting for UpdateClientConnState on the first child balancer: %v", err)
	}*/
	// Verification on the weighted target config
	/*var gotWtCfg *weightedtarget.LBConfig
	var ok bool

	if gotWtCfg, ok = cfg.(*weightedtarget.LBConfig); !ok {
		t.Fatalf("Received child policy config of type %T, want %T", cfg, weightedtarget.LBConfig{})
	} else { // or just declare vars earlier to get rid of the else and have early return earlier
		// Targets map[string]Target

	}*/




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
					// no need to specify config
				},
			},
		},
	}

	// wtCfg vs. wantCfg
	/*if diff := cmp.Diff(gotWtCfg, wantWtCfg); diff != "" {
		t.Fatalf("UpdateClientConnState got unexpected weighted target config (-got, +want): %v", diff)
	}*/
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

