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
	"go.opencensus.io/plugin/ocgrpc"
	"go.opencensus.io/trace"
	"google.golang.org/grpc/codes"
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
	seenSpans []spanInformation // pointer? or inline
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
	print("Export View called")
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
	if len(rows) != len(rows2) {
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

	// gRPC open census plugin does not have these next two types of aggregation
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

// spanMatches checks if the seen spans of the exporter contain the desired
// spans within the scope of a certain time. Returns a nil error if correct span
// information found, non nil error if correct information not found within a
// timeout.
func spanMatches(fe *fakeExporter, wantSpans []spanInformation /*<- pointer?*/) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	// polling loops wraps arbitrary event from a separate thread...
	// only need one error perhaps?
	var err error
	for ctx.Err() == nil { // polling loop
		err = nil
		fe.mu.RLock()
		if diff := cmp.Diff(fe.seenSpans, wantSpans); diff != "" {
			err = fmt.Errorf("got unexpected spans, diff (-got, +want): %v", diff)
		}
		fe.mu.RUnlock()
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
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
			if diff := cmp.Diff(vi, wantVI); diff != "" { // iteartes over [] for you
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
				desc:       "Number of opened client RPCs, by method.",
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
				desc:       "Number of opened server RPCs, by method.",
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
				aggBuckets: bytesDistributionBounds,
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
				aggBuckets: bytesDistributionBounds,
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
				aggBuckets: bytesDistributionBounds,
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
				aggBuckets: bytesDistributionBounds,
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
				aggBuckets: countDistributionBounds,
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
				aggBuckets: countDistributionBounds,
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
				aggBuckets: countDistributionBounds,
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
				aggBuckets: countDistributionBounds,
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
			// measures persisting over multiple testing iterations.
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
			if err := ss.Start([]grpc.ServerOption{ServerOption(TraceOptions{})}, DialOption(TraceOptions{})); err != nil {
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

// compareSpanContext compares...and why only that...
func compareSpanContext(sc trace.SpanContext, sc2 trace.SpanContext) bool {
	/*if sc == nil && sc2 == nil {
		return true
	}
	if (sc != nil) != (sc2 != nil) {
		return false
	}*/
	// if sc.TraceID // isn't this auto generated, so need to persist from client call into server call
	// default value of slice is nil, is this just a numbered slice?
	if sc.TraceID != sc2.TraceID { // [16]byte, can you just do this, can be nil will this compare right I guess you can just run it and see...
		return false
	}
	if sc.SpanID != sc2.SpanID {
		return false
	}
	sc.TraceOptions.IsSampled() // this can nil pointer
	// compare trace options here (figure out) - a uint32 how does the trace package check equality but I guess you want equality tailor made?

	return true // or just return the bool ^^^
}

func compareMessageEvents(me []trace.MessageEvent, me2 []trace.MessageEvent) bool {
	if me == nil && me2 == nil {
		return true
	}
	if (me != nil) != (me2 != nil) {
		return false
	}
	if len(me) != len(me2) {
		return false
	}
	// this should be deterministic vvv
	// [sent, recv, sent, recv]
	for i, e := range me {
		// e vs. me2[i]
		e2 := me2[i]
		if e.EventType != e2.EventType {
			return false
		}
		if e.MessageID != e2.MessageID { // the data that I am persisting...
			return false
		}
		if e.UncompressedByteSize != e2.UncompressedByteSize {
			return false
		}
		if e.CompressedByteSize != e2.CompressedByteSize {
			return false
		}
	}
	return true
}

func compareLinks(ls []trace.Link, ls2 []trace.Link) bool {
	if ls == nil && ls2 == nil {
		return true
	}
	if (ls != nil) != (ls2 != nil) {
		return false
	}
	if len(ls) != len(ls2) {
		return false
	}
	for i, l := range ls {
		l2 := ls2[i]
		// l.SpanID // [8]byte this is compared a lot, if needed declare another helper function to compare these
		// fuck will need to declare a want inline...
		if l.TraceID != l2.TraceID {
			return false
		}
		if l.SpanID != l2.SpanID {
			return false
		}
		if l.Type != l2.Type {
			return false
		}
		// ignore attributes we don't set this anyway...
	}
	return true
}

type spanInformation struct {
	// SpanContext seems dummy important esp since you need to propagate this over the wire from client side and build it server side, persisted and used to build out parent information
	sc trace.SpanContext
	parentSpanID trace.SpanID
	// maybe see what happens without message id, perhaps I should go ahead and add it
	spanKind int // client and server important to test this
	name string
	message string
	attributes map[string]interface{}
	messageEvents []trace.MessageEvent
	status trace.Status
	links []trace.Link
	hasRemoteParent bool
	childSpanCount int
} // separate data structure to persist only what I need and write a comparison function on that persisted data...

func (si spanInformation) equal(si2 spanInformation) bool {
	// go down this list
	/*if si == nil && si2 == nil {
		return true
	}
	if (si != nil) != (si2 != nil) {
		return false
	}*/
	// switch this comparison later since it comes as a struct in ExportSpan
	/*if !compareSpanContext(si.sc, si2.sc) { // ugh so no pointers, just get rid of nil checks in compare functions?
		return false
	}
	// assert si1.field1 == si2.field2
	if si.parentSpanID != si2.parentSpanID { // same question, can I just send this slice through this equality operator?
		return false
	}*/

	if si.spanKind != si2.spanKind {
		return false
	}
	if si.name != si2.name {
		return false
	}
	if si.message != si2.message {
		return false
	}

	// attribute comparison here - add or just ignore?
	// map[string]interface{} vs. map[string]interface{}

	if !compareMessageEvents(si.messageEvents, si2.messageEvents) {
		return false
	}
	// ...
	// if cmp.Equal // dfs the tree of data? switch to that if the operation below doesn't actually compare the fields...
	// code int32 and message string I need to add the checks for both
	if si.status != si2.status { // what does this actually check on this struct
		return false
	}
	// compare the []links here
	/*if !compareLinks(si.links, si2.links) { // probably fails here
		return false
	}*/
	if si.hasRemoteParent != si2.hasRemoteParent {
		return false
	}
	if si.childSpanCount != si2.childSpanCount {
		return false
	}
	return true // combine with statement above?
}

func (si spanInformation) Equal(si2 spanInformation) bool { // how do I get this to hit if comparing structs
	// how to ignore the timestamp, does this already happen?
	return si.equal(si2)
}

/*
what portion of this data passed into exporter is important for correctness?
type SpanData struct {
    // important
	SpanContext  // is this embedded or anonymous (variable name is type name)? Seems to be former...
    ParentSpanID  SpanID // important - we need to call into right function, opencensus handles it, but it is relatively stable with guarantees so not brittle
    SpanKind      int // important
    Name          string // important
    StartTime     time.Time // not important
    EndTime       time.Time // not important
    Attributes    map[string]interface{} // not important (for now), we don't really need to set these
    Annotations   []Annotation // I don't think this is important either (for traces)
    MessageEvents []MessageEvent // important - we set
    Status // important - we set
    Links                    []Link // important - we set on server side
    HasRemoteParent          bool // important...
    DroppedAttributeCount    int
    DroppedAnnotationCount   int
    DroppedMessageEventCount int
    DroppedLinkCount         int
    ChildSpanCount           int
}
*/

func (fe *fakeExporter) ExportSpan(sd *trace.SpanData) { // no pointers...
	// where is the actual span id and trace id populated?
	// It either a: Doesn't pass it (parent is Span Context)
	// or b. passes it's data WITHIN the span context provided. (and parent info is simply the parent span ID)
	/*sdd := trace.SpanData{
		SpanContext: trace.SpanContext{ // so just parent
			/*
			When creating structs with literals, we have to initialize the
			embedding explicitly; here the embedded type serves as the field
			name.

			//wtf
		},
	}

	// so these pieces of data are equivalent

	// can access embedded fields directly
	sd.TraceID // of Span Context Type
	// Alternatively, we can spell out the full path using the embedded type name.
	sd.SpanContext.TraceID // so can read it as a field AND it's embedded?

	sd.Message // can access it as a field since it embeds it...
	// Alternatively, we can spell out the full path using the embedded type name.
	sd.Status.Message // or access it through the field
	*/
	// only compare the subset of this API data that is important for correctness
	// well defined behavior, thus expected to be stable.
	fe.mu.Lock()
	defer fe.mu.Unlock()
	print("Export Span called")
	// of SpanContext type
	// sd.SpanID // make sure this span id shows up in other span as parent
	// sd.TraceID  // make sure this trace shows up in other span as the span id

	// this data gets here, and you need to read the data later...for the other span
	// persists

	// the line comes from whether gRPC adds the data (and needs to) - "data we
	// need to produce"

	// vs. whether opencensus adds it (except perhaps trace ID and span ID is
	// persisted over time) "And make sure you aren't testing the behavior of
	// the OC library (unless it's well documented and supported)"

	// also the things important for correctness

	// I think we have the right subset of data for correctness
	gotSI := spanInformation{ // will this be slow, but it's a test anyway...
		// spanContext?
		sc: sd.SpanContext,
		// parent span id - remote or local, so I think the server should point to client span regardless (in this case remote, when I add local parent client side for top level trace mess around with it then)
		parentSpanID: sd.SpanID, // is this correct
		spanKind: sd.SpanKind,
		name: sd.Name,
		message: sd.Message, // I don't see this being populated
		// annotations - I think correct to ignore
		attributes: sd.Attributes, // could ignore
		// do I need separate data/equal on these events/data in events...
		// above ^^^:  []trace.MessageEvent -> wantMessageEvent? if we want a subset or if want to actually compare?
		messageEvents: sd.MessageEvents,
		status: sd.Status,
		links: sd.Links,
		hasRemoteParent: sd.HasRemoteParent,
		childSpanCount: sd.ChildSpanCount,
	}
	fe.seenSpans = append(fe.seenSpans, gotSI)
}

// TestSpan tests emitted spans from gRPC. It configures a system with a gRPC
// Client and gRPC server with the OpenCensus Dial and Server Option configured,
// and makes a Unary RPC and a Streaming RPC. This should cause spans with
// certain information to be emitted from client and server side for each RPC.
func (s) TestSpan(t *testing.T) {
	so := TraceOptions{ // this stuff is the partition Doug was talking about
		TS: trace.ProbabilitySampler(1), // this name is ugly
		DisableTrace: false,
	}

	// this codeblock is shared, can be made a helper although traces and metrics are orthogonal so perhaps keep separate

	fe := &fakeExporter{
		t: t,
		seenViews: make(map[string]*viewInformation),
		// seenSpans...slice with ordering matters I think so should be client and then server span if shared created data structures on heap map to both
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
	if err := ss.Start([]grpc.ServerOption{ServerOption(so)}, DialOption(so)); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	defer ss.Stop()
	// finish codeblock

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	// Make a Unary RPC. This should cause a span with no? message events to be
	// emitted both from the client and the server.
	if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{Payload: &grpc_testing.Payload{}}); err != nil {
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}

	// server span sends first because it finishes first
	// add comment about ordering
	// 2. recv 3. send msg (end)

	// 1. send 4. recv msg (end) on client side

	// what part (subset) of message events are important for correctness?




	// wantSI := spanInformation...
	// actually I want [](span from client side, span from server side), message events I think are deterministic...
	wantSI := []spanInformation{ // pointer? I have a knob whether I do & or inline struct in ExportSpan()...
		{
			// only has populated span context if parent, no parent in this case...

			// FIX LEADING SLASH, made a decision

			// does the generated client span have a span context? Does it populate it with it's own...
			sc: trace.SpanContext{
				// zero values
			}, // is this even set, if so zero values?
			// I think we want this to just be zero value
			parentSpanID: trace.SpanID{}, // what is none, just an empty array...with zero values?
			spanKind: trace.SpanKindClient,
			// /s/m -> s.m I think
			// streaming call for next expectation below, same Unary call for the other Span in this struct...
			name: "grpc.testing.TestService.UnaryCall", // isn't this related to the bug I brought up in o11y chat...seems like it's working so we just keep backwards compatibility
			message: "", // yeah what is this used for...I don't see this being populated so just set it to zero value and see what diff says
			attributes: map[string]interface{}{
				"Client": true,
				"FailFast": false,
			}, // client + fail fast, this is just something I just threw in there so perhaps could use later...
			messageEvents: []trace.MessageEvent{}, // zero value or nil? I don't think any message events in this scenario? wow so actually has message events
			// Finishes RPC before getting here, so this field should be correctly written to...
			status: trace.Status{
				Code: int32(codes.OK), // maps 1:1 so ok here...
				Message: "", // only populated on error, so just zero value here
			},
			links: []trace.Link{}, // no links right, since from parent
			hasRemoteParent: false,
			// vvv how does this get populated anyway...
			// will this correctly have child span count set? or is generated too early? does it get appended to after the span below is emitted?
			childSpanCount: 1, // can this be set after the fact?
		},
		{
			// span context from client side is used server side...
			// so once client generated trace/spanID, need a way to persist and store here...
			sc: trace.SpanContext{
				// same stuff, data from client pulled off wire which populates fields below...
				// [16]byte this is an array just pull this var from somewhere...
				TraceID: trace.TraceID{}/*Client trace ID here - isn't it same one as here*/,
				SpanID: trace.SpanID{}/*Client Span ID here*/,
				// trace state is sampled bool yes or no
				// ignore trace state
			}, // persist from client generation somehow
			parentSpanID: trace.SpanID{}, // persist from client generation somehow
			spanKind: trace.SpanKindServer,
			name: "grpc.testing.TestService.UnaryCall"/*?*/, // sent recv spans come from span kind by backend, so orthogonal to this
			message: "", // empty because succesfully completes sets status on both client and server...
			attributes: map[string]interface{}{
				"Client": false,
				"FailFast": false,
			}, // on a stats.Begin populates with client and fail fast which I think both are false - scaling these attributes up for A45 anyway
			messageEvents: []trace.MessageEvent{}, // any message events? slice with len 0 or nil these events have a ton of data lol...
			status: trace.Status{ // same status as on client, stats.End calls both times...which populates these fields
				Code: int32(codes.OK),
				Message: "",
			},
			links: []trace.Link{
				{
					TraceID: trace.TraceID{}/*client trace id here*/,
					SpanID: trace.SpanID{}/*client span id here*/,
					Type: trace.LinkTypeChild,
					// ignore attributes
				}, // // link to parent with Span ID and Trace ID, again pull off Span ID and Trace ID, perhaps hook into code somehow...
			}, // print out all the data
			hasRemoteParent: true,
			childSpanCount: 0,
		},
	}

	wantSI = []spanInformation{}
	wantSI = []spanInformation{
		{
			// this span ID points to that one ...again how the fuck do you persist
			// spanid/traceid stuff...

			// IF YOU ACTUALLY READ THIS OFF THE PERSISTED INFORMATION, YOU'RE NOT TESTING THE GENERATION,
			// YOU'REJUST TESTING THE RELATIONSHIP BETWEEN X AND Y where y is the correctly read thing

			spanKind: trace.SpanKindServer,
			name: "grpc.testing.TestService.UnaryCall",
			// java simply doesn't have these, make a note and just don't test
			attributes:   map[string]interface{}{"Client": false, "FailFast": false},
			messageEvents: []trace.MessageEvent{
				{
					// recv mesg
					// IGNORE TIME FOR SURE, COULD VERIFY 2 3 4 1 or just ignore these trace/span IDs all together

					// add messageID when that happens...should correspond to the client side sends/recvs :)
					EventType: trace.MessageEventTypeRecv,
					UncompressedByteSize: 2, // I don't know why...but perhaps leave out but I guess this is important for correctness...
					CompressedByteSize: 7,
				},
				{
					EventType: trace.MessageEventTypeSent,
					CompressedByteSize: 5, // what? no uncompressed yeah even on client side in all 3
				},
			},
			// Don't test...it's not needed for correctness anyway (there's no spec)
			// and our span is off anyway
			links: []trace.Link{
				{
					// traceID
					// spanID
					Type: trace.LinkTypeChild,
				},
			}, // with an ID which points to the one below...only thing you can really verify
			// only thing I can see that actually links is link
			hasRemoteParent: true, // could just use this...I'm just gonna use this and let e2e tests handle there wasn't a test anyway
		},
		{
			// created span and trace id in context
			// parent span id points to that ^^^...?

			spanKind: trace.SpanKindClient,
			name:         "grpc.testing.TestService.UnaryCall",
			attributes:   map[string]interface{}{"Client": true, "FailFast": true},
			messageEvents: []trace.MessageEvent{
				{
					EventType: trace.MessageEventTypeSent,
					UncompressedByteSize: 2,
					CompressedByteSize: 7,
				},
				{
					EventType: trace.MessageEventTypeRecv,
					CompressedByteSize: 5, // I wonder why
				},
			},
			hasRemoteParent: false, // implicit, but better to be implicit
		}, // how to tell what portion failed...
	}
	// the got seems all kinds of messed up,
	// maybe plumb in the normal opencensus instrumentation and see what gets sent there...

	// stanley I coded up a fix, can you please run all your tests on it so I can see if it fixes and contineus to work with other test cases...

	// put this helper in a separate function...

	// does cmp.Diff iterate over the [], or {fieldA, fieldB} yes it iterates over array, and it iterates over struct fields too
	// so call cmp.Diff...will iterate over array and at each node (in this case trace information):
	// use the result of x.Equal(y) even if x or y is nil...
	// if diff := cmp.Diff(fe.seenSpans, wantSI); diff != "" {

	// } // if it appends you need to clear later yeah clear it need the seenViewMatches thing too, can I generalize that helper for both trace information and view information? I don't think so...
	// take read lock in helper (happens in a separate thread :P)

	/*
	        -       {
	        -               sc: trace.SpanContext{
	        -                       TraceID:      s"39b91a70aaed10e4f584faf0b9e5aa8d",
	        -                       SpanID:       s"1d1c50b5e7951c80",
	        -                       TraceOptions: 1,
	        -               },
								// correct span id...reads it off the span context
								// points to itself as parent?
	        -               parentSpanID: s"1d1c50b5e7951c80", // my question is where the heck is this stored?
	        -               spanKind:     1,
	        -               name:         "grpc.testing.TestService.UnaryCall",
	        -               attributes:   map[string]any{"Client": bool(false), "FailFast": bool(false)}, // sure
	        -               messageEvents: []trace.MessageEvent{
	        - 
										// Same, 2 3 1 4
										{
	        -                               Time:                 s"2023-01-23 21:58:59.620649 -0500"...,
	        -                               EventType:            2,
	        -                               UncompressedByteSize: 2,
	        -                               CompressedByteSize:   7,
	        -                       },
	        -                       {
	        -                               Time:               s"2023-01-23 21:58:59.620656 -0500"...,
	        -                               EventType:          1,
	        -                               CompressedByteSize: 5,
	        -                       },
	        -               },
	        -               links: []trace.Link{
	        -                       {
	        -                               TraceID: s"39b91a70aaed10e4f584faf0b9e5aa8d", // right trace id...
	        -                               SpanID:  s"6033e47031a2c816", // what the fuck, at least this shows the pointing to client
	        -                               Type:    1,
	        -                       },
	        -               },
	        -               hasRemoteParent: true, // same
	        -       },
	        -       {
	        -               sc: trace.SpanContext{
	        -                       TraceID:      s"39b91a70aaed10e4f584faf0b9e5aa8d",
	        -                       SpanID:       s"6033e47031a2c816",
	        -                       TraceOptions: 1,
	        -               },
	        -               parentSpanID: s"6033e47031a2c816",
	        -               spanKind:     2,
	        -               name:         "grpc.testing.TestService.UnaryCall",
	        -               attributes:   map[string]any{"Client": bool(true), "FailFast": bool(true)},
	        -               messageEvents: []trace.MessageEvent{
	        -                       {
	        -                               Time:                 s"2023-01-23 21:58:59.620475 -0500"...,
	        -                               EventType:            1,
	        -                               UncompressedByteSize: 2,
	        -                               CompressedByteSize:   7,
	        -                       },
	        -                       {
	        -                               Time:               s"2023-01-23 21:58:59.620776 -0500"...,
	        -                               EventType:          2,
	        -                               CompressedByteSize: 5,
	        -                       },
	        -               },
	        -       },
	          }
	*/

	if err := spanMatches(fe, wantSI); err != nil {
		t.Fatalf("Invalid OpenCensus export span data: %v", err)
	}


	// clear state for fresh verification later (like ginger lol)
	fe.mu.Lock()
	fe.seenSpans = nil // can just nil this because we're appending to it...
	fe.mu.Unlock()

	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend()
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}

	// actually I want [](span from client side with message events, span from server side with message events)
	/*wantSI = []spanInformation{
		// Client side with messages
		{
			// same shit as above except with message events, but I never sent a message or does that come in closesend()?
		},
		// Server side with messages
		{
		},
	}*/
	// because it'll always be coupled?
	// or I could do wantSI[0].messageEvents = although the previous has message events too...
	// also will have a different span id
	wantSI[0].messageEvents = []trace.MessageEvent{}
	// and wantSI[1].messageEvents =
	wantSI[1].messageEvents = []trace.MessageEvent{}
	if err := spanMatches(fe, wantSI); err != nil {
		t.Fatalf("Invalid OpenCensus export span data: %v", err)
	}
}

// we have these whack emissions from my opencensus traces...

// question I have: should the emissions be deterministic wrt ordering, and also
// should the second Span point to the first span?

// what about what actually gets emitted from OpenCensus currently...
// ^^^
// Take my verification of tests and instead of my new dial option,
// use there's with their trace sampler (see o11y for callsite)

func (s) TestActualOpenCensus(t *testing.T) {
	// everything exactly the same expect start options for client and server are different...
	fe := &fakeExporter{
		t: t,
		seenViews: make(map[string]*viewInformation),
	}
	trace.RegisterExporter(fe)
	defer trace.UnregisterExporter(fe)

	// linking in this will f with go.mod right?
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
	// this is really only difference...opencensus options (see o11y package)

	so := trace.StartOptions{
		Sampler: trace.ProbabilitySampler(1.0),
	}


	if err := ss.Start([]grpc.ServerOption{grpc.StatsHandler(&ocgrpc.ServerHandler{StartOptions: so})}, grpc.WithStatsHandler(&ocgrpc.ClientHandler{StartOptions: so})); err != nil {
		t.Fatalf("Error starting endpoint server: %v", err)
	}
	defer ss.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{Payload: &grpc_testing.Payload{}}); err != nil {
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}

	wantSI := []spanInformation{}

	if err := spanMatches(fe, wantSI); err != nil {
		t.Fatalf("Invalid OpenCensus export span data: %v", err)
	}

	// server span comes first? points to client span? Client span has a parent of itself?
	/*
	        -       {
	        -               sc: trace.SpanContext{
	        -                       TraceID:      s"86c9a3ee739bc6d0c208b285463658b7",
	        -                       SpanID:       s"a57e7d9e5b0ba776",
	        -                       TraceOptions: 1,
	        -               },
							// parentSpanID is the span ID from the span context...
	        -               parentSpanID: s"a57e7d9e5b0ba776", // is this correct? How do you even verify this is the right emisison outside of Stanley's e2e tests?
	        -               spanKind:     1, // assuming this is span kind server
	        -               name:         "grpc.testing.TestService.UnaryCall",
	        -               attributes:   map[string]any{"Client": bool(false), "FailFast": bool(false)},
	        -               messageEvents: []trace.MessageEvent{
	        -                       { // second
	        -                               Time:                 s"2023-01-23 18:33:10.753358 -0500"...,
	        -                               EventType:            2,
	        -                               UncompressedByteSize: 2,
	        -                               CompressedByteSize:   7,
	        -                       },
	        -                       { // third
	        -                               Time:               s"2023-01-23 18:33:10.753393 -0500"...,
	        -                               EventType:          1,
	        -                               CompressedByteSize: 5,
	        -                       },
	        -               },
	// I ADDED LINK TYPE CHILD
	        -               hasRemoteParent: true,
	        -       }, // so since third happens before fourth I'm assuming this span gets emitted first
	        -       {
	        -               sc: trace.SpanContext{
	        -                       TraceID:      s"86c9a3ee739bc6d0c208b285463658b7",
	        -                       SpanID:       s"e6da02a3913743b6",
	        -                       TraceOptions: 1,
	        -               },
	        -               parentSpanID: s"e6da02a3913743b6", // client Span's parent is itself...
	        -               spanKind:     2,
	        -               name:         "grpc.testing.TestService.UnaryCall",
	        -               attributes:   map[string]any{"Client": bool(true), "FailFast": bool(true)},
	        -               messageEvents: []trace.MessageEvent{
	        -                       { // first
	        -                               Time:                 s"2023-01-23 18:33:10.753204 -0500"...,
	        -                               EventType:            1,
	        -                               UncompressedByteSize: 2,
	        -                               CompressedByteSize:   7,
	        -                       },
	        -                       { // fourth
	        -                               Time:               s"2023-01-23 18:33:10.753479 -0500"...,
	        -                               EventType:          2,
	        -                               CompressedByteSize: 5,
	        -                       },
	        -               },
	        -       },
	          }
	*/
	/*
	[]opencensus.spanInformation{
	        -       {
	        -               sc: trace.SpanContext{
	        -                       TraceID:      s"ba29c71bf4ecc071abcebfe9af469330",
	        -                       SpanID:       s"33a73c584efcb6ed",
	        -                       TraceOptions: 1,
	        -               },
	        -               parentSpanID: s"33a73c584efcb6ed",
	        -               spanKind:     1,
	        -               name:         "grpc.testing.TestService.UnaryCall",
	        -               attributes:   map[string]any{"Client": bool(false), "FailFast": bool(false)},
	        -               messageEvents: []trace.MessageEvent{
	        -                       {
	        -                               Time:                 s"2023-01-23 21:58:53.237559 -0500"...,
	        -                               EventType:            2,
	        -                               UncompressedByteSize: 2,
	        -                               CompressedByteSize:   7,
	        -                       },
	        -                       {
	        -                               Time:               s"2023-01-23 21:58:53.237611 -0500"...,
	        -                               EventType:          1,
	        -                               CompressedByteSize: 5,
	        -                       },
	        -               },
	        -               hasRemoteParent: true,
	        -       },
	        -       {
	        -               sc: trace.SpanContext{
	        -                       TraceID:      s"ba29c71bf4ecc071abcebfe9af469330",
	        -                       SpanID:       s"76bed01398086384",
	        -                       TraceOptions: 1,
	        -               },
	        -               parentSpanID: s"76bed01398086384",
	        -               spanKind:     2,
	        -               name:         "grpc.testing.TestService.UnaryCall",
	        -               attributes:   map[string]any{"Client": bool(true), "FailFast": bool(true)},
	        -               messageEvents: []trace.MessageEvent{
	        -                       {
	        -                               Time:                 s"2023-01-23 21:58:53.237383 -0500"...,
	        -                               EventType:            1,
	        -                               UncompressedByteSize: 2,
	        -                               CompressedByteSize:   7,
	        -                       },
	        -                       {
	        -                               Time:               s"2023-01-23 21:58:53.237684 -0500"...,
	        -                               EventType:          2,
	        -                               CompressedByteSize: 5,
	        -                       },
	        -               },
	        -       },
	          }

	*/
	// From Stanley's tests, this actually does correct generate span id
	// Overall: traceID
	// client span id
	// server parent span id really points to client ^^^
	// server span id has it's own
	// traceID
	fe.mu.Lock()
	fe.seenSpans = nil
	fe.mu.Unlock()

	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %f", err)
	}

	stream.CloseSend()
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}

	if err := spanMatches(fe, wantSI); err != nil {
		t.Fatalf("Invalid OpenCensus export span data: %v", err)
	}
}
