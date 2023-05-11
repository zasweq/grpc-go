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

// Package xdslbregistry provides utilities to convert proto load balancing
// configuration, defined by the xDS API spec, to JSON load balancing
// configuration.
package xdslbregistry

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	v1xdsudpatypepb "github.com/cncf/xds/go/udpa/type/v1"
	v3xdsxdstypepb "github.com/cncf/xds/go/xds/type/v3"
	v3clusterpb "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	v3clientsideweightedroundrobin "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/client_side_weighted_round_robin/v3"
	v3ringhashpb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/ring_hash/v3"
	v3wrrlocalitypb "github.com/envoyproxy/go-control-plane/envoy/extensions/load_balancing_policies/wrr_locality/v3"
	"github.com/golang/protobuf/proto"
	structpb "github.com/golang/protobuf/ptypes/struct"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/internal/envconfig"
)

const (
	defaultRingHashMinSize = 1024
	defaultRingHashMaxSize = 8 * 1024 * 1024 // 8M
)

// ConvertToServiceConfig converts a proto Load Balancing Policy configuration
// into a json string. Returns an error if:
//   - no supported policy found
//   - there is more than 16 layers of recursion in the configuration
//   - a failure occurs when converting the policy
func ConvertToServiceConfig(lbPolicy *v3clusterpb.LoadBalancingPolicy) (json.RawMessage, error) {
	return convertToServiceConfig(lbPolicy, 0)
}

func convertToServiceConfig(lbPolicy *v3clusterpb.LoadBalancingPolicy, depth int) (json.RawMessage, error) {
	// "Configurations that require more than 16 levels of recursion are
	// considered invalid and should result in a NACK response." - A51
	if depth > 15 {
		return nil, fmt.Errorf("lb policy %v exceeds max depth supported: 16 layers", lbPolicy)
	}

	// "This function iterate over the list of policy messages in
	// LoadBalancingPolicy, attempting to convert each one to gRPC form,
	// stopping at the first supported policy." - A52
	for _, policy := range lbPolicy.GetPolicies() {
		// The policy message contains a TypedExtensionConfig
		// message with the configuration information. TypedExtensionConfig in turn
		// uses an Any typed typed_config field to store policy configuration of any
		// type. This typed_config field is used to determine both the name of a
		// policy and the configuration for it, depending on its type:
		switch policy.GetTypedExtensionConfig().GetTypedConfig().GetTypeUrl() {
		case "type.googleapis.com/envoy.extensions.load_balancing_policies.ring_hash.v3.RingHash":
			if !envconfig.XDSRingHash {
				continue
			}
			rhProto := &v3ringhashpb.RingHash{}
			if err := proto.Unmarshal(policy.GetTypedExtensionConfig().GetTypedConfig().GetValue(), rhProto); err != nil {
				return nil, fmt.Errorf("failed to unmarshal resource: %v", err)
			}
			return convertRingHash(rhProto)
		case "type.googleapis.com/envoy.extensions.load_balancing_policies.round_robin.v3.RoundRobin":
			return makeBalancerConfigJSON("round_robin", json.RawMessage("{}")), nil
		case "type.googleapis.com/envoy.extensions.load_balancing_policies.wrr_locality.v3.WrrLocality":
			wrrlProto := &v3wrrlocalitypb.WrrLocality{}
			if err := proto.Unmarshal(policy.GetTypedExtensionConfig().GetTypedConfig().GetValue(), wrrlProto); err != nil {
				return nil, fmt.Errorf("failed to unmarshal resource: %v", err)
			}
			return convertWrrLocality(wrrlProto, depth)
		case "type.googleapis.com/envoy.extensions.load_balancing_policies.client_side_weighted_round_robin.v3.ClientSideWeightedRoundRobin":
			// gets a []byte from control plane - unmarshal into a defined proto type
			cswrrProto := &v3clientsideweightedroundrobin.ClientSideWeightedRoundRobin{}
			if err := proto.Unmarshal(policy.GetTypedExtensionConfig().GetTypedConfig().GetValue(), cswrrProto); err != nil {
				return nil, fmt.Errorf("failed to unmarshal resource: %v", err)
			}
			return convertClientSideWRR(cswrrProto)
		case "type.googleapis.com/xds.type.v3.TypedStruct":
			tsProto := &v3xdsxdstypepb.TypedStruct{}
			if err := proto.Unmarshal(policy.GetTypedExtensionConfig().GetTypedConfig().GetValue(), tsProto); err != nil {
				return nil, fmt.Errorf("failed to unmarshal resource: %v", err)
			}
			json, cont, err := convertCustomPolicy(tsProto.GetTypeUrl(), tsProto.GetValue())
			if cont {
				continue
			}
			return json, err
		case "type.googleapis.com/udpa.type.v1.TypedStruct":
			tsProto := &v1xdsudpatypepb.TypedStruct{}
			if err := proto.Unmarshal(policy.GetTypedExtensionConfig().GetTypedConfig().GetValue(), tsProto); err != nil {
				return nil, fmt.Errorf("failed to unmarshal resource: %v", err)
			}
			if err := proto.Unmarshal(policy.GetTypedExtensionConfig().GetTypedConfig().GetValue(), tsProto); err != nil {
				return nil, fmt.Errorf("failed to unmarshal resource: %v", err)
			}
			json, cont, err := convertCustomPolicy(tsProto.GetTypeUrl(), tsProto.GetValue())
			if cont {
				continue
			}
			return json, err
		}
		// Any entry not in the above list is unsupported and will be skipped.
		// This includes Least Request as well, since grpc-go does not support
		// the Least Request Load Balancing Policy.
	}
	return nil, fmt.Errorf("no supported policy found in policy list +%v", lbPolicy)
}

// tests take json -> bc declared since this is intended usage of this anyway

// something for ring hash json for marshaling safety

type ringHashLBConfig struct {
	MinRingSize uint64 `json:"minRingSize,omitempty"`
	MaxRingSize uint64 `json:"maxRingSize,omitempty"`
}

// convertRingHash converts a proto representation of the ring_hash LB policy's
// configuration to gRPC JSON format.
func convertRingHash(cfg *v3ringhashpb.RingHash) (json.RawMessage, error) {
	if cfg.GetHashFunction() != v3ringhashpb.RingHash_XX_HASH {
		return nil, fmt.Errorf("unsupported ring_hash hash function %v", cfg.GetHashFunction())
	}

	var minSize, maxSize uint64 = defaultRingHashMinSize, defaultRingHashMaxSize
	if min := cfg.GetMinimumRingSize(); min != nil {
		minSize = min.GetValue()
	}
	if max := cfg.GetMaximumRingSize(); max != nil {
		maxSize = max.GetValue()
	}

	rhCfg := &ringHashLBConfig{ // it marshals a * right?
		MinRingSize: minSize,
		MaxRingSize: maxSize,
	}

	rhCfgJSON, err := json.Marshal(rhCfg)
	if err != nil {
		// maybe log type
		return nil, fmt.Errorf("error marshaling JSON: %v", err) // for type rhCfg?
	}

	// lbCfgJSON := []byte(fmt.Sprintf("{\"minRingSize\": %d, \"maxRingSize\": %d}", minSize, maxSize))
	return makeBalancerConfigJSON("ring_hash_experimental", rhCfgJSON), nil
}

// struct here representing wrr locality json for safety
type wrrLocalityLBConfig struct {
	ChildPolicy json.RawMessage `json:"childPolicy,omitempty"` // is this right? I think so because childPolicy is rawJSON and this will just marshal? Will it solve anything? unsanitized rawJSON?
}

func convertWrrLocality(cfg *v3wrrlocalitypb.WrrLocality, depth int) (json.RawMessage, error) {
	epJSON, err := convertToServiceConfig(cfg.GetEndpointPickingPolicy(), depth+1)
	if err != nil {
		return nil, fmt.Errorf("error converting endpoint picking policy: %v for %+v", err, cfg)
	}
	wrrLCfg := wrrLocalityLBConfig{
		ChildPolicy: epJSON,
	}
	lbCfgJSON, err := json.Marshal(wrrLCfg) // sanitizes quotes like sql?
	if err != nil {
		return nil, fmt.Errorf("error marshaling JSON: %v", err) // what's a better error message
	}

	// everything should work exactly the same? alongside scaled up tests for ignoring type not found
	return makeBalancerConfigJSON("xds_wrr_locality_experimental", lbCfgJSON), nil
}

// convertCustomPolicy attempts to prepare json configuration for a custom lb
// proto, which specifies the gRPC balancer type and configuration. Returns the
// converted json, a bool representing whether the caller should continue to the
// next policy, which is true if the gRPC Balancer registry does not contain
// that balancer type, and an error which should cause caller to error if error
// converting.
func convertCustomPolicy(typeURL string, s *structpb.Struct) (json.RawMessage, bool, error) {
	// The gRPC policy name will be the "type name" part of the value of the
	// type_url field in the TypedStruct. We get this by using the part after
	// the last / character. Can assume a valid type_url from the control plane.
	urls := strings.Split(typeURL, "/")
	name := urls[len(urls)-1]

	if balancer.Get(name) == nil {
		return nil, true, nil
	}

	rawJSON, err := json.Marshal(s)
	if err != nil {
		return nil, false, fmt.Errorf("error converting custom lb policy %v: %v for %+v", err, typeURL, s)
	}

	// The Struct contained in the TypedStruct will be returned as-is as the
	// configuration JSON object.
	return makeBalancerConfigJSON(name, rawJSON), false, nil
}

// wrr struct that marshals into same json for marshaling safety
type wrrLBConfig struct {
	EnableOOBLoadReport bool `json:"enableOobLoadReport,omitempty"`
	OOBReportingPeriod time.Duration `json:"oobReportingPeriod,omitempty"`
	BlackoutPeriod time.Duration `json:"blackoutPeriod,omitempty"`
	WeightExpirationPeriod time.Duration `json:"weightExpirationPeriod,omitempty"`
	WeightUpdatePeriod time.Duration `json:"weightUpdatePeriod,omitempty"`
	ErrorUtilizationPenalty float64 `json:"errorUtilizationPenalty,omitempty"`
}

// send this empty proto and expect default config marshaled to
// goes through ParseConfig of WRR as well so it'll get the default there as well right
func convertClientSideWRR(cfg *v3clientsideweightedroundrobin.ClientSideWeightedRoundRobin) (json.RawMessage, error) {
	// cfg. // do I need cfg

	/*
	if min := cfg.GetMinimumRingSize(); min != nil {
			minSize = min.GetValue()
		}
		if max := cfg.GetMaximumRingSize(); max != nil {
			maxSize = max.GetValue()
		}
	*/
	const (
		defaultEnableOOBLoadReport = false
		defaultOOBReportingPeriod = 10 * time.Second
		defaultBlackoutPeriod = 10 * time.Second
		defaultWeightExpirationPeriod = 3 * time.Minute
		defaultWeightUpdatePeriod = time.Second
		defaultErrorUtilizationPenalty = float32(1.0)
	)
	enableOOBLoadReport := defaultEnableOOBLoadReport/*need to populate with defaults here - see ParseConfig on the balancer*/
	if enableOOBLoadReportCfg := cfg.GetEnableOobLoadReport(); enableOOBLoadReportCfg != nil {
		// think of better names
		enableOOBLoadReport = enableOOBLoadReportCfg.GetValue() // could be zero value, but that's fine
	}
	oobReportingPeriod := defaultOOBReportingPeriod /*default*/
	if oobReportingPeriodCfg := cfg.GetOobReportingPeriod(); oobReportingPeriodCfg != nil {
		oobReportingPeriod = oobReportingPeriodCfg.AsDuration()
	}

	blackoutPeriod := defaultBlackoutPeriod
	if blackoutPeriodCfg := cfg.GetBlackoutPeriod(); blackoutPeriodCfg != nil {
		blackoutPeriod = blackoutPeriodCfg.AsDuration() // I think this is what you need here
	}

	weightExpirationPeriod := defaultWeightExpirationPeriod
	if weightExpirationPeriodCfg := cfg.GetBlackoutPeriod(); weightExpirationPeriodCfg != nil {
		weightExpirationPeriod = weightExpirationPeriodCfg.AsDuration()
	}

	weightUpdatePeriod := defaultWeightUpdatePeriod
	if weightUpdatePeriodCfg := cfg.GetWeightUpdatePeriod(); weightUpdatePeriodCfg != nil {
		weightUpdatePeriod = weightUpdatePeriodCfg.AsDuration() // what does this conversion do
	}

	errorUtilizationPenalty := defaultErrorUtilizationPenalty
	if errorUtilizationPenaltyCfg := cfg.GetErrorUtilizationPenalty(); errorUtilizationPenaltyCfg != nil {
		errorUtilizationPenalty = errorUtilizationPenaltyCfg.GetValue()
	}

		// give it defaults just like ring hash (Eric talked to mea bout that),
		// also gives defaults in ParseConfig in the balancer implementation but that's fine
	wrrLBConfig := &wrrLBConfig{
		EnableOOBLoadReport: enableOOBLoadReport, // protects pnaic? how does it do it for say ring hash?
		OOBReportingPeriod: oobReportingPeriod,
		BlackoutPeriod: blackoutPeriod,
		WeightExpirationPeriod: weightExpirationPeriod,
		WeightUpdatePeriod: weightUpdatePeriod,
		ErrorUtilizationPenalty: float64(errorUtilizationPenalty), // upcast so you're good here I think
	}

	rawJSON, err := json.Marshal(wrrLBConfig)
	if err != nil {
		return nil, fmt.Errorf("error marshaling JSON: %v", err)
	}

	// name: rawJSON, does this need to be a balancer type?

	// still call balancerconfigjson
	return makeBalancerConfigJSON("weighted_round_robin_experimental", rawJSON), nil
}

// everything here as already been escaped, no need for a seperate struct
func makeBalancerConfigJSON(name string, value json.RawMessage) []byte {
	// if we need a type fill it out here
	print(fmt.Sprintf(`[{%q: %s}]`, name, value))
	return []byte(fmt.Sprintf(`[{%q: %s}]`, name, value))
} // does this thing need a struct?


// after two prs merged scramble to pass interop Friday for Custom LB

// mention I can't test because wrr config isn't exported

// pull xDS e2e test from left side - should be rr with no OOB reports received