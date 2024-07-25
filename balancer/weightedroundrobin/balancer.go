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
	"errors"
	"fmt"
	estats "google.golang.org/grpc/experimental/stats"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/balancer/weightedroundrobin/internal"
	"google.golang.org/grpc/connectivity"
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
	// Scoped to just this package...
	rrFallbackHandle = estats.RegisterInt64Count(estats.MetricDescriptor{
		Name:           "grpc.lb.wrr.rr_fallback",
		Description:    "Number of scheduler updates in which there were not enough endpoints with valid weight, which caused the WRR policy to fall back to RR behavior.",
		Unit:           "update",
		Labels:         []string{"grpc.target", "grpc.lb.locality"},
		Default:        false, // all are opt in for now...
	})

	endpointWeightNotYetUsableHandle = estats.RegisterInt64Count(estats.MetricDescriptor{
		Name:           "grpc.lb.wrr.endpoint_weight_not_yet_usable",
		Description:    "Number of endpoints from each scheduler update that don't yet have usable weight information (i.e., either the load report has not yet been received, or it is within the blackout period).",
		Unit:           "endpoint",
		Labels:         []string{"grpc.target", "grpc.lb.locality"},
		Default:        false, // all are opt in for now...
	})

	endpointWeightStaleHandle = estats.RegisterInt64Count(estats.MetricDescriptor{
		Name:           "grpc.lb.wrr.endpoint_weight_stale",
		Description:    "Number of endpoints from each scheduler update whose latest weight is older than the expiration period.",
		Unit:           "endpoint",
		Labels:         []string{"grpc.target", "grpc.lb.locality"},
		Default:        false, // all are opt in for now...
	})
	// are weights float histo? it rounds it so I think int but might change in future...
	endpointWeightsHandle = estats.RegisterFloat64Histo(estats.MetricDescriptor{
		Name:           "grpc.lb.wrr.endpoint_weights",
		Description:    "Weight of each endpoint, recorded on every scheduler update. Endpoints without usable weights will be recorded as weight 0.",
		Unit:           "endpoint",
		Labels:         []string{"grpc.target", "grpc.lb.locality"},
		Default:        false, // all are opt in for now...
	})
) // how to induce different behaviors on balancer?

// argh need to build out infra for metrics atoms...


func init() {
	balancer.Register(bb{})
}

type bb struct{}

func (bb) Build(cc balancer.ClientConn, bOpts balancer.BuildOptions) balancer.Balancer {
	b := &wrrBalancer{
		cc:                cc,
		subConns:          resolver.NewAddressMap(),
		csEvltr:           &balancer.ConnectivityStateEvaluator{},
		scMap:             make(map[balancer.SubConn]*weightedSubConn),
		connectivityState: connectivity.Connecting,

		// persist target from cc
		target: bOpts.Target.String(), // this is canonical target according to documentation..."returns the canonical target representation of Target"
		metricsRecorder: bOpts.MetricsRecorder,
	}

	bOpts.Target.String() // I think it might be this not below
	cc.Target() // canonical target?

	// gets locality from update ccs - can change
	// so need a mutex unless the operation is already sync...

	// bOpts.MetricsRecorder // also persist this around to record on...either pass it through or have it be a method on an object...
	// is has to persist it in the balancer right?
	// What other subcomponents could even get built right here?

	// what two components do the recording picker and...?

	// Give this to picker, which creates scheduler

	// And what component created weighted SubConns, w/e needs both

	// Creates picker from calls *into* balancer...

	b.logger = prefixLogger(b)
	b.logger.Infof("Created")
	return b
}

func (bb) ParseConfig(js json.RawMessage) (serviceconfig.LoadBalancingConfig, error) {
	// This right here is the configuration...

	lbCfg := &lbConfig{
		// Default values as documented in A58.
		OOBReportingPeriod:      iserviceconfig.Duration(10 * time.Second),
		BlackoutPeriod:          iserviceconfig.Duration(10 * time.Second),
		WeightExpirationPeriod:  iserviceconfig.Duration(3 * time.Minute),
		WeightUpdatePeriod:      iserviceconfig.Duration(time.Second),
		ErrorUtilizationPenalty: 1, // utilization is distinct, this defaults to 1 so doesn't seem to be a factor...all bassed of eps qps and utilization in the default case...
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

// wrrBalancer implements the weighted round robin LB policy.
type wrrBalancer struct {
	cc     balancer.ClientConn
	logger *grpclog.PrefixLogger
	// built out at creation time; read only after, so no need for synchronization
	target string
	metricsRecorder estats.MetricsRecorder


	// The following fields are only accessed on calls into the LB policy, and
	// do not need a mutex.
	cfg               *lbConfig            // active config
	subConns          *resolver.AddressMap // active weightedSubConns mapped by address
	scMap             map[balancer.SubConn]*weightedSubConn
	connectivityState connectivity.State // aggregate state
	csEvltr           *balancer.ConnectivityStateEvaluator
	resolverErr       error // the last error reported by the resolver; cleared on successful resolution
	connErr           error // the last connection error; cleared upon leaving TransientFailure
	stopPicker        func()
	locality          string // empty locality if unset Otel sets empty locality emission since configures and emits locality label from this component...
}

func (b *wrrBalancer) UpdateClientConnState(ccs balancer.ClientConnState) error {
	b.logger.Infof("UpdateCCS: %v", ccs)
	b.resolverErr = nil
	cfg, ok := ccs.BalancerConfig.(*lbConfig)
	if !ok {
		return fmt.Errorf("wrr: received nil or illegal BalancerConfig (type %T): %v", ccs.BalancerConfig, ccs.BalancerConfig)
	}

	b.cfg = cfg
	b.locality = /*resolver attribute...how do these work?*/ // needs to be here for updateAddresses to see it, and for new resolvers I like this...
	// zero addresses should have right locality next time (although will get locality in next update so I think not an important consideration)
	b.updateAddresses(ccs.ResolverState.Addresses)

	if len(ccs.ResolverState.Addresses) == 0 {
		b.ResolverError(errors.New("resolver produced zero addresses")) // will call regeneratePicker
		return balancer.ErrBadResolverState
	}

	// only update locality if new picker but always send locality down


	// don't pass because other operations call it also has access to all balancer data
	b.regeneratePicker() // no sync - could I pass the locality here...or does need to persist on b because other operations present...

	return nil
}

func (b *wrrBalancer) updateAddresses(addrs []resolver.Address) {
	addrsSet := resolver.NewAddressMap()

	// Loop through new address list and create subconns for any new addresses.
	for _, addr := range addrs {
		if _, ok := addrsSet.Get(addr); ok {
			// Redundant address; skip.
			continue
		}
		addrsSet.Set(addr, nil)

		var wsc *weightedSubConn
		wsci, ok := b.subConns.Get(addr)
		if ok {
			wsc = wsci.(*weightedSubConn)
		} else {
			// addr is a new address (not existing in b.subConns).
			var sc balancer.SubConn
			sc, err := b.cc.NewSubConn([]resolver.Address{addr}, balancer.NewSubConnOptions{
				StateListener: func(state balancer.SubConnState) {
					b.updateSubConnState(sc, state)
				},
			})
			if err != nil {
				b.logger.Warningf("Failed to create new SubConn for address %v: %v", addr, err)
				continue
			}
			wsc = &weightedSubConn{
				SubConn:           sc,
				logger:            b.logger,
				connectivityState: connectivity.Idle,
				// Initially, we set load reports to off, because they are not
				// running upon initial weightedSubConn creation.
				cfg: &lbConfig{EnableOOBLoadReport: false},

				metricsRecorder: b.metricsRecorder,
				target: b.target,
				locality: b.locality, // written to before in same operation only sync so ok here...
			}
			b.subConns.Set(addr, wsc)
			b.scMap[sc] = wsc
			b.csEvltr.RecordTransition(connectivity.Shutdown, connectivity.Idle)
			sc.Connect()
		}
		// Update config for existing weightedSubConn or send update for first
		// time to new one.  Ensures an OOB listener is running if needed
		// (and stops the existing one if applicable).
		wsc.updateConfig(b.cfg)
	}

	// Loop through existing subconns and remove ones that are not in addrs.
	for _, addr := range b.subConns.Keys() {
		if _, ok := addrsSet.Get(addr); ok {
			// Existing address also in new address list; skip.
			continue
		}
		// addr was removed by resolver.  Remove.
		wsci, _ := b.subConns.Get(addr)
		wsc := wsci.(*weightedSubConn)
		wsc.SubConn.Shutdown()
		b.subConns.Delete(addr)
	}
}

func (b *wrrBalancer) ResolverError(err error) {
	b.resolverErr = err
	if b.subConns.Len() == 0 {
		b.connectivityState = connectivity.TransientFailure
	}
	if b.connectivityState != connectivity.TransientFailure {
		// No need to update the picker since no error is being returned.
		return
	}
	b.regeneratePicker()
}

func (b *wrrBalancer) UpdateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	b.logger.Errorf("UpdateSubConnState(%v, %+v) called unexpectedly", sc, state)
}

func (b *wrrBalancer) updateSubConnState(sc balancer.SubConn, state balancer.SubConnState) {
	wsc := b.scMap[sc]
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
	}
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

func (b *wrrBalancer) readySubConns() []*weightedSubConn {
	var ret []*weightedSubConn
	for _, v := range b.subConns.Values() {
		wsc := v.(*weightedSubConn)
		if wsc.connectivityState == connectivity.Ready {
			ret = append(ret, wsc)
		}
	}
	return ret
}

// mergeErrors builds an error from the last connection error and the last
// resolver error.  Must only be called if b.connectivityState is
// TransientFailure.
func (b *wrrBalancer) mergeErrors() error {
	// connErr must always be non-nil unless there are no SubConns, in which
	// case resolverErr must be non-nil.
	if b.connErr == nil {
		return fmt.Errorf("last resolver error: %v", b.resolverErr)
	}
	if b.resolverErr == nil {
		return fmt.Errorf("last connection error: %v", b.connErr)
	}
	return fmt.Errorf("last connection error: %v; last resolver error: %v", b.connErr, b.resolverErr)
}

func (b *wrrBalancer) regeneratePicker() {
	if b.stopPicker != nil {
		b.stopPicker()
		b.stopPicker = nil
	}

	switch b.connectivityState {
	case connectivity.TransientFailure:
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
	}

	p := &picker{
		v:        rand.Uint32(), // start the scheduler at a random point
		cfg:      b.cfg,
		subConns: b.readySubConns(),
		metricsRecorder: b.metricsRecorder,
		locality: b.locality, // this doesn't need sync because locality written too only in UpdateCCS
		target: b.target,
	}
	var ctx context.Context
	ctx, b.stopPicker = context.WithCancel(context.Background()) // cancels context which exits spawned goroutine...
	p.start(ctx)
	b.cc.UpdateState(balancer.State{
		ConnectivityState: b.connectivityState,
		Picker:            p,
	})
}

// picker is the WRR policy's picker.  It uses live-updating backend weights to
// update the scheduler periodically and ensure picks are routed proportional
// to those weights.
type picker struct {
	scheduler unsafe.Pointer     // *scheduler; accessed atomically
	v         uint32             // incrementing value used by the scheduler; accessed atomically
	cfg       *lbConfig          // active config when picker created
	subConns  []*weightedSubConn // all READY subconns...does it do the metrics on just the ready ones?

	// built out at creation time; read only after, so no need for synchronization or accessed atomically
	// locality can change, but then you'd get a whole new picker object so yeah built out at init time
	target string
	locality string
	metricsRecorder estats.MetricsRecorder
}

// It doesn't matter whether recorded all at once or
// emitted as the

// locality and target - target is fixed? built out at client conn creation
// time... locality changes as you move it between localities, but caps the
// cardinality at the number of localities



// how to rearchitect this?
// keep a local var per call...to record metrics on (immediate record for one count at a time?)

// needs to have data *scoped to a scheduler update call*

// scWeights returns a slice containing the weights from p.subConns in the same
// order as p.subConns.

// getScWeights for scheduler - document side effect of recording weight


func (p *picker) scWeights() []float64 { // this reads sc weights from constantly updating SubConns...
	ws := make([]float64, len(p.subConns))
	now := internal.TimeNow()
	for i, wsc := range p.subConns {

		// In the helper scWeights...
		// is this on "every" scheduler update I think so hits the new

		ws[i] = wsc.weight(now, time.Duration(p.cfg.WeightExpirationPeriod), time.Duration(p.cfg.BlackoutPeriod), true)
		// Added side effects to above...

		// Currently only called here, but make sure not called otherwise...

	}



	return ws
}

func (p *picker) inc() uint32 {
	return atomic.AddUint32(&p.v, 1)
}

// testing deployment: xDS system with mock stats handlers

// xDS System with OTel as well...Yash wanted to avoid a dependency difference...

// for locality label...add "name" of weighted target as a resolver can it
// switch localities? If so need synchronization for this locality read? Maybe
// config

// And in the OTel layer unconditionally do based off what user provides or
// what? (merge this with Doug discussion about per call labels happening next
// week)

// I know for sure this emits labels unconditionally...

func (p *picker) regenerateScheduler() { // there's some sort of hook point into this...

	// on the picker, so pass all the info to the picker when it's created from persisted balancer?

	// pass it here, create the wrapped sc

	// Returning the enum value is complicated so do it in the wrapped sc...

	// make below a function on picker, no need to pass it stuff anymore all
	// data comes out of picker...

	s := p.newScheduler(/*p.scWeights(), p.inc*/) // picker is fixed, this is a new snapshot of scheduler based of live updating sc weights...
	atomic.StorePointer(&p.scheduler, unsafe.Pointer(&s))
}

func (p *picker) start(ctx context.Context) {
	p.regenerateScheduler() // or at start...
	if len(p.subConns) == 1 {
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
			case <-ticker.C: // from timer - this is called in a wrapped goroutine for the regenerated picker, picker regenerated on balancer calls
				p.regenerateScheduler()
			}
		}
	}()
}

func (p *picker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	// Read the scheduler atomically.  All scheduler operations are threadsafe,
	// and if the scheduler is replaced during this usage, we want to use the
	// scheduler that was live when the pick started.
	sched := *(*scheduler)(atomic.LoadPointer(&p.scheduler))

	pickedSC := p.subConns[sched.nextIndex()]
	pr := balancer.PickResult{SubConn: pickedSC.SubConn}
	if !p.cfg.EnableOOBLoadReport {
		pr.Done = func(info balancer.DoneInfo) {
			if load, ok := info.ServerLoad.(*v3orcapb.OrcaLoadReport); ok && load != nil {
				pickedSC.OnLoadReport(load)
			}
		}
	}
	return pr, nil
}

// weightedSubConn is the wrapper of a subconn that holds the subconn and its
// weight (and other parameters relevant to computing the effective weight).
// When needed, it also tracks connectivity state, listens for metrics updates
// by implementing the orca.OOBListener interface and manages that listener.
type weightedSubConn struct {
	balancer.SubConn
	logger *grpclog.PrefixLogger

	// The following fields are only accessed on calls into the LB policy, and
	// do not need a mutex.
	connectivityState connectivity.State
	stopORCAListener  func()

	// what are sync requirements here? Built out at init time right and read only after that? so no need for sync...
	target string
	metricsRecorder estats.MetricsRecorder
	locality string // t


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

func (w *weightedSubConn) OnLoadReport(load *v3orcapb.OrcaLoadReport) {
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
	if w.nonEmptySince == (time.Time{}) {
		w.nonEmptySince = w.lastUpdated
	}
}

// updateConfig updates the parameters of the WRR policy and
// stops/starts/restarts the ORCA OOB listener.
func (w *weightedSubConn) updateConfig(cfg *lbConfig) {
	w.mu.Lock()
	oldCfg := w.cfg
	w.cfg = cfg
	w.mu.Unlock()

	newPeriod := cfg.OOBReportingPeriod
	if cfg.EnableOOBLoadReport == oldCfg.EnableOOBLoadReport &&
		newPeriod == oldCfg.OOBReportingPeriod {
		// Load reporting wasn't enabled before or after, or load reporting was
		// enabled before and after, and had the same period.  (Note that with
		// load reporting disabled, OOBReportingPeriod is always 0.)
		return
	}
	// (Optionally stop and) start the listener to use the new config's
	// settings for OOB reporting.

	if w.stopORCAListener != nil {
		w.stopORCAListener()
	}
	if !cfg.EnableOOBLoadReport {
		w.stopORCAListener = nil
		return
	}
	if w.logger.V(2) {
		w.logger.Infof("Registering ORCA listener for %v with interval %v", w.SubConn, newPeriod)
	}
	opts := orca.OOBListenerOptions{ReportInterval: time.Duration(newPeriod)}
	w.stopORCAListener = orca.RegisterOOBListener(w.SubConn, w, opts)
}

func (w *weightedSubConn) updateConnectivityState(cs connectivity.State) connectivity.State {
	switch cs {
	case connectivity.Idle:
		// Always reconnect when idle.
		w.SubConn.Connect()
	case connectivity.Ready:
		// If we transition back to READY state, reset nonEmptySince so that we
		// apply the blackout period after we start receiving load data.  Note
		// that we cannot guarantee that we will never receive lingering
		// callbacks for backend metric reports from the previous connection
		// after the new connection has been established, but they should be
		// masked by new backend metric reports from the new connection by the
		// time the blackout period ends.
		w.mu.Lock()
		w.nonEmptySince = time.Time{}
		w.mu.Unlock()
	case connectivity.Shutdown:
		if w.stopORCAListener != nil {
			w.stopORCAListener()
		}
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
// will cause the backend weight to be treated as the mean of the weights of
// the other backends. comment here about for scheduler...
func (w *weightedSubConn) weight(now time.Time, weightExpirationPeriod, blackoutPeriod time.Duration, forScheduler bool) (weight float64) { // Three come out of weight helper on weighted SubConn...
	w.mu.Lock() // why is there a mutex here?
	defer w.mu.Unlock()

	// fix tests (might call weight or newScheduler directly) and figure out named return value

	// add a bool or rename to document side effects ("record metrics as
	// scheduler is being rebuilt")
	weight = 0

	// Endpoints without usable weights will be recorded as weight 0 - so as
	// written logic already present...
	if forScheduler {
		// defer return value (read golang playground for this) according to go playground the variable changes over time
		defer endpointWeightsHandle.Record(w.metricsRecorder, weight, []string{w.target, w.locality}... /*[]string{target, locality}*/)
	}

	// If the most recent update was longer ago than the expiration period,
	// reset nonEmptySince so that we apply the blackout period again if we
	// start getting data again in the future, and return 0.
	if now.Sub(w.lastUpdated) >= weightExpirationPeriod {
		// No need to persist around and record at end that would create unnecessarily complex code...
		if forScheduler {
			endpointWeightStaleHandle.Record(w.metricsRecorder, 1, []string{w.target, w.locality}...)
		}
		w.nonEmptySince = time.Time{}
		return
	} // load reporting happens at a layer above - how to trigger this operation...?


	// If we don't have at least blackoutPeriod worth of data, return 0.
	if blackoutPeriod != 0 && (w.nonEmptySince == (time.Time{}) || now.Sub(w.nonEmptySince) < blackoutPeriod) { // this is relevant, so need some sort of data store scoped to this function...
		// Plumb mr through sc local var or have it an object on something else?
		endpointWeightNotYetUsableHandle.Record(w.metricsRecorder, 1, []string{w.target, w.locality}...)

		return
	}
	// This thing determines the weight 1:1...maybe send Doug implementation PR just to see if it makes sense...


	return w.weightVal
} // does a returned 0 mean the endpoint weight is no longer usable...

// rr fallback behavior is the only thing not emitted from the helper above...

// how to trigger different weight updates/scheduler updates?


// "Number of endpoints from each scheduler update whose latest weight is older
// than the expiration period."
// Is number of endpoints = number of ready SubConns, can I merge with the algorithm as written?

// Mechanically try appending to resolver attributes with the name iteration in cluster impl...
