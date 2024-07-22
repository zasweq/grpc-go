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
	v3orcapb "github.com/cncf/xds/go/xds/data/orca/v3"
	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/internal/stats"
	"google.golang.org/grpc/internal/wrr"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/xds/internal/xdsclient"
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
	drops           []*dropper
	s               balancer.State
	loadStore       loadReporter
	counter         *xdsclient.ClusterRequestsCounter
	countMax        uint32
	telemetryLabels map[string]string
}

func (b *clusterImplBalancer) newPicker(config *dropConfigs) *picker {
	return &picker{
		drops:           config.drops,
		s:               b.childState,
		loadStore:       b.loadWrapper,
		counter:         config.requestCounter,
		countMax:        config.requestCountMax,
		telemetryLabels: b.telemetryLabels,
	}
}

func (d *picker) Pick(info balancer.PickInfo) (balancer.PickResult, error) {
	// Unconditionally set labels if present, even dropped or queued RPC's can
	// use these labels.
	if info.Ctx != nil {
		if labels := stats.GetLabels(info.Ctx); labels != nil && labels.TelemetryLabels != nil {
			for key, value := range d.telemetryLabels {
				labels.TelemetryLabels[key] = value
			}
		}
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

	// Locality ID comes from this wrapped SubConn and then passed upward...
	// do we want to unconditionally set...set empty (I think if nil above sets to "unknown")

	// unset/empty/set possibilities passed to stats handler...

	// when do I want to set it, what do I want the possibilities to become
	// labels wise...

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

	// " If no locality information is available, the label will be set to the empty string."

	// I think I can do it right here - pick didn't fail (I don't think will be
	// able to get information from scw if pick fails anyway...)

	// just sets the callback for load reporting...

	lIDStr // empty locality ID will be used possibly, from the if conditional above, otherwise set but otherwise empty "unknown locality"

	if labels := stats.GetLabels(info.Ctx); labels != nil && labels.TelemetryLabels != nil {
		/*for key, value := range d.telemetryLabels {
			labels.TelemetryLabels[key] = value
		}*/

		// I think this key doesn't really matter it's used to access so loses
		// encoding anyway...add this to e2e test? will need to setup a whole
		// xDS Config so do unit tests and make xDS e2e test come from WRR
		// deployment...

		labels.TelemetryLabels["grpc.lb.locality"] = lIDStr // unconditionally overwritten, even if it's overwritten with empty...
	} // Conflates with numerous labels set...? I don't think so

	// How to test this? Maybe same thing as xDS Labels...but then can't trigger
	// locality unless you wrap it with a SubConn...this is a two line change

	// for per call...but our per call doesn't have an xDS tree to test this...

	// xDS Labels test Simulates this by writing it in interceptor...so add it
	// to that for this cluster impl to pick up...

	// ^^^ simulates cluster impl passes it to stats handler...but the issue is we don't have a locality ID Str in this mock yes we do...

	// vvv add OTel functionality and mock the thing in tests too and configure it on as well...yeah need to add OTel functionality that processes it...

	// This is separate so after

	// Get to all of Doug's comments, fix vet (cherry pick commit) for next PR...

	// Then do this - this is honestly standalone...


	// It mocks in OTel so add it to this mock too...I think that's good enough for the *OTel functionality*
	// Simulates this behavior...

	// xDS Tree e2e...

	// so maybe as part of WRR deployed as top level balancer (it'll have stats handlers)
	// I can check the per call emissions of locality ID




	// for non per call in weighted target resolver attribute that comes with my
	// next PR....



	// either I set it to zero in that if or it's just the zero anyway...
	// zero value for string: "" (the empty string) for strings

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
				d.loadStore.CallServerLoad(lIDStr, n, c) // load reporting on locality string here...
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
