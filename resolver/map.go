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

package resolver

import "google.golang.org/grpc/resolver"

type addressMapEntry struct {
	addr  Address
	value any
}

// draw out these data structures
// this list is for attributes

// EndpointMap is a map of endpoints to arbitrary values either not taking or taking into account attributes

// comment about how you compare unordered list of endpoints...
type EndpointMap struct { // now that we know it doesn't take into account attributes...equality check on unordered set. Quick qay to sort?
	endpoints endpointNodeList // cannot use maps as a map key. Thus, keep a list, and o(n) search. Not on RPC fast path so not as important.
}

func (ek endpointKey) Equal(ek2 endpointKey) bool {
	// en.addrs // map[string]int
	// en2.addrs // map[string]int (how to do equality on these?)
	// how to do map equality and should take into account number of addresses...counts don't hurt
	// do it manually, Doug is fine with counts, don't trust reflect.DeepEqual
	if len(ek) != len(ek2) {
		return false
	}
	for addr, count := range ek {
		if count2, ok := ek2[addr]; !ok || count != count2 {
			return false
		}
	} // make this fast, will be done many times
	return true
}

// for the sake of scope resolver.AddressMap only shared util to add, group
// maybe come later and won't let this get done

// from the lowest level...
// attributes don't work as a key, so I don't think my map will work either

// equality is on map[string]count (count for defensive, needs a contains regardless) (this is what you build on
// build out unordered map as you build out entry, to not have to do it on every equal call?
type endpointNode struct {
	// does this need to persist anything else?
	addrs map[string]int // is this the only thing to persist? (build at init time)
	val any
} // build this out at init time...
// construct this as construction time

func (en endpointNode) Equal(en2 endpointNode) bool {
	// en.addrs // map[string]int
	// en2.addrs // map[string]int (how to do equality on these?)
	// how to do map equality and should take into account number of addresses...counts don't hurt
	// do it manually, Doug is fine with counts, don't trust reflect.DeepEqual
	if len(en.addrs) != len(en2.addrs) {
		return false
	}
	for addr, count := range en.addrs {
		if count2, ok := en2.addrs[addr]; !ok || count != count2 {
			return false
		}
	} // make this fast, will be done many times
	return true
}

type endpointKey map[string]int

func toEndpointNode(endpoint Endpoint) endpointNode { // what happens if you put an endpoint with no addresses in?
	en := make(map[string]int) // what happens on a nil panic?
	// do something with server name?
	for _, addr := range endpoint.Addresses {
		if count, ok := en[addr.Addr]; ok {
			en[addr.Addr] = count + 1
		}
		en[addr.Addr] = 1
	}
	return endpointNode{ // new stuff on heap
		addrs: en,
	}
}


// persist all data below
// highest level:
// map[endpoint]any
// api level set, get on resolver.Endpoint (can mutate into something persisted)
func (em *EndpointMap) Get2(endpoint Endpoint) {
	// either a list or a map[unordered set]set
	endpoint.Addresses // [] addresses -> unordered set of addresses...use that to lookup
}

func (e *Endpoint) Equal(e2 *Endpoint) bool { // then cmp will call this...
	e.Attributes // ignore
	/*
	type Address struct {
	    Addr               string
	    ServerName         string
	    Attributes         *attributes.Attributes
	    BalancerAttributes *attributes.Attributes
	    Metadata           any
	}
	*/
	// I think just use Addr
	var addrs map[string]int // if you want number of addresses - account for endpoint, this doesn't make it slower right?
	e.Addresses // []addresses
	for _, addr := range e.Addresses {
		if count, ok := addrs[addr.Addr]; ok {
			addrs[addr.Addr] = count + 1
		}
	}

	var addrs2
	e2.Addresses // []addresses
}

// if it takes into account attribute thsi new call makes sense
func NewEndpointMap() *EndpointMap {
	return &EndpointMap{/*what internal state does this need? can append to nil slices*/}
}

// Regardless of the exact internals of how this plumbs, it's still the same API


// top level api...should I persist some sort of endpoint type

// Get returns the value for the address in the map, if present.
func (em *EndpointMap) Get(e Endpoint/*what object do you pass in? I think endpoint regardless of what you use - w/e you use will be encapsulated in struct*/) (value any, ok bool) {
	// e.Attributes // *attributes.Attributes
	// e.Addresses // []Address
	en := toEndpointNode(e)
	if i := em.endpoints.find(en); i != -1 {
		return em.endpoints[i].val, ok
	}
	return nil, false
}

// updates or adds...
// Set updates or adds the value to the address in the map.
func (em *EndpointMap) Set(e Endpoint, value any) {
	em.endpoints // []endpointNode (or have this slice be a built in type)
	// find the endpoint in the list...
	// why does he do it as a list
	// map[endpointNode]any...how would this do map comparisons?

	// do the conversion once here, need to use this later as well allocate on heap
	en := toEndpointNode(e)
	if i := em.endpoints.find(en); i != -1 {
		em.endpoints[i].val = value
		return
	}
	en.val = value
	em.endpoints = append(em.endpoints, en)

	/*for i, endpoint := range em.endpoints { // could put this on a helper on the endpoint type
		if endpoint.Equal(e) // return this index
	}*/
}

type endpointNodeList []endpointNode

// find returns the index of endpoint in the endpointNodeList slice, or -1 if not
// present.
func (l endpointNodeList) find(e endpointNode) int {
	for i, endpoint := range l {
		if endpoint.Equal(e) {
			return i
		}
	}
	return -1
} // Oh I'll need to write a bunch of unit tests for this, maybe do that.

// AddressMap is a map of addresses to arbitrary values taking into account
// Attributes.  BalancerAttributes are ignored, as are Metadata and Type.
// Multiple accesses may not be performed concurrently.  Must be created via
// NewAddressMap; do not construct directly.
type AddressMap struct {
	// The underlying map is keyed by an Address with fields that we don't care
	// about being set to their zero values. The only fields that we care about
	// are `Addr`, `ServerName` and `Attributes`. Since we need to be able to
	// distinguish between addresses with same `Addr` and `ServerName`, but
	// different `Attributes`, we cannot store the `Attributes` in the map key.
	//
	// The comparison operation for structs work as follows:
	//  Struct values are comparable if all their fields are comparable. Two
	//  struct values are equal if their corresponding non-blank fields are equal.
	//
	// The value type of the map contains a slice of addresses which match the key
	// in their `Addr` and `ServerName` fields and contain the corresponding value
	// associated with them.
	m map[Address]addressMapEntryList
}

func toMapKey(addr *Address) Address {
	return Address{Addr: addr.Addr, ServerName: addr.ServerName}  // uses reflect.DeepEqual on the map so it's fine...map[string]whatever
}

type addressMapEntryList []*addressMapEntry

// NewAddressMap creates a new AddressMap.
func NewAddressMap() *AddressMap {
	return &AddressMap{m: make(map[Address]addressMapEntryList)}
}

// find returns the index of addr in the addressMapEntry slice, or -1 if not
// present.
func (l addressMapEntryList) find(addr Address) int {
	for i, entry := range l {
		// Attributes are the only thing to match on here, since `Addr` and
		// `ServerName` are already equal.
		if entry.addr.Attributes.Equal(addr.Attributes) {
			return i
		}
	}
	return -1
}

// Get returns the value for the address in the map, if present.
func (a *AddressMap) Get(addr Address) (value any, ok bool) { // moving it over to this including wrapped scs so use this
	addrKey := toMapKey(&addr)
	entryList := a.m[addrKey]
	if entry := entryList.find(addr); entry != -1 {
		return entryList[entry].value, true
	}
	return nil, false
}

// Set updates or adds the value to the address in the map.
func (a *AddressMap) Set(addr Address, value any) {
	addrKey := toMapKey(&addr)
	entryList := a.m[addrKey]
	if entry := entryList.find(addr); entry != -1 {
		entryList[entry].value = value
		return
	}
	a.m[addrKey] = append(entryList, &addressMapEntry{addr: addr, value: value})
}

// Delete removes addr from the map.
func (a *AddressMap) Delete(addr Address) {
	addrKey := toMapKey(&addr)
	entryList := a.m[addrKey]
	entry := entryList.find(addr)
	if entry == -1 {
		return
	}
	if len(entryList) == 1 {
		entryList = nil
	} else {
		copy(entryList[entry:], entryList[entry+1:])
		entryList = entryList[:len(entryList)-1]
	}
	a.m[addrKey] = entryList
}

// Len returns the number of entries in the map.
func (a *AddressMap) Len() int {
	ret := 0
	for _, entryList := range a.m {
		ret += len(entryList)
	}
	return ret
}

// similar to Mark's subchannel list and endpoint list...
func (em *EndpointMap) Len() int {
	return len(em.endpoints) // yes unique to those keys
}

/*

uses as a key for the same set, so I think need to rebuild the endpoint data
structure. Unordered set, comment endpoint
returned is not usable for other things but indexing map.


for _, addr := range b.subConns.Keys() {
		if _, ok := addrsSet.Get(addr); ok {
*/

// Keys returns a slice of all current map keys.
func (em *EndpointMap) Keys() []Endpoint { // should this build out the full endpoint data structure?
	ret := make([]Endpoint, 0, len(em.endpoints))
	for _, en := range em.endpoints {
		// key of endpoint node, need to convert to endpoint type
		// en.addrs // build out an endpoint from this
		// en.val // ignore
		var endpoint Endpoint // resolver? yeah resolver.Address
		for addr, count := range en.addrs { // reconstruction, maybe pull this out to a helper
			for i := 0; i < count; i++ {
				endpoint.Addresses = append(endpoint.Addresses, resolver.Address{
					Addr: addr,
				})
			}
			/*address.Addr
			endpoint.Addresses // []Address*/
		}
		ret = append(ret, endpoint)
	}
	return ret
}

// how are these used? I think make it general purpose so don't type to a balancer
func (em *EndpointMap) Values() []any { // can I type this to a balancer? It will always need a balancer? Should this really be generic?
	ret := make([]any, 0, len(em.endpoints))
	for _, en := range em.endpoints {
		ret = append(ret, en.val)
	}
	return ret
}

func (em *EndpointMap) Delete(e Endpoint) {
	// find endpoint and delete?
	en := toEndpointNode(e)
	entry := em.endpoints.find(en)
	if entry == -1 {
		return
	}
	if len(em.endpoints) == 1 {
		em.endpoints = nil
	}
	// delete these two
	copy(em.endpoints[entry:], em.endpoints[entry+1:])
	em.endpoints = em.endpoints[:len(em.endpoints)-1]
}

// Keys returns a slice of all current map keys.
func (a *AddressMap) Keys() []Address {
	ret := make([]Address, 0, a.Len())
	for _, entryList := range a.m {
		for _, entry := range entryList {
			ret = append(ret, entry.addr)
		}
	}
	return ret
}

// Values returns a slice of all current map values.
func (a *AddressMap) Values() []any {
	ret := make([]any, 0, a.Len())
	for _, entryList := range a.m {
		for _, entry := range entryList {
			ret = append(ret, entry.value)
		}
	}
	return ret
}

// reflect deep.Equal, panic if can't do equality
// map[key]value
