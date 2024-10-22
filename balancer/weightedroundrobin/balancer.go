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

package weightedroundrobin

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/endpointsharding"
	"google.golang.org/grpc/balancer/weightedroundrobin/internal"
	"google.golang.org/grpc/balancer/weightedtarget"
	"google.golang.org/grpc/connectivity"
	estats "google.golang.org/grpc/experimental/stats"
	"google.golang.org/grpc/internal/grpclog"
	iserviceconfig "google.golang.org/grpc/internal/serviceconfig"
	"google.golang.org/grpc/orca"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"

	v3orcapb "github.com/cncf/xds/go/xds/data/orca/v3"
)

// Name is the name of the weighted round robin balancer.
const Name = "weighted_round_robin"

var (
	rrFallbackMetric = estats.RegisterInt64Count(estats.MetricDescriptor{
		Name:           "grpc.lb.wrr.rr_fallback",
		Description:    "EXPERIMENTAL. Number of scheduler updates in which there were not enough endpointToWeight with valid weight, which caused the WRR policy to fall back to RR behavior.",
		Unit:           "update",
		Labels:         []string{"grpc.target"},
		OptionalLabels: []string{"grpc.lb.locality"},
		Default:        false,
	})

	endpointWeightNotYetUsableMetric = estats.RegisterInt64Count(estats.MetricDescriptor{
		Name:           "grpc.lb.wrr.endpoint_weight_not_yet_usable",
		Description:    "EXPERIMENTAL. Number of endpointToWeight from each scheduler update that don't yet have usable weight information (i.e., either the load report has not yet been received, or it is within the blackout period).",
		Unit:           "endpoint",
		Labels:         []string{"grpc.target"},
		OptionalLabels: []string{"grpc.lb.locality"},
		Default:        false,
	})

	endpointWeightStaleMetric = estats.RegisterInt64Count(estats.MetricDescriptor{
		Name:           "grpc.lb.wrr.endpoint_weight_stale",
		Description:    "EXPERIMENTAL. Number of endpointToWeight from each scheduler update whose latest weight is older than the expiration period.",
		Unit:           "endpoint",
		Labels:         []string{"grpc.target"},
		OptionalLabels: []string{"grpc.lb.locality"},
		Default:        false,
	})
	endpointWeightsMetric = estats.RegisterFloat64Histo(estats.MetricDescriptor{
		Name:           "grpc.lb.wrr.endpoint_weights",
		Description:    "EXPERIMENTAL. Weight of each endpoint, recorded on every scheduler update. Endpoints without usable weights will be recorded as weight 0.",
		Unit:           "endpoint",
		Labels:         []string{"grpc.target"},
		OptionalLabels: []string{"grpc.lb.locality"},
		Default:        false,
	})
)

var gracefulSwitchPickFirst serviceconfig.LoadBalancingConfig

func init() {
	balancer.Register(bb{})
	var err error
	gracefulSwitchPickFirst, err = endpointsharding.ParseConfig(json.RawMessage(endpointsharding.PickFirstConfig))
	if err != nil {
		logger.Fatal(err)
	}
}

type bb struct{}

func (bb) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	b := &wrrBalancer{
		ClientConn:      cc,
		cc:              cc,
		subConns:        resolver.NewAddressMap(),
		scMap:           make(map[balancer.SubConn]*endpointWeight),
		target:          bOpts.Target.String(),
		metricsRecorder: bOpts.MetricsRecorder,
	}

	b.child = endpointsharding.NewBalancer(b, bOpts)
	b.logger = prefixLogger(b)
	b.logger.Infof("Created")
	return b
}

func (bb) ParseConfig(js json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	lbCfg := &lbConfig{
		// Default values as documented in A58.
		OOBReportingPeriod:      iserviceconfig.Duration(10 * time.Second),
		BlackoutPeriod:          iserviceconfig.Duration(10 * time.Second),
		WeightExpirationPeriod:  iserviceconfig.Duration(3 * time.Minute),
		WeightUpdatePeriod:      iserviceconfig.Duration(time.Second),
		ErrorUtilizationPenalty: 1,
	}
	if err := json.Unmarshal(js, lbCfg); err != nil {
		return nil, fmt.Errorf("wrr: unable to unmarshal LB policy config: %s, error: %v", string(js), err)
	}

	if lbCfg.ErrorUtilizationPenalty < 0 {
		return nil, fmt.Errorf("wrr: errorUtilizationPenalty must be non-negative")
	}

	// For easier comparisons later, ensure the OOB reporting period is unset
	// (0s) when OOB reports are disabled.
	if !lbCfg.EnableOOBLoadReport {
		lbCfg.OOBReportingPeriod = 0
	}

	// Impose lower bound of 100ms on weightUpdatePeriod.
	if !internal.AllowAnyWeightUpdatePeriod && lbCfg.WeightUpdatePeriod < iserviceconfig.Duration(100*time.Millisecond) {
		lbCfg.WeightUpdatePeriod = iserviceconfig.Duration(100 * time.Millisecond)
	}

	return lbCfg, nil
}

func (bb) Name() string {
	return Name
}

/*

What big structural changes/operation changes happen here and how to change data structures?

updateEndpoints - this is a hook point into changes...
// data structure of endpoint->endpointWeight
// addr -> endpoint weight to store sc...does sc need to point to this now?

// endpoint -> scs, or something that allows you to track first endpoint going READY and deleting the others...

// endpoint -> endpoint weight, do I even need to keep track of sc just first sc that goes ready for an endpoint?
*/

// How to migrate/get it working with new system...
func (b *wrrBalancer) updateEndpoints(endpoints []resolver.Endpoint) { // lock the whole operation at callsite or here...
	endpointSet := resolver.NewEndpointMap()
	for _, endpoint := range endpoints {
		// I don't think I need redundant address check; verified by lower layer...
		endpointSet.Set(endpoint, nil)
		var ew *endpointWeight // switch this typename to endpoint weight...
		ewi, ok := b.endpointToWeight.Get(endpoint)
		if ok {
			ew = ewi.(*endpointWeight)
		} else {
			// can this be plural or is this just singular...? Only use first ready one...
			ew = &endpointWeight{ // Endpoint weight - NewSubconns read this map structure so full writes need lock, but this comes before new scs are created that will read new endpointToWeight but throw whole writes in this operation with a lock anyway...
				// SubConn:           sc,
				logger:            b.logger,
				connectivityState: connectivity.Idle, // what does this start with
				// Initially, we set load reports to off, because they are not
				// running upon initial endpointWeight creation.
				cfg: &lbConfig{EnableOOBLoadReport: false},

				metricsRecorder: b.metricsRecorder,
				target:          b.target,
				locality:        b.locality,
			}

			for _, addr := range endpoint.Addresses {
				b.addressWeights.Set(addr, ew)
			}

			b.endpointToWeight.Set(endpoint, ew)

			// delete state change logic from this layer

			ew.updateConfig(b.cfg) // start oob only on first READY really...still singular

			// Get something concrete for Doug 1:1
		}
	}

	// Delete old endpointToWeight...check this algorithm?
	for _, endpoint := range b.endpointToWeight.Keys() {
		if _, ok := endpointSet.Get(endpoint); ok {
			// Existing endpoint also in new endpoint list; skip.
			continue
		}
		b.endpointToWeight.Delete(endpoint) // This is ok to do in iteration on line 479 line writing to something you append to I guess...
		for _, addr := range endpoint.Addresses {
			b.addressWeights.Delete(addr)
		}
	}
}

// wrrBalancer implements the weighted round robin LB policy.
type wrrBalancer struct {
	// The following fields are immutable.
	// Does it need this? Or keep as a field yeah needs to keep to forward to child...
	child               balancer.Balancer // have this but could keep it a field but intercepts all operations before this used to be leaf
	balancer.ClientConn                   // Embed to intercept new sc operations, remove the cc below?
	// To delete this just switch b.cc calls to b.ClientConn...would this create any problems?
	cc              balancer.ClientConn // before talked to full API, now intercept some operations, leave for now...and see how to talk to it wrrBalancer.ClientConn.Operation
	logger          *grpclog.PrefixLogger
	target          string
	metricsRecorder estats.MetricsRecorder

	// The following fields are only accessed on calls into the LB policy, and
	// do not need a mutex.
	cfg      *lbConfig            // active config
	subConns *resolver.AddressMap // active weightedSubConns mapped by address
	scMap    map[balancer.SubConn]*endpointWeight

	// *** Do I still need this or is this handled by lower layer?
	connectivityState connectivity.State // aggregate state
	// This is determined by picker now,
	// regenerate picker just gets from child, no need to track this
	// regenerate picker gets called from same place...

	// This gets attached to TF err picker as error message in the TF case
	// now TF is by endpoint sharding, but doesn't have recent resolver err and conn err
	// ***

	stopPicker func() // I def still need this structure I think...
	locality   string

	// Does anything else now need to be protected by mutex now that I grab in
	// new sc if new sc comes in from old system simply ignore updates...
	mu             sync.Mutex
	addressWeights *resolver.AddressMap // addr -> endpointWeight

	endpointToWeight *resolver.EndpointMap // endpoint -> weight
	// And then where do you access this?
	// potentially numerous scs for an endpoint, but can make an assumption
	// that pick first will use first READY and throw other away (and sync) so just
	// point first READY sc to endpoint as oob...
	// so store numerous?

	scToWeight map[balancer.SubConn]*endpointWeight // need to clear as appropriate...

	// Something that allows sc to map to endpoint to clear all the sc...
}

// Test case for duplicate - sane, but undefined so just make sure works or
// something...
// API guarantee is undefined for that case

func (b *wrrBalancer) UpdateClientConnState(ccs balancer.ClientConnState) error {
	b.logger.Infof("UpdateCCS: %v", ccs)
	cfg, ok := ccs.BalancerConfig.(*lbConfig)
	if !ok {
		return fmt.Errorf("wrr: received nil or illegal BalancerConfig (type %T): %v", ccs.BalancerConfig, ccs.BalancerConfig)
	}

	// Right here do the error check, add a helper to custom round robin...and call it from here, and od, and things that care (petioles and od call for no endpoints, otherwise undefined). Should I document for OD to call helper too?

	// So here:
	// call validation on endpoint sharding for the no addresses at all case, and call resolver error on endpoint sharding
	// old data structures here and in endpoint sharding will work if already warmed up, if not will report TF to parent
	if err := endpointsharding.ValidateEndpoints(ccs.ResolverState.Endpoints); err != nil { // this symbol moved to resolver but I think this is ok...move once other PR is merged...
		// Call resolver error on child - that will allow old system to continue working
		// but generate a tf picker from child putting channel in TF if needed. Will return a picker
		// inline.
		b.child.ResolverError(err) // or err bad resolver state...something to trigger reresolution I think any error does

		return err // or error + failed to validate endpoints? or err bad resolver state or something...
	}

	// ignore empty endpointToWeight in empty sharding, it'll create data here that
	// won't be used...and get cleared on next one so no leak, if multiple goes
	// to same one anyway

	// treat duplicate as undefined...my map structure will work as is...it'll just point to last one it hits...

	b.mu.Lock()
	// Any deadlocks with any of the writes below?
	b.updateEndpoints(ccs.ResolverState.Endpoints) // I think called this locked since this writes config and stuff
	// when to update config...will test same scenarios as his test I guess...

	// I think ordering doesn't matter here - inline callouts come from child call below...
	b.cfg = cfg
	b.locality = weightedtarget.LocalityFromResolverState(ccs.ResolverState)

	b.mu.Unlock()

	// what if this errors after...programming error at that point...for pick first?
	return b.child.UpdateClientConnState(balancer.ClientConnState{ // this will give you a picker update inline, once you get a picker update inline that should regenerate picker, equivalent to regenerate picker, nil
		BalancerConfig: gracefulSwitchPickFirst,
		ResolverState:  ccs.ResolverState,
	}) // this updates picker inline before it returns, that inline picker update should update cc inline...so should be good, inline processing of picker update should update picker inline...

	// Could create scs inline but there's no way it'll go READY since that sc update is sync with this...
	// so can keep regenerate above here...
}

func (b *wrrBalancer) UpdateState(state balancer.State) { // called inline from child update, so will update sync, not a guarantee?
	// Persist most recent state around for later? In a lock?

	// calls regenerate only on diff or unconditionally? Prob unconditionally for simplicity, extra logic here and it's fine to handle update once at a layer above...

	b.regeneratePicker2(state)

}

func (b *wrrBalancer) regeneratePicker2(state balancer.State) { // otherwise call with most recent state?
	childStates := endpointsharding.ChildStatesFromPicker(state.Picker)

	var readyPickersWeight []pickerWeightedEndpoint

	for _, childState := range childStates {
		if childState.State.ConnectivityState == connectivity.Ready {
			ewv, ok := b.endpointToWeight.Get(childState.Endpoint) // this access needs a lock then?
			if !ok {
				// What to do in this case? But this should honestly never happen...
			}
			ew := ewv.(*endpointWeight) // will panic if doesn't hit...
			readyPickersWeight = append(readyPickersWeight, pickerWeightedEndpoint{
				picker:           childState.State.Picker,
				weightedEndpoint: ew,
			})
		}
	}
	if len(readyPickersWeight) == 0 {
		// Defer to rr picker from below...
		state.Picker // what to do with this now?

		// rr across highest precedence ^^^...
		// send this back
		b.ClientConn.UpdateState(balancer.State{
			ConnectivityState: state.ConnectivityState, // if I persist this around does this need to have a mutex here?
			Picker:            state.Picker,
		}) // no need for mutex since read-only

		// But I think we might need to do something with stopPicker...and old picker persisted around...mutex for read maybe not picker sync snapshot of async system...
	}

	p := &picker{
		v:               rand.Uint32(), // start the scheduler at a random point
		cfg:             b.cfg,
		weightedPickers: readyPickersWeight,
		metricsRecorder: b.metricsRecorder,
		locality:        b.locality,
		target:          b.target,
	}

	var ctx context.Context
	ctx, b.stopPicker = context.WithCancel(context.Background()) // Still invoked the same? Need to delete all the logic for trackign state since endpointSharding does that now...
	p.start(ctx)
	b.cc.UpdateState(balancer.State{
		ConnectivityState: state.ConnectivityState, // if I persist this around does this need to have a mutex here?
		Picker:            p,
	})
}

type pickerWeightedEndpoint struct {
	picker           balancer.Picker
	weightedEndpoint *endpointWeight
}

// Just need to think through races of two scs, describe this in 1:1

// Use first ready one, so that solves that...
func (b *wrrBalancer) NewSubConn(addrs []resolver.Address, opts balancer.NewSubConnOptions) (balancer.SubConn, error) { // more subtle - interacts with endpoint weight in certain weights, need to figure out
	addr := addrs[0]

	var sc balancer.SubConn

	sc, err := b.cc.NewSubConn([]resolver.Address{addr}, balancer.NewSubConnOptions{ // Creates a new one
		StateListener: func(state balancer.SubConnState) {
			b.updateSubConnState(sc, state) // does this still go through same flow? This regenerates picker potentially but this might not be equivalent to child state...
		},
	})
	if err != nil {
		return sc, err
	}

	ewv, ok := b.addressWeights.Get(addr)
	if !ok {
		// subconn for no longer relevant address weight (from old system after
		// new update)...will get shutdown but no need to update data
		// structures, last ref in sc wrappers map will get cleared when goes
		// shutdown...
	}
	ew := ewv.(*endpointWeight) // Will panic if doesn't hit...be safer?

	// I honestly think all you need to do here is append, and point it...
	// can't access ew if store ew { []sc }
	// map[sc]->ew is another thing for update sc state to not wrap
	b.scToWeight[sc] = ew
} // and how the overall structure looks, store sc in the field itself?
// Copy his testing sceanrios to see if works normally...

func (b *wrrBalancer) ResolverError(err error) {
	/*b.resolverErr = err
	if b.subConns.Len() == 0 {
		b.connectivityState = connectivity.TransientFailure
	}
	if b.connectivityState != connectivity.TransientFailure {
		// No need to update the picker since no error is being returned.
		return
	}
	b.regeneratePicker()*/

	// I think just forward to child now...
	b.child.ResolverError(err)
}

func (b *wrrBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	b.logger.Errorf("UpdateSubConnState(%v, %+v) called unexpectedly", sc, state)
}

func (b *wrrBalancer) updateSubConnState(sc balancer.SubConn, state balancer.SubConnState) { // Talk about this operation with Doug...
	// First sc mapped to endpoint goes READY,
	// that sets oob listener which writes to ew
	// next ones that go READY will be ignored by pf...

	// what other state transitions are interesting here besides first sc for an
	// endpoint setting the OOB listener...?

	// Shutdown clear it?
	// Figure out the structure of how I want to keep track of endpoint weights...
	oobOpts := orca.OOBListenerOptions{ReportInterval: time.Duration(b.cfg.OOBReportingPeriod)} // Only write to config comes sync so no need for mutex here...
	// get endpoint weight for a sc, how do I want to store this...better ideas for top level data structures?
	ew := b.scToWeight[sc] // and what to do in this operation here?
	if state.ConnectivityState == connectivity.Ready {
		// On the first READY SubConn/Transition for an endpoint, set oob...
		if ew.stopORCAListener == nil {
			ew.stopORCAListener = orca.RegisterOOBListener(sc, ew, oobOpts)
		}

		// when a new sc comes in, *stop tracking* other scs for the endpoint
		// orrrr persist it in endpoint weight slice and then clear it if matches sc ref? But then lifecycles idk...

	}
	// updateConfig can clear this out/update period, how does this operation map...

	// Or any state not READY - transition out of READY...?
	if state.ConnectivityState == connectivity.Shutdown {
		// If same sc that created listener, stop and clear it
	}
	// ew, with more than one sc high level requirement for data structure...

	// And now when do I clear orca listener? When it goes shutdown
	// Interacts with respect to sc state (going unready etc.)
	// and also the config changing from update ccs
	// Say you have two scs for an endpoint, sc1 and sc2
	// first one that goes READY starts watch above...
	// basically if ew oob watch not already started start it...

	// and when *that one* shuts down, it will clear the oob listener for that endpoint...
	// other scs in endpoint do nothing if goes READY or shuts down
	// Two data structures, sc -> ew, ew -> []sc, once it shuts down can read length....

	// Picker determines now, no need to keep track of state...
	/*wsc := b.scMap[sc]
	if wsc == nil {
		b.logger.Errorf("UpdateSubConnState called with an unknown SubConn: %p, %v", sc, state)
		return
	}
	if b.logger.V(2) {
		logger.Infof("UpdateSubConnState(%+v, %+v)", sc, state)
	}

	cs := state.ConnectivityState

	if cs == connectivity.TransientFailure {
		// Save error to be reported via picker.
		b.connErr = state.ConnectionError
	}

	if cs == connectivity.Shutdown {
		delete(b.scMap, sc)
		// The subconn was removed from b.subConns when the address was removed
		// in updateAddresses.
	}

	oldCS := wsc.updateConnectivityState(cs)
	b.connectivityState = b.csEvltr.RecordTransition(oldCS, cs)

	// Regenerate picker when one of the following happens:
	//  - this sc entered or left ready
	//  - the aggregated state of balancer is TransientFailure
	//    (may need to update error message)
	if (cs == connectivity.Ready) != (oldCS == connectivity.Ready) ||
		b.connectivityState == connectivity.TransientFailure {
		b.regeneratePicker()
	}*/
}

// Close stops the balancer.  It cancels any ongoing scheduler updates and
// stops any ORCA listeners.
func (b *wrrBalancer) Close() {
	if b.stopPicker != nil {
		b.stopPicker()
		b.stopPicker = nil
	}
	for _, wsc := range b.scMap {
		// Ensure any lingering OOB watchers are stopped.
		wsc.updateConnectivityState(connectivity.Shutdown)
	}
}

// ExitIdle is ignored; we always connect to all backends.
func (b *wrrBalancer) ExitIdle() {}

func (b *wrrBalancer) readySubConns() []*endpointWeight {
	var ret []*endpointWeight
	for _, v := range b.subConns.Values() {
		wsc := v.(*endpointWeight)
		if wsc.connectivityState == connectivity.Ready {
			ret = append(ret, wsc)
		}
	}
	return ret
}

func (b *wrrBalancer) regeneratePicker() {
	if b.stopPicker != nil {
		b.stopPicker()
		b.stopPicker = nil
	}

	/*switch b.connectivityState { // connectivity state is determined by children
	case connectivity.TransientFailure: // Won't need this anymore, or the cc accesses, so maybe change to ClientConn, now a lower layer does that
		b.cc.UpdateState(balancer.State{
			ConnectivityState: connectivity.TransientFailure,
			Picker:            base.NewErrPicker(b.mergeErrors()),
		})
		return
	case connectivity.Connecting, connectivity.Idle:
		// Idle could happen very briefly if all subconns are Idle and we've
		// asked them to connect but they haven't reported Connecting yet.
		// Report the same as Connecting since this is temporary.
		b.cc.UpdateState(balancer.State{
			ConnectivityState: connectivity.Connecting,
			Picker:            base.NewErrPicker(balancer.ErrNoSubConnAvailable),
		})
		return
	case connectivity.Ready:
		b.connErr = nil
	}*/

	// All this connectivity state management comes from child now...

	/*p := &picker{
		v:               rand.Uint32(), // start the scheduler at a random point
		cfg:             b.cfg,
		subConns:        b.readySubConns(),
		metricsRecorder: b.metricsRecorder,
		locality:        b.locality,
		target:          b.target,
	}
	var ctx context.Context
	ctx, b.stopPicker = context.WithCancel(context.Background())
	p.start(ctx)
	b.cc.UpdateState(balancer.State{
		ConnectivityState: b.connectivityState,
		Picker:            p,
	})*/

	// This becomes my new operation...

}

// picker is the WRR policy's picker.  It uses live-updating backend weights to
// update the scheduler periodically and ensure picks are routed proportional
// to those weights.
type picker struct {
	scheduler unsafe.Pointer // *scheduler; accessed atomically
	v         uint32         // incrementing value used by the scheduler; accessed atomically
	cfg       *lbConfig      // active config when picker created

	// subConns  []*endpointWeight // all READY subconns
	weightedPickers []pickerWeightedEndpoint // pointer? all READY pickers

	// The following fields are immutable.
	target          string
	locality        string
	metricsRecorder estats.MetricsRecorder
}

func (p *picker) endpointWeights(recordMetrics bool) []float64 {
	wp := make([]float64, len(p.weightedPickers))
	now := internal.TimeNow()
	for i, wpi := range p.weightedPickers {
		wp[i] = wpi.weightedEndpoint.weight(now, time.Duration(p.cfg.WeightExpirationPeriod), time.Duration(p.cfg.BlackoutPeriod), recordMetrics)
	}
	return wp
} // and combine this with a list of pickers

func (p *picker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	// Read the scheduler atomically.  All scheduler operations are threadsafe,
	// and if the scheduler is replaced during this usage, we want to use the
	// scheduler that was live when the pick started.
	sched := *(*scheduler)(atomic.LoadPointer(&p.scheduler))

	pickedPicker := p.weightedPickers[sched.nextIndex()] // scheduler indexes - do same thing
	pr, err := pickedPicker.picker.Pick(info)
	if err != nil {
		// Do I want to return early before on load report for per RPC stream?
		return balancer.PickResult{}, err
	}
	if !p.cfg.EnableOOBLoadReport {
		pr.Done = func(info balancer.DoneInfo) {
			if load, ok := info.ServerLoad.(*v3orcapb.OrcaLoadReport); ok && load != nil {
				pickedPicker.weightedEndpoint.OnLoadReport(load) // I could just embed this picker...
			} // ok so this gets ref to endpoint and enters the data structure that way (this has to come after sc creation/READY for the endpoint to actually get chosen)
		}
	}
	return pr, nil
}

func (p *picker) inc() uint32 {
	return atomic.AddUint32(&p.v, 1)
}

func (p *picker) regenerateScheduler() {
	s := p.newScheduler(true)
	atomic.StorePointer(&p.scheduler, unsafe.Pointer(&s))
}

func (p *picker) start(ctx context.Context) {
	p.regenerateScheduler()
	if len(p.weightedPickers) == 1 {
		// No need to regenerate weights with only one backend.
		return
	}

	go func() {
		ticker := time.NewTicker(time.Duration(p.cfg.WeightUpdatePeriod))
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.regenerateScheduler()
			}
		}
	}()
}

// endpointWeight is the wrapper of a subconn that holds the subconn and its
// weight (and other parameters relevant to computing the effective weight).
// When needed, it also tracks connectivity state, listens for metrics updates
// by implementing the orca.OOBListener interface and manages that listener.
type endpointWeight struct {
	// The following fields are immutable.
	balancer.SubConn
	logger          *grpclog.PrefixLogger
	target          string
	metricsRecorder estats.MetricsRecorder
	locality        string

	// The following fields are only accessed on calls into the LB policy, and
	// do not need a mutex.
	connectivityState connectivity.State
	stopORCAListener  func()

	// subconns attached to this endpoint
	subConns []balancer.SubConn // protected by mu?

	// The following fields are accessed asynchronously and are protected by
	// mu.  Note that mu may not be held when calling into the stopORCAListener
	// or when registering a new listener, as those calls require the ORCA
	// producer mu which is held when calling the listener, and the listener
	// holds mu.
	mu            sync.Mutex
	weightVal     float64
	nonEmptySince time.Time
	lastUpdated   time.Time
	cfg           *lbConfig
}

func (w *endpointWeight) OnLoadReport(load *v3orcapb.OrcaLoadReport) {
	if w.logger.V(2) {
		w.logger.Infof("Received load report for subchannel %v: %v", w.SubConn, load)
	}
	// Update weights of this subchannel according to the reported load
	utilization := load.ApplicationUtilization
	if utilization == 0 {
		utilization = load.CpuUtilization
	}
	if utilization == 0 || load.RpsFractional == 0 {
		if w.logger.V(2) {
			w.logger.Infof("Ignoring empty load report for subchannel %v", w.SubConn)
		}
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	errorRate := load.Eps / load.RpsFractional
	w.weightVal = load.RpsFractional / (utilization + errorRate*w.cfg.ErrorUtilizationPenalty)
	if w.logger.V(2) {
		w.logger.Infof("New weight for subchannel %v: %v", w.SubConn, w.weightVal)
	}

	w.lastUpdated = internal.TimeNow()
	if w.nonEmptySince.Equal(time.Time{}) {
		w.nonEmptySince = w.lastUpdated
	}
}

// updateConfig updates the parameters of the WRR policy and
// stops/starts/restarts the ORCA OOB listener.
func (w *endpointWeight) updateConfig(cfg *lbConfig) {
	w.mu.Lock()
	oldCfg := w.cfg
	w.cfg = cfg
	w.mu.Unlock()

	if cfg.EnableOOBLoadReport == oldCfg.EnableOOBLoadReport &&
		cfg.OOBReportingPeriod == oldCfg.OOBReportingPeriod {
		// Load reporting wasn't enabled before or after, or load reporting was
		// enabled before and after, and had the same period.  (Note that with
		// load reporting disabled, OOBReportingPeriod is always 0.)
		return
	}
	if w.connectivityState == connectivity.Ready {
		// (Re)start the listener to use the new config's settings for OOB
		// reporting.
		w.updateORCAListener(cfg)
	}
}

func (w *endpointWeight) updateORCAListener(cfg *lbConfig) {
	if w.stopORCAListener != nil {
		w.stopORCAListener()
	}
	if !cfg.EnableOOBLoadReport {
		w.stopORCAListener = nil
		return
	}
	if w.logger.V(2) {
		w.logger.Infof("Registering ORCA listener for %v with interval %v", w.SubConn, cfg.OOBReportingPeriod)
	}
	opts := orca.OOBListenerOptions{ReportInterval: time.Duration(cfg.OOBReportingPeriod)}
	w.stopORCAListener = orca.RegisterOOBListener(w.SubConn, w, opts)
}

func (w *endpointWeight) updateConnectivityState(cs connectivity.State) connectivity.State {
	switch cs {
	case connectivity.Idle:
		// Always reconnect when idle.
		w.SubConn.Connect()
	case connectivity.Ready:
		// If we transition back to READY state, reset nonEmptySince so that we
		// apply the blackout period after we start receiving load data. Also
		// reset lastUpdated to trigger endpoint weight not yet usable in the
		// case endpoint gets asked what weight it is before receiving a new
		// load report. Note that we cannot guarantee that we will never receive
		// lingering callbacks for backend metric reports from the previous
		// connection after the new connection has been established, but they
		// should be masked by new backend metric reports from the new
		// connection by the time the blackout period ends.
		w.mu.Lock()
		w.nonEmptySince = time.Time{}
		w.lastUpdated = time.Time{}
		cfg := w.cfg
		w.mu.Unlock()
		w.updateORCAListener(cfg)
	}

	oldCS := w.connectivityState

	if oldCS == connectivity.TransientFailure &&
		(cs == connectivity.Connecting || cs == connectivity.Idle) {
		// Once a subconn enters TRANSIENT_FAILURE, ignore subsequent IDLE or
		// CONNECTING transitions to prevent the aggregated state from being
		// always CONNECTING when many backends exist but are all down.
		return oldCS
	}

	w.connectivityState = cs

	return oldCS
}

// weight returns the current effective weight of the subconn, taking into
// account the parameters.  Returns 0 for blacked out or expired data, which
// will cause the backend weight to be treated as the mean of the weights of the
// other backends. If forScheduler is set to true, this function will emit
// metrics through the metrics registry.
func (w *endpointWeight) weight(now time.Time, weightExpirationPeriod, blackoutPeriod time.Duration, recordMetrics bool) (weight float64) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if recordMetrics {
		defer func() {
			endpointWeightsMetric.Record(w.metricsRecorder, weight, w.target, w.locality)
		}()
	}

	// The SubConn has not received a load report (i.e. just turned READY with
	// no load report).
	if w.lastUpdated.Equal(time.Time{}) {
		endpointWeightNotYetUsableMetric.Record(w.metricsRecorder, 1, w.target, w.locality)
		return 0
	}

	// If the most recent update was longer ago than the expiration period,
	// reset nonEmptySince so that we apply the blackout period again if we
	// start getting data again in the future, and return 0.
	if now.Sub(w.lastUpdated) >= weightExpirationPeriod {
		if recordMetrics {
			endpointWeightStaleMetric.Record(w.metricsRecorder, 1, w.target, w.locality)
		}
		w.nonEmptySince = time.Time{}
		return 0
	}

	// If we don't have at least blackoutPeriod worth of data, return 0.
	if blackoutPeriod != 0 && (w.nonEmptySince.Equal(time.Time{}) || now.Sub(w.nonEmptySince) < blackoutPeriod) {
		if recordMetrics {
			endpointWeightNotYetUsableMetric.Record(w.metricsRecorder, 1, w.target, w.locality)
		}
		return 0
	}

	return w.weightVal
}

/* (undefined for same address in numerous endpointToWeight...)
Action Items:
Make balancer a field, and maybe don’t embed cc? What was it in master…

And that relates to the fact the lis cancels can be plural vvv, but not needed if only 1

The biggest thing to figure out is the numerous sc stuff, for simplicity Doug said first one that goes READY clears out the rest of sc for endpoint
Or store just the first one in sc and if it matches up close, but then the first option gets rid of data structures and is more simple, so try that first
and see if it works.
In order to do this though I think you need some sort of mapping back and forth between endpoint -> sc sc, sc sc -> endpoint?


Figure out mutexes I think it mainly works though…and the picker accesses might race…seems like he's ok with structure of picker...
*/
