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
	"errors"
	"google.golang.org/grpc/metadata"
	"io"
	"os"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/internal/testutils/xds/bootstrap"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/stats/opentelemetry"
	"google.golang.org/grpc/stats/opentelemetry/internal"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// setupEnv does...

// returns a function to cleanup the enviornment to defer in the callsite.
func setupEnv(t *testing.T, resourceDetectorEmissions map[string]string, nodeID string, csmCanonicalServiceName string, csmWorkloadName string) func() {
	clearEnv() // do I need to do this too in these tests? I think so...

	cleanup, err := bootstrap.CreateFile(bootstrap.Options{ // Same flow
		NodeID:    nodeID,
		ServerURI: "xds_server_uri", // Should this be a knob?
	}) // return a cleanup func?
	if err != nil {
		// "failed to create bootstrap: %v", err
		t.Fatalf("failed to create bootstrap: %v", err)
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



	return func() {
		cleanup()
		os.Unsetenv("CSM_CANONICAL_SERVICE_NAME")
		os.Unsetenv("CSM_WORKLOAD_NAME")
		getAttrSetFromResourceDetector = origGetAttrSet
	}
} // even not e2e - need to mock, and then expect a certain concatenation for everything

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
/*
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
}*/

// Should I test sendHeaders too?

// I think this is just what it was...
/*
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

}*/ // want shared across wrt extra labels...

// attribute or not


// make the unary and streaming function also a no-op to induce the different permutations of
// operations

// if not sent a message is it trailers only server side (does the absence of a message trigger that?)

// client side attach a header, or send a header, or a msg (doing it now but need to trigger trailers only case)
// can I trigger trailers only from a unary RPC *figure out*


// TestCSMPluginOption tests the CSM Plugin Option and labels. It configures the
// environment for the CSM Plugin Option to read from. It then configures a
// system with a gRPC Client and gRPC server with the OpenTelemetry Dial and
// Server Option configured with a CSM Plugin Option, and makes a Unary RPC and
// a Streaming RPC. These two RPCs should cause certain recording for each
// registered metric observed through a Manual Metrics Reader on the provided
// OpenTelemetry SDK's Meter Provider. The CSM Labels emitted from the plugin
// option should be attached to the relevant metrics.
func (s) TestCSMPluginOption(t *testing.T) {
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
	cleanup := setupEnv(t, resourceDetectorEmissions, nodeID, csmCanonicalServiceName, csmWorkloadName) // persist all around as map to expect CSM Labels from...
	defer cleanup()


	attributesWant := map[string]string {
		"csm.workload_canonical_service": csmCanonicalServiceName, // from env
		"csm.mesh_id": "mesh_id", // from bootstrap env var (set already through helper)

		// No xDS Labels - this happens through interop or I could set with another interceptor...
		// Actually xDS Labels become "unknown" if not set
		// C++ did this as an array to avoid string comparisons

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
	for k, v := range attributesWant { // wait but these get renamed, just the value gets reused - look at full get labels tests...done manually
		csmLabels = append(csmLabels, attribute.String(k, v))
	}
	/*
	csmLabels = append(csmLabels, attribute.String("csm.mesh_id", "mesh_id")) // parsed out of the bootstrap generator above...
	csmLabels = append(csmLabels, attribute.String("csm.workload_canonical_service", csmCanonicalServiceName))
	csmLabels = append(csmLabels, attribute.String("csm.remote_workload_canonical_service", csmWorkloadName))
	 */
	// Just like global...main will create this and pass to test...test is like main, creates bounds
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	tests := []struct{
		name string
		// unaryCallFunc - tests different permutations of trailers only - does the application even have access to this?

		// to test
		// the different operations to plumb metadata exchange header in...
		unaryCallFunc func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error)
		streamingCallFunc func(stream testgrpc.TestService_FullDuplexCallServer) error
		opts internal.MetricDataOptions
	}{
		{
			name: "normal-flow",
			unaryCallFunc: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
				// grpc.SetHeader(ctx, metadata.New(map[string]string{"some-metadata": "some-metadata-val"}))
				// I shouldn't need this call, if I just send a message on unary how to intercept?
				// If application doesn't send header, and doesn't send msg you'd know that


				return &testpb.SimpleResponse{Payload: &testpb.Payload{
					Body: make([]byte, 10000),
				}}, nil
			},
			streamingCallFunc: func(stream testgrpc.TestService_FullDuplexCallServer) error { // this is trailers only
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
		}, // unary does send headers then trailers while streaming is trailers only...how does unary attach it when no send header?
		{
			name: "trailers-only-unary-streaming",
			unaryCallFunc: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
				// grpc.SetHeader(ctx, metadata.New(map[string]string{"some-metadata": "some-metadata-val"}))
				// I shouldn't need this call, if I just send a message on unary how to intercept?
				// If application doesn't send header, and doesn't send msg you'd know that

				return nil, errors.New("some error") // return an error for nil
			}, // nil is an invalid message, error is trailers only, nil induces
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
				UnaryMessageSent: false,
				StreamingMessageSent: false, // this needs to be represented in the client side from this test too...
				UnaryCallFailed: true,
			}, // nil nil also affects the buckets
		},
		{
			name: "set-header-client-server-side",
			unaryCallFunc: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
				grpc.SetHeader(ctx, metadata.New(map[string]string{"some-metadata": "some-metadata-val"}))



				return &testpb.SimpleResponse{Payload: &testpb.Payload{
					Body: make([]byte, 10000),
				}}, nil
			},
			streamingCallFunc: func(stream testgrpc.TestService_FullDuplexCallServer) error {
				stream.SetHeader(metadata.New(map[string]string{"some-metadata": "some-metadata-val"}))
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
		{
			name: "send-header-client-server-side",
			unaryCallFunc: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
				grpc.SendHeader(ctx, metadata.New(map[string]string{"some-metadata": "some-metadata-val"}))



				return &testpb.SimpleResponse{Payload: &testpb.Payload{
					Body: make([]byte, 10000),
				}}, nil
			},
			streamingCallFunc: func(stream testgrpc.TestService_FullDuplexCallServer) error {
				stream.SendHeader(metadata.New(map[string]string{"some-metadata": "some-metadata-val"}))
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
		{
			name: "send-msg-client-server-side",
			unaryCallFunc: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
				return &testpb.SimpleResponse{Payload: &testpb.Payload{
					Body: make([]byte, 10000),
				}}, nil
			},
			streamingCallFunc: func(stream testgrpc.TestService_FullDuplexCallServer) error {
				stream.Send(&testpb.StreamingOutputCallResponse{Payload: &testpb.Payload{
					Body: make([]byte, 10000),
				}})
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
				StreamingMessageSent: true,
			},
		},
		// Different permutations of operations that should all trigger csm md
		// exchange labels to be written on the wire.
		/*{
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
		},*/
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// This happens after, why is it not picking up bootstrap...
			mr, ss := setup(ctx, t, test.unaryCallFunc, test.streamingCallFunc)
			// defer reader.Shutdown?
			defer ss.Stop()

			var request *testpb.SimpleRequest
			if test.opts.UnaryMessageSent {
				request = &testpb.SimpleRequest{Payload: &testpb.Payload{
					Body: make([]byte, 10000),
				}}
			}


			// Make two RPC's, a unary RPC and a streaming RPC. These should cause
			// certain metrics to be emitted, which should be able to be observed
			// through the Metric Reader.
			ss.Client.UnaryCall(ctx, request, grpc.UseCompressor(gzip.Name))
			stream, err := ss.Client.FullDuplexCall(ctx, grpc.UseCompressor(gzip.Name))
			if err != nil {
				t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
			}

			if test.opts.StreamingMessageSent {
				if err := stream.Send(&testpb.StreamingOutputCallRequest{Payload: &testpb.Payload{
					Body: make([]byte, 10000),
				}}); err != nil {
					t.Fatalf("stream.Send failed")
				}
				if _, err := stream.Recv(); err != nil {
					t.Fatalf("stream.Recv failed with error: %v", err)
				}
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
csm.remote_workload_canonical_service" - not tested in interop? this is tested in unit tests...

unit tests also cover the gke/gce/unknown flow, this tests overall plumbing...

this is covered by interop...
*/







// I need to induce trailers only too

// Test retry works with trailers only + metadata exchange labels...

// interop...and plumbing...


// Eric has a long test just for plumbing and smaller ones
// for stuff like trailers only...

// Lighter weight test for stuff like wheter it actually sends metadata... first
// get e2e flow though, trailers only unary and streaming you should see
// attributes...


// setup creates a stub server with the provided unary call and full duplex call
// handlers, alongside with OpenTelemetry component with a CSM Plugin Option
// configured on client and server side. It returns a reader for metrics emitted
// from the OpenTelemetry component and the stub server.
func setup(ctx context.Context, t *testing.T, unaryCallFunc func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error), streamingCallFunc func(stream testgrpc.TestService_FullDuplexCallServer) error) (*metric.ManualReader, *stubserver.StubServer) { // specific for plugin option
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

// just comment out globals and work on interop?

func (s) TestxDSLabels(t *testing.T) {
	// same test as above, expect set xDS Labels in the interceptor, and then
	// make sure it's emitted...or leave to interop (wait on this until I change the plumbing of the in flight PR).
}
