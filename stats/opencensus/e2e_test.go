/*
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
 */

package opencensus

import (
	"context"
	"fmt"
	"go.opencensus.io/trace"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"google.golang.org/grpc"
	"google.golang.org/grpc/internal/grpctest"
	"google.golang.org/grpc/internal/leakcheck"
	"google.golang.org/grpc/internal/stubserver"
	"google.golang.org/grpc/test/grpc_testing"
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
}

var defaultTestTimeout = 5 * time.Second

type fakeExporter struct {
	t *testing.T

	mu        sync.RWMutex
	seenViews map[string]*viewInformation
}

// viewInformation is information Exported from the view package through
// ExportView relevant to testing, i.e. a reasonably non flaky expectation of
// desired emissions to Exporter.
type viewInformation struct {
	aggType    view.AggType
	aggBuckets []float64
	desc       string
	tagKeys    []tag.Key
	rows       []*view.Row
}

func (fe *fakeExporter) ExportView(vd *view.Data) {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	fe.seenViews[vd.View.Name] = &viewInformation{
		aggType:    vd.View.Aggregation.Type,
		aggBuckets: vd.View.Aggregation.Buckets,
		desc:       vd.View.Description,
		tagKeys:    vd.View.TagKeys,
		rows:       vd.Rows,
	}
}

func (vi *viewInformation) equal(vi2 *viewInformation) bool {
	if vi == nil && vi2 == nil {
		return true
	}
	if (vi != nil) != (vi2 != nil) {
		return false
	}
	if vi.aggType != vi2.aggType {
		return false
	}
	if !cmp.Equal(vi.aggBuckets, vi2.aggBuckets) {
		return false
	}
	if vi.desc != vi2.desc {
		return false
	}
	if !cmp.Equal(vi.tagKeys, vi2.tagKeys, cmp.Comparer(func(a tag.Key, b tag.Key) bool {
		return a.Name() == b.Name()
	})) {
		return false
	}
	if !compareRows(vi.rows, vi2.rows) {
		return false
	}
	return true
}

// compareRows compares rows with respect to the information desired to test.
// Both the tags representing the rows and also the data of the row are tested
// for equality. Rows are in nondeterministic order when ExportView is called,
// but handled because ExportView is called every default metrics reporting
// interval, which will work eventually in a reasonable manner (i.e. doesn't
// flake).
func compareRows(rows []*view.Row, rows2 []*view.Row) bool {
	if rows == nil && rows2 == nil {
		return true
	}
	if (rows != nil) != (rows2 != nil) {
		return false
	}
	if len(rows) != len(rows2)  {
		return false
	}

	for i, row := range rows {
		if !cmp.Equal(row.Tags, rows2[i].Tags, cmp.Comparer(func(a tag.Key, b tag.Key) bool {
			return a.Name() == b.Name()
		})) {
			return false
		}
		if !compareData(row.Data, rows2[i].Data) {
			return false
		}
	}
	return true
}

// compareData returns whether the two aggregation data's are equal to each
// other with respect to parts of the data desired for correct emission. The
// function first makes sure the two types of aggregation data are the same, and
// then checks the equality for the respective aggregation data type.
func compareData(ad view.AggregationData, ad2 view.AggregationData) bool {
	if ad == nil && ad2 == nil {
		return true
	}
	if (ad != nil) != (ad2 != nil) {
		return false
	}
	switch ad1 := ad.(type) {
	case *view.DistributionData:
		dd2, ok := ad2.(*view.DistributionData)
		if !ok {
			return false
		}
		// Count and Count Per Buckets are reasonable for correctness,
		// especially since we verify equality of bucket endpoints elsewhere.
		if ad1.Count != dd2.Count {
			return false
		}
		for i, count := range ad1.CountPerBucket {
			if count != dd2.CountPerBucket[i] {
				return false
			}
		}
	case *view.CountData:
		cd2, ok := ad2.(*view.CountData)
		if !ok {
			return false
		}
		return ad1.Value == cd2.Value

	// gRPC open census plugin does not have these next two type sof aggregation
	// data types present, for now just check for type equality between the two
	// aggregation data points.
	case *view.SumData:
		_, ok := ad2.(*view.SumData)
		if !ok {
			return false
		}
	case *view.LastValueData:
		_, ok := ad2.(*view.LastValueData)
		if !ok {
			return false
		}
	}
	return true
}

func (vi *viewInformation) Equal(vi2 *viewInformation) bool {
	return vi.equal(vi2)
}

// seenViewPresent checks if the seen views contain the desired information for
// the view within the scope of a certain time. Returns an nil slice if correct
// information found, non nil slice of at least length 1 if correct information
// not found within a timeout.
func seenViewMatches(fe *fakeExporter, viewName string, wantVI *viewInformation) []error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	var errs []error
	for ctx.Err() == nil {
		errs = nil
		fe.mu.RLock()
		if vi := fe.seenViews[viewName]; vi != nil {
			if diff := cmp.Diff(vi, wantVI); diff != "" {
				errs = append(errs, fmt.Errorf("got unexpected viewInformation, diff (-got, +want): %v", diff))
			}
		} else {
			errs = append(errs, fmt.Errorf("couldn't find %v in the views exported, never collected", viewName))
		}
		fe.mu.RUnlock()
		if len(errs) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errs
}

func (fe *fakeExporter) ExportSpan(sd *trace.SpanData) {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	// persist span data in fakeExporter, did it with a map for o(1) accesses
	// this is deterministic though so you can persist
	// span received, span received, span received in the correct ordering

	// persist only what I want here

}

type spanInformation struct {
	// only the data needed to be compared here...
	name string // important to test this for sure...
	// spankind? what are the different types even?
	message string // somewhat related to status I think...
	attributes map[string]interface{} // hardcoded, but will add for A45
	code int32 // status code || error - codes.Internal
	/*
	type Link struct {
	    TraceID    TraceID
	    SpanID     SpanID
	    Type       LinkType
	    Attributes map[string]interface{}
	}
	*/
	// []links, links from one span to another, assuming left and right span within the trace?
	hasRemoteParent bool
	// annotations?
	// traceState - [] arbitrary key value pairs...
	/*
	type SpanContext struct {
	    TraceID      TraceID
	    SpanID       SpanID
	    TraceOptions TraceOptions
	    Tracestate   *tracestate.Tracestate
	}
	SpanContext contains the state that must propagate across process boundaries.
	SpanContext is not an implementation of context.Context. TODO: add reference to external Census docs for SpanContext
	*/
	spanContext // definelty populated server side to link it back to client, does client also have this...or does it just stick it as a metadata value in bin form?
}

func (si *spanInformation) equal(si2 *spanInformation) bool {
	if si == nil && si2 == nil {
		return true
	}
	if (si != nil) != (si2 != nil) {
		return false
	}
	// field1...
	if si.name != si2.name {
		return false
	}
	if si.message != si2.message {
		return false
	}
	// attributes map[string]interface{} how to compare?
	// cmp.Equal will call equal with a comparer might need this for some
	if si.code != si2.code {
		return false
	}
	// these are three lists...how to compare?
	// []links - expands
	// []message
	// []annotations
	// tracestate - expands

	// TraceID [16]byte
	// SpanID [8]byte
	// Trace Options: I think literally a bit that determines whether the trace will be sampled or not...
	// TraceState: same as top level data, Tracing system specific context in a
	// list of ey value pairs, allows use with legacy systems, I think we can
	// probably just ignore this in both this blob and top level blob...

	// span context - expands into multiple things of data...trace id, span id,
	// trace options, and trace state...

	return true
}

func (si *spanInformation) Equal(si2 *spanInformation) bool {
	return si.equal(si2)
}

// seenSpanMatches checks if the exported received spans contain the desired
// information for the span within the scope of a certain time. Returns an nil
// slice if correct information found, non nil slice of at least length 1 if
// correct information not found within a timeout.
func seenSpanMatches(fe *fakeExporter, wantSI *spanInformation) []error {
	// [] spans a list of spans...
	// cmpdiff want and got calls Equal...
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	var errs []error // can make this one error if only one failure point the cmp.Diff...
	for ctx.Err() == nil {
		errs = nil
		fe.mu.RLock()
		// read siGot here from persisting in fakeExporter
		if diff := cmp.Diff(wantSI/*<- switch to siGot*/, wantSI); diff != "" {
			errs = append(errs, fmt.Errorf("got unexpected spanInformation, diff (-got, +want): %v", diff))
		}
		fe.mu.RUnlock()
		if len(errs) == 0 { // || err == nil
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errs
}

// TestSpan tests emitted spans from gRPC. It configures a system with a gRPC
// Client and gRPC server with the OpenCensus Dial and Server Option configured,
// and makes a Unary RPC and a Streaming RPC. This should cause certain spans to
// be emitted from client and server side.
func (s) TestSpan(t *testing.T) {

	// do it separate so that it's easy to partition trace.register
	// also can test the knob which turns on only traces vs. metrics

	// problem: also needs to plumb start options into the option, as this
	// controls created span type and also the trace sampling rate

	// var so trace.StartOptions
	// so.Sampler
	so := trace.StartOptions{
		Sampler: trace.ProbabilitySampler(1),
	}
	
	
	// *** this codeblock can be made a helper

	fe := &fakeExporter{
		t:         t,
		seenViews: make(map[string]*viewInformation),
		// add seenSpans creation as well (you can append to nil slice though)
	}
	view.RegisterExporter(fe)
	defer view.UnregisterExporter(fe)

	trace.RegisterExporter(fe)
	defer trace.UnregisterExporter(fe)

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
	if err := ss.Start([]grpc.ServerOption{ServerOption()}, DialOption()); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	defer ss.Stop()
	// ***

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// Make two RPC's, a unary RPC and a streaming RPC. These should cause
	// certain spans to be emitted.
	if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{Payload: &grpc_testing.Payload{}}); err != nil {
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}

	// 1. unary call:

	// client side span representing attempt I'm assuming
	// no parent, one attempt for overall csAttempt level...at this granularity

	// unary

	// I think emits one span for client and one for server...

	// need to: persist probably a list of spans and just declare a [] with all
	// the expected spans in it...



	// span emitted client
	wantSI := &spanInformation{
		// what stuff do we want to verify? print this lol
		name: /*this is hardcoded Sent...? or method or something*/,
		message: /**/,
		hasRemoteParent: false,
		attributes: ,
		code: /*codes.OK since rpc went as usual*/,
	}



	// span emitted from server - eventually put into []
	wantSI := &spanInformation{
		name: ,
		message: ,
		hasRemoteParent: true, // points to above, where else is this information passed?
		// message send and recv events, zero? streaming will def differ wrt this
		attributes: ,
		code: ,
		// server side is DERIVED from span context, does that information make it here
	}


	// so (for trace sampling rate) and bool that selectively turns on one or the other (I think we still need to disable traces)
	// in the OpenCensus API
	// I own the rewrite so I can change this up though




	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend()
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}

	// I think two separate verifications makes sense

	// server side span pointing to client side span as a remote parent
	// how is remote represented in spanInformation...? HasRemoteParent bool...anything else

	// streaming
	wantSI = &spanInformation{
		name: /**/,
		message: ,
		hasRemoteParent: true,
		// there's got to be a name attached to this remote parent ^^^...
	}

	// literally just stdout print
	// the events that happened that will be much easier

}

// declare span inline in test and pass to this function fuck need to add a
// selective toggle for one or the other...

// TestAllMetrics tests emitted metrics from gRPC. It configures a system with a
// gRPC Client and gRPC server with the OpenCensus Dial and Server Option
// configured, and makes a Unary RPC and a Streaming RPC. This should cause a
// certain emission of the registered gRPC metric through the OpenCensus View
// package.
func (s) TestAllMetrics(t *testing.T) {
	cmtk := tag.MustNewKey("grpc_client_method")
	smtk := tag.MustNewKey("grpc_server_method")
	cstk := tag.MustNewKey("grpc_client_status")
	sstk := tag.MustNewKey("grpc_server_status")
	tests := []struct {
		name   string
		metric *view.View
		wantVI *viewInformation
	}{
		{
			name:   "client-started-rpcs",
			metric: ClientStartedRPCsView,
			wantVI: &viewInformation{
				aggType:    view.AggTypeCount,
				aggBuckets: []float64{},
				desc:       "Number of started client RPCs.",
				tagKeys: []tag.Key{
					cmtk,
				},

				rows: []*view.Row{
					{
						Tags: []tag.Tag{
							{
								Key:   cmtk,
								Value: "grpc.testing.TestService/UnaryCall",
							},
						},
						Data: &view.CountData{
							Value: 1,
						},
					},
					{
						Tags: []tag.Tag{
							{
								Key:   cmtk,
								Value: "grpc.testing.TestService/FullDuplexCall",
							},
						},
						Data: &view.CountData{
							Value: 1,
						},
					},
				},
			},
		},
		{
			name:   "server-started-rpcs",
			metric: ServerStartedRPCsView,
			wantVI: &viewInformation{
				aggType:    view.AggTypeCount,
				aggBuckets: []float64{},
				desc:       "Number of started server RPCs.",
				tagKeys: []tag.Key{
					smtk,
				},
				rows: []*view.Row{
					{
						Tags: []tag.Tag{
							{
								Key:   smtk,
								Value: "grpc.testing.TestService/UnaryCall",
							},
						},
						Data: &view.CountData{
							Value: 1,
						},
					},
					{
						Tags: []tag.Tag{
							{
								Key:   smtk,
								Value: "grpc.testing.TestService/FullDuplexCall",
							},
						},
						Data: &view.CountData{
							Value: 1,
						},
					},
				},
			},
		},
		{
			name:   "client-completed-rpcs",
			metric: ClientCompletedRPCsView,
			wantVI: &viewInformation{
				aggType:    view.AggTypeCount,
				aggBuckets: []float64{},
				desc:       "Number of completed RPCs by method and status.",
				tagKeys: []tag.Key{
					cmtk,
					cstk,
				},
				rows: []*view.Row{
					{
						Tags: []tag.Tag{
							{
								Key:   cmtk,
								Value: "grpc.testing.TestService/UnaryCall",
							},
							{
								Key:   cstk,
								Value: "OK",
							},
						},
						Data: &view.CountData{
							Value: 1,
						},
					},
					{
						Tags: []tag.Tag{
							{
								Key:   cmtk,
								Value: "grpc.testing.TestService/FullDuplexCall",
							},
							{
								Key:   cstk,
								Value: "OK",
							},
						},
						Data: &view.CountData{
							Value: 1,
						},
					},
				},
			},
		},
		{
			name:   "server-completed-rpcs",
			metric: ServerCompletedRPCsView,
			wantVI: &viewInformation{
				aggType:    view.AggTypeCount,
				aggBuckets: []float64{},
				desc:       "Number of completed RPCs by method and status.",
				tagKeys: []tag.Key{
					smtk,
					sstk,
				},
				rows: []*view.Row{
					{
						Tags: []tag.Tag{
							{
								Key:   smtk,
								Value: "grpc.testing.TestService/UnaryCall",
							},
							{
								Key:   sstk,
								Value: "OK",
							},
						},
						Data: &view.CountData{
							Value: 1,
						},
					},
					{
						Tags: []tag.Tag{
							{
								Key:   smtk,
								Value: "grpc.testing.TestService/FullDuplexCall",
							},
							{
								Key:   sstk,
								Value: "OK",
							},
						},
						Data: &view.CountData{
							Value: 1,
						},
					},
				},
			},
		},
		{
			name:   "client-sent-bytes-per-rpc",
			metric: ClientSentBytesPerRPCView,
			wantVI: &viewInformation{
				aggType:    view.AggTypeDistribution,
				aggBuckets: defaultBytesDistributionBounds,
				desc:       "Distribution of sent bytes per RPC, by method.",
				tagKeys: []tag.Key{
					cmtk,
				},
				rows: []*view.Row{
					{
						Tags: []tag.Tag{
							{
								Key:   cmtk,
								Value: "grpc.testing.TestService/UnaryCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
					{
						Tags: []tag.Tag{
							{
								Key:   cmtk,
								Value: "grpc.testing.TestService/FullDuplexCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
				},
			},
		},
		{
			name:   "server-sent-bytes-per-rpc",
			metric: ServerSentBytesPerRPCView,
			wantVI: &viewInformation{
				aggType:    view.AggTypeDistribution,
				aggBuckets: defaultBytesDistributionBounds,
				desc:       "Distribution of sent bytes per RPC, by method.",
				tagKeys: []tag.Key{
					smtk,
				},
				rows: []*view.Row{
					{
						Tags: []tag.Tag{
							{
								Key:   smtk,
								Value: "grpc.testing.TestService/UnaryCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
					{
						Tags: []tag.Tag{
							{
								Key:   smtk,
								Value: "grpc.testing.TestService/FullDuplexCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
				},
			},
		},

		{
			name:   "client-received-bytes-per-rpc",
			metric: ClientReceivedBytesPerRPCView,
			wantVI: &viewInformation{
				aggType:    view.AggTypeDistribution,
				aggBuckets: defaultBytesDistributionBounds,
				desc:       "Distribution of received bytes per RPC, by method.",
				tagKeys: []tag.Key{
					cmtk,
				},
				rows: []*view.Row{
					{
						Tags: []tag.Tag{
							{
								Key:   cmtk,
								Value: "grpc.testing.TestService/UnaryCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
					{
						Tags: []tag.Tag{
							{
								Key:   cmtk,
								Value: "grpc.testing.TestService/FullDuplexCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
				},
			},
		},
		{
			name:   "server-received-bytes-per-rpc",
			metric: ServerReceivedBytesPerRPCView,
			wantVI: &viewInformation{
				aggType:    view.AggTypeDistribution,
				aggBuckets: defaultBytesDistributionBounds,
				desc:       "Distribution of received bytes per RPC, by method.",
				tagKeys: []tag.Key{
					smtk,
				},
				rows: []*view.Row{
					{
						Tags: []tag.Tag{
							{
								Key:   smtk,
								Value: "grpc.testing.TestService/UnaryCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
					{
						Tags: []tag.Tag{
							{
								Key:   smtk,
								Value: "grpc.testing.TestService/FullDuplexCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
				},
			},
		},
		{
			name:   "client-sent-messages-per-rpc",
			metric: ClientSentMessagesPerRPCView,
			wantVI: &viewInformation{
				aggType:    view.AggTypeDistribution,
				aggBuckets: defaultMessageCountDistributionBounds,
				desc:       "Distribution of sent messages per RPC, by method.",
				tagKeys: []tag.Key{
					cmtk,
				},
				rows: []*view.Row{
					{
						Tags: []tag.Tag{
							{
								Key:   cmtk,
								Value: "grpc.testing.TestService/UnaryCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
					{
						Tags: []tag.Tag{
							{
								Key:   cmtk,
								Value: "grpc.testing.TestService/FullDuplexCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
				},
			},
		},
		{
			name:   "server-sent-messages-per-rpc",
			metric: ServerSentMessagesPerRPCView,
			wantVI: &viewInformation{
				aggType:    view.AggTypeDistribution,
				aggBuckets: defaultMessageCountDistributionBounds,
				desc:       "Distribution of sent messages per RPC, by method.",
				tagKeys: []tag.Key{
					smtk,
				},
				rows: []*view.Row{
					{
						Tags: []tag.Tag{
							{
								Key:   smtk,
								Value: "grpc.testing.TestService/UnaryCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
					{
						Tags: []tag.Tag{
							{
								Key:   smtk,
								Value: "grpc.testing.TestService/FullDuplexCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
				},
			},
		},

		{
			name:   "client-received-messages-per-rpc",
			metric: ClientReceivedMessagesPerRPCView,
			wantVI: &viewInformation{
				aggType:    view.AggTypeDistribution,
				aggBuckets: defaultMessageCountDistributionBounds,
				desc:       "Distribution of received messages per RPC, by method.",
				tagKeys: []tag.Key{
					cmtk,
				},
				rows: []*view.Row{
					{
						Tags: []tag.Tag{
							{
								Key:   cmtk,
								Value: "grpc.testing.TestService/UnaryCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
					{
						Tags: []tag.Tag{
							{
								Key:   cmtk,
								Value: "grpc.testing.TestService/FullDuplexCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
				},
			},
		},
		{
			name:   "server-received-messages-per-rpc",
			metric: ServerReceivedMessagesPerRPCView,
			wantVI: &viewInformation{
				aggType:    view.AggTypeDistribution,
				aggBuckets: defaultMessageCountDistributionBounds,
				desc:       "Distribution of received messages per RPC, by method.",
				tagKeys: []tag.Key{
					smtk,
				},
				rows: []*view.Row{
					{
						Tags: []tag.Tag{
							{
								Key:   smtk,
								Value: "grpc.testing.TestService/UnaryCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
					{
						Tags: []tag.Tag{
							{
								Key:   smtk,
								Value: "grpc.testing.TestService/FullDuplexCall",
							},
						},
						Data: &view.DistributionData{
							Count:          1,
							CountPerBucket: []int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
						},
					},
				},
			},
		},

		// TODO: 3 missing latency metrics, figure out a way to test even though
		// non deterministic, perhaps ignore exact distribution and test
		// everything else?

	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view.Register(test.metric)
			// To cleanup global state between testing iterations, to prevent
			// Measures persisting over multiple testing iterations.
			defer view.Unregister(test.metric)
			mri := time.Millisecond * 100
			view.SetReportingPeriod(mri)
			fe := &fakeExporter{
				t:         t,
				seenViews: make(map[string]*viewInformation),
			}
			view.RegisterExporter(fe)
			defer view.UnregisterExporter(fe)

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
			if err := ss.Start([]grpc.ServerOption{ServerOption()}, DialOption()); err != nil {
				t.Fatalf("Error starting endpoint server: %v", err)
			}
			defer ss.Stop()
			ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
			defer cancel()
			// Make two RPC's, a unary RPC and a streaming RPC. These should cause
			// certain metrics to be emitted.
			if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{Payload: &grpc_testing.Payload{}}); err != nil {
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
			if errs := seenViewMatches(fe, test.metric.Name, test.wantVI); len(errs) != 0 {
				t.Fatalf("Invalid OpenCensus export data: %v", errs)
			}
		})
	}
}
