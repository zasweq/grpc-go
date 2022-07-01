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
 */

package xdsresource

import (
	"google.golang.org/grpc/xds/internal"
	"google.golang.org/protobuf/types/known/anypb"
)

// OverloadDropConfig contains the config to drop overloads.
type OverloadDropConfig struct {
	Category    string
	Numerator   uint32
	Denominator uint32
}

// EndpointHealthStatus represents the health status of an endpoint.
type EndpointHealthStatus int32

const (
	// EndpointHealthStatusUnknown represents HealthStatus UNKNOWN.
	EndpointHealthStatusUnknown EndpointHealthStatus = iota
	// EndpointHealthStatusHealthy represents HealthStatus HEALTHY.
	EndpointHealthStatusHealthy
	// EndpointHealthStatusUnhealthy represents HealthStatus UNHEALTHY.
	EndpointHealthStatusUnhealthy
	// EndpointHealthStatusDraining represents HealthStatus DRAINING.
	EndpointHealthStatusDraining
	// EndpointHealthStatusTimeout represents HealthStatus TIMEOUT.
	EndpointHealthStatusTimeout
	// EndpointHealthStatusDegraded represents HealthStatus DEGRADED.
	EndpointHealthStatusDegraded
)

// "Notifies Envoy of endpoints (host:ports that make up a cluster) iff the cluster was configured to use EDS."

// Endpoint contains information of an endpoint.
type Endpoint struct { // "specific backend" -
	// yes, this can be a host/port. Thus, have the EDS resource specify
	// three addresses, each corresponding to a spun up stub service.
	Address      string // is this logically equivalent to "host:port", test service is "localhost:8080"...
	HealthStatus EndpointHealthStatus
	Weight       uint32
}

// Locality contains information of a locality.
type Locality struct {
	Endpoints []Endpoint
	ID        internal.LocalityID
	Priority  uint32
	Weight    uint32
}

// EndpointsUpdate contains an EDS update.
type EndpointsUpdate struct {
	Drops      []OverloadDropConfig
	Localities []Locality

	// Raw is the resource from the xds response.
	Raw *anypb.Any
}

// EndpointsUpdateErrTuple is a tuple with the update and error. It contains the
// results from unmarshal functions. It's used to pass unmarshal results of
// multiple resources together, e.g. in maps like `map[string]{Update,error}`.
type EndpointsUpdateErrTuple struct {
	Update EndpointsUpdate
	Err    error
}


// stop sending requests to a "specific backend"
// ejects "endpoints" with high error rates


// ring hash flattens the endpoint list across all localities

// each endpoint is a "specific backend", which one or more can be in each "locality".

// Each cluster can have multiple localities with endpoints in each of those localities

// or in the case of ring hash a cluster ("flattened list of endpoints" [])


// so we need 3 endpoints...configure with []{Endpoint, Endpoint, Endpoint}

// Server spins up a default service, also need it to spin up three endpoints
// within that service (s/DefaultEndpoint to []{Endpoint, Endpoint, Endpoint}


