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
	"encoding/base64"
	"google.golang.org/protobuf/proto"
	"testing"

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


// also, for trailers only, need to read x-envoy-peer-metadata if no headers at the OTel layer client side...
//


// unknown at init time for all labels...
// test init time with fake runtime?

func (s) TestAddLabels(t *testing.T) {
	// Test add and get in same function?

	// this is on the globally created blob, override this...
	// maybe add blob and local labels to context, see if it has right values by decoding it and looking at yourself
}

func setupLocalLabels() {
	// The value of “csm.workload_canonical_service” comes from
	// “CSM_CANONICAL_SERVICE_NAME” env var, “unknown” if unset.

	// Setup bootstrap, parse the node id out of it.
	// I can either a. inject bootstrap somehow
	// b. inject local labels
	// c. ignore local labels...
}

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
	}{
		{
			name: "metadata-not-set-only-local-labels",
			metadataExchangeLabels: nil,

			// Also from the xDS bootstrap (need to make a singleton): I guess add it to every one

			// The value of “csm.workload_canonical_service” comes from
			// “CSM_CANONICAL_SERVICE_NAME” env var, “unknown” if unset

			labelsWant: map[string]string{
				// local labels +
				// hardcoded "unknown for the two fields...could make a var
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
				// local labels
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
				// local labels
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
				// local labels
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

		// stuff like numerous metadata values etc.
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

			// either construct md or stick it in the context
			// how will we do this client side....from headers or trailers callout?
			// the interceptor is per call...are headers per attempt
			// how does client interceptor get it SetHeader and SetTrailer...
			// *metadata.Metadata...SetHeader and Trailer



			// does


			// or just build out md with blob above as value...
			// ctx here with timeout and also with incoming metadata having the blob

			// I think I can use one instance of this plugin option move this to top level

			// what's the intended API of this...

			// client can get it from headers and trailers - logic on this side for both
			// server from headers
			// looks like md, anyways the
			cpo := &csmPluginOption{} // or do this top level...
			labelsGot := cpo.GetLabels(md) // first of all is this api even correct? Do I pass it ctx or the headers and trailers received...it is in incoming context but could you either or
			// could just pass in metadata.MD from stats handler, seems cleaner...

			// I could make this interface be md...
			// that might be cleaner...idk if trailers make it's way to ctx

			if diff := cmp.Diff(labelsGot, test.labelsWant); diff != "" {
				// I could use error here...
				t.Fatalf("cpo.GetLabels returned unexpected value (-got, +want): %v", diff)
			}
		})
	}
}

// How to test e2e? sanity check I think for e2e with csm layer configured with
// OTel...need to figure out how to do the meter provider stuff anyway...

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
