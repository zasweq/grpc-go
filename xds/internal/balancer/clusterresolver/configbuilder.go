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

package clusterresolver

import (
	"encoding/json"
	"fmt"
	"google.golang.org/grpc/xds/internal/balancer/wrrlocality"
	"sort"

	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/balancer/weightedroundrobin"
	"google.golang.org/grpc/balancer/weightedtarget"
	"google.golang.org/grpc/internal/envconfig"
	"google.golang.org/grpc/internal/hierarchy"
	internalserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/xds/internal"
	"google.golang.org/grpc/xds/internal/balancer/clusterimpl"
	"google.golang.org/grpc/xds/internal/balancer/outlierdetection"
	"google.golang.org/grpc/xds/internal/balancer/priority"
	"google.golang.org/grpc/xds/internal/balancer/ringhash"
	"google.golang.org/grpc/xds/internal/xdsclient/xdsresource"
)

const million = 1000000

// priorityConfig is config for one priority. For example, if there an EDS and a
// DNS, the priority list will be [priorityConfig{EDS}, priorityConfig{DNS}].
//
// Each priorityConfig corresponds to one discovery mechanism from the LBConfig
// generated by the CDS balancer. The CDS balancer resolves the cluster name to
// an ordered list of discovery mechanisms (if the top cluster is an aggregated
// cluster), one for each underlying cluster.
type priorityConfig struct {
	mechanism DiscoveryMechanism
	// edsResp is set only if type is EDS.
	edsResp xdsresource.EndpointsUpdate
	// addresses is set only if type is DNS.
	addresses []string
	// Each discovery mechanism has a name generator so that the child policies
	// can reuse names between updates (EDS updates for example).
	childNameGen *nameGenerator
}

// buildPriorityConfigJSON builds balancer config for the passed in
// priorities.
//
// The built tree of balancers (see test for the output struct).
//
// If xds lb policy is ROUND_ROBIN, the children will be weighted_target for
// locality picking, and round_robin for endpoint picking.
//
//	                                  ┌────────┐
//	                                  │priority│
//	                                  └┬──────┬┘
//	                                   │      │
//	                       ┌───────────▼┐    ┌▼───────────┐
//	                       │cluster_impl│    │cluster_impl│
//	                       └─┬──────────┘    └──────────┬─┘
//	                         │                          │
//	          ┌──────────────▼─┐                      ┌─▼──────────────┐
//	          │locality_picking│                      │locality_picking│
//	          └┬──────────────┬┘                      └┬──────────────┬┘
//	           │              │                        │              │
//	         ┌─▼─┐          ┌─▼─┐                    ┌─▼─┐          ┌─▼─┐
//	         │LRS│          │LRS│                    │LRS│          │LRS│
//	         └─┬─┘          └─┬─┘                    └─┬─┘          └─┬─┘
//	           │              │                        │              │
//	┌──────────▼─────┐  ┌─────▼──────────┐  ┌──────────▼─────┐  ┌─────▼──────────┐
//	│endpoint_picking│  │endpoint_picking│  │endpoint_picking│  │endpoint_picking│
//	└────────────────┘  └────────────────┘  └────────────────┘  └────────────────┘
//
// If xds lb policy is RING_HASH, the children will be just a ring_hash policy.
// The endpoints from all localities will be flattened to one addresses list,
// and the ring_hash policy will pick endpoints from it.
//
//	          ┌────────┐
//	          │priority│
//	          └┬──────┬┘
//	           │      │
//	┌──────────▼─┐  ┌─▼──────────┐
//	│cluster_impl│  │cluster_impl│
//	└──────┬─────┘  └─────┬──────┘
//	       │              │
//	┌──────▼─────┐  ┌─────▼──────┐
//	│ ring_hash  │  │ ring_hash  │
//	└────────────┘  └────────────┘
//
// If endpointPickingPolicy is nil, roundrobin will be used.
//
// Custom locality picking policy isn't support, and weighted_target is always
// used.
func buildPriorityConfigJSON(priorities []priorityConfig, xdsLBPolicy *internalserviceconfig.BalancerConfig) ([]byte, []resolver.Address, error) {
	pc, addrs, err := buildPriorityConfig(priorities, xdsLBPolicy)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build priority config: %v", err)
	}
	ret, err := json.Marshal(pc)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal built priority config struct into json: %v", err)
	}
	return ret, addrs, nil
}

func buildPriorityConfig(priorities []priorityConfig, xdsLBPolicy *internalserviceconfig.BalancerConfig) (*priority.LBConfig, []resolver.Address, error) {
	var (
		retConfig = &priority.LBConfig{Children: make(map[string]*priority.Child)}
		retAddrs  []resolver.Address // .Balancer attributes then to not affect uniqueness?
	)
	for _, p := range priorities {
		switch p.mechanism.Type {
		case DiscoveryMechanismTypeEDS:
			names, configs, addrs, err := buildClusterImplConfigForEDS(p.childNameGen, p.edsResp, p.mechanism, xdsLBPolicy)
			if err != nil {
				return nil, nil, err
			}
			retConfig.Priorities = append(retConfig.Priorities, names...)
			retAddrs = append(retAddrs, addrs...)
			var odCfgs map[string]*outlierdetection.LBConfig
			if envconfig.XDSOutlierDetection {
				odCfgs = convertClusterImplMapToOutlierDetection(configs, p.mechanism.OutlierDetection)
				for n, c := range odCfgs {
					retConfig.Children[n] = &priority.Child{
						Config: &internalserviceconfig.BalancerConfig{Name: outlierdetection.Name, Config: c},
						// Ignore all re-resolution from EDS children.
						IgnoreReresolutionRequests: true,
					}
				}
				continue
			}
			for n, c := range configs {
				retConfig.Children[n] = &priority.Child{
					Config: &internalserviceconfig.BalancerConfig{Name: clusterimpl.Name, Config: c},
					// Ignore all re-resolution from EDS children.
					IgnoreReresolutionRequests: true,
				}

			}
		case DiscoveryMechanismTypeLogicalDNS:
			name, config, addrs := buildClusterImplConfigForDNS(p.childNameGen, p.addresses, p.mechanism)
			retConfig.Priorities = append(retConfig.Priorities, name)
			retAddrs = append(retAddrs, addrs...)
			var odCfg *outlierdetection.LBConfig
			if envconfig.XDSOutlierDetection {
				odCfg = makeClusterImplOutlierDetectionChild(config, p.mechanism.OutlierDetection)
				retConfig.Children[name] = &priority.Child{
					Config: &internalserviceconfig.BalancerConfig{Name: outlierdetection.Name, Config: odCfg},
					// Not ignore re-resolution from DNS children, they will trigger
					// DNS to re-resolve.
					IgnoreReresolutionRequests: false,
				}
				continue
			}
			retConfig.Children[name] = &priority.Child{
				Config: &internalserviceconfig.BalancerConfig{Name: clusterimpl.Name, Config: config},
				// Not ignore re-resolution from DNS children, they will trigger
				// DNS to re-resolve.
				IgnoreReresolutionRequests: false,
			}
		}
	}
	return retConfig, retAddrs, nil
}

func convertClusterImplMapToOutlierDetection(ciCfgs map[string]*clusterimpl.LBConfig, odCfg outlierdetection.LBConfig) map[string]*outlierdetection.LBConfig {
	odCfgs := make(map[string]*outlierdetection.LBConfig, len(ciCfgs))
	for n, c := range ciCfgs {
		odCfgs[n] = makeClusterImplOutlierDetectionChild(c, odCfg)
	}
	return odCfgs
}

func makeClusterImplOutlierDetectionChild(ciCfg *clusterimpl.LBConfig, odCfg outlierdetection.LBConfig) *outlierdetection.LBConfig {
	odCfgRet := odCfg
	odCfgRet.ChildPolicy = &internalserviceconfig.BalancerConfig{Name: clusterimpl.Name, Config: ciCfg}
	return &odCfgRet
}

func buildClusterImplConfigForDNS(g *nameGenerator, addrStrs []string, mechanism DiscoveryMechanism) (string, *clusterimpl.LBConfig, []resolver.Address) {
	// Endpoint picking policy for DNS is hardcoded to pick_first.
	const childPolicy = "pick_first"
	retAddrs := make([]resolver.Address, 0, len(addrStrs))
	pName := fmt.Sprintf("priority-%v", g.prefix)
	for _, addrStr := range addrStrs {
		retAddrs = append(retAddrs, hierarchy.Set(resolver.Address{Addr: addrStr}, []string{pName}))
	}
	return pName, &clusterimpl.LBConfig{
		Cluster:     mechanism.Cluster,
		ChildPolicy: &internalserviceconfig.BalancerConfig{Name: childPolicy},
	}, retAddrs
}

// buildClusterImplConfigForEDS returns a list of cluster_impl configs, one for
// each priority, sorted by priority, and the addresses for each priority (with
// hierarchy attributes set).
//
// For example, if there are two priorities, the returned values will be
// - ["p0", "p1"]
// - map{"p0":p0_config, "p1":p1_config}
// - [p0_address_0, p0_address_1, p1_address_0, p1_address_1]
//   - p0 addresses' hierarchy attributes are set to p0
func buildClusterImplConfigForEDS(g *nameGenerator, edsResp xdsresource.EndpointsUpdate, mechanism DiscoveryMechanism, xdsLBPolicy *internalserviceconfig.BalancerConfig) ([]string, map[string]*clusterimpl.LBConfig, []resolver.Address, error) {
	drops := make([]clusterimpl.DropConfig, 0, len(edsResp.Drops))
	for _, d := range edsResp.Drops {
		drops = append(drops, clusterimpl.DropConfig{
			Category:           d.Category,
			RequestsPerMillion: d.Numerator * million / d.Denominator,
		})
	}

	priorities := groupLocalitiesByPriority(edsResp.Localities)
	retNames := g.generate(priorities)
	retConfigs := make(map[string]*clusterimpl.LBConfig, len(retNames))
	var retAddrs []resolver.Address
	for i, pName := range retNames {
		priorityLocalities := priorities[i]
		cfg, addrs, err := priorityLocalitiesToClusterImpl(priorityLocalities, pName, mechanism, drops, xdsLBPolicy)
		if err != nil {
			return nil, nil, nil, err
		}
		retConfigs[pName] = cfg
		retAddrs = append(retAddrs, addrs...)
	}
	return retNames, retConfigs, retAddrs, nil
}

// groupLocalitiesByPriority returns the localities grouped by priority.
//
// The returned list is sorted from higher priority to lower. Each item in the
// list is a group of localities.
//
// For example, for L0-p0, L1-p0, L2-p1, results will be
// - [[L0, L1], [L2]]
func groupLocalitiesByPriority(localities []xdsresource.Locality) [][]xdsresource.Locality {
	var priorityIntSlice []int
	priorities := make(map[int][]xdsresource.Locality)
	for _, locality := range localities {
		priority := int(locality.Priority)
		priorities[priority] = append(priorities[priority], locality)
		priorityIntSlice = append(priorityIntSlice, priority)
	}
	// Sort the priorities based on the int value, deduplicate, and then turn
	// the sorted list into a string list. This will be child names, in priority
	// order.
	sort.Ints(priorityIntSlice)
	priorityIntSliceDeduped := dedupSortedIntSlice(priorityIntSlice)
	ret := make([][]xdsresource.Locality, 0, len(priorityIntSliceDeduped))
	for _, p := range priorityIntSliceDeduped {
		ret = append(ret, priorities[p])
	}
	return ret
}

func dedupSortedIntSlice(a []int) []int {
	if len(a) == 0 {
		return a
	}
	i, j := 0, 1
	for ; j < len(a); j++ {
		if a[i] == a[j] {
			continue
		}
		i++
		if i != j {
			a[i] = a[j]
		}
	}
	return a[:i+1]
}

// rrBalancerConfig is a const roundrobin config, used as child of
// weighted-roundrobin. To avoid allocating memory everytime.
var rrBalancerConfig = &internalserviceconfig.BalancerConfig{Name: roundrobin.Name}

// priorityLocalitiesToClusterImpl takes a list of localities (with the same
// priority), and generates a cluster impl policy config, and a list of
// addresses.
func priorityLocalitiesToClusterImpl(localities []xdsresource.Locality, priorityName string, mechanism DiscoveryMechanism, drops []clusterimpl.DropConfig, xdsLBPolicy *internalserviceconfig.BalancerConfig) (*clusterimpl.LBConfig, []resolver.Address, error) {
	clusterImplCfg := &clusterimpl.LBConfig{
		Cluster:               mechanism.Cluster,
		EDSServiceName:        mechanism.EDSServiceName,
		LoadReportingServer:   mechanism.LoadReportingServer,
		MaxConcurrentRequests: mechanism.MaxConcurrentRequests,
		DropCategories:        drops,
		// ChildPolicy is not set. Will be set based on xdsLBPolicy NOPE
		ChildPolicy: xdsLBPolicy, // set this here...where is this converted?!?!?!?
	}


	// also
	// set addrs attributes
	// branch on endpoint weight? ring hash still has both

	var addrs []resolver.Address
	for _, locality := range localities {
		// only need this in certain cases
		var lw uint32 = 1
		if locality.Weight != 0 {
			lw = locality.Weight
		}
		localityStr, err := locality.ID.ToString()
		if err != nil {
			localityStr = fmt.Sprintf("%+v", locality.ID)
		}
		for _, endpoint := range locality.Endpoints {
			if endpoint.HealthStatus != xdsresource.EndpointHealthStatusHealthy && endpoint.HealthStatus != xdsresource.EndpointHealthStatusUnknown {
				continue
			}
			// it is possible to see locality weight attributes with different values for the same locality. - I'm assuming based on locality weights
			// We do not support this kind of an edge case and just use the weight in the first attribute we encounter.
			addr := resolver.Address{Addr: endpoint.Address}
			addr = hierarchy.Set(addr, []string{priorityName, localityStr})
			addr = internal.SetLocalityID(addr, locality.ID)
			addr = wrrlocality.SetAddrInfo(addr, wrrlocality.AddrInfo{LocalityWeight: lw})
			// branching logic of endpoint weight
			var ew uint32 = 1
			if endpoint.Weight != 0 {
				ew = endpoint.Weight
			}

			// this branch is ew * lw, see Marks comment about no-op
			// if ew != 0 (so only set if set) && weighed robin
			//     set weight as ew, I honestly think if we can assume lw is 1 or something in the case we're fine here
			// Would be zero so noop
			weightedroundrobin.SetAddrInfo(addr, weightedroundrobin.AddrInfo{Weight: lw * ew}) // already correct
			addrs = append(addrs, addr)
		}
	}
	// could also make this clusterImplConfig declared in line here
	return clusterImplCfg, addrs, nil


	// You can either try and migrate over (have two two until last PR) or do it all atomically and get e2e tests working etc.
	if xdsLBPolicy == nil || xdsLBPolicy.Name == roundrobin.Name { // branch right here - needs a new conditional
		// If lb policy is ROUND_ROBIN:
		// - locality-picking policy is weighted_target
		// - endpoint-picking policy is round_robin
		logger.Infof("xds lb policy is %q, building config with weighted_target + round_robin", roundrobin.Name)
		// Child of weighted_target is hardcoded to round_robin.
		wtConfig, addrs := localitiesToWeightedTarget(localities, priorityName, rrBalancerConfig) // creates a weighted target here
		// child policy is what arbitrary JSON becomes - same arbitrary JSON? this is a bc type not json? unmarshal
		// emited json than prepare this?
		// what's the layering with respect to representation in system:
		// internalserviceconfig.BalancerConfig vs. raw json representation

		// ping him when ready - give yourself a few hours to iterate on pending PR
		// test balancer instead of wrr_locality

		// Priority Balancer assumes this type anyway, I think changing this type will break the priority balancer right?

		// at this level just writes this field (and marshals)

		// the cluster impl layer you have the child type switching

		clusterImplCfg.ChildPolicy = &internalserviceconfig.BalancerConfig{Name: weightedtarget.Name, Config: wtConfig}
		return clusterImplCfg, addrs, nil
		// this you just stick as the child whatever you get i.e. wrr locality
		// so logic (of building config) can switch to not even need this branch? but you still want to populate addrs...
	}

	// perhaps fix the PR in flight and edit it and make it better?

	// regardless of config:
	// I think convert JSON -> internalserviceconfig.BalancerConfig (here or elsewhere?)
	// stick this as the child

	// populate addrs with weight unconditionally? Or only if you know it's ring hash?
	// i.e. what if you get something like ring_hash child switch round_robin child, you still
	// need to know ring hash right?

	// I think there's two behaviors that need to "switch", somewhat coupled in old system:
	// one: populating child of cluster impl switches from preparation for weighted target and
	// just stick it on in the case of ring hash TO always stick it on

	// two: used to not populate locality weights as part of address attributes, now you need to.
	// Thus, set these attributes. Still need to triage unconditionally or only in case of weighted target
	// since that's all that needs it. Weighted target || wrr_locality? Also think about my switch case. Populate unconditionally.

	// ask Java?

	// at some point you will need to convert from raw JSON to internalserviceconfig.BalancerConfig

	// If it is given an endpoint picking policy (like round_robin), it will
	// instead create a weighted target policy selection that contains the
	// endpoint picking policy selection as a child, creating a two level
	// hierarchy.
	// Creates an internalserviceconfig.BalancerConfig?

	// With the xDS client now creating a configuration that represents the
	// whole policy hierarchy, this special logic can be removed and the policy
	// selection received can be directly passed to the priority policy.
	// So pass it like Ring Hash?

	// Two questions for Eric:
	// Cluster Impl child graceful switch?
	// Also populate locality weights unconditionally?


	if xdsLBPolicy.Name == ringhash.Name {
		// This I still think you need whole thing

		// If lb policy is RIHG_HASH, will build one ring_hash policy as child.
		// The endpoints from all localities will be flattened to one addresses
		// list, and the ring_hash policy will pick endpoints from it.
		logger.Infof("xds lb policy is %q, building config with ring_hash", ringhash.Name)
		addrs := localitiesToRingHash(localities, priorityName) // this can still be built same way
		// Set child to ring_hash, note that the ring_hash config is from
		// xdsLBPolicy.
		clusterImplCfg.ChildPolicy = &internalserviceconfig.BalancerConfig{Name: ringhash.Name, Config: xdsLBPolicy.Config} // attaches ring hash name to config
		return clusterImplCfg, addrs, nil
	}

	// keep the old flow and migrate over?

	// localitiesToWeightedTarget(


	return nil, nil, fmt.Errorf("unsupported xds LB policy %q, not one of {%q,%q}", xdsLBPolicy.Name, roundrobin.Name, ringhash.Name)
}

// localitiesToRingHash takes a list of localities (with the same priority), and
// generates a list of addresses.
//
// The addresses have path hierarchy set to [priority-name], so priority knows
// which child policy they are for.
func localitiesToRingHash(localities []xdsresource.Locality, priorityName string) []resolver.Address {
	var addrs []resolver.Address
	for _, locality := range localities {
		var lw uint32 = 1 // defaults to 1...
		if locality.Weight != 0 {
			lw = locality.Weight // put this in the attribute
			// in wrr_locality:
			// type AddrInfo struct {
			//    uint32 locality weight
			// }
		}
		localityStr, err := locality.ID.ToString()
		if err != nil {
			localityStr = fmt.Sprintf("%+v", locality.ID)
		}
		for _, endpoint := range locality.Endpoints {
			// Filter out all "unhealthy" endpoints (unknown and healthy are
			// both considered to be healthy:
			// https://www.envoyproxy.io/docs/envoy/latest/api-v2/api/v2/core/health_check.proto#envoy-api-enum-core-healthstatus).
			if endpoint.HealthStatus != xdsresource.EndpointHealthStatusHealthy && endpoint.HealthStatus != xdsresource.EndpointHealthStatusUnknown {
				continue
			}

			var ew uint32 = 1
			if endpoint.Weight != 0 {
				ew = endpoint.Weight
			}

			// The weight of each endpoint is locality_weight * endpoint_weight.
			ai := weightedroundrobin.AddrInfo{Weight: lw * ew} // already sets locality_weight * endpoint_weight?
			addr := weightedroundrobin.SetAddrInfo(resolver.Address{Addr: endpoint.Address}, ai)
			addr = hierarchy.Set(addr, []string{priorityName, localityStr})
			addr = internal.SetLocalityID(addr, locality.ID)
			// addr.Attributes // attributes.Attributes, set
			// call Helper defined somewhere to put lw into addr.BalancerAttributes...this is now done unconditionally
			// now you have branch a populate branch b populate
			// now no branch, populate unconditionally and also set child unconditionally as well too.
			// ai := wrrlocality.AddrInfo{LocalityWeight: lw}
			addr = wrrlocality.SetAddrInfo(addr, wrrlocality.AddrInfo{LocalityWeight: lw}) // which type oh just address
			// addr.BalancerAttributes // consumption by the lb (yes, the wrr_locality_experimental balancer uses it to prepare children), doesn't affect uniqueness makes sense, so different part
			addrs = append(addrs, addr)
		}
	}
	// the cluster resolver will populate a new locality weight attribute for each address
	// The attribute will have the weight (as an integer) of the locality the address is part of.
	return addrs
}

// unconditionally - too hard to check
// Do it - automatically




// where does this get converted in system (i.e. raw JSON -> internalserviceconfig.BalancerConfig)

// clusterImplCfg.ChildPolicy = internalserviceconfig.BalancerConfig in struct

// localitiesToWeightedTarget takes a list of localities (with the same
// priority), and generates a weighted target config, and list of addresses.
//
// The addresses have path hierarchy set to [priority-name, locality-name], so
// priority and weighted target know which child policy they are for.
func localitiesToWeightedTarget(localities []xdsresource.Locality, priorityName string, childPolicy *internalserviceconfig.BalancerConfig) (*weightedtarget.LBConfig, []resolver.Address) { // config and builds addresses


	// no branching - stick config unconditionally


	// but now this logic changes - but the conditional is no longer there right?


	// *** This logic you can move to the balancer itself
	weightedTargets := make(map[string]weightedtarget.Target)
	var addrs []resolver.Address
	for _, locality := range localities {
		localityStr, err := locality.ID.ToString()
		if err != nil {
			localityStr = fmt.Sprintf("%+v", locality.ID)
		}

		// the cluster_impl does this now

		// this is locality weight, doesn't get taken into account, so there is two seperate codepaths
		weightedTargets[localityStr] = weightedtarget.Target{Weight: locality.Weight, ChildPolicy: childPolicy}
		// ^^^ different for both

		for _, endpoint := range locality.Endpoints {
			// Filter out all "unhealthy" endpoints (unknown and healthy are
			// both considered to be healthy:
			// https://www.envoyproxy.io/docs/envoy/latest/api-v2/api/v2/core/health_check.proto#envoy-api-enum-core-healthstatus).
			if endpoint.HealthStatus != xdsresource.EndpointHealthStatusHealthy && endpoint.HealthStatus != xdsresource.EndpointHealthStatusUnknown {
				continue
			}
			// *** shared
			addr := resolver.Address{Addr: endpoint.Address}
			// ***

			// needs a branch keeping this - determines attributes
			if childPolicy.Name == weightedroundrobin.Name && endpoint.Weight != 0 {
				ai := weightedroundrobin.AddrInfo{Weight: endpoint.Weight} // this is the only branch, this is set differmet
				addr = weightedroundrobin.SetAddrInfo(addr, ai)
			}

			// *** shared
			// addr = wrrlocality.SetAddrInfo(addr, wrrlocality.AddrInfo{LocalityWeight: lw}) theoretically wouldn't have locality weights at this point
			addr = hierarchy.Set(addr, []string{priorityName, localityStr})
			addr = internal.SetLocalityID(addr, locality.ID)
			addrs = append(addrs, addr)
			// *shared
		}
	}
	return &weightedtarget.LBConfig{Targets: weightedTargets}, addrs
	// ***




}
