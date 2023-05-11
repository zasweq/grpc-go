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

	rhCfg := &ringHashLBConfig{
		MinRingSize: minSize,
		MaxRingSize: maxSize,
	}

	rhCfgJSON, err := json.Marshal(rhCfg)
	if err != nil {
		return nil, fmt.Errorf("error marshaling JSON for type %T: %v", rhCfg, err)
	}
	return makeBalancerConfigJSON("ring_hash_experimental", rhCfgJSON), nil
}

type wrrLocalityLBConfig struct {
	ChildPolicy json.RawMessage `json:"childPolicy,omitempty"`
}

func convertWrrLocality(cfg *v3wrrlocalitypb.WrrLocality, depth int) (json.RawMessage, error) {
	epJSON, err := convertToServiceConfig(cfg.GetEndpointPickingPolicy(), depth+1)
	if err != nil {
		return nil, fmt.Errorf("error converting endpoint picking policy: %v for %+v", err, cfg)
	}
	wrrLCfg := wrrLocalityLBConfig{
		ChildPolicy: epJSON,
	}
	lbCfgJSON, err := json.Marshal(wrrLCfg)
	if err != nil {
		return nil, fmt.Errorf("error marshaling JSON for type %T: %v", wrrLCfg, err)
	}
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

type wrrLBConfig struct {
	EnableOOBLoadReport bool `json:"enableOobLoadReport,omitempty"`
	OOBReportingPeriod time.Duration `json:"oobReportingPeriod,omitempty"`
	BlackoutPeriod time.Duration `json:"blackoutPeriod,omitempty"`
	WeightExpirationPeriod time.Duration `json:"weightExpirationPeriod,omitempty"`
	WeightUpdatePeriod time.Duration `json:"weightUpdatePeriod,omitempty"`
	ErrorUtilizationPenalty float64 `json:"errorUtilizationPenalty,omitempty"`
}

func convertClientSideWRR(cfg *v3clientsideweightedroundrobin.ClientSideWeightedRoundRobin) (json.RawMessage, error) {
	const (
		defaultEnableOOBLoadReport = false
		defaultOOBReportingPeriod = 10 * time.Second
		defaultBlackoutPeriod = 10 * time.Second
		defaultWeightExpirationPeriod = 3 * time.Minute
		defaultWeightUpdatePeriod = time.Second
		defaultErrorUtilizationPenalty = float32(1.0)
	)
	enableOOBLoadReport := defaultEnableOOBLoadReport
	if enableOOBLoadReportCfg := cfg.GetEnableOobLoadReport(); enableOOBLoadReportCfg != nil {
		enableOOBLoadReport = enableOOBLoadReportCfg.GetValue()
	}
	oobReportingPeriod := defaultOOBReportingPeriod
	if oobReportingPeriodCfg := cfg.GetOobReportingPeriod(); oobReportingPeriodCfg != nil {
		oobReportingPeriod = oobReportingPeriodCfg.AsDuration()
	}
	blackoutPeriod := defaultBlackoutPeriod
	if blackoutPeriodCfg := cfg.GetBlackoutPeriod(); blackoutPeriodCfg != nil {
		blackoutPeriod = blackoutPeriodCfg.AsDuration()
	}
	weightExpirationPeriod := defaultWeightExpirationPeriod
	if weightExpirationPeriodCfg := cfg.GetBlackoutPeriod(); weightExpirationPeriodCfg != nil {
		weightExpirationPeriod = weightExpirationPeriodCfg.AsDuration()
	}
	weightUpdatePeriod := defaultWeightUpdatePeriod
	if weightUpdatePeriodCfg := cfg.GetWeightUpdatePeriod(); weightUpdatePeriodCfg != nil {
		weightUpdatePeriod = weightUpdatePeriodCfg.AsDuration()
	}
	errorUtilizationPenalty := defaultErrorUtilizationPenalty
	if errorUtilizationPenaltyCfg := cfg.GetErrorUtilizationPenalty(); errorUtilizationPenaltyCfg != nil {
		errorUtilizationPenalty = errorUtilizationPenaltyCfg.GetValue()
	}

	wrrLBConfig := &wrrLBConfig{
		EnableOOBLoadReport: enableOOBLoadReport,
		OOBReportingPeriod: oobReportingPeriod,
		BlackoutPeriod: blackoutPeriod,
		WeightExpirationPeriod: weightExpirationPeriod,
		WeightUpdatePeriod: weightUpdatePeriod,
		ErrorUtilizationPenalty: float64(errorUtilizationPenalty),
	}

	lbCfgJSON, err := json.Marshal(wrrLBConfig)
	if err != nil {
		return nil, fmt.Errorf("error marshaling JSON for type %T: %v", wrrLBConfig, err)
	}
	return makeBalancerConfigJSON("weighted_round_robin_experimental", lbCfgJSON), nil
}

func makeBalancerConfigJSON(name string, value json.RawMessage) []byte {
	return []byte(fmt.Sprintf(`[{%q: %s}]`, name, value))
}
