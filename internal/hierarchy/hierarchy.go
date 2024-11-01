/*
 *
 * Copyright 2020 gRPC authors.
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

// Package hierarchy contains functions to set and get hierarchy string from
// addresses.
//
// This package is experimental.
package hierarchy

import (
	"google.golang.org/grpc/resolver"
)

type pathKeyType string

const pathKey = pathKeyType("grpc.internal.address.hierarchical_path")

type pathValue []string

func (p pathValue) Equal(o any) bool {
	op, ok := o.(pathValue)
	if !ok {
		return false
	}
	if len(op) != len(p) {
		return false
	}
	for i, v := range p {
		if v != op[i] {
			return false
		}
	}
	return true
}

// What does "Produce addresses" mean? and where does it happen under the xDS resolver?
// How can I tell where this is done?
// I think for sure EDS, where else?

// Produce addresses call to wrap in endpoints:

// 3. Update EDS to output Endpoints with multiple Addresses - after
// So EDS, but does the full address tree come out of the xDS Resolver or is there
// other instances of getting added to in the xDS tree...

// Doug - "As mentioned in other chat, instead of Address.BalancerAttributes, use Endpoint.Attributes"
func GetEndpoint(endpoint resolver.Endpoint) []string {
	attrs := endpoint.Attributes
	if attrs == nil {
		return nil
	}
	path, _ := attrs.Value(pathKey).(pathValue)
	return ([]string)(path)
} // each node groups it, I wonder where it's set mirror the setting of addrs and wrap it all in resolver stuff

// 1. Mirror all these calls throughout the xDS tree...

// 2. Produce Endpoints anywhere Addresses are produced.  Forward Endpoints anywhere Addresses are forwarded.

// Produce endpoints anywhere Addresses are produced (maybe have a helper that takes endpoints and wraps with addresses, what will that be going forward)

// Try to find all occurrences in the xDS tree

// call this helper from everywhere that produces addresses

// Put this in resolver maybe? He already does this from top level operations in channel
func wrapAddressesInEndpoints(addrs []resolver.Address) []resolver.Endpoint { // 1:1, need to do this every time produces addresses and keep parallel flow alongside it as Doug explained with []resolver.Address
	ret := make([]resolver.Endpoint, len(addrs))
	for i, addr := range addrs {
		ep := resolver.Endpoint{
			// Wrap one address, 1:1
			Addresses:  []resolver.Address{addr}, // If anything copied attributes are already in this thing...
			Attributes: nil,                      // Do I need to copy these from somewhere? Will this affect WRR
			// We don't have per-address attributes in Java. Our attributes are per-endpoint. So we didn't face anything similar, best as I can tell from your explanation.
		}
		ret[i] = ep

		// resolver.Endpoint{}
		// where to get endpoint attributes from?
		// create endpoint
		// endpoint.Addresses = append(endpoint.Addresses, addr)
		// append endpoint

		// addr. // what do you put in, I think you just append the whole thing
	}
	return ret
} // so call this everytime we create addrs and stick it in resolver.State struct...

// Forward endpoints anywhere Addresses are forwarded...so stick it in resolver.State structs?
// in all cases and see if resolver.States "link", and if it's all good there then we're good
// breakage in resolver.State -> resolver.State, link these two if not 1:1
// and adding new addresses needs to add endpoints to resolver.State

// Also do xDS Client metrics which takes higher priority but I think I can do both...

// hierarchy.Get()...is the symbol name...

// Rewrite all the unit tests too?

// Get returns the hierarchical path of addr.
func Get(addr resolver.Address) []string { // sticks it as a balancer attribute, and you get it and see what's up, maybe it needs to become a resolver attribute...
	attrs := addr.BalancerAttributes
	if attrs == nil {
		return nil
	}
	path, _ := attrs.Value(pathKey).(pathValue)
	return ([]string)(path)
}

func SetEndpoint(endpoint resolver.Endpoint, path []string) resolver.Endpoint {
	endpoint.Attributes = endpoint.Attributes.WithValue(pathKey, pathValue(path)) // separate set so I'm fine here...
	return endpoint
}

// This is how weighted target, cluster manager, and priority actually unit test this...
// cluster manager right under client channel,
// priority splits addresses into different localities...
// weighted target splits the priority into different localities...

// Run tests on branch that fail and if it passes that works...

// slice of addresses in context, switch to addresses and endpoints

// Set overrides the hierarchical path in addr with path.
func Set(addr resolver.Address, path []string) resolver.Address { // goes through set in cluster resolver, but then should write it...oh it's because child gets endpoints now...
	addr.BalancerAttributes = addr.BalancerAttributes.WithValue(pathKey, pathValue(path))
	return addr
}

// Group splits a slice of addresses into groups based on
// the first hierarchy path. The first hierarchy path will be removed from the
// result.
//
// Input:
// [
//
//	{addr0, path: [p0, wt0]}
//	{addr1, path: [p0, wt1]}
//	{addr2, path: [p1, wt2]}
//	{addr3, path: [p1, wt3]}
//
// ]
//
// Addresses will be split into p0/p1, and the p0/p1 will be removed from the
// path.
//
// Output:
//
//	{
//	  p0: [
//	    {addr0, path: [wt0]},
//	    {addr1, path: [wt1]},
//	  ],
//	  p1: [
//	    {addr2, path: [wt2]},
//	    {addr3, path: [wt3]},
//	  ],
//	}
//
// If hierarchical path is not set, or has no path in it, the address is
// dropped.
func Group(addrs []resolver.Address) map[string][]resolver.Address { // Same question here what do I do about unit tests?
	ret := make(map[string][]resolver.Address)
	for _, addr := range addrs {
		oldPath := Get(addr) // gets it from the balancer attributes, split it up as such, so essentially just have a mirror down the tree?
		if len(oldPath) == 0 {
			continue
		}
		curPath := oldPath[0]
		newPath := oldPath[1:]
		newAddr := Set(addr, newPath)
		ret[curPath] = append(ret[curPath], newAddr) // takes something off the path, and then groups it by that...
	}
	return ret
}

// All of the state update linkages resolver.State -> resolver.State and every time you copy addresses over
// need that to happen...

// Doug - "As mentioned in other chat, instead of Address.BalancerAttributes, use Endpoint.Attributes"

// Do I need to add unit tests for these new helpers or switch the old ones or just nothing at all?
func GroupEndpoints(endpoints []resolver.Endpoint) map[string][]resolver.Endpoint { // does this make sense? What's the fundamental point of this helper?
	// Address.BalancerAttributes

	// Endpoint.Attributes?

	// I think endpoints because each node partitions the endpoint list...and just 1:1
	// with passing addresses around endpoint(one addr) equivalent to one addr from Mark
	ret := make(map[string][]resolver.Endpoint)
	for _, endpoint := range endpoints {
		oldPath := GetEndpoint(endpoint) // now on endpoint attributes GetEndpoint and SetEndpoint and do it across whole tree
		if len(oldPath) == 0 {
			continue
		}
		curPath := oldPath[0]
		newPath := oldPath[1:]
		newEndpoint := SetEndpoint(endpoint, newPath)
		ret[curPath] = append(ret[curPath], newEndpoint)
	} // pulls a node out of the list...discrete data structures lol...and puts them into a map from key
	return ret
} // and you split the endpoints by path and send it down as such just as you do addresses

// how to switch this to work on endpoints, or do you just read into the endpoint and call
// above?
// 3. Update EDS to output Endpoints with multiple Addresses - after

// Cluster manager, priority, and weighted target

// Mirror of splitting?

// How does it work in weighted target and where to get from EDS?

// Doug seems fine with overall concept of forwarding resolver.States
// Pass both parallely down the tree, make sure my e2e tests pass as a result
