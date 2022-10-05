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
	"bytes"
	gcplogging "cloud.google.com/go/logging"
	"context"
	"encoding/json"
	"fmt"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/test/grpc_testing"
	"io"
	"io/ioutil"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"go.opencensus.io/stats/view"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	grpclogrecordpb "google.golang.org/grpc/gcp/observability/internal/logging"
	iblog "google.golang.org/grpc/internal/binarylog"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/leakcheck"
	testgrpc "google.golang.org/grpc/interop/grpc_testing"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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

type testServer struct { // this seems like not what I'm going for in terms of the test server I want
	testgrpc.UnimplementedTestServiceServer
}

// Unary call - Headers/Trailers...?

// rewrite this (def tracing/metrics) with mock?
// I want knobs on headers passed in.

// Logging changes logic somewhat, but can reuse some test cases I guess if it's somewhat similar to new config.

func (s *testServer) UnaryCall(ctx context.Context, in *testpb.SimpleRequest) (*testpb.SimpleResponse, error) {
	if err := grpc.SendHeader(ctx, testHeaderMetadata); err != nil { // This allows granular knobs on the headers, and trailer metadata
		return nil, status.Errorf(status.Code(err), "grpc.SendHeader(_, %v) = %v, want <nil>", testHeaderMetadata, err)
	}
	if err := grpc.SetTrailer(ctx, testTrailerMetadata); err != nil {
		return nil, status.Errorf(status.Code(err), "grpc.SetTrailer(_, %v) = %v, want <nil>", testTrailerMetadata, err)
	}

	if bytes.Equal(in.Payload.Body, testErrorPayload) {
		return nil, fmt.Errorf(testErrorMessage)
	}

	return &testpb.SimpleResponse{Payload: in.Payload}, nil
}

type fakeLoggingExporter struct {
	t            *testing.T
	clientEvents []*grpclogrecordpb.GrpcLogRecord
	serverEvents []*grpclogrecordpb.GrpcLogRecord
	isClosed     bool
	mu           sync.Mutex

	// Any other state here?

	// needs to persist something here from emitgRPCLogrecord that allows you to
	// verify something that happened - can't differentate between client and
	// server exporter? Or I could for testing. Nah.

	// needs to be protected by mu
	// Called by both client and server, no guarantee of the ordering on those.
	// but within the same entry calling it there's a guarantee of ordering?
	entries []*grpcLogEntry
}

/*func (fle *fakeLoggingExporter) EmitGrpcLogRecord(l *grpclogrecordpb.GrpcLogRecord) {
	fle.mu.Lock()
	defer fle.mu.Unlock()
	switch l.EventLogger {
	case grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT:
		fle.clientEvents = append(fle.clientEvents, l)
	case grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER:
		fle.serverEvents = append(fle.serverEvents, l)
	default:
		fle.t.Fatalf("unexpected event logger: %v", l.EventLogger)
	}
	eventJSON, _ := protojson.Marshal(l)
	fle.t.Logf("fakeLoggingExporter Emit: %s", eventJSON)
}*/

/*
fe := &fakeOpenCensusExporter{SeenViews: make(map[string]string), t: te.t} // construct a local var that implements interface

	defer func(ne func(config *config) (tracingMetricsExporter, error)) {
		newExporter = ne
	}(newExporter)

	newExporter = func(config *config) (tracingMetricsExporter, error) { // have the code point to that local var interface
		return fe, nil
	}
// ^^^ use this codepath except with fakeloggingexporter instead
*/

func (fle *fakeLoggingExporter) Close() error {
	fle.isClosed = true
	return nil
}

// on every operation gets to here

// called by operations that have no guarantee of concurrency
func (fle *fakeLoggingExporter) EmitGcpLoggingEntry(entry gcplogging.Entry) { // this gets called on any stream operation, no guarantee there
	fle.mu.Lock()
	defer fle.mu.Unlock()
	print(entry.Timestamp) // important to verify - within a range in tests?


	print(entry.Severity) // hardcoded, could verify on each call but seems like overkill

	// the entry.Payload is what you compare to the expected and define the atoms
	// and expectations of log entries



	// other stuff is hardcoded or populated by exporter...
	// entry.Payload // interface{} - this is where everything interesting comes from - populated with internal struct grpcLogRecord
	// typecast this entry.Payload to the correct type, can verify there.
	grpcLogEntry, ok := entry.Payload.(*grpcLogEntry)
	if !ok { // Shouldn't happen, we pass it in/populate
		fle.t.Errorf("payload passed in isn't grpcLogEntry")
	}
	// could persist the []Payload
	fle.entries = append(fle.entries, grpcLogEntry)
	// This is good for validation - when I add my own test it should work
	/*grpcLogEntry.ServiceName // could probably mess around with the verification of these per call
	// The same for the whole Test body ^^^
	grpcLogEntry.MethodName
	// Seperate for each call ^^^
	grpcLogEntry.Authority
	// You get to learn what this is lol ^^^, whether per test or per call

	grpcLogEntry.Peer // Don't think is important, although this will be localhost

	grpcLogEntry.Payload.StatusMessage // string

	grpcLogEntry.Payload.StatusCode // these happen as a result of the server trailer - at the end of the RPC

	grpcLogEntry.Payload.Metadata // map[string]string - this let's you test the metadata call as this is what headers (reg and bin) get converted to - although if I want bin headers I'll need to specify. Perhaps using call options?

	grpcLogEntry.Payload.*/

	// These fields all encompass every single event, not guaranteed to all be set for certain events.
	// Thus, are we testing the aggregate or are we testing each individual event


	// or have helpers like Lidi which compare equality of this grpcLogEntry

	// I doooo want a unit test on the metadata, esp bin ones. I could also verify it e2e,
	// but if so I need a knob on the headers the client sends. that will allow me to test
	// bin headers

	// Send out implementation before testing is finished

	// can verify count/specific behaviors of this test

}

// ^^^ interface called as part of being hooked into o11y system

/*
func (fle *fakeLoggingExporter) VerifySomething() error {
	// the overall metrics stuff we were talking about
	// or we can do what Lidi did and write wants for specific emitted events...

	// [] log entries what you want to see, not knowing ahead of time, cmp to filter out fields
	// ex. UUID

	// test each side individually, simplify it, not ping ponging still don't know ordering

	// cmp.Diff to do comparison

	// ping pong back and forth. Only way to define

	for _, entry := range fle.entries {
		entry.Peer.Address // if you do this inline, can close on the servers address? because both grpc.Dial and grpc.NewServer dial/serve to same address
		entry.ServiceName // same with address, everything should be same, I'm only starting up one test service only anyway
		entry.MethodName // different for each call, but should be a subset of the methods the service defines...
		entry.Authority // this is constant for the whole thing right?
		entry.CallId // verify it's a uuid
		//   event type one of them, although if appended deterministically can do a
		//       list (between client and server pointers only, not interleaved)
		entry.Type

		entry.Payload.Metadata // map[string]string - can verify it for certain events if set events...
		entry.Payload.StatusCode // within a set of expected status codes for the thing to return, OK or ERROR
		entry.Payload.MessageLength // < configured value - need to hold this as a knob somewhere
		entry.Payload.Timeout // where is this plumned from
	}
	fle.t.Fatalf() // or return error
	// Ask Doug if I really need to even make any of these verifications
}
*/

// single event for client rpc event - log * -> global verification of sink

// single event for server rpc event - log * -> global verification of sink

// plumb fakeLoggingExporter into the system which can verify both sides? or just one at a time

func (s) TestNewLoggingConfigBaseline(t *testing.T) {
	fle := &fakeLoggingExporter{
		t: t,
		// do I need to create []]?
		// is closed and mutex are implicitly crated
		// any state you start with/need/can configure something?
	}

	// override fake logging exporter here to inject my own exporter
	defer func(ne func(ctx context.Context, config *config) (loggingExporter, error)) {
		newLoggingExporter = ne
	}(newLoggingExporter)

	newLoggingExporter = func(ctx context.Context, config *config) (loggingExporter, error) {
		return fle, nil
	}


	// define config (from my musings on testing scenarios), plumb that config
	// into system along side a spun up grpc server/client - is this
	// configurable with his test?

	// tests valid JSON parsing

	// define a json string and put it into one of the environment variables

	// blob converted to JSON -> observability config struct sent around the system

	configJSON := `{
		"destination_project_id":"fake",
		"log_filters":[{"pattern":":-)"}, {"pattern_string":"*"}]
	}`
	cleanup, err := createTmpConfigInFileSystem(configJSON) // already have plumbing for file system config - takes precedence
	defer cleanup()
	if err != nil {
		t.Fatalf("failed to create config in file system: %v", err)
	}

	// or this could be cleaner: if someone argues, it's cleaner
	// to define config, -> JSON, rather than define a config, just adds a new step

	// This configuration should be ignored, as precedence 2.
	validConfig := &config{ // a lot of different configuration options can occur from this config
		ProjectID: "fake"/*pull from old config, see what works and what doesn't*/,
		// cloud logging -> one for server side and one for client side on all methods
		CloudLogging: &cloudLogging{
			ClientRPCEvents: []clientRPCEvents{
				{
					Method: []string{"*"},
					// Default is zero.
					MaxMetadataBytes: 30, // test this by plumbing in? and seeing how many bytes get emitted? Orthogonal to number of events emitted and you can further test the bytes by looking at specific payload
					// Default is zero.
					MaxMessageBytes: 30,
				},
			},
			ServerRPCEvents: []serverRPCEvents{
				{
					Method: []string{"*"},
					MaxMetadataBytes: 30,
					MaxMessageBytes: 30,
				},
			},
		},

		// no cloud monitoring
		// no cloud trace
	}
	validConfigJSON, err := json.Marshal(validConfig)
	if err != nil {
		t.Fatalf("failed to convert config to JSON: %v", err)
	}
	os.Setenv(envObservabilityConfig, string(validConfigJSON))






	/*baselineConfigWant := &config{ // a lot of different configuration options can occur from this config
		ProjectID: /*pull from old config, see what works and what doesn't,
		// cloud logging -> one for server side and one for client side on all methods
		CloudLogging: &cloudLogging{
			ClientRPCEvents: []clientRPCEvents{
				{
					Method: []string{"*"},
					// Default is zero.
					MaxMetadataBytes: 30, // test this by plumbing in? and seeing how many bytes get emitted? Orthogonal to number of events emitted and you can further test the bytes by looking at specific payload
					// Default is zero.
					MaxMessageBytes: 30,
				},
			},
			ServerRPCEvents: []serverRPCEvents{
				{
					Method: []string{"*"},
					MaxMetadataBytes: 30,
					MaxMessageBytes: 30,
				},
			},
		},

		// no cloud monitoring
		// no cloud trace
	}*/

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	// observability.Start() - parses config off env var for you, converts to that want struct above

	err = Start(ctx) // oh shoot - need to test error case as well (what can trigger? invalid configuration?), whether at this layer of abstraction or a lower level of abstraction
	if err != nil {
		t.Fatalf("error in Start: %v", err)
	}
	// observability.End() - tests default logging closing/flushing buffer/clearing out global Dial Options
	defer End() // do I want to test anything here? either direct effects or invariant after?




	// same as me manually configuring WithDialOption() in my e2e test
	// configuring WithServerOption() in my e2e test
	// can I not just pull the logic from my e2e test?
	ss := &stubserver.StubServer{ // differne timprot?
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


	// Try this, get it working, I wouldn't mind rewriting opencensus integration tests to this with normal stub server


	// gets to same place except with o11y layer
	// then I make a grpc.Dial, client connection to a created service, and check the emissions.
	// Previously, configured logger and method logger just adds 1 to a counter

	// Configured logger and method logger, method logger calls exporter that we
	// then mock, so can check counts/number of times the method is actually
	// called, and also specific Emission defined by Eric in his logging schema
	// about what to actually send to exporter

	// grpc.WithBinaryLogger() <- either my e2e flow or this testing flow

	// grpc.BinaryLogger() <- plumb a binary logger GetMethodLogger(for call/stream) method logger has a downstream effect of that.




	// we're not testing the emission of stats and traces, that is orthogonal
	// and you will have to rewrite his tests to new config

	// This is specifically about the logging blob of functionality.


	// baseline with single client rpc event and server rpc event

	// make it a t test

	// configure a grpc client and grpc server to talk to each other. Needs both, and can verify downstream effects of that.



	// validate how many times/what the logging exporter gets called with.
	// This is somewhat dependent on Doug's comment about number of client and server events.
	// Is there anything else I can use to validate this?

	// make unary RPC (need the client ref)

	// verify certain amount of events
	if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{}); err != nil {
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}

	// Downstream effects/verification
	// One: number of events emitted (we have one for client and one for server
	// so will have to add, this is a maintenance nightmare)

	// GetMethodLogger() for method on stream creation. Now every created stream,
	// each stream operation both ways calls .Log() -> EmitGrpcLogRecord()
	// 1:1 with stream operation, just count on EmitGrpcLogRecord().

	// Should = the amount of stream operations as a result of the call
	// assert fle.count == expected number of stream operations

	// Two: the actual content of those events emitted?

	fle./*something persisted.expected part of schema?*/

	// What is actually being sent to exporter
	// {
	// timestamp - verify this somehow

	// Payload is the actual grpcLogRecord being emitted
	// } take a stream of these log entries being emitted,
	// and persist something that can allow you to verify some sort of thing about these entries
	// remember - one per stream operation...
	// how do the different stream operations vary

	// can't differentiate between client and server unless
	// you register two different exporters

	// []gcplogging.Entry...? then would have to search this after
	// each space

	// {gcplogging.Entry, gcplogging.Entry, gcplogging.Entry} take some part of
	// this and persist it, maybe then you can verify something on it that way




	// iterate through and aggregate stats?
	// for event in events
	//   ex: service name is same
	//   ex: method name is dependent on the caller, one of...
	//   address is always the same?
	//   callid is a uuid
	//   event type one of them, although if appended deterministically can do a
	//       list (between client and server pointers only, not interleaved)
	//   authority is constant?

	//     inside payload:
	//        map<string, string> metadata, there is def something to be done there
	//        Duration timeout (what is this popualted from)
	//        status code (within a set of expected status codes)
	//        status message (can't really test this)
	//        status details (same, can't really test this)
	//        message length (isn't this part of this config MaxHeaderBytes, ASSERT < MaxHeaderBytes
	//        bytes message - perhaps, you know what payload the gRPC Server sends back for RPC's
	// you create fle in this function, even though you hook in you still have a ref to it
	// fle.verify() or do that inline



	// make streaming RPC (need the client ref - see my e2e events)

	// verify certain amount of events


} // looks like for bin headers just call it manually unless you can get a bin in default

// Another thing I could do orthogonal to this test is to
// fix all the metrics/tracing tests


// entry test
func (s) TestNewLoggingConfigBaselineee(t *testing.T) {

	// clientRPCEvents configured for this, equivalent to the 5 client side
	// events configured later.


	// we're taking grpcLogEntry as a struct and making validations on that

	// this gets marshaled into exporter. Cloud Logging Exporter calls marshalJSON(),
	// we just validate, so this honestly doesn't block this test.

	// rather than write the expectations inline, get the system setup to the point
	// where you actually get these logEntries emitted from the system

	// Then write the discrete things that are being emitted (from my own e2e
	// flow with the way I set up the RPC's to be called. This is different to
	// Lidi's so specifics will be different but overall will be somewaht
	// similar). And also, from this, can see which fields I cannnnn make
	// validations on and which ones I can't. Like literally print...***

	// Goal is printed emitted grpcLogEntry

	fle := &fakeLoggingExporter{
		t: t,

	}

	defer func(ne func(ctx context.Context, config *config) (loggingExporter, error)) {
		newLoggingExporter = ne
	}(newLoggingExporter)

	newLoggingExporter = func(ctx context.Context, config *config) (loggingExporter, error) {
		return fle, nil
	}

	validConfig := &config{
		ProjectID: "fake",
		CloudLogging: &cloudLogging{
			ClientRPCEvents: []clientRPCEvents{
				{
					Method: []string{"*"},
					// Default is zero.
					MaxMetadataBytes: 30, // test this by plumbing in? and seeing how many bytes get emitted? Orthogonal to number of events emitted and you can further test the bytes by looking at specific payload
					// Default is zero.
					MaxMessageBytes: 30,
				},
			},
			/*ServerRPCEvents: []serverRPCEvents{
				{
					Method: []string{"*"},
					MaxMetadataBytes: 30,
					MaxMessageBytes: 30,
				},
			},*/ // this maps to the server side RPC events
		},

		// no cloud monitoring
		// no cloud trace
	}
	// If they get mad, say it's cleaner this way and tests JSON tags?
	validConfigJSON, err := json.Marshal(validConfig)
	if err != nil {
		t.Fatalf("failed to convert config to JSON: %v", err)
	}
	os.Setenv(envObservabilityConfig, string(validConfigJSON))
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	err = Start(ctx)
	if err != nil {
		t.Fatalf("error in Start: %v", err)
	}
	defer End()

	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
			return &grpc_testing.SimpleResponse{}, nil
		},
	}
	if err := ss.Start(nil); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	defer ss.Stop()

	if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{}); err != nil {
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}
	// Downstream effects of this UnaryCall
	// log the actual events happening,

	for i, gle := range fle.entries {
		print("entry index: %v", i)
		print("entry type: %v", gle.Type)
	}
}

// Draft PR for implementation work, and not for testing, Doug and Easwar can
// iterate there.

// Code gets in, releases happen, blog post happens



// test is an end-to-end test. It should be created with the newTest
// func, modified as needed, and then started with its startServer method.
// It should be cleaned up with the tearDown method.
type test struct {
	t   *testing.T
	fle *fakeLoggingExporter

	testServer testgrpc.TestServiceServer // nil means none
	// srv and srvAddr are set once startServer is called.
	srv     *grpc.Server
	srvAddr string

	cc *grpc.ClientConn // nil until requested via clientConn
}

func (te *test) tearDown() {
	if te.cc != nil {
		te.cc.Close()
		te.cc = nil
	}
	te.srv.Stop()
	End()

	if te.fle != nil && !te.fle.isClosed {
		te.t.Fatalf("fakeLoggingExporter not closed!")
	}
}

// newTest returns a new test using the provided testing.T and
// environment.  It is returned with default values. Tests should
// modify it before calling its startServer and clientConn methods.
func newTest(t *testing.T) *test {
	return &test{
		t: t,
	}
}

// startServer starts a gRPC server listening. Callers should defer a
// call to te.tearDown to clean up.
func (te *test) startServer(ts testgrpc.TestServiceServer) {
	te.testServer = ts
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		te.t.Fatalf("Failed to listen: %v", err)
	}
	var opts []grpc.ServerOption
	s := grpc.NewServer(opts...)
	te.srv = s
	if te.testServer != nil {
		testgrpc.RegisterTestServiceServer(s, te.testServer)
	}

	go s.Serve(lis)
	te.srvAddr = lis.Addr().String()
}

func (te *test) clientConn() *grpc.ClientConn {
	if te.cc != nil {
		return te.cc
	}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithUserAgent("test/0.0.1"),
	}
	var err error
	te.cc, err = grpc.Dial(te.srvAddr, opts...)
	if err != nil {
		te.t.Fatalf("Dial(%q) = %v", te.srvAddr, err)
	}
	return te.cc
}

func (te *test) enablePluginWithConfig(config *config) {
	// Injects the fake exporter for testing purposes
	te.fle = &fakeLoggingExporter{t: te.t}
	defaultLogger = newBinaryLogger(nil)
	iblog.SetLogger(defaultLogger)
	if err := defaultLogger.start(config, te.fle); err != nil { // there's the hook point, stores into a pointer, this pointer gets held in the original binary logger and given to created method loggers, this is what it gets written to
		te.t.Fatalf("Failed to start plugin: %v", err)
	}
}

func (te *test) enablePluginWithCaptureAll() {
	te.enablePluginWithConfig(&config{
		EnableCloudLogging:   true,
		DestinationProjectID: "fake",
		LogFilters: []logFilter{
			{
				Pattern:      "*",
				HeaderBytes:  infinitySizeBytes,
				MessageBytes: infinitySizeBytes,
			},
		},
	}) // this is equiavlent to my test anyway
}

// this would be equivalent to my single node with *

func (te *test) enableOpenCensus() {
	defaultMetricsReportingInterval = time.Millisecond * 100
	/*config := &config{
		EnableCloudLogging:      true,
		EnableCloudTrace:        true,
		EnableCloudMonitoring:   true,
		GlobalTraceSamplingRate: 1.0,
	}*/
	config := &config{
		/*CloudLogging: &cloudLogging{ // disable this you don't need this...just don't declare it?

		},*/
		ProjectID: "fake",
		CloudMonitoring: &cloudMonitoring{},
		CloudTrace: &cloudTrace{
			SamplingRate: 1.0,
		},

	}
	startOpenCensus(config)
}

func checkEventCommon(t *testing.T, seen *grpclogrecordpb.GrpcLogRecord) {
	if seen.RpcId == "" {
		t.Fatalf("expect non-empty RpcId")
	}
	if seen.SequenceId == 0 {
		t.Fatalf("expect non-zero SequenceId")
	}
}

func checkEventRequestHeader(t *testing.T, seen *grpclogrecordpb.GrpcLogRecord, want *grpclogrecordpb.GrpcLogRecord) {
	checkEventCommon(t, seen)
	if seen.EventType != grpclogrecordpb.GrpcLogRecord_GRPC_CALL_REQUEST_HEADER {
		t.Fatalf("got %v, want GrpcLogRecord_GRPC_CALL_REQUEST_HEADER", seen.EventType.String())
	}
	if seen.EventLogger != want.EventLogger {
		t.Fatalf("l.EventLogger = %v, want %v", seen.EventLogger, want.EventLogger)
	}
	if want.Authority != "" && seen.Authority != want.Authority {
		t.Fatalf("l.Authority = %v, want %v", seen.Authority, want.Authority)
	}
	if want.ServiceName != "" && seen.ServiceName != want.ServiceName {
		t.Fatalf("l.ServiceName = %v, want %v", seen.ServiceName, want.ServiceName)
	}
	if want.MethodName != "" && seen.MethodName != want.MethodName {
		t.Fatalf("l.MethodName = %v, want %v", seen.MethodName, want.MethodName)
	}
	if len(seen.Metadata.Entry) != 1 {
		t.Fatalf("unexpected header size: %v != 1", len(seen.Metadata.Entry))
	}
	if seen.Metadata.Entry[0].Key != "header" {
		t.Fatalf("unexpected header: %v", seen.Metadata.Entry[0].Key)
	}
	if !bytes.Equal(seen.Metadata.Entry[0].Value, []byte(testHeaderMetadata["header"][0])) {
		t.Fatalf("unexpected header value: %v", seen.Metadata.Entry[0].Value)
	}
}

func checkEventResponseHeader(t *testing.T, seen *grpclogrecordpb.GrpcLogRecord, want *grpclogrecordpb.GrpcLogRecord) {
	checkEventCommon(t, seen)
	if seen.EventType != grpclogrecordpb.GrpcLogRecord_GRPC_CALL_RESPONSE_HEADER {
		t.Fatalf("got %v, want GrpcLogRecord_GRPC_CALL_RESPONSE_HEADER", seen.EventType.String())
	}
	if seen.EventLogger != want.EventLogger {
		t.Fatalf("l.EventLogger = %v, want %v", seen.EventLogger, want.EventLogger)
	}
	if len(seen.Metadata.Entry) != 1 {
		t.Fatalf("unexpected header size: %v != 1", len(seen.Metadata.Entry))
	}
	if seen.Metadata.Entry[0].Key != "header" {
		t.Fatalf("unexpected header: %v", seen.Metadata.Entry[0].Key)
	}
	if !bytes.Equal(seen.Metadata.Entry[0].Value, []byte(testHeaderMetadata["header"][0])) {
		t.Fatalf("unexpected header value: %v", seen.Metadata.Entry[0].Value)
	}
}

func checkEventRequestMessage(t *testing.T, seen *grpclogrecordpb.GrpcLogRecord, want *grpclogrecordpb.GrpcLogRecord, payload []byte) {
	checkEventCommon(t, seen)
	if seen.EventType != grpclogrecordpb.GrpcLogRecord_GRPC_CALL_REQUEST_MESSAGE {
		t.Fatalf("got %v, want GrpcLogRecord_GRPC_CALL_REQUEST_MESSAGE", seen.EventType.String())
	}
	if seen.EventLogger != want.EventLogger {
		t.Fatalf("l.EventLogger = %v, want %v", seen.EventLogger, want.EventLogger)
	}
	msg := &testpb.SimpleRequest{Payload: &testpb.Payload{Body: payload}}
	msgBytes, _ := proto.Marshal(msg)
	if !bytes.Equal(seen.Message, msgBytes) {
		t.Fatalf("unexpected payload: %v != %v", seen.Message, payload)
	}
}

func checkEventResponseMessage(t *testing.T, seen *grpclogrecordpb.GrpcLogRecord, want *grpclogrecordpb.GrpcLogRecord, payload []byte) {
	checkEventCommon(t, seen)
	if seen.EventType != grpclogrecordpb.GrpcLogRecord_GRPC_CALL_RESPONSE_MESSAGE {
		t.Fatalf("got %v, want GrpcLogRecord_GRPC_CALL_RESPONSE_MESSAGE", seen.EventType.String())
	}
	if seen.EventLogger != want.EventLogger {
		t.Fatalf("l.EventLogger = %v, want %v", seen.EventLogger, want.EventLogger)
	}
	msg := &testpb.SimpleResponse{Payload: &testpb.Payload{Body: payload}}
	msgBytes, _ := proto.Marshal(msg)
	if !bytes.Equal(seen.Message, msgBytes) {
		t.Fatalf("unexpected payload: %v != %v", seen.Message, payload)
	}
}

func checkEventTrailer(t *testing.T, seen *grpclogrecordpb.GrpcLogRecord, want *grpclogrecordpb.GrpcLogRecord) {
	checkEventCommon(t, seen)
	if seen.EventType != grpclogrecordpb.GrpcLogRecord_GRPC_CALL_TRAILER {
		t.Fatalf("got %v, want GrpcLogRecord_GRPC_CALL_TRAILER", seen.EventType.String())
	}
	if seen.EventLogger != want.EventLogger {
		t.Fatalf("l.EventLogger = %v, want %v", seen.EventLogger, want.EventLogger)
	}
	if seen.StatusCode != want.StatusCode {
		t.Fatalf("l.StatusCode = %v, want %v", seen.StatusCode, want.StatusCode)
	}
	if seen.StatusMessage != want.StatusMessage {
		t.Fatalf("l.StatusMessage = %v, want %v", seen.StatusMessage, want.StatusMessage)
	}
	if !bytes.Equal(seen.StatusDetails, want.StatusDetails) {
		t.Fatalf("l.StatusDetails = %v, want %v", seen.StatusDetails, want.StatusDetails)
	}
	if len(seen.Metadata.Entry) != 1 {
		t.Fatalf("unexpected trailer size: %v != 1", len(seen.Metadata.Entry))
	}
	if seen.Metadata.Entry[0].Key != "trailer" {
		t.Fatalf("unexpected trailer: %v", seen.Metadata.Entry[0].Key)
	}
	if !bytes.Equal(seen.Metadata.Entry[0].Value, []byte(testTrailerMetadata["trailer"][0])) {
		t.Fatalf("unexpected trailer value: %v", seen.Metadata.Entry[0].Value)
	}
}

const (
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

func (fe *fakeOpenCensusExporter) ExportView(vd *view.Data) {
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

func (fe *fakeOpenCensusExporter) Flush() {}

func (fe *fakeOpenCensusExporter) Close() error {
	return nil
}

func (s) TestLoggingForOkCall(t *testing.T) {
	te := newTest(t)
	defer te.tearDown()
	te.enablePluginWithCaptureAll()
	te.startServer(&testServer{})
	tc := testgrpc.NewTestServiceClient(te.clientConn())

	var (
		resp *testpb.SimpleResponse
		req  *testpb.SimpleRequest
		err  error
	)
	req = &testpb.SimpleRequest{Payload: &testpb.Payload{Body: testOkPayload}}
	tCtx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	resp, err = tc.UnaryCall(metadata.NewOutgoingContext(tCtx, testHeaderMetadata), req)
	if err != nil {
		t.Fatalf("unary call failed: %v", err)
	}
	t.Logf("unary call passed: %v", resp)

	// Wait for the gRPC transport to gracefully close to ensure no lost event.
	te.cc.Close()
	te.srv.GracefulStop()
	// Check size of events
	if len(te.fle.clientEvents) != 5 {
		t.Fatalf("expects 5 client events, got %d", len(te.fle.clientEvents))
	}
	if len(te.fle.serverEvents) != 5 {
		t.Fatalf("expects 5 server events, got %d", len(te.fle.serverEvents))
	}


	// Client events
	checkEventRequestHeader(te.t, te.fle.clientEvents[0], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT,
		Authority:   te.srvAddr,
		ServiceName: "grpc.testing.TestService",
		MethodName:  "UnaryCall",
	})
	// either check
	// fuck, but the way I'm doing it is with a stub server so won't exactly be same...
	// but event type, heuristically wrt fields it should be similar, and also the granularity of verifications
	// of what we cannnn expect
	/*
	type grpcLogEntry struct {
	    CallId           string    `json:"callId,omitempty"`
	    SequenceID       uint64    `json:"sequenceId,omitempty"`
	    Type             EventType `json:"type,omitempty"`
	    Logger           Logger    `json:"logger,omitempty"`
	    Payload          Payload   `json:"payload,omitempty"`
	    PayloadTruncated bool      `json:"payloadTruncated,omitempty"`
	    Peer             Address   `json:"peer,omitempty"`
	    Authority        string    `json:"authority,omitempty"`
	    ServiceName      string    `json:"serviceName,omitempty"`
	    MethodName       string    `json:"methodName,omitempty"`
	}
	*/
	grpcLogEntryEquv := &grpcLogEntry{
		MethodName: "UnaryCall", // these are going to change
		ServiceName: "grpc.testing.TestService",
		Authority: te.srvAddr,
	}



	// we're taking grpcLogEntry as a struct and making validations on that

	// this gets marshaled into exporter. Cloud Logging Exporter calls marshalJSON(),
	// we just validate, so this honestly doesn't block this test.

	// rather than write the expectations inline, get the system setup to the point
	// where you actually get these logEntries emitted from the system

	// Then write the discrete things that are being emitted (from my own e2e
	// flow with the way I set up the RPC's to be called. This is different to
	// Lidi's so specifics will be different but overall will be somewaht
	// similar). And also, from this, can see which fields I cannnnn make
	// validations on and which ones I can't. Like literally print...***





	// testing the fields emitted only that you're required and are guarnateed,
	// cmp.Diff with ignore or like Lidi's and define your own comparison
	// function

	checkEventRequestMessage(te.t, te.fle.clientEvents[1], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT,
	}, testOkPayload)
	checkEventResponseHeader(te.t, te.fle.clientEvents[2], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT,
	})
	checkEventResponseMessage(te.t, te.fle.clientEvents[3], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT,
	}, testOkPayload)
	checkEventTrailer(te.t, te.fle.clientEvents[4], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT,
		StatusCode:  0,
	})
	// equivalent in


	// client stream ^^^ or server stream vvv is logically equivalent to
	// grpc.Dial and grpc.NewServer

	// This is equivalent to []client RPC events with * for method

	// and vvv []server RPC events with * for method

	// could test it in one, but uses same exporter in my codepath,
	// would need to rewrite my code to use a different exporter for client/server

	// convert this to what the new schema looks like for exact expectations



	// Server events
	checkEventRequestHeader(te.t, te.fle.serverEvents[0], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER,
	})
	checkEventRequestMessage(te.t, te.fle.serverEvents[1], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER,
	}, testOkPayload)
	checkEventResponseHeader(te.t, te.fle.serverEvents[2], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER,
	})
	checkEventResponseMessage(te.t, te.fle.serverEvents[3], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER,
	}, testOkPayload)
	checkEventTrailer(te.t, te.fle.serverEvents[4], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER,
		StatusCode:  0,
	})
}

func (s) TestLoggingForErrorCall(t *testing.T) {
	te := newTest(t)
	defer te.tearDown()
	te.enablePluginWithCaptureAll()
	te.startServer(&testServer{})
	tc := testgrpc.NewTestServiceClient(te.clientConn())

	req := &testpb.SimpleRequest{Payload: &testpb.Payload{Body: testErrorPayload}}
	tCtx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	_, err := tc.UnaryCall(metadata.NewOutgoingContext(tCtx, testHeaderMetadata), req)
	if err == nil {
		t.Fatalf("unary call expected to fail, but passed")
	}

	// Wait for the gRPC transport to gracefully close to ensure no lost event.
	te.cc.Close()
	te.srv.GracefulStop()
	// Check size of events
	if len(te.fle.clientEvents) != 4 {
		t.Fatalf("expects 4 client events, got %d", len(te.fle.clientEvents))
	}
	if len(te.fle.serverEvents) != 4 {
		t.Fatalf("expects 4 server events, got %d", len(te.fle.serverEvents))
	}
	// Client events
	checkEventRequestHeader(te.t, te.fle.clientEvents[0], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT,
		Authority:   te.srvAddr,
		ServiceName: "grpc.testing.TestService",
		MethodName:  "UnaryCall",
	})
	checkEventRequestMessage(te.t, te.fle.clientEvents[1], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT,
	}, testErrorPayload)
	checkEventResponseHeader(te.t, te.fle.clientEvents[2], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT,
	})
	checkEventTrailer(te.t, te.fle.clientEvents[3], &grpclogrecordpb.GrpcLogRecord{
		EventLogger:   grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT,
		StatusCode:    2,
		StatusMessage: testErrorMessage,
	})
	// Server events
	checkEventRequestHeader(te.t, te.fle.serverEvents[0], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER,
	})
	checkEventRequestMessage(te.t, te.fle.serverEvents[1], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER,
	}, testErrorPayload)
	checkEventResponseHeader(te.t, te.fle.serverEvents[2], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER,
	})
	checkEventTrailer(te.t, te.fle.serverEvents[3], &grpclogrecordpb.GrpcLogRecord{
		EventLogger:   grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER,
		StatusCode:    2,
		StatusMessage: testErrorMessage,
	})
}

func (s) TestEmptyConfig(t *testing.T) {
	te := newTest(t)
	defer te.tearDown()
	te.enablePluginWithConfig(&config{})
	te.startServer(&testServer{})
	tc := testgrpc.NewTestServiceClient(te.clientConn())

	req := &testpb.SimpleRequest{Payload: &testpb.Payload{Body: testOkPayload}}
	tCtx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	resp, err := tc.UnaryCall(metadata.NewOutgoingContext(tCtx, testHeaderMetadata), req)
	if err != nil {
		t.Fatalf("unary call failed: %v", err)
	}
	t.Logf("unary call passed: %v", resp)

	// Wait for the gRPC transport to gracefully close to ensure no lost event.
	te.cc.Close()
	te.srv.GracefulStop()
	// Check size of events
	if len(te.fle.clientEvents) != 0 {
		t.Fatalf("expects 0 client events, got %d", len(te.fle.clientEvents))
	}
	if len(te.fle.serverEvents) != 0 {
		t.Fatalf("expects 0 server events, got %d", len(te.fle.serverEvents))
	}
}

func (s) TestOverrideConfig(t *testing.T) {
	te := newTest(t)
	defer te.tearDown()
	// Setting 3 filters, expected to use the third filter, because it's the
	// most specific one. The third filter allows message payload logging, and
	// others disabling the message payload logging. We should observe this
	// behavior latter.
	te.enablePluginWithConfig(&config{
		EnableCloudLogging:   true,
		DestinationProjectID: "fake",
		LogFilters: []logFilter{
			{
				Pattern:      "wont/match",
				MessageBytes: 0,
			},
			{
				Pattern:      "*",
				MessageBytes: 0,
			},
			{
				Pattern:      "grpc.testing.TestService/*",
				MessageBytes: 4096,
			},
		},
	})
	te.startServer(&testServer{})
	tc := testgrpc.NewTestServiceClient(te.clientConn())

	var (
		resp *testpb.SimpleResponse
		req  *testpb.SimpleRequest
		err  error
	)
	req = &testpb.SimpleRequest{Payload: &testpb.Payload{Body: testOkPayload}}
	tCtx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	resp, err = tc.UnaryCall(metadata.NewOutgoingContext(tCtx, testHeaderMetadata), req)
	if err != nil {
		t.Fatalf("unary call failed: %v", err)
	}
	t.Logf("unary call passed: %v", resp)

	// Wait for the gRPC transport to gracefully close to ensure no lost event.
	te.cc.Close()
	te.srv.GracefulStop()
	// Check size of events
	if len(te.fle.clientEvents) != 5 {
		t.Fatalf("expects 5 client events, got %d", len(te.fle.clientEvents))
	}
	if len(te.fle.serverEvents) != 5 {
		t.Fatalf("expects 5 server events, got %d", len(te.fle.serverEvents))
	}
	// Check Client message payloads
	checkEventRequestMessage(te.t, te.fle.clientEvents[1], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT,
	}, testOkPayload)
	checkEventResponseMessage(te.t, te.fle.clientEvents[3], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT,
	}, testOkPayload)
	// Check Server message payloads
	checkEventRequestMessage(te.t, te.fle.serverEvents[1], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER,
	}, testOkPayload)
	checkEventResponseMessage(te.t, te.fle.serverEvents[3], &grpclogrecordpb.GrpcLogRecord{
		EventLogger: grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER,
	}, testOkPayload)
}

func (s) TestNoMatch(t *testing.T) {
	te := newTest(t)
	defer te.tearDown()
	// Setting 3 filters, expected to use the second filter. The second filter
	// allows message payload logging, and others disabling the message payload
	// logging. We should observe this behavior latter.
	te.enablePluginWithConfig(&config{
		EnableCloudLogging:   true,
		DestinationProjectID: "fake",
		LogFilters: []logFilter{
			{
				Pattern:      "wont/match",
				MessageBytes: 0,
			},
		},
	})
	te.startServer(&testServer{})
	tc := testgrpc.NewTestServiceClient(te.clientConn())

	var (
		resp *testpb.SimpleResponse
		req  *testpb.SimpleRequest
		err  error
	)
	req = &testpb.SimpleRequest{Payload: &testpb.Payload{Body: testOkPayload}}
	tCtx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	resp, err = tc.UnaryCall(metadata.NewOutgoingContext(tCtx, testHeaderMetadata), req)
	if err != nil {
		t.Fatalf("unary call failed: %v", err)
	}
	t.Logf("unary call passed: %v", resp)

	// Wait for the gRPC transport to gracefully close to ensure no lost event.
	te.cc.Close()
	te.srv.GracefulStop()
	// Check size of events
	if len(te.fle.clientEvents) != 0 {
		t.Fatalf("expects 0 client events, got %d", len(te.fle.clientEvents))
	}
	if len(te.fle.serverEvents) != 0 {
		t.Fatalf("expects 0 server events, got %d", len(te.fle.serverEvents))
	}
}

func (s) TestRefuseStartWithInvalidPatterns(t *testing.T) {
	config := &config{
		EnableCloudLogging:   true,
		DestinationProjectID: "fake",
		LogFilters: []logFilter{
			{
				Pattern: ":-)",
			},
			{
				Pattern: "*",
			},
		},
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to convert config to JSON: %v", err)
	}
	os.Setenv(envObservabilityConfigJSON, "")
	os.Setenv(envObservabilityConfig, string(configJSON))
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
		"destination_project_id": "fake",
		"log_filters":[{"pattern":"*","header_bytes":1073741824,"message_bytes":1073741824}]
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
	configJSON := `{
		"destination_project_id":"fake",
		"log_filters":[{"pattern":":-)"}, {"pattern_string":"*"}]
	}`
	cleanup, err := createTmpConfigInFileSystem(configJSON)
	defer cleanup()
	if err != nil {
		t.Fatalf("failed to create config in file system: %v", err)
	}
	// This configuration should be ignored, as precedence 2.
	validConfig := &config{
		EnableCloudLogging:   true,
		DestinationProjectID: "fake",
		LogFilters: []logFilter{
			{
				Pattern:      "*",
				HeaderBytes:  infinitySizeBytes,
				MessageBytes: infinitySizeBytes,
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

// These OpenCensus tests use te? This is same logic just different
// config, plumbed through differently. I think it would make sense to
// figure this out first before

func (s) TestOpenCensusIntegration(t *testing.T) {
	te := newTest(t)
	defer te.tearDown()
	fe := &fakeOpenCensusExporter{SeenViews: make(map[string]string), t: te.t}

	defer func(ne func(config *config) (tracingMetricsExporter, error)) {
		newExporter = ne
	}(newExporter)

	newExporter = func(config *config) (tracingMetricsExporter, error) {
		return fe, nil
	}

	te.enableOpenCensus() // already switched
	// should I keep this plumbing? Sure for this, can using this for logging
	// also help? Seems to allow more granular control on stream operations

	// Right now you do ss.Client.UnaryCall() -> emits 5 stream operations

	// Full duplex call -> emits operations

	// I guess it would be more useful to have manual stream operations that you
	// can control like knobs to create headers for bin logging, can you take atomic
	// stream operations and build out a unary and streaming RPC like I desire to?

	te.startServer(&testServer{})
	tc := testgrpc.NewTestServiceClient(te.clientConn())
	tc. // can call rpcs - different methods

	for i := 0; i < defaultRequestCount; i++ {
		req := &testpb.SimpleRequest{Payload: &testpb.Payload{Body: testOkPayload}}
		tCtx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
		defer cancel()
		_, err := tc.UnaryCall(metadata.NewOutgoingContext(tCtx, testHeaderMetadata), req)
		if err != nil {
			t.Fatalf("unary call failed: %v", err)
		}
	}
	t.Logf("unary call passed count=%v", defaultRequestCount)

	// Wait for the gRPC transport to gracefully close to ensure no lost event.
	te.cc.Close()
	te.srv.GracefulStop()

	var errs []error
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	for ctx.Err() == nil {
		errs = nil
		fe.mu.RLock()
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
		ct := config.CustomTags
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
		"destination_project_id": "fake",
		"enable_cloud_trace": true,
		"enable_cloud_monitoring": true,
		"global_trace_sampling_rate": 1.0,
		"custom_tags":{"customtag1":"wow","customtag2":"nice"}
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
}
