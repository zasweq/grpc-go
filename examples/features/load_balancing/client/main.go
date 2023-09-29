/*
 *
 * Copyright 2018 gRPC authors.
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

// Binary client is an example client.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/resolver/manual"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/credentials/insecure"
	ecpb "google.golang.org/grpc/examples/features/proto/echo"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

const (
	exampleScheme      = "example"
	exampleServiceName = "lb.example.grpc.io"
)

var addrs = []string{"localhost:50051", "localhost:50052"}

func callUnaryEcho(c ecpb.EchoClient, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := c.UnaryEcho(ctx, &ecpb.EchoRequest{Message: message})
	if err != nil {
		log.Fatalf("could not greet: %v", err)
	}
	fmt.Println(r.Message)
}

func makeRPCs(cc *grpc.ClientConn, n int) {
	hwc := ecpb.NewEchoClient(cc)
	for i := 0; i < n; i++ {
		callUnaryEcho(hwc, "this is examples/load_balancing")
	}
}
// rewrite just the example in a seperate repo to use this?
func main() {
	mr := manual.NewBuilderWithScheme("example")
	defer mr.Close()
	// helper to parse...just don't have service config?
	// json :=
	addrs := []resolver.Address{
		{Addr: "localhost:50051"},
		{Addr: "localhost:50052"},
	}
	mr.InitialState(resolver.State{
		Addresses: addrs, // specific addresses you want to throw in
	}) // should pick up pick first by default rihgT?

	// dependent on scheme
	//target := fmt.Sprintf("%s:///%s", mr.Scheme(), exampleServiceName)
	target := fmt.Sprintf("%s:///", mr.Scheme())

	// "pick_first" is the default, so there's no need to set the load balancing policy.
	cc, err := grpc.Dial(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer cc.Close()

	fmt.Println("--- calling helloworld.Greeter/SayHello with pick_first ---")
	print("96")
	makeRPCs(cc, 10)

	fmt.Println()
	print("100")

	json := `{"loadBalancingConfig": [{"round_robin":{}}]}`
	// Make another ClientConn with round_robin policy.
	sc := internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(json)
	print("addrs: ", addrs)
	print("sc: ", sc)

	// Update the manual resolvers state. This will cause the load balancing
	// policy of the channel to switch. This will cause a different
	// distribution. This call blocks until it's updated?

	// comment about how you specify sc through manual resolver...?
	mr.InitialState(resolver.State{ // blocks until it finishes processing?
		Addresses: addrs,
		ServiceConfig: sc,
	})

	/*roundrobinConn, err := grpc.Dial(
		target,
		grpc.WithResolvers(mr),
		// grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`), // This sets the initial balancing policy.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer roundrobinConn.Close()*/

	fmt.Println("--- calling helloworld.Greeter/SayHello with round_robin ---")
	makeRPCs(cc, 10)

	// You can also plug in your own custom lb policy, which needs to be
	// configurable. This n is configurable. Try changing it and see how the
	// behavior changes.
	json = `{"loadBalancingConfig": [{"custom_round_robin":{"n": 3}}]}` // I guess the parsing pulls it from registry
	sc = internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(json)
	mr.UpdateState(resolver.State{
		Addresses: addrs,
		ServiceConfig: sc,
	}) // coming out of name resolver the service config.
	// You don't need to redial to pick up new service config, it goes through manual resolver
	/*customroundrobinConn, err := grpc.Dial( // does this pick up the mr, do I even need to declare service config?
		target,
		grpc.WithResolvers(mr),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer customroundrobinConn.Close()*/
	fmt.Println("--- calling helloworld.Greeter/SayHello with custom_round_robin ---")
	// after the resolver state update, does this block...to make this work?
	makeRPCs(customroundrobinConn, 20)
}

func init() {
	balancer.Register(&customRoundRobinBuilder{})
}

// Get this working then move over.
const customRRName = "custom_round_robin"

type customRRConfig struct {
	serviceconfig.LoadBalancingConfig `json:"-"`

	// N represents how often pick iterations chose the second SubConn
	// in the list. Defaults to 3.
	N uint32 `json:"n,omitempty"`
}

func (customRoundRobinBuilder) ParseConfig(s json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	lbConfig := &customRRConfig{
		N: 3,
	}
	if err := json.Unmarshal(s, lbConfig); err != nil {
		return nil, fmt.Errorf("custom-round-robin: unable to unmarshal customRRConfig: %v", err)
	}
	return lbConfig, nil
}

type customRoundRobinBuilder struct{}

func (customRoundRobinBuilder) Name() string {
	return customRRName
}

func (customRoundRobinBuilder) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	// create pick first builder - honestly could go to the middle object
	pfBuilder := balancer.Get(grpc.PickFirstBalancerName)
	if pfBuilder == nil {
		return nil
	}
	return &customRoundRobin3{
		bOpts: bOpts,
		cse: &balancer.ConnectivityStateEvaluator{},
		state: connectivity.Connecting, // start off as connecting?
		pfs: resolver.NewEndpointMap(),
		pickFirstBuilder: pfBuilder,
	}
	/*return &customRoundRobin2{
		ClientConn: cc,
		cc: cc,
		bOpts: bOpts,
		cse: &balancer.ConnectivityStateEvaluator{},
		// Hardcoded initial state and picker.
		state: connectivity.Connecting,
		picker: newErrPicker(balancer.ErrNoSubConnAvailable),

		subConns: resolver.NewAddressMap(),
		pfs: resolver.NewEndpointMap(), // heap allocation, so allocate all you need
		scStates: make(map[balancer.SubConn]connectivity.State),

		pickFirstBuilder: pfBuilder,
	}*/
}

var logger = grpclog.Component("example")

// same behavior, but implements the full balancer.Balancer API
// same behavior as base but don't call upward
// call NewSubConn and connect() and stuff, will need to change after pick first becomes petiole
// same connectivity semantics on updating on a READY, how to keep deterministic?
// he's fine with copying all of the logic into here sans Build
// Doesn't need to be 1:1

// as much complexity as needed
type customRoundRobin2 struct { // move this to own seperate package - more complicated the better
	// embed a ClientConn to wrap only UpdateState() operation
	balancer.ClientConn // I think can just use cc...

	cc balancer.ClientConn // initialized, and then read only after that need this to update
	bOpts balancer.BuildOptions
	cse *balancer.ConnectivityStateEvaluator

	state connectivity.State
	picker balancer.Picker

	// pick firsts - but not balancer group
	pfs *resolver.EndpointMap

	// map[endpoint]balancerWrapper (which wraps pick first)

	subConns *resolver.AddressMap // shows the specificity of the SCs


	scStates map[balancer.SubConn]connectivity.State
	// keyed on something that represents a subbalancer...
	// whether a uniquely generated id or pick first
	// the thing that triggers pick first is endpoint uniqueness,

	// peristed multiple pick firsts as values of resolver.Endpoint map

	// pick first that implements interface can be used as key...
	// what you persist of the key is shown below

	// key is pick first instance, value of pfs
	pickers map[balancer.Balancer]balancer.State // aggregated state? has the picker and the connectivity state can just loop over this

	// needs both
	resolverErr error
	connErr error

	n uint32 // persisted in UpdateClientConnState, read in UpdateState to persist picker so needs a mutex grab before and after read building picker because not calling out into another system

	pickFirstBuilder balancer.Builder // to build pick first children...constantly allocates new memory on the heap

	inhibitPickerUpdates bool // either protect access with a mutex or event loop...
	// read in UpdateState, which is async with balancer.Balancer
}

// Either here or above: note that the balancer.Balancer API has a guarantee to
// be called synchrously. Thus, all the state/logic of each operation doesn't
// need a mutex and doesn't race.

// update client conn state with numerous addresses
func (crr2 *customRoundRobin2) UpdateClientConnState2(state balancer.ClientConnState) error {
	// how does this split subconns? Defer to bg?
	endpointSet := resolver.NewEndpointMap() // new endpoint map...used only for presence checks later in code

	// inhibit picker updates until end then send...I don't think need a run() (event loop)...
	crr2.inhibitPickerUpdates = true

	state.ResolverState.Endpoints // []Endpoint
	for _, endpoint := range state.ResolverState.Endpoints {
		// pass endpoints and all it's data essentially to endpoint set
		endpointSet.Set(endpoint, nil) // for presence checks

		// uncondtionally update...

		// if in map already:
		pf, ok := crr2.pfs.Get(endpoint)
		var pickFirst balancer.Balancer
		if ok { //
			pickFirst = pf.(balancer.Balancer)
		} else { // not present create a new pick first
			// pfBuilder := balancer.Get(grpc.PickFirstBalancerName) // or persist this in the balancer
			pickFirst = crr2.pickFirstBuilder.Build(crr2, crr2.bOpts) // embed everything but UpdateState()...
			crr2.cse.RecordTransition(connectivity.Shutdown, connectivity.Idle)
		}

		// Update pick first child unconditionally...so it can see any reorderings or attributes. Way to much overhead to check a difference before sending down.
		// This can cause an inline UpdateState...any sync requirements I don't think so because no mutex grab...

		// Could get numerous picker updates...but needs to be sync. Inhibit
		// until you can send a singular picker update upward?
		pickFirst.UpdateClientConnState(balancer.ClientConnState{
			ResolverState: resolver.State{
				Endpoints: []resolver.Endpoint{endpoint},
				Attributes: state.ResolverState.Attributes,
			},
			// no service config, top level balancer of channel
		})
		/*
		if pf, ok := crr2.pfs.Get(endpoint); ok { // uncondtionally update exisiting pick first
			// do something with pick first...i.e. unconditonally update addresses
			pickFirst
		} else { // create new pick first and give it it's first address update

			/*pf, _ := crr2.pfs.Get(endpoint)
			pickFirst := pf.(balancer.Balancer)

			// create a new pick first balancer here...

			pickFirst.UpdateClientConnState(balancer.ClientConnState{
				ResolverState: resolver.State{
					Endpoints: []resolver.Endpoint{endpoint},
					Attributes: state.ResolverState.Attributes,
				},
				// no service config, top level balancer of channel
			})
		}*/
		// or do it after

		// else
		/*
		if _, ok := crr2.pfs.Get(endpoint); !ok { // A new Endpoint is presence. Create a new pick first corresponding.
			// Should this persist a builder? That it constantly calls? Or does it a new one per?
			// Look at cluster resolver to see how it treats it's builder...a new json every time?

			// create a new pick first (from builder)

			endpoint.Addresses // []addresses...sent down to pick first one received

			// on future updates send unconditionally to send down new ordering
			// + attributes...keyed by endpoint key and that sends down the
			// addresses uncondidtionally to that policy
			ccs := &balancer.ClientConnState{
				ResolverState: resolver.State{
					// should it just send down the endpoint
					Endpoints: []resolver.Endpoint{endpoint}, // forward all the attributes or just this?
				},
				// BalancerConfig: , // address list randomization off I think so? no config, balancer already created...
				// comment specify service config through resolver
			}

			pf, _ := crr2.pfs.Get(endpoint) //
			pickFirst := pf.(balancer.Balancer) // typecast to interface...
			// you have a ref from above, put it in the map

			// because we do not have or anticipate any use cases where a petiole policy will need to enable shuffling
			pickFirst.UpdateClientConnState(balancer.ClientConnState{
				ResolverState: resolver.State{
					Endpoints: []resolver.Endpoint{endpoint},
					Attributes: state.ResolverState.Attributes, // forward top level attributes down...
				},
			}) // it's the singular endpoint with all it's addresses, I think just pass the endpoint down will contain the address
		}*/ // user wraps to get happy eyeballs + health checking, but can interact with health checking.

		// what to do about pickers from below?

		// each iteration scoped to endpoint, endpoint contains addresses, that
		// gets sent down to the pick first

		// creates a pick first child for every endpoint(address, address)
		// endpoint.Addresses // [] addresses
		// endpoint.Attributes

		// if no longer present in the map

		// address attributes are important attributes on an address

		// unordered set + attributes. Attributes that affect address uniqueness are at address level

		// balancer attributes is for lb policies...

		// nothing should care about addresses except for channel and pick first

		// key endpoint equality list of addresses, compare address string and attributes also server name

	}
	// not balancer group!

	// after endpoint iteration
	// Delete any PickFirsts for endpoints no longer present in list
	for _, e := range crr2.pfs.Keys() { // implement Keys
		ep, _ := crr2.pfs.Get(e)
		pickFirst := ep.(balancer.Balancer)
		// pick first was removed by resolver (unique endpoint logically corresponding to pick first was removed)
		if _, ok := endpointSet.Get(e); !ok {
			pickFirst.Close() // I think this is the only logical "destruction" that needs to happen...
		}
	}
	// inhibit picker updates...need a mutex if not sync with event loop

	crr2.inhibitPickerUpdates = false
	crr2.regeneratePicker() // do this in one operation...update state *and set to false* (look at event loop of priority)
	// I think operation to build picker(), and also operation to updateState() upward




	// if new...create new subconn or in this case pick first
	pfBuilder // can I use same builder for balancer child?
	pfBuilder := balancer.Get(grpc.PickFirstBalancerName) // can I have this dependency.....yes lol gRPC doesn't import this
	pfBuilder.Name()
	// when I have a policy...how does the pick first talk to it? Does it need to intercept calls
	// pf pf pf pf UpdateState from each, need to concatanate...so need the state to aggregate...

	// only need update state interception...unless you use balancer group then
	// that consilidates?
	pfBuilder.Build(crr2, crr2.bOpts/*persisted build options...*/)
	// and the *pick first* creates sub conn

	// how do I persist a lot of pick firsts...should I use balancer group?


	// if deleted, delete pick first...
	// so the any value would be a **pick first itself**

	var pfBal balancer.Balancer
	pfBal.Close() // this is all you need any other cleanup? Logical destructor...

}

// needs an intermediary to id pick first balancer calling it
// either a. wrap client conn with something (instance id), then need new client conn instance every time
//
// b. wrap balancer, use that as key, I think use this since lifecycle of new pick first anyway
type balancerWrapper struct {
	balancer.Balancer // waht to do with this...
	bal balancer.Balancer // embed or hold ref

	crr customRoundRobin2 // give it ref at init time

	state balancer.State // persist this
	// map[endpoint]pickFirst...what if pickFirst had a wrapper of state...what to do about state transitions?

}

func (bw *balancerWrapper) UpdateState(state balancer.State) {
	state.ConnectivityState // connectivity.State, moving around in state space, similar to UpdateSubConnState()
	state.Picker

	// michael said same logic as subconns except now with balancers...
	// aggregated connectivity states?
	// record transitions?
	// selectively update upward based on state logic?

	// for simiplicitly...
	// no sticky tf
	// uncondtionally update picker, it's an example, no need for complicated logic...


	// 5 possible connectivity states
	connectivity.Shutdown // can this even come up from the balancer?
	// Close() is only thing that can close, possibily leak? connectivity.Shutdown
	// map[endpoint] -> balancer wrapper (does this need to delete?) can it delete itself? What happens to endpoint?
	connectivity.Idle
	connectivity.Connecting
	connectivity.TransientFailure
	connectivity.Ready



	// mutex protection? calls come sync in balancer.Balancer and async in UpdateState
	oldState := bw.state.ConnectivityState // reads old state into new
	// bw.state = previous, state = current...
	newState := state.ConnectivityState
	bw.state = state // persists picker...

	// these can all be async updated
	/*state := (do I need this state otherwise?)*/
	/*round robin connectivity.State*/

	// reading fields from top level, I think same sync issues though as if you
	// had accessed directly...

	// how to sync...this is all the aggergated
	// need two ready subconns and overall a ready state
	bw.crr.state = bw.crr.cse.RecordTransition(oldState, newState)


	bw.crr.regeneratePicker()

}



// doesn't need to wrap SubConns or wrap state listeners so doesn't need to
// intercept new subconn and subsribe to state.
func (crr2 *customRoundRobin2) UpdateState(state balancer.State) {


	// buildPicker() - either one operation or two seperate operations
	// updatePicker()
	// wait no this comes in the operation to create a picker update
	if crr2.inhibitPickerUpdates { // needs sync, either from mutex or run this in an event loop (async event, and serializes them)
		// log it's ignoring - this flow also comes in from resolver error
		return
	}
	// end operation of creating a picker update





	// idToPickerState

	// map[string]*weightedPickerState

	// but you don't know which child balancer it came from...
	crr2.pickers // map[balancer.Balancer]scState // don't know balancer unless you wrap pick first with an id...this might be a nesseary concequence

	// what does a READY picker represent?
	// loop through and send back the ready ones? but then call
	// the algorithm on the picker.Pick() hierarchy...?

	// aggregator persists whole state...including connecitivity state and picker
	// also persists the aggregated state but I don't know how to do this...
	state.ConnectivityState // there can be multiple connectivity states...

	state.Picker // need to consildate all pickers from children...how do I persist these?
	// scStates is a map[balancer.SubConn]connectivity.State
	// map[child balancer?]picker...
	// consildate ready pickers into one? do a n distribution and build out this picker...

}



// child states now affect


// even for non petiole policies you need to change resolver.AddressMap key type

// resolver.AddressMap, where instead of the key being address
// it's a map key where ordering doesn't matter

func (crr2 *customRoundRobin2) UpdateClientConnState(state balancer.ClientConnState) error {
	if logger.V(2) {
		logger.Info("custom_round_robin: got new ClientConn state: ", state)
	}
	crrCfg, ok := state.BalancerConfig.(*customRRConfig)
	if !ok {
		return balancer.ErrBadResolverState
	}
	crrCfg.N = crrCfg.N // update scs - yes guaranteed from Doug's chat
	// Successful resolution; clear resolver error and ensure we return nil.
	crr2.resolverErr = nil // should this come before or after

	addrsSet := resolver.NewAddressMap() // To track which SubConns to delete.

	// Use the getter for presence...regular subconn, wrapped subconn types

	for _, a := range state.ResolverState.Addresses {
		addrsSet.Set(a, nil)
		if _, ok := crr2.subConns.Get(a); !ok { // A new address was created. Create a SubConn corresponding.
			// Create a SubConn, and subsribe it to a state listener so it can receive updates.
			var sc balancer.SubConn
			opts := balancer.NewSubConnOptions{
				StateListener: func(scs balancer.SubConnState) { crr2.updateSubConnState(sc, scs) },
			}
			sc, err := crr2.cc.NewSubConn([]resolver.Address{a}, opts)
			if err != nil {
				logger.Warningf("custom_round_robin: failed to create new SubConn: %v", err)
				continue
			}
			crr2.subConns.Set(a, sc)
			crr2.scStates[sc] = connectivity.Idle
			crr2.cse.RecordTransition(connectivity.Shutdown, connectivity.Idle) // ah because creating
			sc.Connect() // new sub conn and connect, triggering what if different?
		}
	}

	// so either update unconditionally or only on a list change...I think the former
	// keep state, keeps it simple

	// Delete any SubConns that are tied to addresses no longer present in list.
	for _, a := range crr2.subConns.Keys() {
		// presence which triggers something (SubConn creation, pick first
		// creation) attributes?

		// The petiole policies will receive a list of endpoints, each of which
		// may contain multiple addresses. They will create a pick_first child
		// policy for each endpoint, to which they will pass a list containing a
		// single endpoint with all of its addresses.

		// the triggering is a pick first child creation

		sci, _ := crr2.subConns.Get(a) // can be anything - generalizes it
		sc := sci.(balancer.SubConn)
		if _, ok := addrsSet.Get(a); ok {
			sc.Shutdown()
			crr2.subConns.Delete(a)
		}
	}

	if len(state.ResolverState.Addresses) == 0 {
		crr2.ResolverError(errors.New("produced zero addresses"))
		return balancer.ErrBadResolverState
	}
	crr2.regeneratePicker()
	crr2.cc.UpdateState(balancer.State{ConnectivityState: crr2.state, Picker: crr2.picker})
	return nil
}

func (crr2 *customRoundRobin2) ResolverError(err error) {


	// persists error and writes that back (so it gets most recent error)
	crr2.resolverErr = err
	if crr2.subConns.Len() == 0 {
		crr2.state = connectivity.TransientFailure
	}

	if crr2.state != connectivity.TransientFailure {
		// The picker will not change since the balancer does not currently
		// report an error.
		return
	}
	crr2.regeneratePicker()
	crr2.cc.UpdateState(balancer.State{
		ConnectivityState: crr2.state,
		Picker:            crr2.picker,
	})
}

func (crr2 *customRoundRobin2) ResolverError2(err error) {
	crr2.resolverErr = err // persist and forward or persist and try and forward

	crr2.inhibitPickerUpdates = true

	// update state with transient picker...
	// cause inline update state, cause another inline update state?
	for _, pf := range crr2.pfs.Values() {
		pickFirst := pf.(balancer.Balancer)
		pickFirst.ResolverError(err)
	}

	crr2.inhibitPickerUpdates = false
	// operation of unconditionally updating...

	// again, what to do about inline picker update from child in this scenario?
	// how to handle the spray of pickers back in the tree like structure?
}

func (crr2 *customRoundRobin2) Close2() {
	for _, pf := range crr2.pfs.Values() {
		pickFirst := pf.(balancer.Balancer)
		pickFirst.Close()
	}
}
// Spraying these operations to children...

// no need for newSubConn and UpdateSubConnState (updateSubConnState)...pick first handles that


// mergeErrors builds an error from the last connection error and the last
// resolver error.  Must only be called if b.state is TransientFailure.
func (crr2 *customRoundRobin2) mergeErrors() error {
	if crr2.connErr == nil {
		return fmt.Errorf("last resolver error: %v", crr2.resolverErr)
	}
	if crr2.resolverErr == nil {
		return fmt.Errorf("last connection error: %v", crr2.connErr)
	}
	return fmt.Errorf("last connection error: %v; last resolver error: %v", crr2.connErr, crr2.resolverErr)
}
// or look into the children and do that...

// shared logic amongst all the petiole policies
// similar to picking a SubConn except you're picking a picker to delegate to.
// all the logic otherwise stays the same wrt connection states and transferring etc.

// need to id the child to know what picker it came from, Michael created a
// wrapper on pick first and so did Larry, similar to graceful switch...need to id where to picker came from
// can search the child state orrrr trigger it here

// wrapper type on the pick first child
// it's an endpoint, does happy eyeballs and a lot of the connection management logic we need

// the instantiation of this data object logically ids the data object
// type balancerWrapper
// pick first balancer
// balancer state?

// thin wrapper Michael and Larry did on this child pick first type...

func (crr2 *customRoundRobin2) regeneratePicker2() {
	if crr2.inhibitPickerUpdates { // this comes in sync and within a mutex grab...I think need a run which cleans up (run is event loop)
		// log something
		return
	}

	var readyPickers2 []balancer.Picker
	for _, bw := range crr2.pfs.Values() { // does this read need sync, gets written to in UpdateCCS
		pickFirst := bw.(balancerWrapper)
		if pickFirst.state.ConnectivityState == connectivity.Ready {
			readyPickers2 = append(readyPickers2, pickFirst.state.Picker)
		}
	}
	// there's a lot of logic here that collides...
	// UpdateCCS (async) mutex with deadlock
	// UpdateState/this operation inline

	// UpdateState (async)




	// what gets spit out of the manual resolver? endpoints or addresses?
	crr2.pfs // resolver.EndpointsMap (map[endpoint]balancer)
	crr2.pickers // map[balancer.Balancer]balancer.State, so you have a list of pickers
	// I feel like both have one

	// logically a pick first picker has a subconn right
	// pick first - returns connectivity state ready and a picker
	// which chooses a single subconn...

	// the problem is building out crr2.pickers requires the balancer key which is
	// id. needs the id to track this state changing over time.
	var readyPickers []balancer.Picker // once you get this data structure can build out below...
	for _, state := range crr2.pickers {
		if state.ConnectivityState == connectivity.Ready {
			readyPickers = append(readyPickers, state.Picker)
			// state.Picker // balancer.Picker
			// build out a round robin picker that persists
			// three of these pickers...
			// when called picker.Pick(), picker.Pick on one of the three children...
			// state.Picker.Pick() // client conn calls this
		}
	}

	// if you regenerate picker on UpdateState, that can call async. other
	// operations come in sync, but maybe guard against that. Inhibit picker
	// updates and send one picker udpate per synchronous operation?

	// if you want determinism:
	if len(readyPickers2) != 2 {
		return
	}


	crr2.picker = &customRoundRobinPicker2{ // does this need to persist, also what to do about operations coming in/out async/sync
		pickers: readyPickers2,
		n: crr2.n, // esp if you can read this from UpdateState()...put on event loop?
		next: 0,
	}

	/*
	RecordTransition records state change happening in subConn and based on that it evaluates what aggregated state should be.
	  - If at least one SubConn in Ready, the aggregated state is Ready;
	  - Else if at least one SubConn in Connecting, the aggregated state is Connecting;
	  - Else if at least one SubConn is Idle, the aggregated state is Idle;
	  - Else if at least one SubConn is TransientFailure (or there are no SubConns), the aggregated state is Transient Failure.
	Shutdown is not considered.
	*/
	// balancer.ConnectivityStateEvaluator{} // layer above this layer that aggregates connectivity states in the same way...

	// trigger once per update...building picker and updating state once...reads the map which is determined
	// by endpoints which can call inline. Also messes with map values
	// map[endpoint]balancerwrapper, reads and writes state for endpoint on update in, and balancer wrapper on update upward
	crr2.cc.UpdateState(balancer.State{
		ConnectivityState: crr2.state/*aggregated connecitvity state or what?*/, // how to add sync points to this?
		Picker: crr2.picker, // synchronous snapshot of the balancer
	})
	/*
	if len(readySCs) != 2 {
			return
		}
		crr2.picker = &customRoundRobinPicker{
			subConns: readySCs,
			n: crr2.n,
			next: 0,
		}
	*/
}

// regeneratePicker generates a picker from the balancer's state. If the
// balancer is in transient failure, an error picker is persisted which always
// gives an error. Otherwise, it persists a picker with all the ready SubConns.
func (crr2 *customRoundRobin2) regeneratePicker() { // either make the same operation as updating picker or keep as is
	// the scope of this is to persist a new picker update (maybe have this send too)

	if crr2.inhibitPickerUpdates { // this comes in sync and within a mutex grab...I think need a run which cleans up (run is event loop)
		// log something
		return
	}


	if crr2.state == connectivity.TransientFailure {
		crr2.picker = newErrPicker(crr2.mergeErrors())
		return
	}

	var readySCs []balancer.SubConn
	// The same string address can be logically treated as multiple addresses
	// based on attributes alongside that address. This creates a logically
	// distinct connection, which is why the building of Ready SubConns here
	// uses a resolver.AddressMap. (maybe move this comment to
	// UpdateClientConnState). One address with multiple connnections (not multiple addresses), same address treated as seperate connections
	// same address appears multiple times, may need more than one *based on the attributes*

	// each unique address, if it's ready send up
	for _, addr := range crr2.subConns.Keys() { // ah because address specifity - lowest level balancer which is why you need to do this here.
		sci, _ := crr2.subConns.Get(addr)
		sc := sci.(balancer.SubConn)
		if st, ok := crr2.scStates[sc]; ok && st == connectivity.Ready {
			readySCs = append(readySCs, sc)
		}
	}
	// might simplify to wrap pick first...
	// sticky tf can go away

	// maybe have a pick first child, be future proof.
	// recommend wrapping pick first...easy to wrap and use...want to recommend that
	// wrap pick first, here's how you do it...
	// operate on endpoints ***now*** all addresses end up in endpoint slice...one per endpoint
	// future proof
	// health checking in pick_first, moving to pick_first so you're fine.
	// pass through some things to pick_first.

	// if you want determinism: I can't even get to this balancer...rewrite this
	// to use stub server? No but needs deploy the server.

	if len(readySCs) != 2 {
		return
	}
	crr2.picker = &customRoundRobinPicker{ // does this need to persist, also what to do about operations coming in/out async/sync
		subConns: readySCs,
		n: crr2.n,
		next: 0,
	}
	// trigger once
	crr2.cc.UpdateState()

}

// This function is deprecated. SubConnState updates now come through listener
// callbacks.
func (crr2 *customRoundRobin2) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	logger.Errorf("custom_round_robin: UpdateSubConnState(%v, %+v) called unexpectedly", sc, state)
}

// set as a listener callback in what is passed upward.
func (crr2 *customRoundRobin2) updateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	if logger.V(2) {
		logger.Infof("custom_round_robin: handle SubConn state change: %p, %v", sc, state.ConnectivityState)
	}
	// this is persisting sc -> state...state can move around, go from permutation of state to another permutation of state...
	// balancers can also change state...old state to new state, happens in UpdateState()
	// needs to update picker in that case

	// oh this state update is sync, whereas picker updates aren't sync...

	oldState, ok := crr2.scStates[sc]
	if !ok {
		if logger.V(2) {
			logger.Infof("custom_round_robin: got state changes for an unknown SubConn: %p, %v", sc, state.ConnectivityState)
		}
		return
	}
	newState := state.ConnectivityState

	if oldState == connectivity.TransientFailure &&
		(newState == connectivity.Connecting || newState == connectivity.Idle) {
		// Once a subconn enters TRANSIENT_FAILURE, ignore subsequent IDLE or
		// CONNECTING transitions to prevent the aggregated state from being
		// always CONNECTING when many backends exist but are all down. This is
		// called Sticky Transient Failure, and this logic will be moved to the
		// pick first lb policy once it is the universal leaf balancer. See A62
		// for details.
		if newState == connectivity.Idle {
			sc.Connect()
		}
		return
	}
	crr2.scStates[sc] = newState

	switch newState {
	case connectivity.Idle:
		sc.Connect()
	case connectivity.TransientFailure:
		crr2.connErr = state.ConnectionError
	case connectivity.Shutdown:
		delete(crr2.scStates, sc)
	}

	crr2.state = crr2.cse.RecordTransition(oldState, newState)

	// Regenerate picker if the sc entered or left ready, or if the aggregated
	// state of the balancer is TRANSIENT_FAILURE (may need to update error
	// message).
	if (newState == connectivity.Ready) != (oldState == connectivity.Ready) ||
		crr2.state == connectivity.TransientFailure {
		crr2.regeneratePicker()
	}
	crr2.cc.UpdateState(balancer.State{
		ConnectivityState: crr2.state,
		Picker: crr2.picker,
	})
}

// it is itself the leaf, so doesn't need to be a balancer.ClientConn write a
// comment about this. Some balancers implement balancer.ClientConn. These are
// for policies that defer SubConn creation to a leaf policy. Eventually, the
// codebase will have pick_first as the universal leaf policy. However, this can
// still be a leaf right? Ask Doug I think the intent is a user can still
// implement a wrapper of leaf.
func (crr2 *customRoundRobin2) Close() {
	// Call shutdown on the SubConns here, since this is a leaf balancer that
	// actually creates the SubConns. Once pick first becomes the universal leaf
	// policy, it will be pick firsts responsibility to shut down/cleanuop all
	// SubConns, and we can delete this logic from all balancers.
	for sc := range crr2.scStates {
		sc.Shutdown() // cleanup in graceful switch
	}
}

// ExitIdle is a nop because the base balancer attempts to stay connected to all
// SubConns at all times, thus, the triggering of reconnection does not apply.
func (crr2 *customRoundRobin2) ExitIdle() {}

type customRoundRobinPicker2 struct {
	pickers []balancer.Picker
	n uint32
	next uint32
}

func (crrp2 *customRoundRobinPicker2) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	next := atomic.AddUint32(&crrp2.next, 1)
	index := 0
	if next%crrp2.n == 0 {
		index = 1
	}
	childPicker := crrp2.pickers[index%len(crrp2.pickers)]
	return childPicker.Pick(info)
}

type customRoundRobinPicker struct {
	subConns []balancer.SubConn
	n        uint32

	next uint32
}

func (crrp *customRoundRobinPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	next := atomic.AddUint32(&crrp.next, 1)
	index := 0
	if next%crrp.n == 0 {
		index = 1
	}
	sc := crrp.subConns[index%len(crrp.subConns)]
	return balancer.PickResult{SubConn: sc}, nil
}

// newErrPicker returns a Picker that always returns err on Pick().
func newErrPicker(err error) balancer.Picker {
	return &errPicker{err: err}
}

type errPicker struct {
	err error // Pick() always returns this err.
}

func (p *errPicker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	return balancer.PickResult{}, p.err
}