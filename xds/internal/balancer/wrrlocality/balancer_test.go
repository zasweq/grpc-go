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
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/internal/balancer/stub"
	"google.golang.org/grpc/internal/grpctest"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/serviceconfig"
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
		name string
		input string
		wantCfg serviceconfig.LoadBalancingConfig
		wantErr string // do we want this? substring? couples the test with the error string implementation plumbed back
	}{
		{
			name: "happy-case-round robin-child",
			input: `{
						"childPolicy": [
						{
							"round_robin": {}
						}
					]
					}`,
			wantCfg: &LBConfig{
				ChildPolicy: &internalserviceconfig.BalancerConfig{
					Name: roundrobin.Name,
				},
			},
		},

		// this is just regurgitating validations already there right?

		// error cases:
		{
			name: "invalid-json",
			input: "{{invalidjson{{",
			wantErr: "invalid character",
		},

		{
			name: "child-policy-field-isn't-set",
			input: `{}`,
			wantErr: "child policy field must be set",
		},
		{
			name: "child-policy-type-is-empty",
			input: `{"childPolicy": []}`,
			wantErr: "invalid loadBalancingConfig: no supported policies found in []",
		},
		{
			name: "child-policy-empty-config",
			input: `{
						"childPolicy": [
						{
							"": {}
						}
					]
					}`,
			wantErr: "invalid loadBalancingConfig: no supported policies found in []",/*same error as below, except maybe with an empty string in balancer.Get() error message*/
		},
		{
			name: "child-policy-type-isn't-registered",
			input: `{
						"childPolicy": [
						{
							"doesNotExistBalancer": {
								"cluster": "test_cluster"
							}
						}
					]
					}`,
			wantErr: "invalid loadBalancingConfig: no supported policies found in [doesNotExistBalancer]",
		},
		{
			name: "child-policy-config-is-invalid",
			input: `{
						"childPolicy": [
						{
							"errParseConfigBalancer": {
								"cluster": "test_cluster"
							}
						}
					]
					}`,
			wantErr: "error parsing loadBalancingConfig for policy \"errParseConfigBalancer\"",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// substring match would make this very tightly coupled to the
			// balancer configuration - but you do want same errors still so I
			// think it's fine. Important to know even though a brittle test

			// ^^^ Reword into better comment

			gotCfg, gotErr := parser.ParseConfig(json.RawMessage(test.input)) // we only have exported
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
