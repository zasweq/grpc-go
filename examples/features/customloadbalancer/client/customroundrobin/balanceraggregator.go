package customroundrobin

import (
	"encoding/json"
	"fmt"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"
)

// full balancer

// Takes in []Endpoints, each endpoint becomes a child that it sends to (child config determined by ParseConfig) - type this out in a nice comment

// passes back a tuple of child state []childState, caller is free to aggregator how caller likes
type childState struct {
	endpoint resolver.Endpoint
	state    balancer.State
} // []childState, caller is free to use how it likes such as aggregation and
// picker algorithm
type childUpdates interface { // petiole policies need to implement this interface
	UpdateChildState(childStates []childState) // pointer?
}

type parent interface {
	balancer.ClientConn
	UpdateChildState(childStates []childState)
}

func Build(parent parent, opts balancer.BuildOptions) *balancerAggregator {
	return &balancerAggregator{
		parent: parent,
		bOpts: opts,
	}
}

type balancerAggregator struct { // build returns this?

	parent parent


	bOpts balancer.BuildOptions
	cfg lbConfig // persist config instead

	// Do I want to store a graceful switch in this or not?
	children              *resolver.EndpointMap // child per endpoint, this is what I want right since it's per endpoint, if it switches needs to delete and add

	inhibitPickerUpdates bool
}

func (ba *balancerAggregator) UpdateClientConnState(state balancer.ClientConnState) error {
	// same thing here, create childrens for every endpoint
	cfg, ok := state.BalancerConfig.(*lbConfig)
	if !ok {
		return balancer.ErrBadResolverState
	}
	ba.cfg // old config
	cfg // new config - should I have logic for type switching? I feel like I should? would then need to recreate all endpoints/clear

	endpointSet := resolver.NewEndpointMap()
	ba.inhibitPickerUpdates = true
	// Update/Create new children
	for _, endpoint := range state.ResolverState.Endpoints {
		endpointSet.Set(endpoint, nil)
		var bal *balancerWrapper
		if child, ok := ba.children.Get(endpoint); ok {
			bal = child.(*balancerWrapper)
		} else {
			// build it, and then put it in the child
			bal = &balancerWrapper{
				ClientConn: ba.parent,
				ba: ba,
			}
			bal.Balancer = ba.cfg.childBuilder.Build(bal, ba.bOpts)
			ba.children.Set(endpoint, bal) // child is still balancer.Balancer so we're good? unit test this?
		}
		bal.UpdateClientConnState(balancer.ClientConnState{
			ResolverState: resolver.State{
				Endpoints:  []resolver.Endpoint{endpoint},
				Attributes: state.ResolverState.Attributes,
			},
		})
	}
	// Delete old children that are no longer present.
	for _, e := range ba.children.Keys() {
		child, _ := ba.children.Get(e)
		bal := child.(balancer.Balancer)
		if _, ok := endpointSet.Get(e); !ok {
			bal.Close()
			ba.children.Delete(e)
		}
	}
	ba.inhibitPickerUpdates = false
	// Talks to above using new interface, so needs to have a build that takes the interface...
	ba.BuildChildStates()
	return nil
}

func (ba *balancerAggregator) ResolverError(err error) {
	ba.inhibitPickerUpdates = true
	for _, child := range ba.children.Values() {
		bal := child.(balancer.Balancer)
		bal.ResolverError(err)
	}
	ba.inhibitPickerUpdates = false
}

func (ba *balancerAggregator) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	logger.Errorf("custom_round_robin: UpdateSubConnState(%v, %+v) called unexpectedly", sc, state)
}

func (ba *balancerAggregator) Close() {
	for _, child := range ba.children.Values() {
		bal := child.(balancer.Balancer)
		bal.Close()
	}
}

func (ba *balancerAggregator) BuildChildStates() {
	if ba.inhibitPickerUpdates {
		return
	}

	childUpdates := make([]childState, ba.children.Len())
	for _, child := range ba.children.Values() {
		bw := child.(*balancerWrapper)
		childUpdates = append(childUpdates, childState{
			endpoint: bw.endpoint,
			state:    bw.state,
		})
	}
	// caller does logic of ready == 2 <<<
	ba.parent.UpdateChildState(childUpdates)
}

// would graceful switch live here or in component above...

// ids child with endpoint...to send endpoint upward in the tuple
type balancerWrapper struct {
	// both a balancer and a clientconn?
	balancer.Balancer   // Simply forward balancer.Balancer operations
	balancer.ClientConn // embed to intercept UpdateState, doesn't deal with SubConns

	ba *balancerAggregator

	endpoint resolver.Endpoint // the endpoint this balancer is linked to...created at construction time
	state balancer.State
}

func (bw *balancerWrapper) UpdateState(state balancer.State) {
	// persist around to send upward in BuildChildStates...
	bw.state = state

	// call into above? to tell to regenerate picker...yeah you need it since
	// this can come outside of the UpdateClientConnState call
	bw.ba.BuildChildStates()
}

type lbConfig struct { // for balanceraggregator
	serviceconfig.LoadBalancingConfig

	childBuilder balancer.Builder
	childConfig  serviceconfig.LoadBalancingConfig
}

// full balancer - update client conn state operation (v1) is listed above
// what about
func ParseConfig(cfg json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	gracefulswitch.
}




// Unit tests for this?
