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
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/testutils/xds/bootstrap"
	"google.golang.org/grpc/metadata"

	"go.opentelemetry.io/otel/attribute"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// send a pr to move bootstrap too

// need a full working bootstrap in this case...
// maybe for bootstrap stuff I don't need to move everything and just rewrite
// the env var parsing and handling...and just unmarshal it without all the
// extra logic we do from json -> internal struct

// csm plugin option creation time is when it takes snapshot...
/*
func setupLocalLabels() {
	// The value of “csm.workload_canonical_service” comes from
	// “CSM_CANONICAL_SERVICE_NAME” env var, “unknown” if unset.
	// this could be non deterministic - unset it? and then once unset
	// then should be unknown or set and expect it...distinct from set and empty?...send a lightweight?
	// PR?

	// Setup bootstrap, parse the node id out of it.
	// I can either a. inject bootstrap somehow maybe try this to help my helper to see if helper works...how do I set the bootstrap before it gets parsed?
	// b. inject local labels
	// c. ignore local labels...well will get emitted regardless so yeah
}*/

func setupLocalLabels() {
	// bootstrap turn off in get and just test getMeshID/getNodeID flow?

	// the other two two bools alongside...also think of better names than "something"/"something2"
}

func (s) TestGetLabels(t *testing.T) {
	cpo := NewCSMPluginOption()


	tests := []struct{
		name string
		metadataExchangeLabels map[string]string
		labelsWant map[string]string
	}{ // Local Labels: Can the unknowns be messed up by something set in the env...make deterministic or what? Also applies to others...
		{
			name: "metadata-not-set-only-local-labels",
			metadataExchangeLabels: nil,

			// Also from the xDS bootstrap (need to make a singleton): I guess add it to every one

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
		{
			name: "metadata-partially-set",
			metadataExchangeLabels: map[string]string{
				"type": "not-gce-or-gke",
				"ignore-this": "ignore-this",
			},
			labelsWant: map[string]string{
				// Local labels:
				"csm.workload_canonical_service": "unknown",
				"csm.mesh_id": "unknown",

				"csm.remote_workload_type": "not-gce-or-gke",
				"csm.remote_workload_canonical_service": "unknown",
			},
		},
		{
			name: "google-compute-engine",
			metadataExchangeLabels: map[string]string{ // All of these labels get emitted when type is "gcp_compute_engine".
				"type": "gcp_compute_engine",
				"canonical_service": "canonical_service_val",

				"project_id": "unique-id",
				"location": "us-east",
				"workload_name": "workload_name_val",
			},
			labelsWant: map[string]string{
				"csm.workload_canonical_service": "unknown",
				"csm.mesh_id": "unknown",

				"csm.remote_workload_type": "gcp_compute_engine",
				"csm.remote_workload_canonical_service": "canonical_service_val",
				"csm.remote_workload_project_id": "unique-id",
				"csm.remote_workload_location": "us-east",
				"csm.remote_workload_name":	"workload_name_val",
			},
		},
		// unset should go to unknown, ignore GKE labels that are not relevant
		// to GCE.
		{
			name: "google-compute-engine-labels-partially-set-with-extra",
			metadataExchangeLabels: map[string]string{
				"type": "gcp_compute_engine",
				"canonical_service": "canonical_service_val",
				"project_id": "unique-id",
				"location": "us-east",
				// "workload_name": "", unset workload name - should become "unknown"
				"namespace_name": "should-be-ignored",
				"cluster_name": "should-be-ignored",
			},
			labelsWant: map[string]string{
				// Local labels:
				"csm.workload_canonical_service": "unknown",
				"csm.mesh_id": "unknown",

				"csm.remote_workload_type": "gcp_compute_engine",
				"csm.remote_workload_canonical_service": "canonical_service_val",
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
				"project_id": "unique-id",
				"namespace_name": "namespace_name_val",
				"cluster_name": "cluster_name_val",
				"location": "us-east",
				"workload_name": "workload_name_val",
			},
			labelsWant: map[string]string{
				// Local labels:
				"csm.workload_canonical_service": "unknown",
				"csm.mesh_id": "unknown",

				"csm.remote_workload_type": "gcp_kubernetes_engine",
				"csm.remote_workload_canonical_service": "canonical_service_val",
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
				"project_id": "unique-id",
				"namespace_name": "namespace_name_val",
				// "cluster_name": "", cluster_name unset, should become "unknown"
				"location": "us-east",
				// "workload_name": "", workload_name unset, should become "unknown"
			},
			labelsWant: map[string]string{
				// Local labels:
				"csm.workload_canonical_service": "unknown",
				"csm.mesh_id": "unknown",

				"csm.remote_workload_type": "gcp_kubernetes_engine",
				"csm.remote_workload_canonical_service": "canonical_service_val",
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
		// maybe unhappy case below with invalid syntax?
		// If I do add busted proto inputs, I need to
		// do another test because this successfully marshals the protos...

	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pbLabels := &structpb.Struct{
				Fields: map[string]*structpb.Value{},
			}
			for k, v := range test.metadataExchangeLabels {
				pbLabels.Fields[k] = structpb.NewStringValue(v)
			}
			protoWireFormat, err := proto.Marshal(pbLabels)
			if err != nil {
				t.Fatalf("Error marshaling proto: %v", err)
			}
			metadataExchangeLabelsEncoded := base64.RawStdEncoding.EncodeToString(protoWireFormat)
			md := metadata.New(map[string]string{
				"x-envoy-peer-metadata": metadataExchangeLabelsEncoded,
			})

			labelsGot := cpo.GetLabels(md)

			if diff := cmp.Diff(labelsGot, test.labelsWant); diff != "" {
				t.Fatalf("cpo.GetLabels returned unexpected value (-got, +want): %v", diff)
			}
		})
	}
}

// send out what I have...mock certain types in e2e? Since then can test full flow
// yeah for mock should I do unit or e2e? Mock same way in e2e I'm assuming


// TestAddLabels tests the AddLabels function on the csm plugin option. This
// should append the metadata exchange labels constructed at static init time
// based on the environment. Due to the environment being of type unknown, only
// certain labels are expected to be sent? The metadata exchange labels should
// be able to be successfully base 64 decoded, then unmarshaled from proto wire
// format, and the expected labels should be present.
func (s) TestAddLabels(t *testing.T) { // this tests global flow which is fine...and also md flow

	// Doesn't trigger stuff in init... that might discover a failing codepath
	// works so far...only for further processing if gce or gke...

	// This tests flow of the blob, could also test NewLabelsMD somehow...
	cpo := NewCSMPluginOption() // Two metadata exchange labels should be prepared for this (assuming clear env)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second * 5)
	defer cancel()
	ctx = cpo.AddLabels(ctx) // appends to outgoing, should work in streaming and unary interceptor...

	// only way this would break is type unknown but when would that ever hit if
	// not run in a certain env

	md, ok := metadata.FromOutgoingContext(ctx)
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
	fields := spb.GetFields() // Default/make t test but would need to provide knobs on env so I think seperate test...?

	// fields // map[string]*Value, can make assertions on this that are guaranteed to be true...
	// local labels come in from get, unless they're not set (can you unset an env var)
	// you can assert stuff won't work...ugh set globally
	if len(fields) != 2 { // two local labels, plus two local labels set to stuff
		t.Fatalf("wrong number of Struct proto metadata fields: got %v, want %v", len(fields), 2)
	}
	// Should have string val...this can be asserted on e2e

	// 2 labels, could be unknown, dependent on type...mock the call for determinism?
	// type b has labels...
	// type c has labels...
	if _, ok := fields["type"]; !ok { // If I want I can also read out this val...
		t.Fatal("type label is not present in metadata exchange proto Struct")
	}
	if _, ok := fields["canonical_service"]; !ok {
		t.Fatal("canonical_service label is not in metadata exchange proto Struct")
	}


	// could assert fields all have value unknown...
	// or too much properties
	// def at the least the 4 values come in hardcoded, the only way this doesn't work is this test is deployed on gce,
	// which will never happen, you deploy prod code on gcp
} // send PR with fake bootstrap, do I need to mock a resource for the scope of this PR or is that e2e...

// determineCSM(target/cc ref)...called once call option specifies to, and calls into OTel with this string...

// I think can still take string target here...
// Even if you give it the whole cc, just deref it like cc.Target to pass the string...yeah because giving the full OTel plugin pointer
// so can deref before calling into plugin option, plus this is experimental so we can iterate

// Remove target filtering from underlying target filter...in base OTel plugin

// TestDetermineTargetCSM tests the helper function that determines whether a
// target is relevant to CSM or not, based off the rules outlined in design.
func (s) TestDetermineTargetCSM(t *testing.T) {
	cpo := &csmPluginOption{} // could just be a global func, but it's fine I think if OTel holds a ref to this...it is in c, pluggable (and based on properties we might change) as to how the plugin option determines it's "relevant"
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
		t.Run(test.name, func(t *testing.T) { // more extensible comes later so if this is internal we can always iterate
			if got := cpo.determineTargetCSM(test.target); got != test.targetCSM {
				t.Fatalf("cpo.determineTargetCSM(%v): got %v, want %v", test.target, got, test.targetCSM)
			}
		})
	}
}

// mock the resource detector once I start adding e2e...?
func (s) TestBootstrapLocalLabel(t *testing.T) {
	// ***Plumb a bootstrap into the system *before* init time...
	// or just send him the code and test it later...
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

// Shouldn't this have it's own go.mod...it's in internal, takes dependency on xDS
// no OTel dependency right?
// How

// Permutations of things to unit test:
// Bootstrap config tested maybe both invalid (numerous ways of triggering this -
// maybe pull the logic out from init so you can unit test...) and valid

// unset vs. set env vars

// on both send and recv path - unknown (which we've tested), gce, and gke
// (tested gce/gke on the recv path from md), need to test preparation based
// on mocked resource/bootstrap generator...

// So yeah seems like I need to continue to test the creation path - which is
// called at init time and sets the global Probably pull out the logic of
// determining from env into helper that I can test, and have the thing from
// init() invoke this helper (won't be racy right init comes before this?)

// Yeah the issue is we can't init() everything and then downstream effects in
// one testing package if it's a snapshot at the start (like picker what Eric
// mentioned...one snapshot of synchronous system), we have unregister/register
// which writes to a global for the scope of a single test, so pull it out
// rn the test test unknown invoke -> sets global -> GetLabels() which read from global...



// For e2e or unit you'll need to figure this out:
// Need to figure out:
// 1. How to inject bootstrap config before init (or pull out, but need some way to test local labels but maybe if you pull out that's enough)
// 2. How to deterministcally set and unset env var do we have helpers or just do it in env (will env break as a result?)

// See if you can get basic bootstrap helper done
// And then send this out once I hit scenarios above...

// this lives in OTel/internal to keep it the same module

// This structure should be ready to test...
// Just need to figure out how to:
// 1. inject bootstrap config
// plugin the env var but then when global singleton won't be malleable if you read it from there since only takes snapshot once...
// 2. mock the resource,
// 3. and inject env vars...
/*
func (s) TestSetLabels(t *testing.T) {
	origSet := set
	set = func() *attribute.Set{
		// return something here
		// how do I create an attribute.Set?
		attrSet := attribute.NewSet()
		return &attrSet
		return attribute.NewSet(attribute.String("cloud.platform", "gce/gke")) // how do I make this variable like with a t-test or something?
	}
	defer func() {set = origSet}()
}*/
// Move this to OTel so keeps the go.mod...

// This needs it's own go.mod...try to get a PR out for this by EOD
/*
func newXdsServerConfig() string {
	return `
	{
		"server_uri": "xds_server_uri",
		"channel_creds": [{"type": "google_default"}],
		"server_features": ["xds_v3", "ignore_resource_deletion", "xds.config.resource-in-sotw"]
	}`
}
// are there helpers for this
/*
func newNodeConfig(nodeID string) string {
	bootstrap.CreateFile()
}

func createBootstrap(nodeID string) []byte {
	/* From C2P:
	[]byte(fmt.Sprintf(`
	{
		"xds_servers": [%s],
		"client_default_listener_resource_name_template": "%%s",
		"authorities": %s,
		"node": %s
	}`, xdsServerCfg, authoritiesCfg, nodeCfg))
	*/

	/*
	if config.XDSServer == nil {
			return nil, fmt.Errorf("xds: required field %q not found in bootstrap %s", "xds_servers", jsonData["xds_servers"])
		}
		if config.XDSServer.ServerURI == "" {
			return nil, fmt.Errorf("xds: required field %q not found in bootstrap %s", "xds_servers.server_uri", jsonData["xds_servers"])
		}
		if config.XDSServer.CredsDialOption() == nil {
			return nil, fmt.Errorf("xds: required field %q doesn't contain valid value in bootstrap %s", "xds_servers.channel_creds", jsonData["xds_servers"])
		}

	// the things above +
	// node proto with node id, that's only thing it needs, node id, stuff will get filled in but just need to parse the node id out

	// return a boostrap config that passes the validations, but with node plumbed in...
}*/

// This needs it's own go.mod...clear bootstrap in others...
// ok label will just go to "unknown"

func (s) TestBootstrap(t *testing.T) {
	tests := []struct {
		name string
		nodeID string
		meshIDWant string // format and also test the different unknown cases...
	}{
		{
			name: "malformed-node-id-unknown",
			nodeID: "malformed", // will this fail proto preperation?
			meshIDWant: "unknown",
		},
		{
			name: "node-id-parsed",
			nodeID: "projects/12345/networks/mesh:mesh_id/nodes/aaaa-aaaa-aaaa-aaaa",
			meshIDWant: "mesh_id",
		},
		{
			name: "wrong-syntax-unknown",
			nodeID: "wrong-syntax/12345/networks/mesh:mesh_id/nodes/aaaa-aaaa-aaaa-aaaa",
			meshIDWant: "unknown",
		},
		{
			name: "node-id-parsed",
			nodeID: "projects/12345/networks/mesh:/nodes/aaaa-aaaa-aaaa-aaaa",
			meshIDWant: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// could either call top level helper or helper "nested"
			// createBootstrap(test.nodeID) // could be too heavyweight, could just take nodeID and pass it to helper
			// set the env...
			cleanup, err := bootstrap.CreateFile(bootstrap.Options{
				NodeID: test.nodeID,
				ServerURI: "xds_server_uri",
			})
			if err != nil {
				t.Fatalf("failed to create bootstrap: %v", err)
			}
			defer cleanup() // does the func on testing.T do it...or do I need to wrap with another func maybe print to see how it works?
			nodeIDGot := getNodeID() // this should return the node ID plumbed into bootstrap above
			if nodeIDGot != test.nodeID {
				t.Fatalf("getNodeID: got %v, want %v", nodeIDGot, test.nodeID)
			}

			meshIDGot := parseMeshIDFromNodeID(nodeIDGot)
			if meshIDGot != test.meshIDWant {
				t.Fatalf("parseMeshIDFromNodeID(%v): got %v, want %v", nodeIDGot, meshIDGot, test.meshIDWant)
			}
		})
	}
}

// This needs it's own go.mod, "something" in tests below needs better name, also finish bootstrap test below...bootstrap test worked!

func clearEnv() { // only invoke for tests that need this...
	// defer to original
	// or just do like Eric and mock the env var calls...

	// os.UnsetEnv?

	// return cleanup func that defers all 3 to original...like other tests

} // or just see what Doug said...

// TestSetLabels tests the setting of labels, which snapshots the resource and
// environment. It mocks the resource and environment, and then calls into
// labels creation. It verifies to local labels created and metadata exchange
// labels emitted from the setLabels function.
func (s) TestSetLabels(t *testing.T) {
	tests := []struct {
		name string
		resourceKeyValues map[string]string
		// env vars are also a variable...if issue of prepopulated explicitly
		// set it off in say GetLabels tests... with helper above...
		csmCanonicalServiceNamePopulated bool
		csmWorkloadNamePopulated bool
		bootstrapGeneratorPopulated bool
		localLabelsWant map[string]string
		metadataExchangeLabelsWant map[string]string
	}{
		{
			name: "no-type",
			csmCanonicalServiceNamePopulated: true,
			bootstrapGeneratorPopulated: true,
			resourceKeyValues: map[string]string{},
			localLabelsWant: map[string]string{
				"csm.workload_canonical_service": "canonical_service_name_val", // env var populated so should be set.
				"csm.mesh_id": "mesh_id", // env var populated so should be set.
			},
			metadataExchangeLabelsWant: map[string]string{
				"type": "unknown",
				"canonical_service": "canonical_service_name_val", // env var populated so should be set.
			},
		},
		{
			name: "gce",
			csmWorkloadNamePopulated: true,
			resourceKeyValues: map[string]string{
				"cloud.platform": "gcp_compute_engine",
				// csm workload name is an env var
				"cloud.availability_zone": "something",
				"cloud.region": "should-be-ignored", // cloud.availability_zone takes precedence
				"cloud.account.id": "something1",
			},
			localLabelsWant: map[string]string{
				"csm.workload_canonical_service": "unknown", // dependent on env so...oh right this ends up in md exchange and local labels...
				"csm.mesh_id": "unknown", // dependent on bootstrap so figure out if var
			},
			metadataExchangeLabelsWant: map[string]string{
				"type": "gcp_compute_engine",
				"canonical_service": "unknown",
				"workload_name": "workload_name_val",
				"location": "something",
				"project_id": "something1",
			},
		},
		{
			name: "gce-half-unset",
			resourceKeyValues: map[string]string{
				"cloud.platform": "gcp_compute_engine",
				// csm workload name is an env var
				"cloud.availability_zone": "something",
				"cloud.region": "should-be-ignored", // cloud.availability_zone takes precedence
				// "cloud.account.id": "something1",
			},
			localLabelsWant: map[string]string{
				"csm.workload_canonical_service": "unknown", // dependent on env so...oh right this ends up in md exchange and local labels...
				"csm.mesh_id": "unknown", // dependent on bootstrap so figure out if var
			},
			metadataExchangeLabelsWant: map[string]string{
				"type": "gcp_compute_engine",
				"canonical_service": "unknown",
				"workload_name": "unknown",
				"location": "something",
				"project_id": "unknown",
			},
		},
		{
			name: "gke",
			resourceKeyValues: map[string]string{
				"cloud.platform": "gcp_kubernetes_engine",
				// csm workload name is an env var
				"cloud.region": "something", // set cloud.region to something...should get picked up since availability_zone isn't present...
				"cloud.account.id": "something1",
				"k8s.namespace.name": "something2",
				"k8s.cluster.name": "something3",
			},
			localLabelsWant: map[string]string{
				"csm.workload_canonical_service": "unknown", // dependent on env so...oh right this ends up in md exchange and local labels...
				"csm.mesh_id": "unknown", // dependent on bootstrap so figure out if var
			}, // until I figure out how to plumb bootstrap in...
			metadataExchangeLabelsWant: map[string]string{
				"type": "gcp_kubernetes_engine",
				"canonical_service": "unknown",
				"workload_name": "unknown",
				"location": "something",
				"project_id": "something1",
				"namespace_name": "something2",
				"cluster_name": "something3",
			},
		},
		{
			name: "gke-half-unset",
			resourceKeyValues: map[string]string{ // unset should become unknown
				"cloud.platform": "gcp_kubernetes_engine",
				// csm workload name is an env var
				"cloud.region": "something", // set cloud.region to something...should get picked up since availability_zone isn't present...
				// "cloud.account.id": "something1",
				"k8s.namespace.name": "something2",
				// "k8s.cluster.name": "something3",
			},
			localLabelsWant: map[string]string{
				"csm.workload_canonical_service": "unknown", // dependent on env so...oh right this ends up in md exchange and local labels...
				"csm.mesh_id": "unknown", // dependent on bootstrap so figure out if var
			}, // until I figure out how to plumb bootstrap in...
			metadataExchangeLabelsWant: map[string]string{
				"type": "gcp_kubernetes_engine",
				"canonical_service": "unknown",
				"workload_name": "unknown",
				"location": "something",
				"project_id": "unknown",
				"namespace_name": "something2",
				"cluster_name": "unknown",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			func() {
				if test.csmCanonicalServiceNamePopulated {
					os.Setenv("CSM_CANONICAL_SERVICE_NAME", "canonical_service_name_val")
					defer os.Unsetenv("CSM_CANONICAL_SERVICE_NAME")
				}
				if test.csmWorkloadNamePopulated { // If can't assume clean env clear out bootstrap config and these two (resource detection I'll always mock)
					os.Setenv("CSM_WORKLOAD_NAME", "workload_name_val")
					defer os.Unsetenv("CSM_WORKLOAD_NAME")
				}
				if test.bootstrapGeneratorPopulated {
					cleanup, err := bootstrap.CreateFile(bootstrap.Options{
						NodeID: "projects/12345/networks/mesh:mesh_id/nodes/aaaa-aaaa-aaaa-aaaa",
						ServerURI: "xds_server_uri",
					})
					if err != nil {
						t.Fatalf("failed to create bootstrap: %v", err)
					}
					defer cleanup()
				}
				var attributes []attribute.KeyValue
				for k, v := range test.resourceKeyValues {
					attributes = append(attributes, attribute.String(k, v))
				}
				// Return the attributes configured as part of the test in place of reading from resource.
				attrSet := attribute.NewSet(attributes...)
				origSet := set // how to make sure this doesn't race...
				set = func() *attribute.Set{
					return &attrSet
				}
				defer func() {set = origSet}()

				// see below - bootstrap and env var toggle still up in air...


				localLabelsGot, mdEncoded := constructMetadataFromEnv()
				if diff := cmp.Diff(localLabelsGot, test.localLabelsWant); diff != "" {
					t.Fatalf("constructMetadataFromEnv() want: %v, got %v", test.localLabelsWant, localLabelsGot)
				}

				verifyMetadataExchangeLabels(mdEncoded, test.metadataExchangeLabelsWant)
			}()
		})
	}
}

func verifyMetadataExchangeLabels(mdEncoded string, mdLabelsWant map[string]string) error {
	protoWireFormat, err := base64.RawStdEncoding.DecodeString(mdEncoded)
	if err != nil {
		return fmt.Errorf("error base 64 decoding metadata val: %v", err)
	}
	spb := &structpb.Struct{}
	if err := proto.Unmarshal(protoWireFormat, spb); err != nil {
		return fmt.Errorf("error unmarshaling proto wire format: %v", err)
	}
	fields := spb.GetFields()
	for k, v := range mdLabelsWant {
		if val, ok := fields[k]; !ok { // fields can have extra...len check if you want this...
			if _, ok := val.GetKind().(*structpb.Value_StringValue); !ok {
				return fmt.Errorf("struct value for key %v should be string type", k)
			}
			if val.GetStringValue() != v {
				return fmt.Errorf("struct value for key %v got: %v, want %v", k, val.GetStringValue(), v)
			}
		}
	}
	return nil
}

// this comes after
// this will test bootstrap as well...
// mock bootstrap test here...
func (s) TestInjectBootstrap(t *testing.T) { // Both bootstrap and normal env vars (two of them, so three total go through same mechanics, I can figure out a way to add to t-test)
	// How do I inject a bootstrap?

	// Once I do inject a bootstrap, this is per helper snapshot, so could make
	// it scoped to the t-test above...

	// parse then mock a layer that reaches into global singleton...
	// set a raw json in env, how to make this mock a singleton?
	/*
		os.Unsetenv(envconfig.XDSBootstrapFileContentEnv) // I guess it's ok to reac into internal env vars...
		os.Setenv(envconfig.XDSBootstrapFileContent, /*workable bootstrap here...) // set the bootstrap env var - see elsewhere in the codebase for a concrete bootstrap example...
		// label for label env vars should show up as above (os std lib package)


		os.Unsetenv() // this clears the env var...
		// labels related to this should become "unknown"

		// Two env vars: (maybe set one and unset the other, the other should go "unknown"...do I really need to test all this?)
		// CSM_WORKLOAD_NAME
		os.Setenv("CSM_WORKLOAD_NAME", ) // make this a const?
		// CSM_CANONICAL_SERVICE_NAME
		os.Setenv("CSM_CANONICAL_SERVICE_NAME")*/
	/*

		The value of “csm.mesh_id” is derived from the id field in node from the xDS
		bootstrap (generated by gRPC’s TD xDS bootstrap generator).

		The format of this field is -
		projects/[GCP Project number]/networks/mesh:[Mesh ID]/nodes/[UUID]
	*/

	// Maybe move bootstrap generator to unmarshal into a map? Also Eric
	// manually verifies the other part of the node id - "doesn't matter", could
	// leave to xDS parsing...
	// {"node": <JSON form of Node proto>}, just map["node"]->["field-in-node-proto-strongly-defined"]

	// How to plumb the permutations of env vars, there's really only
	// the two that become labels and also the bootstrap config...
	// Plumb in either a full bootstrap config, or lightweight with just a mesh id...
	// need to test permutations of bootstrap that become unknown?

	// or a path to file but just do it inline env var

}

// Eric had a blob for getting the env vars...maybe I could do something like that

// could unit test the unknown for the parsing based on Eric's assertions, Eric and Yash both had assertions

// two layered JSON for node id...figure out structure...write something inline that will trigger it to be parsed

// Eric discussion:
// Plugin option interface in OTel package
// Since env vars/unknown from resource go through same helper, can have three test cases and
// see whether the unknowns work, and if they do let it work
