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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/google/go-cmp/cmp"
	binlogpb "google.golang.org/grpc/binarylog/grpc_binarylog_v1"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/test/grpc_testing"
	"io"
	"os"
	"sync"
	"testing"
)


// expected log entries
// throughout this goddamn test will need to define expected log entries

// In regards to sharing the []LoggingEntry
// Unary Server/ClientSide are the same

// Streaming RPC Server/Client Side share the same thing expect extra server headers (stick that into []?)

// Empty Call has a different []LoggingEntry - only for last test




type fakeLoggingExporter struct {
	t        *testing.T

	mu       sync.Mutex
	isClosed bool
	entries []*grpcLogEntry
}

func (fle *fakeLoggingExporter) EmitGcpLoggingEntry(entry gcplogging.Entry) {
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
	fle.isClosed = true // maybe verfiy this gets set when observability exits in Close()
	return nil
}

// setupObservabilitySystemWithConfig sets up
func setupObservabilitySystemWithConfig(cfg *config) (func(), error) {
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
	}, nil
}

// TestClientRPCEventsLogAll tests the observability system setup with a client
// RPC event that logs every call. It performs a Unary and Bidirectional
// Streaming RPC, and expects certain gRPC Logging entries to make it's way to
// the exporter.
func (s) TestClientRPCEventsLogAll(t *testing.T) {
	fle := &fakeLoggingExporter{
		t: t,
	}
	defer func(ne func(ctx context.Context, config *config) (loggingExporter, error)) {
		newLoggingExporter = ne
	}(newLoggingExporter)

	newLoggingExporter = func(ctx context.Context, config *config) (loggingExporter, error) {
		return fle, nil
	} // this and server setup are common to every test - put into setup()?

	// test this by plumbing in? and seeing how many bytes get emitted?
	// Orthogonal to number of events emitted and you can further test the bytes
	// by looking at specific payload. This is a dependency for logic for last
	// test (i.e. correct bytes logged). Need to fill up Metadata and Messages
	// with a big enough thing to log it pushes against the max.

	clientRPCEventLogAllConfig := &config{
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
	cleanup, err := setupObservabilitySystemWithConfig(clientRPCEventLogAllConfig)
	if err != nil {
		t.Fatalf("error setting up observability %v", err)
	}
	defer cleanup()

	ss := &stubserver.StubServer{
		UnaryCallF: func(ctx context.Context, in *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
			return &grpc_testing.SimpleResponse{}, nil
		},
		FullDuplexCallF: func(stream grpc_testing.TestService_FullDuplexCallServer) error {
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
		print("gle.payload.Type: ", gle.Payload.Message, " ")
		print("gle.payload.StatusCode: ", gle.Payload.StatusCode, " ")
		print("gle.payload.Metadata: ", gle.Payload.Metadata, " ")
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
		    payload          payload   `json:"payload,omitempty"`
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
		Type:        clientHeader,
		Logger:      client, // these are all for client streams in the bin, should all be client stream, yeah lines up with client events in Lidi's verification
		ServiceName: "grpc.testing.TestService",
		MethodName:  "UnaryCall",
		// doesn't have an address? Is this correct?
		Authority: "127.0.0.1:501", // authority only on the first client header? This is a port where do I get this information
		// Authority:   te.srvAddr, in Lidi's, only tests on the first event
	}
	// Need a comparator for Metadata. This seems like an important thing to compare.
	// cmp.Diff() // on the whole slice of log entries?
	/*
		type payload struct {
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
		Type:        clientMessage,
		Logger:      client,
		ServiceName: "grpc.testing.TestService",
		MethodName:  "UnaryCall",
	}
	grpcLogEntriesWant[2] = grpcLogEntry{
		Type:        serverHeader,
		Logger:      client,
		ServiceName: "grpc.testing.TestService",
		MethodName:  "UnaryCall",
		// this is the only thing with address,
		Peer: address{
			Address: "127.0.0.1", // localhost :)
			Type:    1,           // weird, IPV4, I don't think this is really fixed and something we should verify. This seems to be a variable.
			IpPort:  50166,       // what?
		},
	}
	grpcLogEntriesWant[3] = grpcLogEntry{
		Type:        serverMessage,
		Logger:      client,
		ServiceName: "grpc.testing.TestService",
		MethodName:  "UnaryCall",
	}
	grpcLogEntriesWant[4] = grpcLogEntry{
		Type:        serverTrailer,
		Logger:      client,
		ServiceName: "grpc.testing.TestService",
		MethodName:  "UnaryCall",
		Peer: address{
			Address: "127.0.0.1", // localhost :)
			Type:    1,           // weird, IPV4, I don't think this is really fixed and something we should verify. This seems to be a variable.
			IpPort:  50166,       // what?
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
		print("gle.payload.Type: ", gle.payload.Message, " ")
		print("gle.payload.StatusCode: ", gle.payload.StatusCode, " ")
		print("gle.payload.Metadata: ", gle.payload.Metadata, " ")
		// payload stuff comes later*/
	}

	// entry type: 1 Client Header
	gle := grpcLogEntry{
		Type: clientHeader,
	}
	grpcLogEntriesWant = append(grpcLogEntriesWant, gle)
	// entry type: 5 Client Half Close
	gle = grpcLogEntry{
		Type: clientHalfClose,
	}
	grpcLogEntriesWant = append(grpcLogEntriesWant, gle)
	// ^^^^ shared atoms with server side

	// entry type: 2 Server Header WRONG, logs headers if trailers only.
	gle = grpcLogEntry{
		Type: serverHeader,
	}

	// vvvv shared atoms with client side
	grpcLogEntriesWant = append(grpcLogEntriesWant, gle)
	// entry type: 6 Server Trailer
	gle = grpcLogEntry{
		Type: serverTrailer,
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
					Method:           []string{"*"},
					MaxMetadataBytes: 30,
					MaxMessageBytes:  30,
				},
			},
		},
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
		print("gle.payload.Type: ", gle.payload.Message, " ")
		print("gle.payload.StatusCode: ", gle.payload.StatusCode, " ")
		print("gle.payload.Metadata: ", gle.payload.Metadata, " ")*/
		// payload stuff comes later
	}

	// entry type: 1 - Client Header
	// entry type: 5 - Client Half Close
	// entry type: 6 - Server Trailer

	print("final count: ", len(fle.entries))
}

// after these basic things (unary/streaming on capture all on client and server) have other logic to test:

// TestBothClientAndServerRPCEvents tests the scenario where you have both
// Client and Server RPC Events configured to log. Both sides should log and
// share the exporter, so the exporter should receive the collective amount of
// calls for both a client stream (corresponding to a Client RPC Event) and a
// server stream (corresponding ot a Server RPC Event). The specificity of the
// entries are tested in previous tests.
func (s) TestBothClientAndServerRPCEvents(t *testing.T) {
	fle := &fakeLoggingExporter{
		t: t,
	}
	defer func(ne func(ctx context.Context, config *config) (loggingExporter, error)) {
		newLoggingExporter = ne
	}(newLoggingExporter)

	newLoggingExporter = func(ctx context.Context, config *config) (loggingExporter, error) {
		return fle, nil
	}

	serverRPCEventLogAllConfig := &config{
		ProjectID: "fake",
		CloudLogging: &cloudLogging{
			ClientRPCEvents: []clientRPCEvents{
				{
					Method:           []string{"*"},
					MaxMetadataBytes: 30,
					MaxMessageBytes:  30,
				},
			},
			ServerRPCEvents: []serverRPCEvents{
				{
					Method:           []string{"*"},
					MaxMetadataBytes: 30,
					MaxMessageBytes:  30,
				},
			},
		},
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

	// Same thing as client,

	// unary
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{}); err != nil {
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}

	// Both client side and server side streams should log entries, which share
	// the same exporter. The exporter should thus receive both entries (the
	// specificity of entries is checked in previous tests).
	if len(fle.entries) != 10 {
		t.Fatalf("Unexpected length of entries %v, want 10 (collective of client and server)", len(fle.entries))
	}



	// "both sides of streams should write to exporter", so expect it with n
	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend() // set handshake - causes return nil on server side, causes an io.EOF error to come out of client side due to strema ending with no error present
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}
	if len(fle.entries) != 17 {
		t.Fatalf("Unexpected length of entries %v, want 17 (collective of client and server)", len(fle.entries))
	}
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
					Method:           []string{"/grpc.testing.TestService/UnaryCall"},
					MaxMetadataBytes: 30,
					MaxMessageBytes:  30,
				},
				{
					Method:           []string{"/grpc.testing.TestService/FullDuplexCall"},
					Exclude:          true,
					MaxMetadataBytes: 30,
					MaxMessageBytes:  30,
				},
				{
					Method:           []string{"/grpc.testing.TestService/*"},
					MaxMetadataBytes: 30,
					MaxMessageBytes:  20, // can verify first doesn't hit this.
				},
			},
		},
	}

	cleanup, err := setupObservabilitySystemWithConfig(threeEventsConfig)
	if err != nil {
		t.Fatalf("error setting up observability %v", err)
	}
	defer cleanup()

	// This is common setup ^^^ factor out into setup() with config as parameter?
	ss := &stubserver.StubServer{
		EmptyCallF: func(ctx context.Context, in *grpc_testing.Empty) (*grpc_testing.Empty, error) {
			return &grpc_testing.Empty{}, nil
		},
		UnaryCallF: func(ctx context.Context, in *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
			return &grpc_testing.SimpleResponse{}, nil
		},
		FullDuplexCallF: func(stream grpc_testing.TestService_FullDuplexCallServer) error {
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

	// A Unary RPC should match with first event and logs should correspond
	// accordingly. The first event it matches to should be used for the
	// configuration, even though it could potentially match to events in the
	// future.
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{}); err != nil {
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}
	print("len(fle.entries) after unary call: ", len(fle.entries), " ")

	// A streaming RPC should match with the second event, which has the exclude
	// flag set. Thus, a streaming RPC should cause no downstream logs.


	// []grpcLogEntries shared with first test, have it the same metadata and header bytes
	// to keep logic consistent and can test it doens't log like last one.



	// Streaming RPC should match with second event and cause no downstream logs
	// (ASSERT count is same as after Unary RPC?)
	// streaming
	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend()
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}
	// (ASSERT count is same as after Unary RPC?)

	// The exporter should have received no new log entries due to this call,
	// and should have the same number of entries as after the Unary Call.
	if len(fle.entries) != 5 {
		t.Fatalf("Unexpected length of entries %v, want 5", len(fle.entries))
	}

	print("len(fle.entries) after full duplex call: ", len(fle.entries), " ")

	// A third RPC, which doesn't match with first two and matches to last one,
	// due to being a wildcard for every method in the service, should log
	// accordingly to the last event's logic.
	if _, err := ss.Client.EmptyCall(ctx, &grpc_testing.Empty{}); err != nil {
		t.Fatalf("Unexpected error from EmptyCall: %v", err)
	} // will this be able to trigger the max, this is literally an empty call
	print("len(fle) after empty call: ", len(fle.entries), " ") // ugh also need to cmp.Diff fuck

	// declare a [] that tests the logic of new bytes (metadata length)

}

// oh also need to test bin headers fuck, unit test (see examples in codebase for this)
func (s) TestTranslateMetadata(t *testing.T) {
	// translateMetadata(m *binlogpb.Metadata) map[string]string
	concatBinLogValue := base64.StdEncoding.EncodeToString([]byte("value1")) + "," + base64.StdEncoding.EncodeToString([]byte("value2"))
	tests := []struct {
		name string
		binLogMD *binlogpb.Metadata
		wantMD map[string]string
	}{
		{
			name: "two-entries-different-key",
			binLogMD: &binlogpb.Metadata{
				Entry: []*binlogpb.MetadataEntry{
					{
						Key: "header1",
						Value: []byte("value1"),
					},
					{
						Key: "header2",
						Value: []byte("value2"),
					},
				},
			},
			wantMD: map[string]string{
				"header1": "value1",
				"header2": "value2",
			},
		},
		{
			name: "two-entries-same-key",
			binLogMD: &binlogpb.Metadata{
				Entry: []*binlogpb.MetadataEntry{
					{
						Key: "header1",
						Value: []byte("value1"),
					},
					{
						Key: "header1",
						Value: []byte("value2"),
					},
				},
			},
			wantMD: map[string]string{
				"header1": "value1,value2",
			},
		},
		{
			// two kvs with same key and key is a bin header -> k: "base64 encoded", "base64 encoded"
			name: "two-entries-same-key-bin-header",
			binLogMD: &binlogpb.Metadata{
				Entry: []*binlogpb.MetadataEntry{
					{
						Key: "header1-bin",
						Value: []byte("value1"),
					},
					{
						Key: "header1-bin",
						Value: []byte("value2"),
					},
				},
			},
			wantMD: map[string]string{
				"header1-bin": concatBinLogValue,
			},
		},
		// both of them combined - tests the interleaving of these two
		{
			name: "four-entries-two-keys",
			binLogMD: &binlogpb.Metadata{
				Entry: []*binlogpb.MetadataEntry{
					{
						Key: "header1",
						Value: []byte("value1"),
					},
					{
						Key: "header1",
						Value: []byte("value2"),
					},
					{
						Key: "header1-bin",
						Value: []byte("value1"),
					},
					{
						Key: "header1-bin",
						Value: []byte("value2"),
					},
				},
			},
			wantMD: map[string]string{
				"header1": "value1,value2",
				"header1-bin": concatBinLogValue,
			},
		},
	}

	// How to compare maps? Map ordering isn't guaranteed.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if gotMD := translateMetadata(test.binLogMD); !cmp.Equal(gotMD, test.wantMD) {
				t.Fatalf("translateMetadata(%v) = %v, want %v", test.binLogMD, gotMD, test.wantMD)
			}
		})
	}
}
