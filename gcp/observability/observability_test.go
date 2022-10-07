/*
 *
 * Copyright 2022 gRPC authors.
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

package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/test/grpc_testing"
	"io"
	"io/ioutil"
	"os"
	"sync"
	"testing"
	"time"

	"go.opencensus.io/stats/view"
	"go.opencensus.io/trace"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/leakcheck"
	"google.golang.org/grpc/metadata"
)

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

func init() {
	// OpenCensus, once included in binary, will spawn a global goroutine
	// recorder that is not controllable by application.
	// https://github.com/census-instrumentation/opencensus-go/issues/1191
	leakcheck.RegisterIgnoreGoroutine("go.opencensus.io/stats/view.(*worker).start")
	// google-cloud-go leaks HTTP client. They are aware of this:
	// https://github.com/googleapis/google-cloud-go/issues/1183
	leakcheck.RegisterIgnoreGoroutine("internal/poll.runtime_pollWait")
}

var (
	defaultTestTimeout        = 10 * time.Second
	testHeaderMetadata        = metadata.MD{"header": []string{"HeADer"}}
	testTrailerMetadata       = metadata.MD{"trailer": []string{"TrAileR"}}
	testOkPayload             = []byte{72, 101, 108, 108, 111, 32, 87, 111, 114, 108, 100}
	testErrorPayload          = []byte{77, 97, 114, 116, 104, 97}
	testErrorMessage          = "test case injected error"
	infinitySizeBytes   int32 = 1024 * 1024 * 1024
	defaultRequestCount       = 24
)

const ( // ugh i'll need to rewrite the one liner too, ohhh maybe I can add that, I'm done with testing outside of Doug's comment about starting with an error...
	TypeOpenCensusViewDistribution string = "distribution"
	TypeOpenCensusViewCount               = "count"
	TypeOpenCensusViewSum                 = "sum"
	TypeOpenCensusViewLastValue           = "last_value"
)

type fakeOpenCensusExporter struct {
	// The map of the observed View name and type
	SeenViews map[string]string
	// Number of spans
	SeenSpans int

	t  *testing.T
	mu sync.RWMutex
}

func (fe *fakeOpenCensusExporter) ExportView(vd *view.Data) { // How do the 4 default views registered plumb into/relate to view.Data?
	// Now this only exports the metric of completed RPCs (which seems to be
	// what Lidi is testing) vs. ClientSentBytesPerRPCView,
	// ClientReceivedBytesPerRPCView, ClientRoundtripLatencyView,
	// ClientCompletedRPCsView
	fe.mu.Lock()
	defer fe.mu.Unlock()
	for _, row := range vd.Rows {
		fe.t.Logf("Metrics[%s]", vd.View.Name)
		switch row.Data.(type) {
		case *view.DistributionData:
			fe.SeenViews[vd.View.Name] = TypeOpenCensusViewDistribution
		case *view.CountData:
			fe.SeenViews[vd.View.Name] = TypeOpenCensusViewCount
		case *view.SumData:
			fe.SeenViews[vd.View.Name] = TypeOpenCensusViewSum
		case *view.LastValueData:
			fe.SeenViews[vd.View.Name] = TypeOpenCensusViewLastValue
		}
	}
}

func (fe *fakeOpenCensusExporter) ExportSpan(vd *trace.SpanData) {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	fe.SeenSpans++
	fe.t.Logf("Span[%v]", vd.Name)
}

func (fe *fakeOpenCensusExporter) Flush() {} // can this be a sync point for all the RPCs writing to it?

func (fe *fakeOpenCensusExporter) Close() error {
	return nil // nothing beautiful, so doesn't clear out seen state...
}

func (s) TestRefuseStartWithInvalidPatterns(t *testing.T) {
	invalidConfig := &config{
		ProjectID: "fake",
		CloudLogging: &cloudLogging{
			ClientRPCEvents: []clientRPCEvents{
				{
					Method: []string{":-)"},
					MaxMetadataBytes: 30,
					MaxMessageBytes: 30,
				},
			},
		},
	}
	invalidConfigJSON, err := json.Marshal(invalidConfig)
	if err != nil {
		t.Fatalf("failed to convert config to JSON: %v", err)
	}
	os.Setenv(envObservabilityConfigJSON, "") // I feel like I need to clear some of these os.SetEnv calls
	os.Setenv(envObservabilityConfig, string(invalidConfigJSON))
	// If there is at least one invalid pattern, which should not be silently tolerated.
	if err := Start(context.Background()); err == nil {
		t.Fatalf("Invalid patterns not triggering error")
	}
	// need to scale this up to take in exclude + * is wrong
}

// oh also need to test bin headers

// TestRefuseStartWithExcludeAndWildCardAll tests the sceanrio wehre
func (s) TestRefuseStartWithExcludeAndWildCardAll(t *testing.T) {
	invalidConfig := &config{
		ProjectID: "fake",
		CloudLogging: &cloudLogging{
			ClientRPCEvents: []clientRPCEvents{
				{
					Method: []string{"*"},
					Exclude: true,
					MaxMetadataBytes: 30,
					MaxMessageBytes: 30,
				},
			},
		},
	}
	invalidConfigJSON, err := json.Marshal(invalidConfig)
	if err != nil {
		t.Fatalf("failed to convert config to JSON: %v", err)
	}
	os.Setenv(envObservabilityConfigJSON, "")
	os.Setenv(envObservabilityConfig, string(invalidConfigJSON))
	// If there is at least one invalid pattern, which should not be silently tolerated.
	if err := Start(context.Background()); err == nil {
		t.Fatalf("Invalid patterns not triggering error")
	}
}

// createTmpConfigInFileSystem creates a random observability config at a random
// place in the temporary portion of the file system dependent on system. It
// also sets the environment variable GRPC_CONFIG_OBSERVABILITY_JSON to point to
// this created config.
func createTmpConfigInFileSystem(rawJSON string) (func(), error) {
	configJSONFile, err := ioutil.TempFile(os.TempDir(), "configJSON-")
	if err != nil {
		return nil, fmt.Errorf("cannot create file %v: %v", configJSONFile.Name(), err)
	}
	_, err = configJSONFile.Write(json.RawMessage(rawJSON))
	if err != nil {
		return nil, fmt.Errorf("cannot write marshalled JSON: %v", err)
	}
	os.Setenv(envObservabilityConfigJSON, configJSONFile.Name())
	return func() {
		configJSONFile.Close()
		os.Setenv(envObservabilityConfigJSON, "")
	}, nil
}

// TestJSONEnvVarSet tests a valid observability configuration specified by the
// GRPC_CONFIG_OBSERVABILITY_JSON environment variable, whose value represents a
// file path pointing to a JSON encoded config.
func (s) TestJSONEnvVarSet(t *testing.T) {
	configJSON := `{
		"project_id": "fake"
	}`
	cleanup, err := createTmpConfigInFileSystem(configJSON)
	defer cleanup()

	if err != nil {
		t.Fatalf("failed to create config in file system: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if err := Start(ctx); err != nil {
		t.Fatalf("error starting observability with valid config through file system: %v", err)
	}
	defer End()
}

// TestBothConfigEnvVarsSet tests the scenario where both configuration
// environment variables are set. The file system environment variable should
// take precedence, and an error should return in the case of the file system
// configuration being invalid, even if the direct configuration environment
// variable is set and valid.
func (s) TestBothConfigEnvVarsSet(t *testing.T) {
	invalidConfig := &config{
		ProjectID: "fake",
		CloudLogging: &cloudLogging{
			ClientRPCEvents: []clientRPCEvents{
				{
					Method: []string{":-)"},
					MaxMetadataBytes: 30,
					MaxMessageBytes: 30,
				},
			},
		},
	}
	invalidConfigJSON, err := json.Marshal(invalidConfig)
	if err != nil {
		t.Fatalf("failed to convert config to JSON: %v", err)
	}
	// invalid pattern == what in new config?
	cleanup, err := createTmpConfigInFileSystem(string(invalidConfigJSON))
	defer cleanup()
	if err != nil {
		t.Fatalf("failed to create config in file system: %v", err)
	}
	// This configuration should be ignored, as precedence 2.
	validConfig := &config{
		ProjectID: "fake",
		CloudLogging: &cloudLogging{
			ClientRPCEvents: []clientRPCEvents{
				{
					Method: []string{"*"},
					MaxMetadataBytes: 30,
					MaxMessageBytes: 30,
				},
			},
		},
	}
	validConfigJSON, err := json.Marshal(validConfig)
	if err != nil {
		t.Fatalf("failed to convert config to JSON: %v", err)
	}
	os.Setenv(envObservabilityConfig, string(validConfigJSON))
	if err := Start(context.Background()); err == nil {
		t.Fatalf("Invalid patterns not triggering error")
	}
}

// TestErrInFileSystemEnvVar tests the scenario where an observability
// configuration is specified with environment variable that specifies a
// location in the file system for configuration, and this location doesn't have
// a file (or valid configuration).
func (s) TestErrInFileSystemEnvVar(t *testing.T) {
	os.Setenv(envObservabilityConfigJSON, "/this-file/does-not-exist")
	defer os.Setenv(envObservabilityConfigJSON, "")
	if err := Start(context.Background()); err == nil {
		t.Fatalf("Invalid file system path not triggering error")
	}
}

func (s) TestNoEnvSet(t *testing.T) {
	os.Setenv(envObservabilityConfigJSON, "")
	os.Setenv(envObservabilityConfig, "")
	// If there is no observability config set at all, the Start should return an error.
	if err := Start(context.Background()); err == nil {
		t.Fatalf("Invalid patterns not triggering error")
	}
}

func (s) TestOpenCensusIntegration(t *testing.T) {
	// does this still need the new test stuff? Rewrite this test to my own
	// thing, just Start defer Stop yourself?
	// also, if I do my own stuff use stub server handles all the starting and dialing for you,
	// like what I had in sanity_test.go
	fe := &fakeOpenCensusExporter{SeenViews: make(map[string]string), t: t}

	defer func(ne func(config *config) (tracingMetricsExporter, error)) {
		newExporter = ne
	}(newExporter)

	newExporter = func(config *config) (tracingMetricsExporter, error) {
		return fe, nil
	}

	openCensusOnConfig := &config{
		ProjectID: "fake",
		// no cloud monitoring
		CloudMonitoring: &cloudMonitoring{},
		// no cloud trace
		CloudTrace: &cloudTrace{
			SamplingRate: 1.0,
		},
	}
	cleanup, err := setupObservabilitySystemWithConfig(openCensusOnConfig)
	if err != nil {
		t.Fatalf("error setting up observability %v", err)
	}

	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
			return &grpc_testing.SimpleResponse{}, nil
		},
		FullDuplexCallF: func(stream grpc_testing.TestService_FullDuplexCallServer) error { // Another dependency - he has some comment about making this cleaner
			for {
				_, err := stream.Recv()
				if err == io.EOF {
					return nil
				}
			}
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	defer ss.Stop()


	for i := 0; i < defaultRequestCount; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
		defer cancel()
		if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{Payload: &grpc_testing.Payload{Body: testOkPayload}}); err != nil {
			t.Fatalf("Unexpected error from UnaryCall: %v", err)
		}
	}
	t.Logf("unary call passed count=%v", defaultRequestCount)


	// Wait for the gRPC transport to gracefully close to ensure no lost event.
	// will I need to wait for anything in my test? Explicitly flush buffer perhaps?
	// te.cc.Close()
	// te.srv.GracefulStop()
	// flush the buffer for opencensus metrics and tracing? do I need to test the logging flow as well?
	cleanup() // sync point for flushing buffer, but does this clear exporters memory too?
	/*
	// Wait for the gRPC transport to gracefully close to ensure no lost event.
	te.cc.Close()
	te.srv.GracefulStop()
	*/ // - how does this guarantee all the seen views are correctly written to exporter?

	// this isn't plugged into stream operations? So you don't have a happens before relationship?

	// After you switch views to the correct default client/server views,
	// you can change this expectation as well

	// what the fuck do I want emitted here?, the view count which is a number which counts
	// how many completed RPCs there are
	var errs []error
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	for ctx.Err() == nil {
		errs = nil
		fe.mu.RLock()
		// Two things need to happen here: sync point higher up, and also make sure this SeenViews is the same string,b ut it should be
		// This is exactly what I want/care about, the completed rpcs
		if value := fe.SeenViews["grpc.io/client/completed_rpcs"]; value != TypeOpenCensusViewCount {
			errs = append(errs, fmt.Errorf("unexpected type for grpc.io/client/completed_rpcs: %s != %s", value, TypeOpenCensusViewCount))
		}
		if value := fe.SeenViews["grpc.io/server/completed_rpcs"]; value != TypeOpenCensusViewCount {
			errs = append(errs, fmt.Errorf("unexpected type for grpc.io/server/completed_rpcs: %s != %s", value, TypeOpenCensusViewCount))
		}
		if fe.SeenSpans <= 0 {
			errs = append(errs, fmt.Errorf("unexpected number of seen spans: %v <= 0", fe.SeenSpans))
		}
		fe.mu.RUnlock()
		if len(errs) == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(errs) != 0 {
		t.Fatalf("Invalid OpenCensus export data: %v", errs)
	}
}

// TestCustomTagsTracingMetrics verifies that the custom tags defined in our
// observability configuration and set to two hardcoded values are passed to the
// function to create an exporter.
func (s) TestCustomTagsTracingMetrics(t *testing.T) {
	defer func(ne func(config *config) (tracingMetricsExporter, error)) {
		newExporter = ne
	}(newExporter)
	fe := &fakeOpenCensusExporter{SeenViews: make(map[string]string), t: t}
	newExporter = func(config *config) (tracingMetricsExporter, error) {
		ct := config.Labels
		if len(ct) < 1 {
			t.Fatalf("less than 2 custom tags sent in")
		}
		if val, ok := ct["customtag1"]; !ok || val != "wow" {
			t.Fatalf("incorrect custom tag: got %v, want %v", val, "wow")
		}
		if val, ok := ct["customtag2"]; !ok || val != "nice" {
			t.Fatalf("incorrect custom tag: got %v, want %v", val, "nice")
		}
		return fe, nil
	}

	// This configuration present in file system and it's defined custom tags should make it
	// to the created exporter.
	configJSON := `{
		"project_id": "fake",
		"cloud_trace": {},
		"cloud_monitoring": {"sampling_rate": 1.0},
		"labels":{"customtag1":"wow","customtag2":"nice"}
	}`

	cleanup, err := createTmpConfigInFileSystem(configJSON)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	err = Start(ctx)
	defer End()
	if err != nil {
		t.Fatalf("Start() failed with err: %v", err)
	}
} // get this and env var precedence tests to both actually work with the config

// Default view changes, *Done
// opencensus test (still need to do)

// Doug's e2e test case here he brought up in documents
func (s) TestStartErrorsThenEnd(t *testing.T) {
	// bad config that plumbs back error
	invalidConfig := &config{
		ProjectID: "fake",
		CloudLogging: &cloudLogging{
			ClientRPCEvents: []clientRPCEvents{
				{
					Method: []string{":-)"},
					MaxMetadataBytes: 30,
					MaxMessageBytes: 30,
				},
			},
		},
	}
	invalidConfigJSON, err := json.Marshal(invalidConfig)
	if err != nil {
		t.Fatalf("failed to convert config to JSON: %v", err)
	}
	os.Setenv(envObservabilityConfigJSON, "") // I feel like I need to clear some of these os.SetEnv calls
	os.Setenv(envObservabilityConfig, string(invalidConfigJSON))
	// If there is at least one invalid pattern, which should not be silently tolerated.
	if err := Start(context.Background()); err == nil {
		t.Fatalf("Invalid patterns not triggering error")
	}
	// call End even after Start, see if anything bad happens
	End()
}

// cleanup, then done :)))))))) (when I go back to my desk go ahead and clean this up)