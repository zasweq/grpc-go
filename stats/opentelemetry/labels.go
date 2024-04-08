/*
 *
 * Copyright 2024 gRPC authors.
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

package opentelemetry

import "context"

// Questions:
// does it need to be set for logical DNS? yes

// how to take this and send it to stats handler?

// get the thing out of context, *that* is mutable, this thing gets it then sets it on that mutable thing
// thread safe...not shared, per attempt

// put these helpers in OTel eventually...
// ctx -> mutable, ctx is not mutable

// Labels...
type Labels struct {
	// TelemetryLabels...
	TelemetryLabels map[string]string // mutable map, if I mutate this is it a pointer? this can only be tested through e2e test...
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
// xds/internal/balancer/cdsbalancer (processes Labels) (does this need e2e test?)
// xds/internal/balancer/clusterimpl (takes Labels from config - puts it in picker (could write a unit test for this))
// xds/internal/balancer/clusterresolver (takes Labels in discovery mechanism, and sets them in cluster impl) (does this need e2e test?)
// these three - only behavior except plumbing is this clusterresolver component...

// what is a valid e2e test for this?

// will parsing/unparsing through the tree create any headache?
// Need to set Labels in CDS, the e2e test can catch any parsing/unparsing logic...
// do I need to add any unit tests for this?

// test with fake stats handler move to OTel...will need to configure with CDS eventually so might as well
// xDS e2e test with fake stats handler...
// for e2e test - have stats handler plumbed into a channel with xDS
// set Labels, or can test later? or test at the picker level?

// rebase onto cds-metadata change nothing should conflict (after done?)

// GetLabels returns the Labels stored in theo context, or nil if there is one
func GetLabels(ctx context.Context) *Labels {
	labels, _ := ctx.Value(labelsKey{}).(*Labels) // get pointer, than set mutable data through that pointer...
	return labels
}

// SetLabels sets the Labels
func SetLabels(ctx context.Context, labels *Labels) context.Context {
	return context.WithValue(ctx, labelsKey{}, labels)
}