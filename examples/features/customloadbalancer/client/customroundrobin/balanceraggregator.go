package customroundrobin

import (
	"encoding/json"
	"fmt"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/internal/balancer/gracefulswitch"
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

type Parent interface {
	balancer.ClientConn
	UpdateChildState(childStates []childState)
}

// Build returns a new BalancerAggregator.
func Build(parent Parent, opts balancer.BuildOptions) *BalancerAggregator {
	return &BalancerAggregator{
		parent: parent,
		bOpts: opts,
		children: resolver.NewEndpointMap(),
	}
}

// BalancerAggregator...
type BalancerAggregator struct {
	parent Parent
	bOpts  balancer.BuildOptions

	children              *resolver.EndpointMap

	inhibitPickerUpdates bool
}

func (ba *BalancerAggregator) UpdateClientConnState(state balancer.ClientConnState) error {
	endpointSet := resolver.NewEndpointMap()
	ba.inhibitPickerUpdates = true
	// Update/Create new children.
	for _, endpoint := range state.ResolverState.Endpoints {
		endpointSet.Set(endpoint, nil)
		var bal *balancerWrapper
		if child, ok := ba.children.Get(endpoint); ok {
			bal = child.(*balancerWrapper)
		} else {
			bal = &balancerWrapper{
				endpoint: endpoint,
				ClientConn: ba.parent,
				ba: ba,
			}
			bal.Balancer = gracefulswitch.NewBalancer(bal, ba.bOpts)
			ba.children.Set(endpoint, bal)
		}
		if err := bal.UpdateClientConnState(balancer.ClientConnState{
			BalancerConfig: state.BalancerConfig,
			ResolverState: resolver.State{
				Endpoints:  []resolver.Endpoint{endpoint},
				Attributes: state.ResolverState.Attributes,
			},
		}); err != nil {
			return fmt.Errorf("error updating child balancer: %v", err)
		}
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
	ba.BuildChildStates()
	return nil
}

func (ba *BalancerAggregator) ResolverError(err error) {
	ba.inhibitPickerUpdates = true
	for _, child := range ba.children.Values() {
		bal := child.(balancer.Balancer)
		bal.ResolverError(err)
	}
	ba.inhibitPickerUpdates = false
}

func (ba *BalancerAggregator) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	logger.Errorf("custom_round_robin: UpdateSubConnState(%v, %+v) called unexpectedly", sc, state)
}

func (ba *BalancerAggregator) Close() {
	for _, child := range ba.children.Values() {
		bal := child.(balancer.Balancer)
		bal.Close()
	}
}

func (ba *BalancerAggregator) BuildChildStates() {
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
	ba.parent.UpdateChildState(childUpdates)
}

// balancerWrapper is a wrapper of a balancer. It ID's a child balancer by
// endpoint, and persists recent child balancer state.
type balancerWrapper struct {
	balancer.Balancer   // Simply forward balancer.Balancer operations.
	balancer.ClientConn // embed to intercept UpdateState, doesn't deal with SubConns

	ba *BalancerAggregator

	endpoint resolver.Endpoint
	state balancer.State
}

func (bw *balancerWrapper) UpdateState(state balancer.State) {
	bw.state = state
	bw.ba.BuildChildStates()
}

// ParseConfig returns the child config for it
func ParseConfig(cfg json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	return gracefulswitch.ParseConfig(cfg)
}

// export a const for basic pick_first config...
const PickFirstConfig = "[{\"pick_first\": {}}]"

// Unit tests for this?

// **Scope out time estimates for CSM**
