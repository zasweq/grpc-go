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

package test

import (
	"bytes"
	"io"
	"net"
	"testing"

	"golang.org/x/net/http2"
)

var (
	clientPreface   = []byte(http2.ClientPreface)
)

// http2 client might have a tester

// this is a conn, so it's assuming caller has already accepted a conn
// on a listener. listen on a port, client needs to connect to the service?
// listening on the port? grpc.Dial(...) start a test service at blank
func newClientTester(t *testing.T, conn net.Conn) *clientTester {
	ct := &clientTester{
		t:    t,
		conn: conn,
	}
	ct.fr = http2.NewFramer(conn, conn)
	return ct
}

type clientTester struct {
	t    *testing.T
	conn net.Conn // to write/read directly onto
	fr *http2.Framer
	// do we want to persist
}

// plumb a context around and have every operation block on that?
func (ct *clientTester) greet() {
	// only the steps needed up to setting ack

	// 1. read client preface - this needs to happen
	ct.wantClientPreface()

	// 2. read client setting frame - server requires a setting frame sent from
	// the client as part of the client - if not, connection error. server
	// tester wants settings too or else fails t (this needs to happen, but
	// either passed in or persisted in ct)
	ct.wantSettingsFrame()

	// 3. send a settings frame (pulled from serverTester's logic)
	ct.writeSettingsFrame()

	// 4. write settings ack unconditionally...?
	ct.writeSettingsAck()

	// why does he have a writer.Flush() for client preface?

	// Need to wait for a certain sequence of frames before exiting from this ReadFrame sequence
	// for {
	//     SAME LOGIC AS SERVER TESTER
	// }
	for {
		f, err := ct.fr.ReadFrame()
		if err != nil {
			ct.t.Errorf("error reading frame from client side: %v", err) // do we want this to end or continue? if end all of these should be fatals
		}
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if f.IsAck() {
				return // only way to exit this function
			}
		default:
			ct.t.Fatalf("during greet, unexpected frame type %T", f)
		}
	}
}

// I feel like all of these event expectations need to be wrapped with a timeout

// step 1 of http2 requirement? keep this helper keeps it consistent with
// this caller in greet calling it and the user calling it
func (ct *clientTester) wantClientPreface() {
	// read client preface, if not there/want a timeout

	// don't just read client preface, also verify it's accurate
	preface := make([]byte, len(clientPreface))
	if _, err := io.ReadFull(ct.conn, preface); err != nil {
		ct.t.Errorf("Error at server-side while reading preface from client. Err: %v", err)
	}
	if !bytes.Equal(preface, clientPreface) {
		ct.t.Errorf("received bogus greeting from client %q", preface)
	}
}

// step 2 of http2 requirement?
func (ct *clientTester) wantSettingsFrame() { // initial settings frame from client
	// read settings frame
	frame, err := ct.fr.ReadFrame()
	if err != nil {
		ct.t.Errorf("error reading initial settings frame from client: %v", err)
	}
	_, ok := frame.(*http2.SettingsFrame)
	if !ok {
		ct.t.Errorf("initial frame sent from client is not a settings frame, type %T", frame)
	}
}

// step 3 of http2 requirement?
func (ct *clientTester) writeSettingsFrame() {
	if err := ct.fr.WriteSettings(); err != nil {
		ct.t.Fatalf("Error writing initial SETTINGS frame from client to server: %v", err)
	}
}

// expecting frames wantxframes
func (ct *clientTester) writeSettingsAck() {
	if err := ct.fr.WriteSettingsAck(); err != nil {
		ct.t.Fatalf("Error writing ACK of client's SETTINGS: %v", err)
	}
}

// this is my only goal - serialized sending of frames, don't couple everything
// to the http2 transport's layer/implementation. basic conforming to http2
func (ct *clientTester) writeGoAway(maxStreamID uint32, code http2.ErrCode, debugData []byte/*maxStreamID uint32, code ErrCode, debugData []byte?*/) {
	if err := ct.fr.WriteGoAway(maxStreamID, code, debugData/*maxStreamID uint32, code ErrCode, debugData []byte*/); err != nil {
		ct.t.Fatalf("Error writing GOAWAY: %v", err)
	}
}

// more functions in the future to scale this up




// use this, what is the concurrent thing that needs to be happening?

// the concurrent thing is the creation of streams - which in e2e isn't done by
// client.NewStream, but by the sending of RPC's on the created client after it
// establishes the HTTP2 connection, and just the HTTP2 connection. wait for like 5 RPC's to send
// and send stream id at third I think

// create a client (this should speak HTTP2, then have it create streams)

// see how server tester is used in it's e2e test

// client side: normal service/RPCs called on that service
// server side: accepted conn (with a service), but
// "running the service but with a knob on the conn object to pass to this constructor