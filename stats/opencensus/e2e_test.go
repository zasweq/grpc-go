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
	"errors"
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
	seenSpans []spanInformation
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

// spanMatches checks if the seen spans of the exporter contain the desired
// spans within the scope of a certain time. Returns a nil error if correct span
// information found, non nil error if correct information not found within a
// timeout.
func spanMatches(fe *fakeExporter, wantSpans []spanInformation) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	var err error
	for ctx.Err() == nil {
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

// compareSpanContext only checks the equality of the trace options, which
// represent whether the span should be sampled. The other fields are checked
// for presense in later assertions.
func compareSpanContext(sc trace.SpanContext, sc2 trace.SpanContext) bool {
	return sc.TraceOptions.IsSampled() == sc2.TraceOptions.IsSampled()
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
	// Order matters here, message events are deterministic so no flakiness to
	// test.
	for i, e := range me {
		e2 := me2[i]
		if e.EventType != e2.EventType {
			return false
		}
		if e.MessageID != e2.MessageID {
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

// compareLinks compares the type of link received compared to the wanted link.
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
		if l.Type != l2.Type {
			return false
		}
	}
	return true
}

// spanInformation is the information received about the span. This is a subset
// of information that is important to verify that gRPC has knobs over, which
// goes through a stable OpenCensus API with well defined behavior. This keeps
// the robustness of assertions over time.
type spanInformation struct {
	// SpanContext either gets pulled off the wire in certain cases server side
	// or created.
	sc              trace.SpanContext
	parentSpanID    trace.SpanID
	spanKind        int
	name            string
	message         string
	messageEvents   []trace.MessageEvent
	status          trace.Status
	links           []trace.Link
	hasRemoteParent bool
	childSpanCount  int
}

// presenceAndRelationshipAssertionsClientServerSpan checks for consistent trace
// ID across the full trace. It also asserts each span has a corresponding
// generated SpanID, and makes sure in the case of a server span and a client
// span, the server span points to the client span as it's parent. This is
// assumed to be called with spans from the same RPC (thus the same trace).
// These assertions are orthogonal to pure equality assertions, as this data is
// generated at runtime, so can only test relations between IDs (i.e. this part
// of the data has the same ID as this part of the data).
//
// Returns an error in the case of a failing assertion, non nil error otherwise.
func presenceAndRelationshipAssertionsClientServerSpan(sis []spanInformation) error {
	var traceID trace.TraceID
	for i, si := range sis {
		// Trace IDs should all be consistent across every span, since this
		// function assumes called with Span from one RPC, which all fall under
		// one trace.
		if i == 0 {
			traceID = si.sc.TraceID
		} else {
			if !cmp.Equal(si.sc.TraceID, traceID) {
				return fmt.Errorf("TraceIDs should all be consistent: %v, %v", si.sc.TraceID, traceID)
			}
		}
		// Due to the span IDs being 8 bytes, the documentation states that it
		// is practically a mathematical uncertainity in practice to create two
		// colliding IDs. Thus, for a presence check (the ID was actually
		// generated, I will simply compare to the zero value, even though a
		// zero value is a theoretical possibility of generation). This is
		// because in practice, this zero value defined by this test will never
		// collide with the generated ID.
		if cmp.Equal(si.sc.SpanID, trace.SpanID{}) {
			return errors.New("span IDs should be populated from the creation of the span")
		}
	}
	// If the length of spans of an RPC is 2, it means there is a server span
	// which exports first and a client span which exports second. Thus, the
	// server span should point to the client span as it's parent, represented
	// by it's ID.
	if len(sis) == 2 {
		if !cmp.Equal(sis[0].parentSpanID, sis[1].sc.SpanID) {
			return fmt.Errorf("server span should point to the client span as it's parent. parentSpanID: %v, clientSpanID: %v", sis[0].parentSpanID, sis[1].sc.SpanID)
		}
	}
	return nil
}

// equal compares the constant data of the exported span information that is
// important for correctness known before runtime.
func (si spanInformation) equal(si2 spanInformation) bool {
	if !compareSpanContext(si.sc, si2.sc) {
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
	// Ignore attribute comparison because Java doesn't even populate any so not
	// important for correctness.
	if !compareMessageEvents(si.messageEvents, si2.messageEvents) {
		return false
	}
	if !cmp.Equal(si.status, si2.status) {
		return false
	}
	// compare link type as link type child is important.
	if !compareLinks(si.links, si2.links) {
		return false
	}
	if si.hasRemoteParent != si2.hasRemoteParent {
		return false
	}
	return si.childSpanCount == si2.childSpanCount
}

func (si spanInformation) Equal(si2 spanInformation) bool {
	return si.equal(si2)
}

func (fe *fakeExporter) ExportSpan(sd *trace.SpanData) {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	// Persist the subset of data received that is important for correctness and
	// to make various assertions on later. Keep the ordering as ordering of
	// spans is deterministic in the context of one RPC.
	gotSI := spanInformation{
		sc:           sd.SpanContext,
		parentSpanID: sd.ParentSpanID,
		spanKind:     sd.SpanKind,
		name:         sd.Name,
		message:      sd.Message,
		// annotations - ignore
		// attributes - ignore, I just left them in from previous but no spec
		// for correctness so no need to test. Java doesn't even have any
		// attributes.
		messageEvents:   sd.MessageEvents,
		status:          sd.Status,
		links:           sd.Links,
		hasRemoteParent: sd.HasRemoteParent,
		childSpanCount:  sd.ChildSpanCount,
	}
	fe.seenSpans = append(fe.seenSpans, gotSI)
}

// TestSpan tests emitted spans from gRPC. It configures a system with a gRPC
// Client and gRPC server with the OpenCensus Dial and Server Option configured,
// and makes a Unary RPC and a Streaming RPC. This should cause spans with
// certain information to be emitted from client and server side for each RPC.
func (s) TestSpan(t *testing.T) {
	fe := &fakeExporter{
		t: t,
	}
	trace.RegisterExporter(fe)
	defer trace.UnregisterExporter(fe)

	so := TraceOptions{
		TS:           trace.ProbabilitySampler(1),
		DisableTrace: false,
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	// Make a Unary RPC. This should cause a span with message events
	// corresponding to the request message and response message to be emitted
	// both from the client and the server.
	if _, err := ss.Client.UnaryCall(ctx, &grpc_testing.SimpleRequest{Payload: &grpc_testing.Payload{}}); err != nil {
		t.Fatalf("Unexpected error from UnaryCall: %v", err)
	}

	// The spans received are server first, then client. This is due to the RPC
	// finishing on the server first. The ordering of message events for a Unary Call is as
	// follows: client send, server recv, server send (server span end), client
	// recv (client span end).
	wantSI := []spanInformation{
		{
			// Sampling rate of 100 percent, so this should populate every span
			// with the information that this span is being sampled. Here and
			// every other span emitted in this test.
			sc: trace.SpanContext{
				TraceOptions: 1,
			},
			spanKind: trace.SpanKindServer,
			name:     "grpc.testing.TestService.UnaryCall",
			// message id - "must be calculated as two different counters
			// starting from 1 one for sent messages and one for received
			// message. This way we guarantee that the values will be consistent
			// between different implementations. In case of unary calls only
			// one sent and one received message will be recorded for both
			// client and server spans."
			messageEvents: []trace.MessageEvent{
				{
					EventType:            trace.MessageEventTypeRecv,
					MessageID:            1, // First msg recv so 1 (see comment above)
					UncompressedByteSize: 2,
					CompressedByteSize:   7,
				},
				{
					EventType:          trace.MessageEventTypeSent,
					MessageID:          1, // First msg send so 1 (see comment above)
					CompressedByteSize: 5,
				},
			},
			links: []trace.Link{
				{
					Type: trace.LinkTypeChild,
				},
			},
			// For some reason, status isn't populated in the data sent to the
			// exporter. This seems wrong, but it didn't send status in old
			// instrumentation code, so I'm iffy on it but fine.
			hasRemoteParent: true,
		},
		{
			sc: trace.SpanContext{
				TraceOptions: 1,
			},
			spanKind: trace.SpanKindClient,
			name:     "grpc.testing.TestService.UnaryCall",
			messageEvents: []trace.MessageEvent{
				{
					EventType:            trace.MessageEventTypeSent,
					MessageID:            1, // First msg send so 1 (see comment above)
					UncompressedByteSize: 2,
					CompressedByteSize:   7,
				},
				{
					EventType:          trace.MessageEventTypeRecv,
					MessageID:          1, // First msg recv so 1 (see comment above)
					CompressedByteSize: 5,
				},
			},
			hasRemoteParent: false,
		},
	}

	if err := spanMatches(fe, wantSI); err != nil {
		t.Fatalf("Invalid OpenCensus export span data: %v", err)
	}
	fe.mu.Lock()
	if err := presenceAndRelationshipAssertionsClientServerSpan(fe.seenSpans); err != nil {
		fe.mu.Unlock()
		t.Fatalf("Error in runtime data assertions: %v", err)
	}
	fe.seenSpans = nil
	fe.mu.Unlock()

	stream, err := ss.Client.FullDuplexCall(ctx)
	if err != nil {
		t.Fatalf("ss.Client.FullDuplexCall failed: %v", err)
	}
	// Send two messages. This should be accounted for in the emitted spans
	// message events, with message IDs which increase.
	if err := stream.Send(&grpc_testing.StreamingOutputCallRequest{}); err != nil {
		t.Fatalf("stream.Send failed: %v", err)
	}
	if err := stream.Send(&grpc_testing.StreamingOutputCallRequest{}); err != nil {
		t.Fatalf("stream.Send failed: %v", err)
	}

	stream.CloseSend()
	if _, err = stream.Recv(); err != io.EOF {
		t.Fatalf("unexpected error: %v, expected an EOF error", err)
	}

	wantSI = []spanInformation{
		{
			sc: trace.SpanContext{
				TraceOptions: 1,
			},
			spanKind: trace.SpanKindServer,
			name:     "grpc.testing.TestService.FullDuplexCall",
			links: []trace.Link{
				{
					Type: trace.LinkTypeChild,
				},
			},
			messageEvents: []trace.MessageEvent{
				{
					EventType:          trace.MessageEventTypeRecv,
					MessageID:          1, // First msg recv so 1
					CompressedByteSize: 5,
				},
				{
					EventType:          trace.MessageEventTypeRecv,
					MessageID:          2, // Second msg recv so 2
					CompressedByteSize: 5,
				},
			},
			hasRemoteParent: true,
		},
		{
			sc: trace.SpanContext{
				TraceOptions: 1,
			},
			spanKind: trace.SpanKindClient,
			name:     "grpc.testing.TestService.FullDuplexCall",
			messageEvents: []trace.MessageEvent{
				{
					EventType:          trace.MessageEventTypeSent,
					MessageID:          1, // First msg recv so 1
					CompressedByteSize: 5,
				},
				{
					EventType:          trace.MessageEventTypeSent,
					MessageID:          2, // Second msg recv so 2
					CompressedByteSize: 5,
				},
			},
			hasRemoteParent: false,
		},
	}
	if err := spanMatches(fe, wantSI); err != nil {
		t.Fatalf("Invalid OpenCensus export span data: %v", err)
	}
	fe.mu.Lock()
	defer fe.mu.Unlock()
	if err := presenceAndRelationshipAssertionsClientServerSpan(fe.seenSpans); err != nil {
		t.Fatalf("Error in runtime data assertions: %v", err)
	}
}
