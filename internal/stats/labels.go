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

package stats

import "context"

// Labels...
type Labels struct {
	// TelemetryLabels...
	TelemetryLabels map[string]string // mutable map, if I mutate this is it a pointer? this can only be tested through e2e test...
}

type labelsKey struct {}

// all helpers scale TelemetryLabels up and down...


// this will be the same always Get

// persist the raw map[string]string, but that is immutable, do I need the
// second layer here...lol xDS e2e doesn't even work...does default work for the
// normal case out of the box?

// return raw map now...?

// GetLabels returns the Labels stored in theo context, or nil if there is one
func GetLabels(ctx context.Context) *Labels {
	labels, _ := ctx.Value(labelsKey{}).(*Labels) // get pointer, than set mutable data through that pointer...
	return labels
}

// Probably want these setters and getters to take map[string]string
// vs. layer( map[string]string )...do I even need this layer?

// SetLabels sets the Labels
func SetLabels(ctx context.Context, labels *Labels) context.Context {
	// could also append
	return context.WithValue(ctx, labelsKey{}, labels)
}

// unit tests what components affected, what breaks and doesn't break...
// xds/internal/balancer/cdsbalancer (processes Labels) (does this need e2e test?)
// xds/internal/balancer/clusterimpl (takes Labels from config - puts it in picker (could write a unit test for this))
// xds/internal/balancer/clusterresolver (takes Labels in discovery mechanism, and sets them in cluster impl) (does this need e2e test?)
// these three - only behavior except plumbing is this clusterresolver component...

// what is a valid e2e test for this?

// will parsing/unparsing through the tree create any headache?
// Need to set Labels in CDS, the e2e test can catch any parsing/unparsing logic...
// do I need to add any unit tests for this?

// rebase onto cds-metadata change nothing should conflict (after done?)

/*
// Labels...
type labels struct {
	// TelemetryLabels...
	telemetryLabels map[string]string // mutable map, if I mutate this is it a pointer? this can only be tested through e2e test...
}

type labelsKey struct {}

// all helpers scale TelemetryLabels up and down...


// this will be the same always Get

// persist the raw map[string]string, but that is immutable, do I need the
// second layer here...lol xDS e2e doesn't even work...does default work for the
// normal case out of the box?

// return raw map now...?

// GetLabels returns the Labels stored in theo context, or nil if there is one
func GetLabels(ctx context.Context) map[string]string {
	labels, _ := ctx.Value(labelsKey{}).(*labels) // get pointer, than set mutable data through that pointer...
	if labels == nil {
		return nil
	}
	return labels.telemetryLabels
}

// Probably want these setters and getters to take map[string]string
// vs. layer( map[string]string )...do I even need this layer?

// SetLabels sets the Labels
func SetLabels(ctx context.Context, telemetryLabels map[string]string) context.Context {
	// could also append
	return context.WithValue(ctx, labelsKey{}, &labels{
		telemetryLabels: telemetryLabels,
	})
}
*/