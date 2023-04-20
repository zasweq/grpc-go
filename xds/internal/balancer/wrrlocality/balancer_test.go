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
	builder := balancer.Get(Name)
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
	cfgOrErrCh := testutils.NewChannel()
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

	// ccsCh := testutils.NewChannel()
	// hardcodes weighted target child but even if you mock you can
	// map the mock verifications anyway
	// but the balancer brings it in, so prob need to mock constructor since prebuilt
	stub.Register("mock_weighted_target", stub.BalancerFuncs{ // can't do this because it conflicts with weighted target on the stack
		UpdateClientConnState: func(bd *stub.BalancerData, ccs balancer.ClientConnState) error {
			ccsCh.Send(ccs.BalancerConfig)
			wtCfg, ok := s.BalancerConfig.(*weightedtarget.LBConfig)
			if !ok {
				// send on err ch - config sent down must be weighted target, important
				// verification to make.
				mb.cfgOrErrCh.Send(errors.New("child received config that was not a weighted target config"))
			}

			// can't do verification here unless you persist want, which will be more work

			// send the wtCfg on the channel - either sends error or config
			mb.cfgOrErrCh.Send(wtCfg) // typecast to *weightedtarget.LBConfig
		},
		// only test functionality other operations are tested by e2e
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
	ccs.ResolverState.Addresses = addrs

	// 2. The wrrL config with child policy round robin...
	// can merge ccs into an inline declaration later
	ccs.BalancerConfig = &LBConfig{
		ChildPolicy: &internalserviceconfig.BalancerConfig{
			Name: "round_robin",
		},
	}

	wrrL.UpdateClientConnState(ccs)




	// honestly easier just to overwrite name and do it this way
	// by registering stub balancer...combine with codeblock below with channels etc.
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	cfg, err := ccsCh.Receive(ctx)
	if err != nil {
		t.Fatalf("timed out waiting for UpdateClientConnState on the first child balancer: %v", err)
	}
	// Verification on the weighted target config
	var gotWtCfg *weightedtarget.LBConfig
	var ok bool

	if gotWtCfg, ok = cfg.(*weightedtarget.LBConfig); !ok {
		t.Fatalf("Received child policy config of type %T, want %T", cfg, weightedtarget.LBConfig{})
	} else { // or just declare vars earlier to get rid of the else and have early return earlier
		// Targets map[string]Target

	}







	wantWtCfg := &weightedtarget.LBConfig{
		Targets: map[string]weightedtarget.Target{
			"locality-1": {
				Weight: 2,
				ChildPolicy: &internalserviceconfig.BalancerConfig{
					Name: "round_robin",
				},
			},
			"locality-2": {
				Weight: 1,
				ChildPolicy: &internalserviceconfig.BalancerConfig{
					Name: "round_robin",
					// no need to specify config
				},
			},
		},
	}

	// wtCfg vs. wantCfg
	if diff := cmp.Diff(gotWtCfg, wantWtCfg); diff != "" {
		t.Fatalf("UpdateClientConnState got unexpected weighted target config (-got, +want): %v", diff)
	}
	// too many knobs to make t-test



	// either it times out
	// or sends an (config || error)
	// recv config off channel, cfg or error

	// if timed out fail
	//     early return

	// typecast
	//     if error fail
	//     if cfg gotCfg = cfg

	// cmp.Diff wantCfg vs. wantCfg

	cfgOrErr, err := cfgOrErrCh.Receive(ctx)
	if err != nil {
		// Logically, this means UpdateClientConnState never got called in the first place.
		t.Fatalf("error from cfgOrErrCh: %v", err)
	}
	if err, ok := cfgOrErr.(error); ok {
		t.Fatalf("error received from cfgOrErrCh: %v", err)
	}

	gotWtCfg, ok = cfgOrErr.(*weightedtarget.LBConfig)
	if !ok {
		// Shouldn't happen
	}

	if diff := cmp.Diff(gotWtCfg, wantWtCfg); diff != "" {

	}
} // this test worked it just didn't have child correctly mocked tp capture emissions

// what is the expected result if it gets invalid UpdateClientConnState inputs?

// will it import it's a different file...

type mockBalancer struct {
	// channel to send on - either verify in the component or send the config on the channel and verify in test goroutine
	// errCh
	// wtCh // one channel or two channels - I think I've done this before and I typecasted on errCh

	cfgOrErrCh *testutils.Channel
}

func (mb *mockBalancer) UpdateClientConnState(s balancer.ClientConnState) error {
	// s.BalancerConfig // here
	// typecast to balancer config, should work
	wtCfg, ok := s.BalancerConfig.(*weightedtarget.LBConfig)
	if !ok {
		// send on err ch - config sent down must be weighted target, important
		// verification to make.
		mb.cfgOrErrCh.Send(errors.New("child received config that was not a weighted target config"))
	}

	// can't do verification here unless you persist want, which will be more work

	// send the wtCfg on the channel - either sends error or config
	mb.cfgOrErrCh.Send(wtCfg) // typecast to *weightedtarget.LBConfig
}

// other operations as a no-op
func (mb *mockBalancer) ResolverError(_ error) {}

func (mb *mockBalancer) UpdateSubConnState(_ balancer.SubConn, _ balancer.SubConnState) {}

func (mb *mockBalancer) Close() {}


// assertion on LB Config equality:

// Equal method on LB Config type? it's just child policy
// will that affect other tests

// wait no it's wtCfg vs. wtCfg. This might already be there...



// e2e test top level balancer with weights determining percentage of RPCs sent to backends

// just a wrapper that prepares weighed target config
// so just take weighted target e2e_tests and wrap them with this
// UpdateClientConnState...once you write test you can see exactly how to wrap


// It does create a weighted targt, need to mock
// Well at least it works with creation etc.

// or is there another way to verify the child balancer works?


// and obv. this will get tested e2e in the xDS tree in xDS e2e tests...

// separate e2e package to test public API


func (s) TestUpdateClientConnStateE2E(t *testing.T) {
	// Doug's suggestion - through config parser
	parser := bb{}
	// you get load balancing config from public API
	lbc, err := parser.ParseConfig(/*json.RawMessage*/)
	if err != nil {

	}
	parser.Build() // wait my test already does this through setup - just not through ParseConfig like the balancer API expects

	// cleaner way of populating addresses passed into UpdateClientConnState
	// here

	// PROBLEM SOLVING: mock the balancer below somehow to verify
	// it gets configured with correct configuration

}
