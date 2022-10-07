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
	gcplogging "cloud.google.com/go/logging"
	"context"
	"encoding/json"
	"fmt"
	"google.golang.org/grpc/internal/leakcheck"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/test/grpc_testing"
	"io"
	"os"
	"sync"
	"testing"
)

func init() {
	// OpenCensus, once included in binary, will spawn a global goroutine
	// recorder that is not controllable by application.
	// https://github.com/census-instrumentation/opencensus-go/issues/1191
	leakcheck.RegisterIgnoreGoroutine("go.opencensus.io/stats/view.(*worker).start")
	// google-cloud-go leaks HTTP client. They are aware of this:
	// https://github.com/googleapis/google-cloud-go/issues/1183
	leakcheck.RegisterIgnoreGoroutine("internal/poll.runtime_pollWait")
}

type fakeLoggingExporter struct {
	t            *testing.T
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

// called by operations that have no guarantee of concurrency
func (fle *fakeLoggingExporter) EmitGcpLoggingEntry(entry gcplogging.Entry) { // this gets called on any stream operation, no guarantee there
	fle.mu.Lock()
	defer fle.mu.Unlock()
	print(entry.Timestamp.String())
	print(entry.Severity)
	grpcLogEntry, ok := entry.Payload.(*grpcLogEntry)
	if !ok {
		fle.t.Errorf("payload passed in isn't grpcLogEntry")
	}
	fle.entries = append(fle.entries, grpcLogEntry)
}

func (fle *fakeLoggingExporter) Close() error {
	fle.isClosed = true
	return nil
}

// setupObservabilitySystemWithConfig sets up
func setupObservabilitySystemWithConfig(cfg *config) (func(), error) { // returns a cleanup function and error
	/*fle := &fakeLoggingExporter{
		t: t,

	}
	defer func(ne func(ctx context.Context, config *config) (loggingExporter, error)) {
		newLoggingExporter = ne
	}(newLoggingExporter)

	newLoggingExporter = func(ctx context.Context, config *config) (loggingExporter, error) {
		return fle, nil
	}*/ // unless there is a way to plumb this within this function (particularly the cleanup function seems tricky)

	// If they get mad it's not declared JSON, say it's cleaner this way and tests JSON tags?
	validConfigJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to convert config to JSON: %v", err)
	}
	os.Setenv(envObservabilityConfig, string(validConfigJSON))
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	err = Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("error in Start: %v", err)
	}
	return func() {
		End()
		os.Setenv(envObservabilityConfig, "")
	}, nil // return this cleanup function
}


// TestClientRPCEventsLogAll tests the observability system setup with a client
// RPC event that logs every call. It performs a Unary and Bidirectional
// Streaming RPC, and expects certain gRPC Logging entries to make it's way to
// the exporter.
func (s) TestClientRPCEventsLogAll(t *testing.T) {

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
	} // this and server setup are common to every test - put into setup()?

	clientRPCEventLogAllConfig := &config{
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
	cleanup, err := setupObservabilitySystemWithConfig(clientRPCEventLogAllConfig)
	if err != nil {
		t.Fatalf("error setting up observability %v", err)
	}
	defer cleanup() // oh now you can test this and see if it works
	// If they get mad, say it's cleaner this way and tests JSON tags?
	/*validConfigJSON, err := json.Marshal(clientRPCEventLogAllConfig)
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
	defer End() // return this cleanup function*/

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

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{}); err != nil {
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}
	// Downstream effects of this UnaryCall
	// log the actual events happening,

	for i, gle := range fle.entries {
		print("entry index: ", i, " ")
		print("entry type: ", gle.Type, " ")
		print("method name: ", gle.MethodName, " ")
		print("service name: ", gle.ServiceName, " ")
		print("authority: ", gle.Authority, " ")
		print("gle.Peer.Address: ", gle.Peer.Address, " ")
		print("gle.Peer.Type: ", gle.Peer.Type, " ")
		print("gle.Peer.IpPort: ", gle.Peer.IpPort, " ")
		// the payload seems to be dependent on what type of stream operation is happening on the RPC.
		print("gle.Payload.Type: ", gle.Payload.Message, " ")
		print("gle.Payload.StatusCode: ", gle.Payload.StatusCode, " ")
		print("gle.Payload.Metadata: ", gle.Payload.Metadata, " ")
		// payload stuff comes later
	}
	// Get this sanity test working in regards to the events specific to logging
	// var expectedGCPLoggingEntries []grpcLogEntry // do we want this to be a pointer?

	/*
	EventTypeUnknown EventType = iota 0
	// ClusterTypeLogicalDNS represents the Logical DNS cluster type, which essentially
	// maps to the gRPC behavior of using the DNS resolver with pick_first LB policy.
	ClientHeader 1
	// ClusterTypeAggregate represents the Aggregate Cluster type, which provides a
	// prioritized list of clusters to use. It is used for failover between clusters
	// with a different configuration.
	ServerHeader 2
	ClientMessage 3
	ServerMessage 4
	ClientHalfClose 5
	ServerTrailer 6
	Cancel 7
	*/

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

	// fill out a want for the entries client side


	// cmp.Diff only the entries you reallyyyyy want to test and are verifiable
	// IgnoreFields <- keep adding these until it works.



	// cmp.Diff with ignoring fields or something like Lidi's where you verify each field individually
	grpcLogEntriesWant := make([]grpcLogEntry, 5)
	grpcLogEntriesWant[0] = grpcLogEntry{
		Type: ClientHeader,
		Logger: Client, // these are all for client streams in the bin, should all be client stream, yeah lines up with client events in Lidi's verification
		ServiceName: "grpc.testing.TestService",
		MethodName: "UnaryCall",
		// doesn't have an address? Is this correct?
		Authority: "127.0.0.1:501", // authority only on the first client header? This is a port where do I get this information
		// Authority:   te.srvAddr, in Lidi's, only tests on the first event
	}
	// Need a comparator for Metadata. This seems like an important thing to compare.
	// cmp.Diff() // on the whole slice of log entries?
	/*
	type Payload struct {
	    Metadata      map[string]string `json:"metadata,omitempty"`
	    Timeout       time.Duration     `json:"timeout,omitempty"`
	    StatusCode    uint32            `json:"statusCode,omitempty"`
	    StatusMessage string            `json:"statusMessage,omitempty"`
	    StatusDetails []byte            `json:"statusMessage,omitempty"`
	    MessageLength uint32            `json:"messageLength,omitempty"`
	    Message       []byte            `json:"message,omitempty"`
	}
	*/
	/*
	type Address struct {
	    Type    Type   `json:"type,omitempty"`
	    Address string `json:"address,omitempty"`
	    IpPort  uint32 `json:"ipPort,omitempty"`
	}
	*/

	// cmp.Diff across the whole array, but then the fields you want to ignore for others won't happen
	// I think if you try cmp.Diff and play around with it you can go from utmost granularity to work backwards
	// and see the least amount of things you need to exclude.

	grpcLogEntriesWant[1] = grpcLogEntry{
		Type: ClientMessage,
		Logger: Client,
		ServiceName: "grpc.testing.TestService",
		MethodName: "UnaryCall",
	}
	grpcLogEntriesWant[2] = grpcLogEntry{
		Type: ServerHeader,
		Logger: Client,
		ServiceName: "grpc.testing.TestService",
		MethodName: "UnaryCall",
		// this is the only thing with address,
		Peer: Address{
			Address: "127.0.0.1", // localhost :)
			Type: 1, // weird, IPV4, I don't think this is really fixed and something we should verify. This seems to be a variable.
			IpPort: 50166, // what?
		},
	}
	grpcLogEntriesWant[3] = grpcLogEntry{
		Type: ServerMessage,
		Logger: Client,
		ServiceName: "grpc.testing.TestService",
		MethodName: "UnaryCall",
	}
	grpcLogEntriesWant[4] = grpcLogEntry{
		Type: ServerTrailer,
		Logger: Client,
		ServiceName: "grpc.testing.TestService",
		MethodName: "UnaryCall",
		Peer: Address{
			Address: "127.0.0.1", // localhost :)
			Type: 1, // weird, IPV4, I don't think this is really fixed and something we should verify. This seems to be a variable.
			IpPort: 50166, // what?
		},
	} // Why doesn't this have status?

	// If you want to triage streaming difference,
	// log the events here
	// streaming RPC
	// could see 4 client events

	// in another test case, if you configure with server, can see 5 then 3

	// Make a streaming RPC. This should cause Log calls on the MethodLogger.
	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend() // set handshake - causes return nil on server side, causes an io.EOF error to come out of client side due to strema ending with no error present
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}
	for i, gle := range fle.entries {
		print("entry index: ", i, " ")
		print("entry type: ", gle.Type, " ")
		/*print("method name: ", gle.MethodName, " ")
		print("service name: ", gle.ServiceName, " ")
		print("authority: ", gle.Authority, " ")
		print("gle.Peer.Address: ", gle.Peer.Address, " ")
		print("gle.Peer.Type: ", gle.Peer.Type, " ")
		print("gle.Peer.IpPort: ", gle.Peer.IpPort, " ")
		// the payload seems to be dependent on what type of stream operation is happening on the RPC.
		print("gle.Payload.Type: ", gle.Payload.Message, " ")
		print("gle.Payload.StatusCode: ", gle.Payload.StatusCode, " ")
		print("gle.Payload.Metadata: ", gle.Payload.Metadata, " ")
		// payload stuff comes later*/
	}

	// entry type: 1 Client Header
	gle := grpcLogEntry{

	}
	grpcLogEntriesWant = append(grpcLogEntriesWant, gle)
	// entry type: 5 Client Half Close
	gle = grpcLogEntry{

	}
	grpcLogEntriesWant = append(grpcLogEntriesWant, gle)
	// entry type: 2 Server Header
	gle = grpcLogEntry{

	}
	grpcLogEntriesWant = append(grpcLogEntriesWant, gle)
	// entry type: 6 Server Trailer
	gle = grpcLogEntry{

	}
	grpcLogEntriesWant = append(grpcLogEntriesWant, gle)

	print("final count: ", len(fle.entries))
}

func (s) TestServerRPCEventsLogAll(t *testing.T) {
	fle := &fakeLoggingExporter{
		t: t,

	}
	defer func(ne func(ctx context.Context, config *config) (loggingExporter, error)) {
		newLoggingExporter = ne
	}(newLoggingExporter)

	newLoggingExporter = func(ctx context.Context, config *config) (loggingExporter, error) {
		return fle, nil
	} // this and server setup are common to every test - put into setup()?

	serverRPCEventLogAllConfig := &config{
		ProjectID: "fake",
		CloudLogging: &cloudLogging{
			ServerRPCEvents: []serverRPCEvents{
				{
					Method: []string{"*"},
					MaxMetadataBytes: 30,
					MaxMessageBytes: 30,
				},
			}, // this maps to the server side RPC events
		},

		// no cloud monitoring
		// no cloud trace
	}
	cleanup, err := setupObservabilitySystemWithConfig(serverRPCEventLogAllConfig)
	if err != nil {
		t.Fatalf("error setting up observability %v", err)
	}
	defer cleanup()

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

	// same thing as client,
	// unary
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{}); err != nil {
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}
	// expected logEntries




	// streaming
	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend() // set handshake - causes return nil on server side, causes an io.EOF error to come out of client side due to strema ending with no error present
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}

	// expected logEntries

	// Same exact expectations as the client except Logger: Server,
	for i, gle := range fle.entries {
		print("entry index: ", i, " ")
		print("entry type: ", gle.Type, " ")
		/*print("method name: ", gle.MethodName, " ")
		print("service name: ", gle.ServiceName, " ")
		print("authority: ", gle.Authority, " ")
		print("gle.Peer.Address: ", gle.Peer.Address, " ")
		print("gle.Peer.Type: ", gle.Peer.Type, " ")
		print("gle.Peer.IpPort: ", gle.Peer.IpPort, " ")
		// the payload seems to be dependent on what type of stream operation is happening on the RPC.
		print("gle.Payload.Type: ", gle.Payload.Message, " ")
		print("gle.Payload.StatusCode: ", gle.Payload.StatusCode, " ")
		print("gle.Payload.Metadata: ", gle.Payload.Metadata, " ")*/
		// payload stuff comes later
	}


	// entry type: 1 - Client Header
	// entry type: 5 - Client Half Close
	// entry type: 6 - Server Trailer

	print("final count: ", len(fle.entries))
}


// after these basic things (unary/streaming on capture all on client and server) have other logic to test:

// if both client are server are configured, just do count at that point len of persisted events, combine into one blob since
// more specific details have previously been tested
func (s) TestBothClientAndServerRPCEvents(t *testing.T) {
	serverRPCEventLogAllConfig := &config{
		ProjectID: "fake",
		CloudLogging: &cloudLogging{
			ClientRPCEvents: []clientRPCEvents{
				{
					Method: []string{"*"},
					MaxMetadataBytes: 30,
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
		// no cloud trace - oh god, will have to rewrite all those tests too
	}

	cleanup, err := setupObservabilitySystemWithConfig(serverRPCEventLogAllConfig)
	if err != nil {
		t.Fatalf("error setting up observability %v", err)
	}
	defer cleanup()
	// Same thing as client,
	// unary

	// expected logEntries (just count of both of them combined)

	// "both sides of streams should write to exporter", so expect it with n


	// streaming

	// expected logEntries (just count of both of them combined)

	// "both sides of streams should write to exporter", so expect it with n

}


// Config with three blobs of events: [!exclude, exclude, !exclude], have one
// that hits each event (also makes sure it doesn't hit two nodes and log twice
// and makes sure it stops processing the moment it hits), explain what logic
// this test covers further in comment, and how the three nodes cover logic we
// want tested

// First test case: service/unary super specific - should log (this would also match third, if this only logs once tests
// it hits first match and doesn't proceed further)

// Second test case: service/streaming - shouldn't log

// Third test case: service/ - should log (needs to be a third function call which doesn't hit first one)


// TestPrecedenceOrderingInConfiguration tests the scenario where the logging
// part of observability is configured with three client RPC events, the first
// two on specific methods in the service, the last one for any method within
// the service. This test sends three RPC's, one corresponding to each log
// entry. The logging logic dictated by that specific event should be what is
// used for emission (i.e. a specific method in a service will match the third
// event too, but should only use logic within one it first matches to). The
// second event will specify to exclude logging on RPC's, which should generate
// no log entries if an RPC gets to and matches that event.
func (s) TestPrecedenceOrderingInConfiguration(t *testing.T) {
	fle := &fakeLoggingExporter{
		t: t,
	}

	defer func(ne func(ctx context.Context, config *config) (loggingExporter, error)) {
		newLoggingExporter = ne
	}(newLoggingExporter)

	newLoggingExporter = func(ctx context.Context, config *config) (loggingExporter, error) {
		return fle, nil
	}

	threeEventsConfig := &config{
		ProjectID: "fake",
		CloudLogging: &cloudLogging{
			ClientRPCEvents: []clientRPCEvents{
				{
					Method: []string{"/grpc.testing.TestService/UnaryCall"},
					MaxMetadataBytes: 30,
					MaxMessageBytes: 30,
				},
				{
					Method: []string{"/grpc.testing.TestService/FullDuplexCall"},
					Exclude: true,
					MaxMetadataBytes: 30, // To not conflate no bytes logged with exclude
					MaxMessageBytes: 30,
				},
				{
					Method: []string{"/grpc.testing.TestService/*"},
					MaxMetadataBytes: 30,
					MaxMessageBytes: 20, // can verify first doesn't hit this.
				},
			},
		},

		// no cloud monitoring
		// no cloud trace - oh god, will have to rewrite all those tests too
	}

	cleanup, err := setupObservabilitySystemWithConfig(threeEventsConfig)
	if err != nil {
		t.Fatalf("error setting up observability %v", err)
	}
	defer cleanup()

	// This is common setup ^^^ factor out into setup() with config as parameter?

	// Also this with three methods is shared amongst all the test cases,
	// do you want this to be for all of them and part of setup() too?
	ss := &stubserver.StubServer{
		EmptyCallF: func(ctx context.Context, in *grpc_testing.Empty) (*grpc_testing.Empty, error) {
			return &grpc_testing.Empty{}, nil
		},
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


	// Unary RPC should match with first event and logs should correspond
	// accordingly (also verifies stops iteration at the first node hit).
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{}); err != nil {
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}
	print("len(fle.entries) after unary call: ", len(fle.entries), " ")


	// Streaming RPC should match with second event and cause no downstream logs
	// (ASSERT count is same as after Unary RPC?)
	// streaming
	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend() // set handshake - causes return nil on server side, causes an io.EOF error to come out of client side due to strema ending with no error present
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}
	print("len(fle.entries) after full duplex call: ", len(fle.entries), " ") // fuck there's a bug this still logs

	// A third RPC should match with the end wildcard and log accordingly
	if _, err := ss.Client.EmptyCall(ctx, &grpc_testing.Empty{}); err != nil {
		t.Fatalf("Unexpected error from EmptyCall: %v", err)
	}
	print("len(fle) after empty call: ", len(fle.entries), " ")
}

/*
func (s) TestErrorStart(t testing.T) {
	// T test?
	// One error is that * wildcard is configured with Exclude = true, not allowed
	// any others?
}
*/


// all of the other tests, rewritten to use new config
// opencensus, env vars, failure paths?

// Doug's e2e test that End() works if Start() errors (look at technical document for exact scenario)

// JSON Marshaling (I think the only special logic needed is the ints, otherwise
// should be trivial), will call MarshalJSON on that type, see
// DiscoveryMechanism for example.

// Defining log entries sounds so boring

// pull the o11y tests first