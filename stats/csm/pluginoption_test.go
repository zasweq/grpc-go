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

package csm

import (
	"context"
	"encoding/base64"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// what scope of tests for this PR...
// local e2e tests - provide a resource itself...fake gce/gke unknown
// setup env vars
// enable csm - injecting labels you want...
// rpc and verify labels
// mock xDS enabled channel...

// Unit tests:
// Behavior of adds labels - send it blob of data, local labels, what labels it generates
// no metadata - local + unknown
// get gce env
// get gke env
// blob has unknown, or random

// blob creation, same code...
// ignoring network add two local labels
// in test doesn't set it

// send a pr to move bootstrap too

// could add a seperate layer that passes in local so you can get just the metadat emissions or just
// don't set bootstrap set env var to {} and expect local labels and


// maybe for bootstrap stuff I don't need to move everything and just rewrite
// the env var parsing and handling...and just unmarshal it without all the
// extra logic we do from json -> internal struct


// also, for trailers only, need to read x-envoy-peer-metadata if no headers at
// the OTel layer client side... Done in sh, are we guaranteed the calls are
// correct here?


// unknown at init time for all labels...
// test init time with fake runtime?
/*
func (s) TestAddLabels(t *testing.T) {
	// Test add and get in same function?

	// this is on the globally created blob, override this...
	// maybe add blob and local labels to context, see if it has right values by decoding it and looking at yourself
}

func setupLocalLabels() {
	// The value of “csm.workload_canonical_service” comes from
	// “CSM_CANONICAL_SERVICE_NAME” env var, “unknown” if unset.

	// Setup bootstrap, parse the node id out of it.
	// I can either a. inject bootstrap somehow maybe try this to help my helper to see if helper works...
	// b. inject local labels
	// c. ignore local labels...
}*/

func (s) TestGetLabels(t *testing.T) {
	// function signature is:
	// GetLabels(ctx context.Context) map[string]string (need to plumb something into context)

	// Populate metadata in blob with base64encoded headers, helper for
	// this blob?

	// expect labels emitted
	// local labels (need to set this up, this should be appended to all labels emitted...)


	// see if this compiles and fails because of local labels first...
	tests := []struct{ // sanity checks for panics at least...
		name string
		// yeah do proto encoding in test readable
		metadataExchangeLabels map[string]string
		labelsWant map[string]string // can you use reflection on this?
	}{ // Need to append local labels to this now. Can the unknowns be messed up by something set in the env...
		{
			name: "metadata-not-set-only-local-labels",
			metadataExchangeLabels: nil,

			// Also from the xDS bootstrap (need to make a singleton): I guess add it to every one

			// The value of “csm.workload_canonical_service” comes from
			// “CSM_CANONICAL_SERVICE_NAME” env var, “unknown” if unset

			labelsWant: map[string]string{
				// local labels +
				// hardcoded "unknown for the two fields...could make a var
				// any edge cases here? or in any test case

				// Local labels:
				"csm.workload_canonical_service": "unknown",
				"csm.mesh_id": "unknown",


				"csm.remote_workload_type": "unknown",
				"csm.remote_workload_canonical_service": "unknown",
			},
		},
		// Local labels should show up on the below conversions as well...
		{
			name: "metadata-partially-set",
			metadataExchangeLabels: map[string]string{
				"type": "not-gce-or-gke",
				"ignore-this": "ignore-this",
			},
			labelsWant: map[string]string{ // when I run this it'll probably break
				// Local labels:
				"csm.workload_canonical_service": "unknown",
				"csm.mesh_id": "unknown",

				"csm.remote_workload_type": "not-gce-or-gke",
				"csm.remote_workload_canonical_service": "unknown",
			},
		},
		{
			name: "google-compute-engine",
			metadataExchangeLabels: map[string]string{
				"type": "gcp_compute_engine",
				"canonical_service": "canonical_service_val",
				// populate all labels top level and gce
				"project_id": "unique-id",
				"location": "us-east",
				"workload_name": "workload_name_val",
			}, // bootstrap config stuff parsing test too...
			labelsWant: map[string]string{ // when I run this it'll probably break
				// local labels // wrt env var, what distincts unset from set and empty?
				// Local labels:
				"csm.workload_canonical_service": "unknown",
				"csm.mesh_id": "unknown",

				"csm.remote_workload_type": "gcp_compute_engine",
				"csm.remote_workload_canonical_service": "canonical_service_val", // comes off the wire
				"csm.remote_workload_project_id": "unique-id",
				"csm.remote_workload_location": "us-east",
				"csm.remote_workload_name":	"workload_name_val",
			},
		},
		{
			name: "google-compute-engine-labels-partially-set-with-extra", // unset should go to unknown, ignore others
			metadataExchangeLabels: map[string]string{
				"type": "gcp_compute_engine",
				"canonical_service": "canonical_service_val",
				// set half the labels for gcp, set some for gke, should ignore
				"project_id": "unique-id",
				"location": "us-east",
				// "workload_name": , unset workload name
				// set the labels here...
				"namespace_name": "should-be-ignored",
				"cluster_name": "should-be-ignored",
			},
			labelsWant: map[string]string{
				// local labels // wrt env var, what distincts unset from set and empty?
				// Local labels:
				"csm.workload_canonical_service": "unknown",
				"csm.mesh_id": "unknown",

				"csm.remote_workload_type": "gcp_compute_engine",
				"csm.remote_workload_canonical_service": "canonical_service_val", // comes off the wire
				"csm.remote_workload_project_id": "unique-id",
				"csm.remote_workload_location": "us-east",
				"csm.remote_workload_name":	"unknown",
			},
		},
		{
			name: "google-kubernetes-engine",
			metadataExchangeLabels: map[string]string{
				"type": "gcp_kubernetes_engine",
				"canonical_service": "canonical_service_val",
				// populate all labels top level and gce
				"project_id": "unique-id",
				"namespace_name": "namespace_name_val",
				"cluster_name": "cluster_name_val",
				"location": "us-east",
				"workload_name": "workload_name_val",
			}, // bootstrap config stuff parsing test too...
			labelsWant: map[string]string{
				// Local labels:
				"csm.workload_canonical_service": "unknown",
				"csm.mesh_id": "unknown",

				"csm.remote_workload_type": "gcp_kubernetes_engine",
				"csm.remote_workload_canonical_service": "canonical_service_val", // comes off the wire
				"csm.remote_workload_project_id": "unique-id",
				"csm.remote_workload_cluster_name": "cluster_name_val",
				"csm.remote_workload_namespace_name": "namespace_name_val",
				"csm.remote_workload_location": "us-east",
				"csm.remote_workload_name":	"workload_name_val",
			},
		},
		{
			name: "google-kubernetes-engine-labels-partially-set",
			metadataExchangeLabels: map[string]string{
				"type": "gcp_kubernetes_engine",
				"canonical_service": "canonical_service_val",
				// populate all labels top level and gce
				"project_id": "unique-id",
				"namespace_name": "namespace_name_val",
				// "cluster_name": "cluster_name_val",
				"location": "us-east",
				// "workload_name": "workload_name_val",
			}, // bootstrap config stuff parsing test too...
			labelsWant: map[string]string{
				// Local labels:
				"csm.workload_canonical_service": "unknown",
				"csm.mesh_id": "unknown",

				"csm.remote_workload_type": "gcp_kubernetes_engine",
				"csm.remote_workload_canonical_service": "canonical_service_val", // comes off the wire
				"csm.remote_workload_project_id": "unique-id",
				"csm.remote_workload_cluster_name": "unknown",
				"csm.remote_workload_namespace_name": "namespace_name_val",
				"csm.remote_workload_location": "us-east",
				"csm.remote_workload_name":	"unknown",
			},
		},
		// error cases/edge cases like two md just make everything unknown, perhaps a const for the full struct for above too maybe just inline?

		// stuff like numerous metadata values etc. error conditions which
		// trigger unknown as like the first test case

	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pbLabels := &structpb.Struct{
				Fields: map[string]*structpb.Value{},
			}
			for k, v := range test.metadataExchangeLabels {
				pbLabels.Fields[k] = structpb.NewStringValue(v)
			}
			protoWireFormat, err := proto.Marshal(pbLabels) // once this works add it to main...test it?
			if err != nil {
				t.Fatalf("Error marshaling proto: %v", err)
			}
			metadataExchangeLabelsEncoded = base64.RawStdEncoding.EncodeToString(protoWireFormat)

			// key value, if it's an error test that...
			// maybe unhappy case below with invalid syntax?
			md := metadata.New(map[string]string{
				"x-envoy-peer-metadata": metadataExchangeLabelsEncoded,
			})

			// how will we do this client side....from headers or trailers
			// callout? appendtooutgoing context in interceptors, once you've
			// init had local labels. will go out on wire, can test this in e2e from server side...

			// the interceptor is per call...are headers per attempt
			// how does client interceptor get it SetHeader and SetTrailer...
			// *metadata.Metadata...SetHeader and Trailer




			// I think I can use one instance of this plugin option move this to top level

			// client can get it from headers and trailers - logic on this side in sh whichever comes first
			// server from headers
			// atomic bool in wrapped stream...

			cpo := &csmPluginOption{} // or do this top level...
			labelsGot := cpo.GetLabels(md)

			if diff := cmp.Diff(labelsGot, test.labelsWant); diff != "" {
				// I could use error here...
				t.Fatalf("cpo.GetLabels returned unexpected value (-got, +want): %v", diff)
			}
		})
	}
}

// send out what I have...mock certain types in e2e? Since then can test full flow

// I also need to figure out a way to mock bootstrap config...
type mockResourceDetector struct {}

func (mrd *mockResourceDetector) {} // there's a lottt of methods on this resource type, what to do here?

// Maybe have no resource, unknown for both local labels...and assert on length and presence for all the headers you want out
// only way this happens is if you run in env

// TestAddLabels tests the AddLabels function on the csm plugin option. This
// should append the metadata exchange labels constructed at static init time
// based on the environment. Due to the enviornment being of type unknown, only
// certain labels are expected to be sent.
func (s) TestAddLabels(t *testing.T) {

	// Doesn't trigger stuff in init... that might discover a failing codepath
	// works so far...only for further processing if gce or gke...

	// Nothing set up in env...from init, should set labels once explicitly passed in

	// make an RPC and call out into md? Or just e2e with labels implicitly tested in OTel e2e tests...

	// AddLabels as a snapshot seems like you could just pull out the context
	// to not have to be e2e
	cpo := &csmPluginOption{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second * 5)
	defer cancel()
	ctx = cpo.AddLabels(ctx) // appends to outgoing, should work in streaming and unary interceptor...

	// or could honestly read the global thing that hits
	// base 64 decode it proto unmarshal it (useful length)
	// and assert length and values, only way this would break is
	// type unknown but when would that ever hit if not run in a certain env

	// regardless get a blob out, and then base 64 decode it...I like pulling it out of the ctx
	md, ok := metadata.FromOutgoingContext(ctx) // this I think, since it's append to outgoing context
	if !ok {
		t.Fatalf("no metadata in outgoing context")
	}
	// The metadata should contain the metadata "x-envoy-peer-metadata". The
	// value for this metadata should be a single value that is base 64 encoded
	// proto wire format.
	val := md.Get("x-envoy-peer-metadata")
	if len(val) != 1 {
		t.Fatalf("wrong number of values for metadata key \"x-envoy-peer-metadata\": got %v, want %v", len(val), 1)
	}
	// This value should successfully base 64 decode and unmarshal from proto
	// wire format.
	protoWireFormat, err := base64.RawStdEncoding.DecodeString(val[0])
	if err != nil {
		t.Fatalf("error base 64 decoding metadata val: %v", err)
	}
	spb := &structpb.Struct{}
	if err := proto.Unmarshal(protoWireFormat, spb); err != nil {
		t.Fatalf("error unmarshaling proto wire format: %v", err)
	}
	// The final labels should have two fields in them, type, and canonical
	// service. These two labels are the only labels that should be populated if
	// the type of environment is unknown.

	// They should have value "unknown".
	fields := spb.GetFields() // Default/make t test?

	// fields // map[string]*Value, can make assertions on this that are guaranteed to be true...
	// local labels come in from get, unless they're not set (can you unset an env var)
	// you can assert stuff won't work...ugh set globally
	if len(fields) != 2 { // two local labels, plus two local labels set to stuff
		t.Fatalf("wrong number of Struct proto metadata fields: got %v, want %v", len(fields), 2)
	}
	// Should have string val...this can get tested e2e

	// 2 labels, could be unknown, dependent on type...mock the call for determinism?
	// type b has labels...
	// type c has labels...
	if _, ok := fields["type"]; !ok { // If I want I can also read out this val...
		t.Fatal("type label is not present in metadata exchange proto Struct")
	}
	if _, ok := fields["canonical_service"]; !ok {
		t.Fatal("canonical_service label is not in metadata exchange proto Struct")
	}



	// If I figure out to inject a mock resource reader...
	// I should do gce labels, and gke labels, and make sure there's a distinction
	// t-test?
	// gce/gke are tested in GetLabels by different ways of populating context...



	// csm.workload_canonical_service
	// csm.mesh_id
	// csm.remote_workload_type
	// csm.remote_workload_canonical_service

	// could assert fields all have value unknown...
	// or too much properties
	// def at the least the 4 values come in hardcoded, the only way this doesn't work is this test is deployed on gce,
	// which will never happen, you deploy prod code on gcp
} // send PR with fake bootstrap, do I need to mock a resource for the scope of this PR or is that e2e...

// determineCSM(target/cc ref)...called once call option specifies to, and calls into OTel with this string...

// I think can still take string target here...
// Even if you give it the whole cc, just deref it like cc.Target to pass the string...

func (s) TestDetermineTargetCSM(t *testing.T) {
	// return t.URL.Scheme + "://" + t.URL.Host + "/" + t.Endpoint()
	// passthrough:///Non-Existent.Server:80
	tests := []struct{
		name string
		target string
		targetCSM bool
	}{
		{
			name: "dns:///localhost",
			target: "normal-target-here",
			targetCSM: false,
		},
		// can play around this wrt rules
		// url schema here..
		{
			name: "xds-no-authority",
			target: "xds:///localhost",
			targetCSM: true,
		},
		{
			name: "xds-traffic-director-authority",
			target: "xds://traffic-director-global.xds.googleapis.com/localhost",
			targetCSM: true,
		},
		{
			name: "xds-not-traffic-director-authority",
			target: "xds://not-traffic-director-authority/localhost",
			targetCSM: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cpo := &csmPluginOption{} // could just be a global func, but it's fine I think if OTel holds a ref to this...
			if got := cpo.determineTargetCSM(test.target); got != test.targetCSM {
				t.Fatalf("cpo.determineTargetCSM(%v): got %v, want %v", test.target, got, test.targetCSM)
			}
		})
	}
}



// How to test e2e? sanity check I think for e2e with csm layer configured with
// OTel...need to figure out how to do the meter provider stuff anyway...

// pass either a bit or something that determines a bit like target to stats handler...
// stats handler chooses per call whether to add CSM labels or not...

// e2e test that ringhash works...

// with csm enabled make sure retries work...
// Do it by the ordering of stream operations...
// as you intercept them and can handle it that way


// bootstrap in separate code, just need nodeID so a lot more lightweight parsing logic,
// and then helper for parsing with str.Split...

// if I do bootstrap myself, I can just send the csm plugin option as is (need to unit test...)
// just keep marshaling, if it errors, it will error at xds time

// unexported lightweight function

// yeah append to outgoing context...

// is determine target right?
