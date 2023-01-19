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
	seenSpans []*spanInformation // pointer? or inline
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
func compareSpanContext(sc *trace.SpanContext, sc2 *trace.SpanContext) bool {
	if sc == nil && sc2 == nil {
		return true
	}
	if (sc != nil) != (sc2 != nil) {
		return false
	}
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

func (si *spanInformation) equal(si2 *spanInformation) bool {
	// go down this list
	if si == nil && si2 == nil {
		return true
	}
	if (si != nil) != (si2 != nil) {
		return false
	}
	// switch this comparison later since it comes as a struct in ExportSpan
	if !compareSpanContext(&si.sc, &si2.sc) { // ugh so no pointers, just get rid of nil checks in compare functions?
		return false
	}
	// assert si1.field1 == si2.field2
	if si.parentSpanID != si2.parentSpanID { // same question, can I just send this slice through this equality operator?
		return false
	}
	if si.spanKind != si2.spanKind {
		return false
	}
	if si.name != si2.name {
		return false
	}
	if si.message != si2.message {
		return false
	}

	// attribute comparison here

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
	if !compareLinks(si.links, si2.links) {
		return false
	}
	if si.hasRemoteParent != si2.hasRemoteParent {
		return false
	}
	if si.childSpanCount != si2.childSpanCount {
		return false
	}
	return true // combine with statement above?
}

func (si *spanInformation) Equal(si2 *spanInformation) bool {
	return si.equal(si2)
}

func (fe *fakeExporter) ExportSpan(sd *trace.SpanData) { // no pointers...
	fe.mu.Lock()
	defer fe.mu.Unlock()
	// build out span data want
	gotSI := &spanInformation{
		// spanContext?
		sc: sd.SpanContext,
		// parent span id - remote or local, so I think the server should point to client span regardless (in this case remote, when I add local parent client side for top level trace mess around with it then)
		parentSpanID: sd.SpanID,
		spanKind: sd.SpanKind,
		name: sd.Name,
		message: sd.Message, // I don't see this being populated
		attributes: sd.Attributes,
		// do I need separate data/equal on these events/data in events...
		// above ^^^:  []trace.MessageEvent -> wantMessageEvent? if we want a subset or if want to actually compare?
		messageEvents: sd.MessageEvents,
		status: sd.Status,
		links: sd.Links,
		hasRemoteParent: sd.HasRemoteParent,
		childSpanCount: sd.ChildSpanCount,
	}
	// append to fe.gotSpans
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
			messageEvents: []trace.MessageEvent{}, // zero value or nil? I don't think any message events in this scenario?
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

	// stanley I coded up a fix, can you please run all your tests on it so I can see if it fixes and contineus to work with other test cases...

	// put this helper in a separate function...

	// does cmp.Diff iterate over the [], or {fieldA, fieldB} yes it iterates over array, and it iterates over struct fields too
	// so call cmp.Diff...will iterate over array and at each node (in this case trace information):
	// use the result of x.Equal(y) even if x or y is nil...
	// if diff := cmp.Diff(fe.seenSpans, wantSI); diff != "" {

	// } // if it appends you need to clear later yeah clear it need the seenViewMatches thing too, can I generalize that helper for both trace information and view information? I don't think so...
	// take read lock in helper (happens in a separate thread :P)

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
	// or I could do wantSI[0].messageEvents =
	wantSI[0].messageEvents = []trace.MessageEvent{}
	// and wantSI[1].messageEvents =
	wantSI[1].messageEvents = []trace.MessageEvent{}
	if err := spanMatches(fe, wantSI); err != nil {
		t.Fatalf("Invalid OpenCensus export span data: %v", err)
	}
}
