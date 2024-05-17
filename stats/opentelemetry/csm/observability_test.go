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
	"fmt"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata/metricdatatest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats/opentelemetry"
	"google.golang.org/grpc/stats/opentelemetry/internal"
	"io"
	"os"
	"testing"

	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils/xds/bootstrap"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func setupEnv(resourceDetectorEmissions map[string]string, nodeID string, csmCanonicalServiceName string, csmWorkloadName string) {
	clearEnv() // do I need to do this too in these tests? I think so...

	cleanup, err := bootstrap.CreateFile(bootstrap.Options{ // Same flow
		NodeID:    nodeID,
		ServerURI: "xds_server_uri", // Should this be a knob?
	}) // return a cleanup func?
	if err != nil {
		// "failed to create bootstrap: %v", err
	}
	os.Setenv("CSM_CANONICAL_SERVICE_NAME", csmCanonicalServiceName)
	os.Setenv("CSM_WORKLOAD_NAME", csmWorkloadName)



	var attributes []attribute.KeyValue
	for k, v := range resourceDetectorEmissions {
		attributes = append(attributes, attribute.String(k, v))
	}
	// Return the attributes configured as part of the test in place
	// of reading from resource.
	attrSet := attribute.NewSet(attributes...)
	origGetAttrSet := getAttrSetFromResourceDetector
	getAttrSetFromResourceDetector = func(context.Context) *attribute.Set {
		return &attrSet
	}



	func () {
		cleanup()
		os.Unsetenv("CSM_CANONICAL_SERVICE_NAME")
		os.Unsetenv("CSM_WORKLOAD_NAME")
		getAttrSetFromResourceDetector = origGetAttrSet
	}()
} // even not e2e - need to mock, and then expect a certain concatenation for everything

// TestCSMPluginOption tests the CSM Plugin Option and labels. It configures the
// environment for the CSM Plugin Option to read from. It then configures a
// system with a gRPC Client and gRPC server with the OpenTelemetry Dial and
// Server Option configured with a CSM Plugin Option, and makes a Unary RPC and
// a Streaming RPC. These two RPCs should cause certain recording for each
// registered metric observed through a Manual Metrics Reader on the provided
// OpenTelemetry SDK's Meter Provider. The CSM Labels emitted from the plugin
// option should be attached to the relevant metrics.
func (s) TestCSMPluginOption(t *testing.T) { // Can test without late apply - global layer tested at interop level...
	resourceDetectorEmissions := map[string]string{
		"cloud.platform":     "gcp_kubernetes_engine",
		"cloud.region":       "cloud_region_val", // availability_zone isn't present, so this should become location
		"cloud.account.id":   "cloud_account_id_val",
		"k8s.namespace.name": "k8s_namespace_name_val",
		"k8s.cluster.name":   "k8s_cluster_name_val",
	}
	nodeID := "projects/12345/networks/mesh:mesh_id/nodes/aaaa-aaaa-aaaa-aaaa"
	csmCanonicalServiceName := "csm_canonical_service_name"
	csmWorkloadName := "csm_workload_name"
	setupEnv(resourceDetectorEmissions, nodeID, csmCanonicalServiceName, csmWorkloadName) // persist all around as map to expect CSM Labels from...


	// Samething as OTel e2e except:
	// Just like global...main will create this and pass to test...test is like main, creates bounds
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	csmPluginOption := newPluginOption(ctx)
	// instead of opentelemetry.DialOption
	dialOptionWithCSMPluginOption(csmPluginOption, /*same options as e2e test*/)

	// and opentelemetry.ServerOption
	serverOptionWithCSMPluginOption(csmPluginOption, /*same options as e2e test*/)

	// func (something additional) {
	//       something additional (CSM labels logic)
	//       baseOTelTest()
	// }

	// func baseOTelTest() {
	//      baseOTelTest expectations/logic...
	// }


	// extra on top of OTel...I'm sure Yash did this I have all the logic just
	// wrap the logic...

	// Converts some of the csm names to these new ones...xDS Labels tested in interop, if I wanted
	// to test at this layer manually plumb in...
	// csm.service_name: xDS Label passed in
	// csm.service_namespace_name: xDS Label passed in - set in own interceptor...xDS labels come in interop?

	// build these from resource emissions, then wrap in that attribute.NewSet call...
	// already did all the mesh ID (with bad bootstrap maybe can scale up the t test)
	// local xDS test done
	// unit test for target parsing need to add some failing cases maybe...

	resourceDetectorEmissions // map[string]string -> new map based on names
	// resource detected go to metadata exchange, which then become labels...
	/*
	resourceDetectorEmissions := map[string]string{
			"cloud.platform":     "gcp_kubernetes_engine",
			"cloud.region":       "cloud_region_val", // availability_zone isn't present, so this should become location
			"cloud.account.id":   "cloud_account_id_val",
			"k8s.namespace.name": "k8s_namespace_name_val",
			"k8s.cluster.name":   "k8s_cluster_name_val",
		}
	*/
	/* What is the equivalent of this in the setup - mock through resource detector...
	metadataExchangeLabels: map[string]string{
					"type":              "gcp_kubernetes_engine",
					"canonical_service": "canonical_service_val",
					"project_id":        "unique-id",
					"namespace_name":    "namespace_name_val",
					"cluster_name":      "cluster_name_val",
					"location":          "us-east",
					"workload_name":     "workload_name_val",
				},
	*/
	/*
		labelsWant: map[string]string{
						"csm.workload_canonical_service": "unknown",
						"csm.mesh_id":                    "unknown",

						"csm.remote_workload_type":              "gcp_kubernetes_engine",
				"csm.remote_workload_canonical_service": "canonical_service_val",
				"csm.remote_workload_project_id":        "unique-id",
				"csm.remote_workload_cluster_name":      "cluster_name_val",
				"csm.remote_workload_namespace_name":    "namespace_name_val",
				"csm.remote_workload_location":          "us-east",
				"csm.remote_workload_name":              "workload_name_val",
					},
	*/


	// derived from resource detector override...

	// GetLabels -> attributes
	attributesWant := map[string]string {
		"csm.workload_canonical_service": csmCanonicalServiceName, // from env
		"csm.mesh_id": "mesh_id", // from bootstrap env var (set already through helper)

		// No xDS Labels - this happens through interop or I could set with another interceptor...

		"csm.remote_workload_type":              "gcp_kubernetes_engine",
		"csm.remote_workload_canonical_service": csmCanonicalServiceName,
		"csm.remote_workload_project_id":        "cloud_account_id_val",
		"csm.remote_workload_cluster_name":      "k8s_cluster_name_val",
		"csm.remote_workload_namespace_name":    "k8s_namespace_name_val",
		"csm.remote_workload_location":          "cloud_region_val",
		"csm.remote_workload_name":              csmWorkloadName,
	}

	// expectations of emitted metrics have extra csm labels of them...
	var csmAttributes []attribute.KeyValue
	for k, v := range attributesWant { // wait but these get renamed, just the value gets reused - look at full get labels tests...
		csmAttributes = append(csmAttributes, attribute.String(k, v))
	}
	csmAttributes = append(csmAttributes, attribute.String("csm.mesh_id", "mesh_id")) // parsed out of the bootstrap generator above...
	csmAttributes = append(csmAttributes, attribute.String("csm.workload_canonical_service", csmCanonicalServiceName))
	csmAttributes = append(csmAttributes, attribute.String("csm.remote_workload_canonical_service", csmWorkloadName))

	metricsWant := metricData(true, csmAttributes, ss.Target)

	// got metrics, logic on the gotMetrics to wait for async...

	// got metrics happen in layer below, could share...




	// same verification as otel? How to share this logic...?

	// once constructed set looks like only getters...
	// expectation is to merge []attribute.KeyValue into set only for metrics with certain name...

	// set := attribute.NewSet(kvs...) // will be kvs... merged with the OTel expectations, not here, attribute.NewSet

}  // once this is done and late apply unit tests are on rebase do this and then cleanup
// Also exclude go.1.20 from OTel tests

// mock stats handler to inject xDS Labels for this e2e test to pick up and add
// when do I read it?
// Read when receives headers from the wire, gets the full metadata exchange labels from that...
func something() { // If I want to test xDS Labels at this level...
	// set in Tag, read from context in picker and then written to
	// this is the attempts context so idk what to do it'll collide with key
}
// or leave xDS labels testing to interop? interop has it...so could leave up to that...

// maybe induce one e2e test with headers set, one with trailers only (I think
// in flight OTel is trailers only)

func setHeaders() {
	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
			grpc.SetHeader(ctx, metadata.Pairs("foo", bar))

			return &testpb.SimpleResponse{Payload: &testpb.Payload{
				Body: make([]byte, 10000),
			}}, nil
		},

		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			stream.SetHeader() // this should add it
			stream.SendHeader() // this should send it - not add it, but no way to verify
			stream.SendMsg() // this should also trigger md showing up client side so maybe unit test for this with server to see if client receives it, leave e2e test down to sceanrio below?
		}, // How to test permutations of operations?
	} // test all these operations...do this after e2e test
}

// Should I test sendHeaders too?

// I think this is just what it was...
func induceTrailersOnlyResponse() { // Important to test functionality of wrapped server stream...
	// how to induce trailers only response? is this available to our mock?

	// mock full duplex call, that has stream object, write operations to that?

	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {

			// fuck I still need to do Unary case and wrap the stream...

			return &testpb.SimpleResponse{Payload: &testpb.Payload{
				Body: make([]byte, 10000),
			}}, nil


		},
		// above wraps unary, also need to reply back...
		// if I don't set headers I think it's trailers only and should write to wire
		// vs. a set header call...
		// SendHeader too granular if needed just variable unary and streaming and make sure all
		// have extra attributes...


		// separate test entirely? Need operations to trigger
		// send header, set header? vs. trailers only implied from returning early
		// and it writing md exchange labels on the wire
		FullDuplexCallF: func(stream testgrpc.TestService_FullDuplexCallServer) error {
			// This thing is a stream passed in...is this the body of handler?


			// None of these are called...so I think what we send is trailers only...unless
			// this return

			// this is the handler?

			stream.SetHeader()
			stream.SendHeader()
			stream.SendMsg()
			// Need to do both this and not trailers only so maybe set and send a header and make sure it shows up?
			// Also send msg first? There's no way to test all paths...

			// can you set a call option on the specific messages or for the whole stream?

			// yeah can do it from the client when you start stream so set gzip call option in that thing...
			stream.SendMsg(testpb.SimpleResponse{Payload: &testpb.Payload{
				Body: make([]byte, 10000),
			}}) // this is an any...do I want this to be type safe or not? do you just send anything youw ant


			// Normal flow corresponding to client side sending one message as per e2e tests...
			for {
				_, err := stream.Recv()
				if err == io.EOF {
					return nil
				}
			}
			// Normal flow corresponding to client side sending one message as per e2e tests...



		},
	}

} // want shared across wrt extra labels...

// attribute or not

// shared testing utils in internal/

// variable: what dial option,
// and this thing...

// make the unary and streaming function also a no-op to induce the different permutations of
// operations

// if not sent a message is it trailers only server side (does the absence of a message trigger that?)

// client side attach a header, or send a header, or a msg (doing it now but need to trigger trailers only case)
// can I trigger trailers only from a unary RPC *figure out*


func (s) e2etestdefinition(t *testing.T) {
	// setup env - create a []attribute.KeyValue to close on...
	resourceDetectorEmissions := map[string]string{
		"cloud.platform":     "gcp_kubernetes_engine",
		"cloud.region":       "cloud_region_val", // availability_zone isn't present, so this should become location
		"cloud.account.id":   "cloud_account_id_val",
		"k8s.namespace.name": "k8s_namespace_name_val",
		"k8s.cluster.name":   "k8s_cluster_name_val",
	}
	nodeID := "projects/12345/networks/mesh:mesh_id/nodes/aaaa-aaaa-aaaa-aaaa"
	csmCanonicalServiceName := "csm_canonical_service_name"
	csmWorkloadName := "csm_workload_name"
	setupEnv(resourceDetectorEmissions, nodeID, csmCanonicalServiceName, csmWorkloadName) // persist all around as map to expect CSM Labels from...


	// Samething as OTel e2e except:
	// Just like global...main will create this and pass to test...test is like main, creates bounds
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	csmPluginOption := newPluginOption(ctx)
	// instead of opentelemetry.DialOption
	dialOptionWithCSMPluginOption(csmPluginOption, /*same options as e2e test*/)

	// and opentelemetry.ServerOption
	serverOptionWithCSMPluginOption(csmPluginOption, /*same options as e2e test*/)



	attributesWant := map[string]string {
		"csm.workload_canonical_service": csmCanonicalServiceName, // from env
		"csm.mesh_id": "mesh_id", // from bootstrap env var (set already through helper)

		// No xDS Labels - this happens through interop or I could set with another interceptor...

		"csm.remote_workload_type":              "gcp_kubernetes_engine",
		"csm.remote_workload_canonical_service": csmCanonicalServiceName,
		"csm.remote_workload_project_id":        "cloud_account_id_val",
		"csm.remote_workload_cluster_name":      "k8s_cluster_name_val",
		"csm.remote_workload_namespace_name":    "k8s_namespace_name_val",
		"csm.remote_workload_location":          "cloud_region_val",
		"csm.remote_workload_name":              csmWorkloadName,
	}

	// expectations of emitted metrics have extra csm labels of them...
	var csmLabels []attribute.KeyValue
	for k, v := range attributesWant { // wait but these get renamed, just the value gets reused - look at full get labels tests...
		csmLabels = append(csmLabels, attribute.String(k, v))
	}
	csmLabels = append(csmLabels, attribute.String("csm.mesh_id", "mesh_id")) // parsed out of the bootstrap generator above...
	csmLabels = append(csmLabels, attribute.String("csm.workload_canonical_service", csmCanonicalServiceName))
	csmLabels = append(csmLabels, attribute.String("csm.remote_workload_canonical_service", csmWorkloadName))


	tests := []struct{
		name string
		// unaryCallFunc - tests different permutations of trailers only - does the application even have access to this?

		// make function body of unary and streaming server side a knob to test
		// the different operations to plumb header in...

		// have this packages setup func take these two...
		unaryCallFunc func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error)
		// streamingCallFunc
		streamingCallFunc func(stream testgrpc.TestService_FullDuplexCallServer) error
		// options to configure the emissions
		opts internal.MetricDataOptions
	}{
		{
			name: "normal-flow",
			unaryCallFunc: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
				return &testpb.SimpleResponse{Payload: &testpb.Payload{
					Body: make([]byte, 10000),
				}}, nil
			},
			streamingCallFunc: func(stream testgrpc.TestService_FullDuplexCallServer) error {
				for {
					_, err := stream.Recv()
					if err == io.EOF {
						return nil
					}
				}
			},
			opts: internal.MetricDataOptions{
				CSMLabels: csmLabels,
				UnaryMessageSent: true,
				StreamingMessageSent: false, // this needs to be represented in the client side from this test too...
			},
		},
		// Different permutations of operations that should all trigger csm md
		// exchange labels to be written on the wire.
		{
			name: "trailers-only-unary-streaming", // does the application have the knob for trailers only?
			unaryCallFunc: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
				return nil, nil // does this actually trigger trailers only?
			},
			streamingCallFunc: func(stream testgrpc.TestService_FullDuplexCallServer) error {
				for {
					_, err := stream.Recv()
					if err == io.EOF {
						return nil
					}
				}
			},
			opts: internal.MetricDataOptions{
				CSMLabels: csmLabels, // it should really take this...
			},
		},
		{ // only logic is server side, client side above means knob on messages sent for streaming
			name: "set/send header client-server-side",
			unaryCallFunc: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
				grpc.SetHeader(ctx, metadata.New(map[string]string{"some-metadata": "some-metadata-key"}))
				return &testpb.SimpleResponse{Payload: &testpb.Payload{
					Body: make([]byte, 10000),
				}}, nil
			},
		},
		{
			name: "send header only client-server-side",
		},
		{
			name: "send message client/server side",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mr, ss := setup(t, test.unaryCallFunc, test.streamingCallFunc)
			// defer reader.Shutdown?
			defer ss.Stop()
			// or do this at the top level?
			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
			defer cancel()

			// Make two RPC's, a unary RPC and a streaming RPC. These should cause
			// certain metrics to be emitted, which should be able to be observed
			// through the Metric Reader.
			if _, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{Payload: &testpb.Payload{
				Body: make([]byte, 10000),
			}}, grpc.UseCompressor(gzip.Name)); err != nil { // Deterministic compression.
				t.Fatalf("Unexpected error from UnaryCall: %v", err)
			}
			stream, err := ss.Client.FullDuplexCall(ctx)
			if err != nil {
				t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
			}

			stream.CloseSend()
			if _, err = stream.Recv(); err != io.EOF {
				t.Fatalf("unexpected error: %v, expected an EOF error", err)
			}

			rm := &metricdata.ResourceMetrics{}
			mr.Collect(ctx, rm)

			gotMetrics := map[string]metricdata.Metrics{}
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					gotMetrics[m.Name] = m
				}
			}


			opts := test.opts
			opts.Target = ss.Target
			wantMetrics := internal.MetricData(opts)
			// just reuse the polling logic
			internal.CompareGotWantMetrics(ctx, t, mr, gotMetrics, wantMetrics)
		})
	}

}

// per target dial option rebase, other one seems to be set in stone so I think we're good here...
//


/*

Also "If client does not send the mx (metadata-exchange) metadata, server
records “unknown” value for csm.remote_workload_type and
csm.remote_workload_canonical_service" - not tested in interop?

this is covered by interop...
*/





// unary/streaming both sides tested by unary/full duplex

// Yash has a lot of rules for which specific metrics get these extra labels
func testExtraLabels() {
	// all metrics get it but attempt.started and call.started (all client attempt metrics should get it)
	// ignore the started stuff?

	// I don't know how to induce this expect through e2e flow since it's
	// coupled (leave it to interop):

	// If client does not send the mx (metadata-exchange) metadata, server
	// records “unknown” value for `csm.remote_workload_type` and
	// `csm.remote_workload_canonical_service`

	// Retries : Test that metadata exchange and corresponding service mesh
	// labels are received and recorded even if the server sends a trailers-only
	// response.

	// How do I send a trailers only response manually? End RPC without sending
	// any headers or messages? Need the handler to return before sending
	// headers or messages.

	// triggers all the metrics since needs a header from client and also server
	// end metrics, just no compressed messages information...


}




// I need to induce trailers only too

// Test retry works with trailers only + metadata exchange labels...

func (s) TestCSMObservability(t *testing.T) { // Blocked on late apply thingy...plumb in xDS and CDS to fully test
	// finish configuring the globals in ether...

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// Like main - expect testing
	Observability(ctx, /*e2e otel options here...*/) // this instantiates both globals - document this?

	// same thing except not a dial option/server option

	// for csm channel - see if it has extra labels (how to put in servers...)
	// non csm channel - no extra labels
	// note that unless I mock determiner xds with td authority (or no authority, does configuring with management server count?)

	// note that for the thing that does hit the csm, through xDS so can I plumb
	// in labels through that? same xDS helpers as before




	// for any server - extra labels

	// take metrics - assert right labels on non xDS channel
	// and correct labels on xDS Channel?

	// server just two sets of recording points with all labels?

	// global API tested through interop, also xDS Labels e2e flow tested through interop...

	// xDS Labels not tested in get labels so this csm prefix comes in expectation above...

	// xDS labels just set in interceptor or something
	// plugin option always configured...

}

// wrap unary stream, late apply dial option, and test I think is all I have left...

// Eric has a long test just for plumbing and smaller ones
// for stuff like trailers only...

// Lighter weight test for stuff like wheter it actually sends metadata... first
// get e2e flow though, trailers only unary and streaming you should see
// attributes...


// try and have the setup go through this...
// or just remake setup

func (s) remakeSetup() {
	reader, ss := setup(t)
	defer ss.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// Make two RPC's, a unary RPC and a streaming RPC. These should cause
	// certain metrics to be emitted, which should be able to be observed
	// through the Metric Reader.
	if _, err := ss.Client.UnaryCall(ctx, &testpb.SimpleRequest{Payload: &testpb.Payload{
		Body: make([]byte, 10000),
	}}, grpc.UseCompressor(gzip.Name)); err != nil { // Deterministic compression.
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}
	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend()
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}

	rm := &metricdata.ResourceMetrics{}
	reader.Collect(ctx, rm)

	gotMetrics := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			gotMetrics[m.Name] = m
		}
	}

	// Move to shared helper...
	for _, metric := range wantMetrics {
		if metric.Name == "grpc.server.call.sent_total_compressed_message_size" || metric.Name == "grpc.server.call.rcvd_total_compressed_message_size" {
			// Sync the metric reader to see the event because stats.End is
			// handled async server side. Thus, poll until metrics created from
			// stats.End show up.
			if gotMetrics, err = waitForServerCompletedRPCs(ctx, reader, metric, t); err != nil {
				t.Fatalf("error waiting for sent total compressed message size for metric: %v", metric.Name)
			}
			continue
		}

		// If one of the duration metrics, ignore the bucket counts, and make
		// sure it count falls within a bucket <= 5 seconds (maximum duration of
		// test due to context).
		val, ok := gotMetrics[metric.Name]
		if !ok {
			t.Fatalf("metric %v not present in recorded metrics", metric.Name)
		}
		if metric.Name == "grpc.client.attempt.duration" || metric.Name == "grpc.client.call.duration" || metric.Name == "grpc.server.call.duration" {
			if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars(), metricdatatest.IgnoreValue()) {
				t.Fatalf("metrics data type not equal for metric: %v", metric.Name)
			}
			if err := assertDataPointWithinFiveSeconds(val); err != nil {
				t.Fatalf("Data point not within five seconds for metric %v: %v", metric.Name, err)
			}
			continue
		}

		if !metricdatatest.AssertEqual(t, metric, val, metricdatatest.IgnoreTimestamp(), metricdatatest.IgnoreExemplars()) {
			t.Fatalf("metrics data type not equal for metric: %v", metric.Name)
		}
	}
}

func setup(t *testing.T, unaryCallFunc func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error), streamingCallFunc func(stream testgrpc.TestService_FullDuplexCallServer) error) (*metric.ManualReader, *stubserver.StubServer) { // specific for plugin option
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(
		metric.WithReader(reader),
	)
	ss := &stubserver.StubServer{
		UnaryCallF: unaryCallFunc,
		FullDuplexCallF: streamingCallFunc,
	}

	po := newPluginOption(ctx) // Same thing with po plumbed in...
	if err := ss.Start([]grpc.ServerOption{serverOptionWithCSMPluginOption(opentelemetry.Options{
		MetricsOptions: opentelemetry.MetricsOptions{
			MeterProvider:         provider,
			Metrics:               opentelemetry.DefaultMetrics,
		}}, po)}, dialOptionWithCSMPluginOption(opentelemetry.Options{
		MetricsOptions: opentelemetry.MetricsOptions{
			MeterProvider:         provider,
			Metrics:               opentelemetry.DefaultMetrics,
		},
	}, po)); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	return reader, ss
}

// Also move to observability package...yeah waitForServerCompletedRPC's will work
// when I move needs ctx, reader, and t...
// move to o11y package, switch current test to use helpers
// then this then try and compile it...
