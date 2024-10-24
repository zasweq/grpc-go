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
		target:          bOpts.Target.String(),
		metricsRecorder: bOpts.MetricsRecorder,

		addressWeights:   resolver.NewAddressMap(),
		endpointToWeight: resolver.NewEndpointMap(),
		scToWeight:       make(map[balancer.SubConn]*endpointWeight),
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

// updateEndpointsLocked updates endpoint weight state based off new update, by starting
// and clearing any endpoint weights needed.
// Caller must hold b.mu.
func (b *wrrBalancer) updateEndpointsLocked(endpoints []resolver.Endpoint) {
	endpointSet := resolver.NewEndpointMap()
	for _, endpoint := range endpoints {
		endpointSet.Set(endpoint, nil)
		var ew *endpointWeight
		ewi, ok := b.endpointToWeight.Get(endpoint)
		if ok {
			ew = ewi.(*endpointWeight)
		} else {
			ew = &endpointWeight{
				logger:            b.logger,
				connectivityState: connectivity.Connecting, // what does this start with - do child balancers start in IDLE mode?
				// Initially, we set load reports to off, because they are not
				// running upon initial endpointWeight creation.
				cfg:             &lbConfig{EnableOOBLoadReport: false},
				metricsRecorder: b.metricsRecorder,
				target:          b.target,
				locality:        b.locality,
			}

			for _, addr := range endpoint.Addresses {
				b.addressWeights.Set(addr, ew)
			}
			b.endpointToWeight.Set(endpoint, ew)
		}

		ew.updateConfig(b.cfg)
	}

	// Is there a way to test this doesn't leak or some sort of functionality here?

	// Update 1 with two endpoints, Update 2 with two endpoints or three that partially overlap
	// Test case of duplicate addresses to make sure doesn't crash, undefined behavior

	// Test case of endpoints with multiple addresses, how many endpoints and what diff up in the air...

	// child endpoitnSharding to ping ExitIdle...

	// Delete old endpointToWeight...check this algorithm (through unit tests :))?
	for _, endpoint := range b.endpointToWeight.Keys() {
		if _, ok := endpointSet.Get(endpoint); ok {
			// Existing endpoint also in new endpoint list; skip.
			continue
		}
		b.endpointToWeight.Delete(endpoint) // This is ok to do in iteration on line 479 line writing to something you append to I guess...will find out in tests
		for _, addr := range endpoint.Addresses {
			b.addressWeights.Delete(addr)
		}
		// SubConn map will get handled in updateSubConnState
		// when receives SHUTDOWN signal.
	}
}

// wrrBalancer implements the weighted round robin LB policy.
type wrrBalancer struct {
	// The following fields are set at initialization time and read only after that,
	// so they do not need to be protected by a mutex.
	child               balancer.Balancer
	balancer.ClientConn // Embed to intercept NewSubConn operation
	logger              *grpclog.PrefixLogger
	target              string
	metricsRecorder     estats.MetricsRecorder

	mu               sync.Mutex
	cfg              *lbConfig // active config
	locality         string
	stopPicker       func()
	addressWeights   *resolver.AddressMap  // addr -> endpointWeight
	endpointToWeight *resolver.EndpointMap // endpoint -> endpointWeight
	scToWeight       map[balancer.SubConn]*endpointWeight
}

// Test case for duplicate - sane, but undefined so just make sure works or
// something...make sure it doesn't crash...undefined behavior unless overlapped address
// doesn't connect...
// API guarantee is undefined for that case

func (b *wrrBalancer) UpdateClientConnState(ccs balancer.ClientConnState) error {
	b.logger.Infof("UpdateCCS: %v", ccs)
	cfg, ok := ccs.BalancerConfig.(*lbConfig)
	if !ok {
		return fmt.Errorf("wrr: received nil or illegal BalancerConfig (type %T): %v", ccs.BalancerConfig, ccs.BalancerConfig)
	}

	// So here: (switch this to actual comment)
	// call validation on endpoint sharding for the no addresses at all case, and call resolver error on endpoint sharding
	// old data structures here and in endpoint sharding will work if already warmed up, if not will report TF to parent
	if err := endpointsharding.ValidateEndpoints(ccs.ResolverState.Endpoints); err != nil { // this symbol moved to resolver but I think this is ok...move once other PR is merged...
		// Call resolver error on child - that will allow old system to continue working
		// but generate a tf picker from child putting channel in TF if needed. Will return a picker
		// inline.
		b.child.ResolverError(err)
		return err // or error + failed to validate endpoints? or err bad resolver state or something...
	}

	// ignore empty endpointToWeight in endpoint sharding lol, it'll create data here that
	// won't be used...and get cleared on next one so no leak, if multiple goes
	// to same one anyway

	// treat duplicate as undefined...my map structure will work as is...it'll just point to last one it hits...
	// Could comment this ^^^

	b.mu.Lock()
	b.cfg = cfg
	b.locality = weightedtarget.LocalityFromResolverState(ccs.ResolverState)
	b.updateEndpointsLocked(ccs.ResolverState.Endpoints)
	b.mu.Unlock()

	// Note: if this call ever starts erroring (as of writing this won't happen
	// unless programmer error), will need to rethink this operation, as once
	// it gets here it has already updated all the data structures.
	return b.child.UpdateClientConnState(balancer.ClientConnState{
		BalancerConfig: gracefulSwitchPickFirst,
		ResolverState:  ccs.ResolverState,
	}) // this updates picker inline
}

// Called from below from UpdateCCS ResolverError and UpdateSCS as it was, call out from this
func (b *wrrBalancer) UpdateState(state balancer.State) { // called inline from child update, so will update sync, not a guarantee?
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.stopPicker != nil {
		b.stopPicker()
		b.stopPicker = nil
	}

	childStates := endpointsharding.ChildStatesFromPicker(state.Picker)

	var readyPickersWeight []pickerWeightedEndpoint

	for _, childState := range childStates {
		if childState.State.ConnectivityState == connectivity.Ready {
			ewv, ok := b.endpointToWeight.Get(childState.Endpoint)
			if !ok {
				// Should never happen, simply continue and ignore this endpoint
				// for READY pickers.
				continue
			}
			ew := ewv.(*endpointWeight)
			readyPickersWeight = append(readyPickersWeight, pickerWeightedEndpoint{
				picker:           childState.State.Picker,
				weightedEndpoint: ew,
			})
		}
	}
	// If no ready pickers are present, simply defer to the round robin picker
	// from endpoint sharding, which will round robin across the most relevant
	// pick first children in the highest precedence connectivity state.
	if len(readyPickersWeight) == 0 {
		b.ClientConn.UpdateState(balancer.State{
			ConnectivityState: state.ConnectivityState,
			Picker:            state.Picker,
		})
		return
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
	ctx, b.stopPicker = context.WithCancel(context.Background())
	p.start(ctx)

	b.ClientConn.UpdateState(balancer.State{
		ConnectivityState: state.ConnectivityState,
		Picker:            p,
	})
}

type pickerWeightedEndpoint struct {
	picker           balancer.Picker
	weightedEndpoint *endpointWeight
}

func (b *wrrBalancer) NewSubConn(addrs []resolver.Address, opts balancer.NewSubConnOptions) (balancer.SubConn, error) { // more subtle - interacts with endpoint weight in certain weights, need to figure out
	addr := addrs[0]
	var sc balancer.SubConn

	oldListener := opts.StateListener
	opts.StateListener = func(state balancer.SubConnState) {
		b.updateSubConnState(sc, state)
		oldListener(state)
	}

	sc, err := b.ClientConn.NewSubConn([]resolver.Address{addr}, opts)
	if err != nil {
		return sc, err
	}

	b.mu.Lock()
	ewv, ok := b.addressWeights.Get(addr)
	if !ok {
		b.mu.Unlock()
		// SubConn state updates can come in for a no longer relevant endpoint
		// weight (from the old system after a new config update is applied).
		// Will eventually get cleared from scMap once receives Shutdown signal.
		return sc, err
	}
	ew := ewv.(*endpointWeight)
	b.scToWeight[sc] = ew
	b.mu.Unlock()

	return sc, err
}

func (b *wrrBalancer) ResolverError(err error) {
	// Will cause inline picker update from endpoint sharding.
	b.child.ResolverError(err)
}

func (b *wrrBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	b.logger.Errorf("UpdateSubConnState(%v, %+v) called unexpectedly", sc, state)
}

func (b *wrrBalancer) updateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	b.mu.Lock()
	ew := b.scToWeight[sc]
	// updates from a no longer relevant SubConn update, nothing to do here but
	// forward in state listener.
	if ew == nil {
		b.mu.Unlock()
		return
	}
	if state.ConnectivityState == connectivity.Shutdown {
		delete(b.scToWeight, sc) // this will get shutdown when pick first shuts down
	}
	b.mu.Unlock()

	// On the first READY SubConn/Transition for an endpoint, set pickedSC,
	// clear endpoint tracking weight state, and potentially start an OOB watch.
	if state.ConnectivityState == connectivity.Ready && ew.pickedSC == nil {
		ew.pickedSC = sc
		ew.mu.Lock()
		ew.nonEmptySince = time.Time{}
		ew.lastUpdated = time.Time{}
		cfg := ew.cfg
		ew.mu.Unlock()
		ew.updateORCAListener(cfg)
		return
	}

	// sc connect call when child goes IDLE, like it does now, operations now
	// need to be translated
	// when connection is lost, pick first says it's idle, call it that says connect
	// immediately
	// when child updates it's picker idle, call connect...
	// ExitIdle()...expose child balancer?
	// Exit Idle propagate to all children (Doug doesn't like)
	// expose child balancers to call ExitIdle to call on them
	// or endpoint sharding should do this?
	// option endpoint sharding - add knob to config
	// add to what it's behavior should be
	// endpoint sharding calls children exit idle if idle
	// todo of making it configurable...all petiole and rr will probably use this
	// so happens at a lower layer...
	// breaking change if configurable

	// updateConfig can clear this out/update period, how does this operation map...

	// Or any state not READY - transition out of READY...?

	// If the pickedSC (the one pick first uses for an endpoint) transitions out of READY,
	// stop oob listener if needed and clear pickedSC so the next created sc for endpoint
	// that goes READY will be chosen for endpoint.
	if state.ConnectivityState != connectivity.Ready && ew.pickedSC == sc {
		// If same sc that created listener, stop OOB Listener if needed and clear it.

		// First one that goes READY = what pick first will pick. Only if that goes not ready will pick first do something else
		// implied by whatever goes READY even if 2, one goes READY, fails, then the next ready one for endpoint pick first will choose

		if ew.stopORCAListener != nil {
			ew.stopORCAListener()
		}
		ew.pickedSC = nil
	}
}

// Close stops the balancer.  It cancels any ongoing scheduler updates and
// stops any ORCA listeners.
func (b *wrrBalancer) Close() {
	b.mu.Lock()
	if b.stopPicker != nil {
		b.stopPicker()
		b.stopPicker = nil
	}
	b.mu.Unlock()

	// Ensure any lingering OOB watchers are stopped.
	for _, ewv := range b.endpointToWeight.Values() {
		ew := ewv.(*endpointWeight)
		if ew.stopORCAListener != nil {
			ew.stopORCAListener()
		}
	}
}

// ExitIdle is ignored; we always connect to all backends.
func (b *wrrBalancer) ExitIdle() {}

// picker is the WRR policy's picker.  It uses live-updating backend weights to
// update the scheduler periodically and ensure picks are routed proportional
// to those weights.
type picker struct {
	scheduler unsafe.Pointer // *scheduler; accessed atomically
	v         uint32         // incrementing value used by the scheduler; accessed atomically
	cfg       *lbConfig      // active config when picker created

	weightedPickers []pickerWeightedEndpoint // all READY pickers

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
}

func (p *picker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	// Read the scheduler atomically.  All scheduler operations are threadsafe,
	// and if the scheduler is replaced during this usage, we want to use the
	// scheduler that was live when the pick started.
	sched := *(*scheduler)(atomic.LoadPointer(&p.scheduler))

	pickedPicker := p.weightedPickers[sched.nextIndex()]
	pr, err := pickedPicker.picker.Pick(info)
	if err != nil {
		return balancer.PickResult{}, err
	}
	if !p.cfg.EnableOOBLoadReport {
		pr.Done = func(info balancer.DoneInfo) {
			if load, ok := info.ServerLoad.(*v3orcapb.OrcaLoadReport); ok && load != nil {
				pickedPicker.weightedEndpoint.OnLoadReport(load)
			}
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

// endpointWeight is the weight for an endpoint. It tracks the SubConn that will
// be picked for the endpoint, and other parameters relevant to computing the
// effective weight. When needed, it also tracks connectivity state, listens for
// metrics updates by implementing the orca.OOBListener interface and manages
// that listener.
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
	pickedSC          balancer.SubConn // the first sc for the endpoint that goes READY, cleared on that sc disconnecting (i.e. going out of READY). Represents what pick first will use as it's picked SubConn for a certain endpoint.

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
	// Update weights of this endpoint according to the reported load.
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
	// (Re)start the listener to use the new config's settings for OOB
	// reporting.
	w.updateORCAListener(cfg)
}

func (w *endpointWeight) updateORCAListener(cfg *lbConfig) {
	if w.stopORCAListener != nil {
		w.stopORCAListener()
	}
	if !cfg.EnableOOBLoadReport {
		w.stopORCAListener = nil
		return
	}
	if w.pickedSC == nil { // No picked SC for this endpoint yet, nothing to listen on.
		return
	}
	if w.logger.V(2) {
		w.logger.Infof("Registering ORCA listener for %v with interval %v", w.pickedSC, cfg.OOBReportingPeriod)
	}
	opts := orca.OOBListenerOptions{ReportInterval: time.Duration(cfg.OOBReportingPeriod)}
	w.stopORCAListener = orca.RegisterOOBListener(w.pickedSC, w, opts)
}

// weight returns the current effective weight of the endpoint, taking into
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

	// The endpoint has not received a load report (i.e. just turned READY with
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

// seems mostly right
// Endpoints get emitted so should just work...
// pick first needs to be new one...
// pick_first.

// This is done outside of from lower layer
// trigger Exit Idle the moment something goes idle

// and write cleanup/write tests...

// For testing: what is different is it used to be on scs,
// now it's on pf policies

// Done outside of cleaning up UpdateClientConnState and cleaning up TODO's in updateSubConnState
// But need to write tests and ping exit idle at a lower layer (maybe separate PR for faster iteration)
// and move endpoint sharding helper to resolver...
