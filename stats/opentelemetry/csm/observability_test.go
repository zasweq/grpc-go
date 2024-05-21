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
	istats "google.golang.org/grpc/internal/stats"
	"io"
	"os"
	"testing"

	"google.golang.org/grpc/metadata"
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

// setupEnv configures the enviornment for CSM Testing.
func setupEnv(t *testing.T, resourceDetectorEmissions map[string]string, nodeID string, csmCanonicalServiceName string, csmWorkloadName string) func() {
	clearEnv()

	cleanup, err := bootstrap.CreateFile(bootstrap.Options{
		NodeID:    nodeID,
		ServerURI: "xds_server_uri",
	})
	if err != nil {
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
}

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
	for k, v := range attributesWant {
		csmLabels = append(csmLabels, attribute.String(k, v))
	}
	// Just like global...main will create this and pass to test...test is like main, creates bounds
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	tests := []struct{
		name string
		// to test the different operations to plumb metadata exchange header in...
		unaryCallFunc func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error)
		streamingCallFunc func(stream testgrpc.TestService_FullDuplexCallServer) error
		opts internal.MetricDataOptions
	}{
		// Different permutations of operations that should all trigger csm md
		// exchange labels to be written on the wire.
		{
			name: "normal-flow",
			unaryCallFunc: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
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
				StreamingMessageSent: false,
			},
		}, // unary does send headers then trailers while streaming is trailers only...how does unary attach it when no send header?
		{
			name: "trailers-only-unary-streaming",
			unaryCallFunc: func(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
				return nil, errors.New("some error") // return an error and no message - this triggers trailers only
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
			},
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
				StreamingMessageSent: false,
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
			internal.CompareGotWantMetrics(ctx, t, mr, gotMetrics, wantMetrics)
		})
	}

}

// Make a note to ignore OTel...
// per target dial option rebase, other one seems to be set in stone so I think we're good here...
//



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

func unaryInterceptorAttachxDSLabels(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	// both the tag setter
	// and then set the labels,

	// the chain set labels thing is hard to test...
	// need to configure two stats handlers to test it...
	ctx = istats.SetLabels(ctx, &istats.Labels{
		TelemetryLabels: map[string]string{
			// mock what the cluster impl would write here ("csm.")
			"csm.service_name": "service_name_val",
			"csm.service_namespace": "service_namespace_val",

		},
	})

	// tag will just see this in the context and point to new map on heap...

	return invoker(ctx, method, req, reply, cc, opts...)
}


// TestxDSLabels tests that xDS Labels get emitted from OpenTelemetry metrics.
// This test configures OpenTelemetry with the CSM Plugin Option, and xDS
// Optional Labels turned on. It then configures an interceptor to attach
// labels, representing the cluster_impl picker. It then makes two RPC's, and
// expects these labels to be emitted alongside relevant metrics. Full xDS
// System alongside OpenTelemetry will be tested with interop.
func (s) TestxDSLabels(t *testing.T) { // configure with xDS Labels...
	// same test as above, expect set xDS Labels in the interceptor, and then
	// make sure it's emitted...or leave to interop (for e2e flow)

	// make sure to configure on optional labels (and delete target attribute filter)
	// "csm.service_name"
	// "csm.service_namespace"
	opts := opentelemetry.Options{
		MetricsOptions: opentelemetry.MetricsOptions{ // The rest as is, to get test working
			OptionalLabels: []string{"csm.service_name", "csm.service_namespace"},
		},
	} // same thing metrics reader yada yada...

	// stub server with just a unary handler...

	// make a unary RPC

	// just assert the labels for the want...inline wantmetrics for just client side, just
	// verify client side...

	// unarymethodclientsideend make it the xDS Labels (hardcoded)

	wantMetrics := []metricdata.Metrics{
		{
			Name:        "grpc.client.attempt.started",
			Description: "Number of client call attempts started.",
			Unit:        "attempt",
			Data: metricdata.Sum[int64]{
				DataPoints: []metricdata.DataPoint[int64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr),
						Value:      1,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
				IsMonotonic: true,
			},
		}, // Doesn't have xDS Labels, CSM Labels start from header or trailer from server, whichever comes first, so this doesn't need it
		{
			Name:        "grpc.client.attempt.duration",
			Description: "End-to-end time taken to complete a client call attempt.",
			Unit:        "s",
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes: attribute.NewSet(unaryMethodClientSideEnd...),
						Count:      1,
						Bounds:     defaultLatencyBounds,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name:        "grpc.client.attempt.sent_total_compressed_message_size",
			Description: "Compressed message bytes sent per client call attempt.",
			Unit:        "By",
			Data: metricdata.Histogram[int64]{
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes:   attribute.NewSet(unaryMethodClientSideEnd...),
						Count:        1,
						Bounds:       defaultSizeBounds,
						BucketCounts: unaryBucketCounts,
						Min:          unaryExtrema,
						Max:          unaryExtrema,
						Sum:          unaryCompressedBytesSentRecv,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name:        "grpc.client.attempt.rcvd_total_compressed_message_size",
			Description: "Compressed message bytes received per call attempt.",
			Unit:        "By",
			Data: metricdata.Histogram[int64]{
				DataPoints: []metricdata.HistogramDataPoint[int64]{
					{
						Attributes:   attribute.NewSet(unaryMethodClientSideEnd...),
						Count:        1,
						Bounds:       defaultSizeBounds,
						BucketCounts: unaryBucketCounts,
						Min:          unaryExtrema,
						Max:          unaryExtrema,
						Sum:          unaryCompressedBytesSentRecv,
					},
					{
						Attributes:   attribute.NewSet(streamingMethodClientSideEnd...),
						Count:        1,
						Bounds:       defaultSizeBounds,
						BucketCounts: streamingBucketCounts,
						Min:          streamingExtrema,
						Max:          streamingExtrema,
						Sum:          streamingCompressedBytesSentRecv,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
		{
			Name:        "grpc.client.call.duration",
			Description: "Time taken by gRPC to complete an RPC from application's perspective.",
			Unit:        "s",
			Data: metricdata.Histogram[float64]{
				DataPoints: []metricdata.HistogramDataPoint[float64]{
					{
						Attributes: attribute.NewSet(unaryMethodAttr, targetAttr, unaryStatusAttr),
						Count:      1,
						Bounds:     defaultLatencyBounds,
					},
					{
						Attributes: attribute.NewSet(duplexMethodAttr, targetAttr, streamingStatusAttr),
						Count:      1,
						Bounds:     defaultLatencyBounds,
					},
				},
				Temporality: metricdata.CumulativeTemporality,
			},
		},
	}

	// reuse testing logic...
	internal.CompareGotWantMetrics(ctx, t, mr, gotMetrics, wantMetrics)

	// no extra csm labels just xDS don't even set up env - keep it lightweight...


	// this now gets read from xDS but not passed to plugin option, but filtered in OTel, try that and then
	// write this test to see if it works

	// this test only works once rebased since rn comes out of plugin option logic...

	// same e2e setup expect add interceptor that sets labels as Tag does, sets it in the context *and then writes to it*
	// or just write the labels and then set that into context...

	// *If you have multiple stats handlers, the per attempt scope of these labels needs to point to the same heap
	// that way when the cluster picker writes to it it all points to the same heap, still only client side...


	// same locality label on same metrics I assume, if so commit it at the end of attempt into call info


	// Same thing as above expect configure OTel with optional labels + plugin option (in options struct)

	// Add interceptor that does what csh does and sets the labels, it's already there so will pick those up
	// just not written to by xDS
	// or set it here and then put it in context with certain map[string]string

}

// Delete target filter from this and add optional labels API...
