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

package clusterimpl

import (
	"context"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/wrr"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/xds/internal/xdsclient"
	"google.golang.org/grpc/xds/internal/xdsclient/load"

	v3orcapb "github.com/cncf/xds/go/xds/data/orca/v3"
)

// NewRandomWRR is used when calculating drops. It's exported so that tests can
// override it.
var NewRandomWRR = wrr.NewRandom

const million = 1000000

type dropper struct {
	category string
	w        wrr.WRR
}

// greatest common divisor (GCD) via Euclidean algorithm
func gcd(a, b uint32) uint32 {
	for b != 0 {
		t := b
		b = a % b
		a = t
	}
	return a
}

func newDropper(c DropConfig) *dropper {
	w := NewRandomWRR()
	gcdv := gcd(c.RequestsPerMillion, million)
	// Return true for RequestPerMillion, false for the rest.
	w.Add(true, int64(c.RequestsPerMillion/gcdv))
	w.Add(false, int64((million-c.RequestsPerMillion)/gcdv))

	return &dropper{
		category: c.Category,
		w:        w,
	}
}

func (d *dropper) drop() (ret bool) {
	return d.w.Next().(bool)
}

// loadReporter wraps the methods from the loadStore that are used here.
type loadReporter interface {
	CallStarted(locality string)
	CallFinished(locality string, err error)
	CallServerLoad(locality, name string, val float64)
	CallDropped(locality string)
}

// Picker implements RPC drop, circuit breaking drop and load reporting.
type picker struct {
	drops     []*dropper
	s         balancer.State
	loadStore loadReporter
	counter   *xdsclient.ClusterRequestsCounter
	countMax  uint32
	telemetryLabels map[string]string
} // plumbs the map[string]string all the way to OTel component...

func newPicker(s balancer.State, config *dropConfigs, loadStore load.PerClusterReporter, telemetryLabels map[string]string) *picker {
	return &picker{
		drops:     config.drops,
		s:         s,
		loadStore: loadStore,
		counter:   config.requestCounter,
		countMax:  config.requestCountMax,
	}
}

/*
Plumbing in Java

On the client side, an interface will be passed into the load balancing policy
via CallOptions. The interface will allow adding the service labels. The OTel
stats interceptor will add in an implementation of this interface such that the
attributes can be added for CSM metrics.

Plumbing in Go
The plumbing will be the same as Java, but with Context instead of CallOptions.

So I pass in something *in* the context?
*/

// Questions:
// does it need to be set for logical DNS? yes

// how to take this and send it to stats handler?

// get the thing out of context, *that* is mutable, this thing gets it then sets it on that mutable thing
// thread safe...not shared, per attempt

// put these helpers in OTel eventually...
// ctx -> mutable, ctx is not mutable
type labels struct {
	telemetryLabels map[string]string // mutable map, if I mutate this is it a pointer? this can only be tested through e2e test...
}

type labelsKey struct {}

/*
// rpcInfo is RPC information scoped to the RPC attempt life span client side,
// and the RPC life span server side.
type rpcInfo struct {
	mi *metricsInfo
}

type rpcInfoKey struct{}

func setRPCInfo(ctx context.Context, ri *rpcInfo) context.Context {
	return context.WithValue(ctx, rpcInfoKey{}, ri)
}

// getRPCInfo returns the rpcInfo stored in the context, or nil
// if there isn't one.
func getRPCInfo(ctx context.Context) *rpcInfo {
	ri, _ := ctx.Value(rpcInfoKey{}).(*rpcInfo)
	return ri
}
*/

// unit tests what components affected, what breaks and doesn't break...
// xds/internal/balancer/cdsbalancer (processes labels) (does this need e2e test?)
// xds/internal/balancer/clusterimpl (takes labels from config - puts it in picker (could write a unit test for this))
// xds/internal/balancer/clusterresolver (takes labels in discovery mechanism, and sets them in cluster impl) (does this need e2e test?)
// these three - only behavior except plumbing is this clusterresolver component...

// what is a valid e2e test for this?

// will parsing/unparsing through the tree create any headache?
// Need to set labels in CDS, the e2e test can catch any parsing/unparsing logic...
// do I need to add any unit tests for this?

// test with fake stats handler move to OTel...will need to configure with CDS eventually so might as well
// xDS e2e test with fake stats handler...
// for e2e test - have stats handler plumbed into a channel with xDS
// set labels, or can test later? or test at the picker level?

// rebase onto cds-metadata change nothing should conflict (after done?)

// GetLabels returns the labels stored in theo context, or nil if there is one
func GetLabels(ctx context.Context) *labels {
	labels, _ := ctx.Value(labelsKey{}).(*labels)
	return labels
}

// SetLabels sets the labels
func SetLabels(ctx context.Context, labels *labels) context.Context {
	return context.WithValue(ctx, labelsKey{}, labels)
}

// c tests given
// Give it config see what picker

// e2e test xDS OTel, fake stats handler...
// what c ended up doing since separate, no dependencies between each other
// in xDS e2e test fake stats handler sets...check fake stats handler gets optional labels from

// clean this up then run xds test suite to see what breaks and what doesn't...

// otel e2e case - interceptor adds labels, make sure OTel plugin records those
// labels...


// unit tests all pass

// now time to write xDS e2e test testing this (will add OTel e2e test once that and this is merged)
// I guess new feature in OTel so can add e2e test then...

// Do I need to write unit tests for any of these components maybe the cluster impl?


func (d *picker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	if info.Ctx != nil {
		if labels := GetLabels(info.Ctx); labels != nil {
			for key, value := range labels.telemetryLabels {
				labels.telemetryLabels[key] = value // is this write permanent on the heap? need e2e test...also need to write an example test?
			}
		} // even for dropped or queue ones
	}

	// Don't drop unless the inner picker is READY. Similar to
	// https://github.com/grpc/grpc-go/issues/2622.
	if d.s.ConnectivityState == connectivity.Ready {
		// Check if this RPC should be dropped by category.
		for _, dp := range d.drops {
			if dp.drop() {
				if d.loadStore != nil {
					d.loadStore.CallDropped(dp.category)
				}
				return balancer.PickResult{}, status.Errorf(codes.Unavailable, "RPC is dropped")
			}
		}
	}

	// Check if this RPC should be dropped by circuit breaking.
	if d.counter != nil {
		if err := d.counter.StartRequest(d.countMax); err != nil {
			// Drops by circuit breaking are reported with empty category. They
			// will be reported only in total drops, but not in per category.
			if d.loadStore != nil {
				d.loadStore.CallDropped("")
			}
			return balancer.PickResult{}, status.Errorf(codes.Unavailable, err.Error())
		}
	}

	var lIDStr string
	pr, err := d.s.Picker.Pick(info)
	if scw, ok := pr.SubConn.(*scWrapper); ok {
		// This OK check also covers the case err!=nil, because SubConn will be
		// nil.
		pr.SubConn = scw.SubConn
		var e error
		// If locality ID isn't found in the wrapper, an empty locality ID will
		// be used.
		lIDStr, e = scw.localityID().ToString()
		if e != nil {
			logger.Infof("failed to marshal LocalityID: %#v, loads won't be reported", scw.localityID())
		}
	}

	if err != nil {
		if d.counter != nil {
			// Release one request count if this pick fails.
			d.counter.EndRequest()
		}
		return pr, err
	}

	if d.loadStore != nil {
		d.loadStore.CallStarted(lIDStr)
		oldDone := pr.Done
		pr.Done = func(info balancer.DoneInfo) {
			if oldDone != nil {
				oldDone(info)
			}
			d.loadStore.CallFinished(lIDStr, info.Err)

			load, ok := info.ServerLoad.(*v3orcapb.OrcaLoadReport)
			if !ok || load == nil {
				return
			}
			for n, c := range load.NamedMetrics {
				d.loadStore.CallServerLoad(lIDStr, n, c)
			}
		}
	}

	if d.counter != nil {
		// Update Done() so that when the RPC finishes, the request count will
		// be released.
		oldDone := pr.Done
		pr.Done = func(doneInfo balancer.DoneInfo) {
			d.counter.EndRequest()
			if oldDone != nil {
				oldDone(doneInfo)
			}
		}
	}

	return pr, err
}
