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
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/serviceconfig"
)

// takes child endpoint config and wraps it with locality weights is all this balancer is

// LBConfig is the config for the wrr locality balancer.
type LBConfig struct {
	serviceconfig.LoadBalancingConfig

	// Why is this repeated in the design? I think i remember
	// ServiceConfig contains a list of loadBalancingConfigs
	// and UnmarshalJSON iterates through that list so logically equivalent
	// to a "list" of lb configs as repeated in design.

	// do we want the child to be an internalserviceconfig.BalancerConfig or just the serviceconfig.LoadBalancingConfig?

	// Why did I declare this as a pointer? should this jsut be a value?

	// ChildPolicy is the config for the child policy.
	ChildPolicy *internalserviceconfig.BalancerConfig `json:"childPolicy,omitempty"`
}
