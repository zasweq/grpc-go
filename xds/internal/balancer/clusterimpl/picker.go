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
// does it need to be set for logical DNS?

// how to take this and send it to stats handler?

// get the thing out of context, *that* is mutable, this thing gets it then sets it on that mutable thing
// thread safe...not shared, per attempt

// put these helpers in OTel eventually...
// ctx -> mutable, ctx is not mutable
type labels struct {
	telemetryLabels map[string]string // mutable map, if I mutate this is it a pointer?
}

type labelsKey struct {}

// GetLabels...
func GetLabels(ctx context.Context) *labels {

}

// SetLabels sets the labels
func SetLabels(ctx context.Context, labels *labels) {

}

// Update in flight PR to only parse the two labels wanted (still ignore not
// string type), wait until Yash sends his out...

// unconditionally set even for DNS, since it uses cluster_impl

func (d *picker) Pick(info balancer.PickInfo) (balancer.PickResult, error) { // inject labels into something that gets passed to OTel
	// d.telemetryLabels // map[string]string
	// stick these telemetryLabels onto something that gets passed to the stats handler...
	// context...or maybe something else
	// info has context in it, that's only link I think

	// info.Ctx // this is the RPC's context, and may contain relevant RPC-Level information like the outgoing header's metadata
	if labels := GetLabels(info.Ctx); labels != nil {
		for key, value := range labels.telemetryLabels {
			labels.telemetryLabels[key] = value // is this write permanent on the heap?
		}
	} // This is it...am I done? Write an e2e test or what?

	// stick it RPC's ctx throguh a helper in OTel, then pull it out in OTel, if set use it if not "unknown" or "other"

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
