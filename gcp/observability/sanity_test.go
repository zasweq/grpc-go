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
	"encoding/json"
	"google.golang.org/grpc/internal/stubserver"
	"os"
	"testing"
)

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
