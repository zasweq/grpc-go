/*
 *
 * Copyright 2018 gRPC authors.
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

// Binary client is an example client.
package main

import (
	"context"
	"flag"
	"fmt"
	"go.opencensus.io/trace"
	"io"
	"log"
	"sync/atomic"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/examples/data"
	ecpb "google.golang.org/grpc/examples/features/proto/echo"
)

var addr = flag.String("addr", "localhost:50051", "the address to connect to")

const fallbackToken = "some-secret-token"

// logger is to mock a sophisticated logging system. To simplify the example, we just print out the content.
func logger(format string, a ...interface{}) {
	fmt.Printf("LOG:\t"+format+"\n", a...)
}

// flow for HandleRPCEvents that count:

// In HandleBegin:
// b := GetBlob(attempts context)
// if s.IsTransparentRetryAttempt:
//      atomic.AddUint32(&b.countTransparentRetries, 1)
// else {
//      somehow plumb logic of if first attempt don't count...either through the data already passed or some invariant?
//      atomic.AddUint32(&b.countNonTransparentRetries, 1)
// }

// Ok this stores information in blob, but how do you relate this to the defined
// measure? Update the measure with the read count? And how does it split it per
// call? And then what do you call to update, so many different calls update the
// same measure, how does the measure split per RPC?





// I think discrete intervals make sense, you can't contionously stream time
// lol. Time spent without an attempt logically makes sense to be reported when
// there is an attempt. Does it overwrite the view or I guess you add to it?


// State space of events that can lead to this being added to,
// a lotttt of different possibile scenarios can lead to adding to vvv

// timeNoActiveAttempt is a bit tricker - any synchronization issues wrt hedging
// and even current system - which is just csAttempt plumbing around wrt
// retries... how will this even work with hedging much less synchronize?


// Also this is the data defined/a bit of the plumbing, still need to define the
// metrics and views on the metrics in opencensus plugin






// traces is a bit easier

// create top level trace in interceptor

// do you unconditionally register both interceptor and stats handler now? (this will change whether first TagRPC call could have a parent or not)

// Adjust logic in TagRPC and HandleRPC accordingly...TagRPC needs to add child
// HandleRPC has to count those required attributes now...

// already have musings about these in opencensus library:

// `previous-rpc-attempts` : an integer value, number of preceding attempts,
// transparent retry not included. (attempt begins are plumbed and have bools, count based on that?, shows system when attempts are, and wehther they are transparent retries or not...)

// `transparent-retry`     : a boolean value, whether the attempt is a transparent retry. Easy, literally just read off the bool in Begin

// Message events are already correctly handled I think in opencensus package




type blob struct {

	// it needs to be concurrency-safe (atomic.Add) for when hedging comes along
	// one day and attempts can be simultaneous

	// atomic count of non transparent retries (atomic.Int64)
	// count of retries; transparent not included
	countNonTranparentRetries uint32 // What triggers this (i.e. ++)?

	/*
	// Number of retry or hedging attempts excluding transparent retries made during
	// the client call. The original attempt is not counted as a retry/hedging
	// attempt. for this view:
	Trigger point for count: a stats.Begin{} object passed whenever you get isTransparentRetryAttempt in HandleRPC() - first attempt (1). Or just leave as is (the actual thing persisted) and don't subtract one for reusability.
	*/

	// atomic count of transparent retries
	countTransparentRetries uint32 // What triggers this (i.e. ++)?
	// Trigger point for this: a stats.Begin{} object passed in with IsTransparentRetryAttempt set to true in HandleRPC()


	// time spent with no active attempt during client call...
	timeNoActiveAttempt time.Time // When is this added to? Timestamp - timestamp, then an add? Just realized the end could be (the end of rpc triggers the collection || it attempts, then rpc ends, this will be interesting logic to get right with the nuianse at end)
}

// for both unary and streaming...
// within interceptor create trace object

// TagRPC and HandleRPC need to be rewritten...because this creates the span now...
// the population of context is simple, populates context, TagRPC and HandleRPC just need to interface
// with this new object to create the new steady stream metrics...mused about up top...what does TagRPC do wrt stats?

// On the parent call object, there should be some way to record an event when
// we do the first LB pick attempt for a given call attempt. - why is this
// important?

type blobKey struct{}

// GetBlob returns the Blob stored in a context, or nil if there isn't one.
func GetBlob(ctx context.Context) *blob {
	b, _ := ctx.Value(blobKey{}).(*blob)
	return b
} // opencensus Tag and Handle will use this to count. Should we change callsites or are we good here?

func SetBlob(ctx context.Context, /*do you need extra knobs on creation - this is just a blob that counts regardless right?*/) context.Context /*, if you need ref to blob - I don't see why you would though*/ {
	// has something to do with the context keys

	// once you stick this blob object in the context, you have to interface with it later...

	// Remembe, it's pointing to same heap memory...
	// context attribute? copies context, any implications?
	// read this into local var if need extra logic...
	return context.WithValue(ctx, blobKey{}, &blob{}) // allocates new heap memory here - layering aspect
}

// related to the question of whether you unconditionally register stats handler and interceptor,
// traces and metrics have knobs...but then you just don't register exporter right and it just runs
func unaryInterceptorOpenCensus(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	// for traces:
	// Call out to create a top level span...

	// The parent span is named in the form of Sent.<service name>.<method name>
	// s, m := ServiceMethod(method)
	// I wanna say this returns an error - wtf do we do with it/what do we want the functionality to be if method name is wrong? Seems like it would never happen?
	name := "Sent.<service name>.<method name>"
	// Find the correct letter for string
	// fmt.Sprintf("Sent.%v.%v", s, m)

	ctx, span := trace.StartSpan(ctx, name, /*options here*/) // this adds a "layer" to the context - data wrapped in (need to import opencensus trace once you rewrite plugin to where we own it)

	// TagRPC() for sure I think, this creates a trace object in the context
	// right now this call is
	// ctx = sh.TagRPC(ctx, ...)
	// are we persisting the sh?
	// need access to a sh object, or are we doing this some other way now that this is an interceptor

	// wait this interceptor is in the opencensus package itself, not a call outward into Stats Handler,
	// can do all the logic like trace.Etc in this interceptor since it's a plugin itself, logical opencensus objects
	// can be interfaced with this interceptor


	// HandleRPC()...? Other callsites in the codebase of TagRPC() have HandleRPC() right after...


	// for retry metrics (and Yashes):
	// populate the context with a blob, a struct with atomic counts for transparent retries and non transparent retries...
	ctx /*, do we need a reference to the blob here in this package or can you just attach it to context and leave there?*/ = SetBlob(ctx /*, any further options to populate context that you need to set?*/)


	// Does this matter? If it's telemetry and steady stream keep doing it at
	// each call outward

	// I'm assuming object will be in context at the end, this context gets used for the client stream

	// Actually invoking the RPC, this should call Tag/HandleRPC as a downstream effect...
	// err := invoker(ctx, method, req, reply, cc, opts...)
	err := invoker(ctx, method, req, reply, cc, opts...)

	// Any post processing wrt the RPC for metrics collection? esp wrt (err
	// returned || not returned) - what happens to certain stats and do you need
	// cleanup? If you don't need you can just do return invoker(ctx, method, req, reply, cc, opts...)

	// esp the time no csAttempt, how often do you upload this, at discrete
	// intervals? (perhaps look at other languages opencensus implementations)
	// how do current just counts (++) and time interval metrics/stats logically
	// work?

}

// all my musings are for ^^^..., idk if it also applies to vvv
// so add callsite to create top level span

// and add object + creation of that object pointed to by context hierarchy for lifetime of call...

// All the other logic handled in opencensus library? Wrt to creation of the
// parent span and children span (already there outside top level span and some
// specifics)

// Also the emission of this data...every time the library gets passed the context with data it emits it upward?


// streaming interceptor here and how it works...

// unaryInterceptor is an example unary interceptor.
func unaryInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	var credsConfigured bool
	for _, o := range opts {
		_, ok := o.(grpc.PerRPCCredsCallOption)
		if ok {
			credsConfigured = true
			break
		}
	}
	if !credsConfigured {
		opts = append(opts, grpc.PerRPCCredentials(oauth.NewOauthAccess(&oauth2.Token{
			AccessToken: fallbackToken,
		})))
	}
	start := time.Now()

	// Is there any data here that can support these metrics
	// is there any data plumbed in somewhere the number of retries...

	err := invoker(ctx, method, req, reply, cc, opts...) // is there where all the retries happen?

	// Likewise, the client will receive the number of retry attempts made when
	// receiving the results of an RPC.

	// "maxAttempts specifies the maximum number of RPC attempts, including the original request."
	// How this code handles this knob is the right level of granularity,
	// at the csAttempt level, which I think wraps operations. See how this is counted
	// and where and that will give you good info for how to count this and at the right layer.

	end := time.Now()
	logger("RPC: %s, start time: %s, end time: %s, err: %v", method, start.Format("Basic"), end.Format(time.RFC3339), err)
	return err
}

// wrappedStream  wraps around the embedded grpc.ClientStream, and intercepts the RecvMsg and
// SendMsg method call.
type wrappedStream struct {
	grpc.ClientStream
}

func (w *wrappedStream) RecvMsg(m interface{}) error {
	logger("Receive a message (Type: %T) at %v", m, time.Now().Format(time.RFC3339))
	return w.ClientStream.RecvMsg(m)
}

func (w *wrappedStream) SendMsg(m interface{}) error {
	logger("Send a message (Type: %T) at %v", m, time.Now().Format(time.RFC3339))
	return w.ClientStream.SendMsg(m)
}

func newWrappedStream(s grpc.ClientStream) grpc.ClientStream {
	return &wrappedStream{s}
}

// do we even need to wrap a stream for opencensus, calls outward happen in stream operations anyway...

func streamInterceptorOpenCensus(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	// Same sort of logic as unary? populate context?
	// Create a trace object?

	// yeah same logic, preprocess the RPC...but rather than make the rpc and
	// postprocess you just intercept stream operations...

	// how do retries work with streaming RPCs?
	// I guess it doesn't matter, when you get this first blob triggering events,

	// and callouts like Stats.Begin() whenever it does retry that abstracts
	// away the logic for you...you don't need to worry about it and can treat
	// it as a black box, one of the advantages of layering and API's :D...

	s, err := streamer(ctx, desc, cc, method, opts...)
	// what do you do here? you intercept stream operations but do you even need to intercept anything?

	// how do retries work with stream operations? it does retries to the point
	// the user specifies? or is this a blob/behavior you don't need to
	// interface with wrt all the plumbing for retries, can just deal with call
	// outs?

}

// streamInterceptor is an example stream interceptor.
func streamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	var credsConfigured bool
	for _, o := range opts {
		_, ok := o.(*grpc.PerRPCCredsCallOption)
		if ok {
			credsConfigured = true
			break
		}
	}
	if !credsConfigured {
		opts = append(opts, grpc.PerRPCCredentials(oauth.NewOauthAccess(&oauth2.Token{
			AccessToken: fallbackToken,
		})))
	}
	s, err := streamer(ctx, desc, cc, method, opts...)

	// how does it instrument cs attempts?

	if err != nil {
		return nil, err
	}
	return newWrappedStream(s), nil // simply returning the object not calling operations on the stream
}

func callUnaryEcho(client ecpb.EchoClient, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := client.UnaryEcho(ctx, &ecpb.EchoRequest{Message: message})
	if err != nil {
		log.Fatalf("client.UnaryEcho(_) = _, %v: ", err)
	}
	fmt.Println("UnaryEcho: ", resp.Message)
}

func callBidiStreamingEcho(client ecpb.EchoClient) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := client.BidirectionalStreamingEcho(ctx)
	if err != nil {
		return
	}
	for i := 0; i < 5; i++ {
		if err := c.Send(&ecpb.EchoRequest{Message: fmt.Sprintf("Request %d", i+1)}); err != nil {
			log.Fatalf("failed to send request due to error: %v", err)
		}
	}
	c.CloseSend()
	for {
		resp, err := c.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("failed to receive response due to error: %v", err)
		}
		fmt.Println("BidiStreaming Echo: ", resp.Message)
	}
}

func main() {
	flag.Parse()

	// Create tls based credential.
	creds, err := credentials.NewClientTLSFromFile(data.Path("x509/ca_cert.pem"), "x.test.example.com")
	if err != nil {
		log.Fatalf("failed to load credentials: %v", err)
	}

	// Set up a connection to the server.
	conn, err := grpc.Dial(*addr, grpc.WithTransportCredentials(creds), grpc.WithUnaryInterceptor(unaryInterceptor), grpc.WithStreamInterceptor(streamInterceptor))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	// Make a echo client and send RPCs.
	rgc := ecpb.NewEchoClient(conn)
	callUnaryEcho(rgc, "hello world")
	callBidiStreamingEcho(rgc)
}
