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

package test

import (
	"context"
	"log"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	estats "google.golang.org/grpc/experimental/stats"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
	"google.golang.org/grpc/serviceconfig"
	"google.golang.org/grpc/internal/grpctest"
)

var defaultTestTimeout = 5 * time.Second

type s struct {
	grpctest.Tester
}

func Test(t *testing.T) {
	grpctest.RunSubTests(t, s{})
}

// Write comment explaining what the mock stats handler actually tests...

// balancer deployed as top level that registers instruments/emits telemetry on
// operations using those package instruments...

// TestMetricsRecorderList tests the metrics recorder list functionality of the
// ClientConn. It configures a global and local stats handler Dial Option. These
// stats handlers implement the MetricsRecorder interface. It also configures a
// balancer which registers metrics and records on metrics at build time. This
// test then asserts that the recorded metrics show up on both configured stats
// handlers, and that metrics calls with the incorrect number of labels does not
// make it's way to stats handlers.
func (s) TestMetricsRecorderList(t *testing.T) {
	mr := manual.NewBuilderWithScheme("test-metrics-recorder-list")
	defer mr.Close()

	// read from 5 channels of the two fake metrics recorder channels?

	json := `{"loadBalancingConfig": [{"recording_load_balancer":{}}]}`
	sc := internal.ParseServiceConfig.(func(string) *serviceconfig.ParseResult)(json)
	mr.InitialState(resolver.State{
		ServiceConfig: sc,
	})

	// Create two stats.Handlers which also implement MetricsRecorder, configure
	// one as a global dial option and one as a local dial option.
	mr1 := NewTestMetricsRecorder(t) // What does t assert?
	mr2 := NewTestMetricsRecorder(t)

	// set global dial options to what they were previously? local + global snapshot and send it around
	// Expect both to get same emissions?

	// Seems like clear is fine here compared with setting to old state...Doug
	// had logic for back and forth but I think this is fine...

	defer internal.ClearGlobalDialOptions() // so this just clears it which I guess is fine
	internal.AddGlobalDialOptions.(func(opt ...grpc.DialOption))(grpc.WithStatsHandler(mr1))

	cc, err := grpc.NewClient(mr.Scheme()+":///", grpc.WithResolvers(mr), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithStatsHandler(mr2))
	if err != nil {
		log.Fatalf("Failed to dial: %v", err)
	}
	defer cc.Close()
	// Do I need to make an RPC for this to pull the lb I want...and have a spun up server with a certain address?
	// register the custom lb by importing

	// There's no peer to connect too...
	// Also what actually triggers the balancer to build...
	// and the resolver to resolve? First RPC?

	/*
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		ec := pb.NewEchoClient(cc)
		if err := waitForDistribution(ctx, ec); err != nil {
			log.Fatalf(err.Error())
		}
		fmt.Println("Successful multiple iterations of 1:2 ratio")
	*/



	// read off mr1 and mr2's channels?

	// Whatever operation triggered build above, build emits metrics which can
	// be asserted on here...

	// read off channel....extra layer can do tests like labels etc...

	// and different assertions for the three types of metrics...

	// read from one would block so need to do it in another goroutine

	// or could do it essentially like a buffered sink...

	// to essentially see if an event happened,

	// fork a goroutine from the test...?

	// mr1 // could have a wait for assertion on this

	// either have a helper on mr1 that waits for a certain thing orrrr
	// just do it here

	// both metrics recorders same assertions...
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()


	mdWant := MetricsData{
		Handle: (*estats.MetricDescriptor)(intCountHandle), // The handle comes in the lb...just ignore this field?

		LabelKeys: []string{"int counter label", "int counter optional label"}, // coupled with balancer registration...
		LabelVals: []string{"int counter label val", "int counter optional label val"}, // emitted from balancer...same package so access to handle...make consts?
	}
	// Maybe just get this first assertion working/compiled etc than move onto next...
	mr1.WaitForInt64Count(ctx, mdWant) // metrics data want...does it really need to wait for it it's sync...
	mr2.WaitForInt64Count(ctx, mdWant) // same with this one...

}

// Need to upload that 1.65 docker image...

