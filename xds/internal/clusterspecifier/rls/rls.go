/*
 *
 * Copyright 2021 gRPC authors.
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

// Package rls implements the RLS cluster specifier plugin.
package rls

import (
	"encoding/json"
	"fmt"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes"
	"google.golang.org/grpc/internal/proto/grpc_lookup_v1"
	"google.golang.org/grpc/xds/internal/clusterspecifier"
	"google.golang.org/protobuf/types/known/anypb"
)

func init() {
	clusterspecifier.Register(rls{})
}

type rls struct{}

func (rls) TypeURLs() []string {
	return []string{"type.googleapis.com/grpc.lookup.v1.RouteLookupClusterSpecifier"}
}

// LBConfigJSON is the RLS LB Policies configuration in JSON format.
// RouteLookupConfig will be a raw JSON string from the passed in proto
// configuration, and the other fields will be hardcoded.
type LBConfigJSON struct {
	RouteLookupConfig                interface{}              `json:"routeLookupConfig"`
	ChildPolicy                      []map[string]interface{} `json:"childPolicy"`
	ChildPolicyConfigTargetFieldName string                   `json:"childPolicyConfigTargetFieldName"`
}

func (rls) ParseClusterSpecifierConfig(cfg proto.Message) (clusterspecifier.BalancerConfig, error) {
	if cfg == nil {
		return nil, fmt.Errorf("rls_csp: nil configuration message provided")
	}
	any, ok := cfg.(*anypb.Any)
	if !ok {
		return nil, fmt.Errorf("rls_csp: error parsing config %v: unknown type %T", cfg, cfg)
	}
	rlcs := new(grpc_lookup_v1.RouteLookupClusterSpecifier)

	if err := ptypes.UnmarshalAny(any, rlcs); err != nil {
		return nil, fmt.Errorf("rls_csp: error parsing config %v: %v", cfg, err)
	}
	// Do we use protojson or regular marshal function - see if Marshal produces correct results?
	// rlcJSON, err := protojson.Marshal(rlcs.GetRouteLookupConfig())
	rlcJSON, err := json.Marshal(rlcs.GetRouteLookupConfig())
	if err != nil {
		return nil, fmt.Errorf("rls_csp: error marshaling route lookup config: %v: %v", rlcs.GetRouteLookupConfig(), err)
	}

	lbCfgJSON := &LBConfigJSON{
		RouteLookupConfig: rlcJSON, // "JSON form of RouteLookupClusterSpecifier.config" - RLS in xDS Design Doc
		ChildPolicy: []map[string]interface{}{
			{
				"cds_experimental": struct{}{}, // Is this right? {} in design doc
			},
		},
		ChildPolicyConfigTargetFieldName: "cluster",
	}

	// Will be uncommented once RLS LB Policy completed

	// rawJSON, err := json.Marshal(lbCfgJSON)
	// if err != nil {
	//	return nil, fmt.Errorf("rls_csp: error marshaling load balancing config %v: %v", lbCfgJSON, err)
	// }

	// _, err := rls.ParseConfig(rawJSON) (will this function need to change its logic at all?)
	// if err != nil {
	// return nil, fmt.Errorf("error: %v", err)
	// }

	return []map[string]interface{}{{"rls_experimental": lbCfgJSON}}, nil
}
