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
	"bytes"
	gcplogging "cloud.google.com/go/logging"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/grpcutil"
	"strings"
	"time"

	"github.com/google/uuid"
	binlogpb "google.golang.org/grpc/binarylog/grpc_binarylog_v1"
	iblog "google.golang.org/grpc/internal/binarylog"
)

var lExporter loggingExporter

var newLoggingExporter = newCloudLoggingExporter

// translateMetadata translates the metadata from Binary Logging format to
// its GrpcLogEntry equivalent.
func translateMetadata(m *binlogpb.Metadata) map[string]string {
	metadata := make(map[string]string)
	for _, entry := range m.GetEntry() {
		entryKey := entry.GetKey()
		var newVal string
		if strings.HasSuffix(entryKey, "-bin") { // bin header
			base64.StdEncoding.EncodeToString(entry.GetValue()) // I don't need to decode anything right?
		} else { // normal header
			newVal = string(entry.GetValue())
		}
		var oldVal string
		var ok bool
		if oldVal, ok = metadata[entryKey]; !ok {
			metadata[entryKey] = newVal
			continue
		}
		metadata[entryKey] = oldVal + "," + newVal
	}
	return metadata
}

func setPeerIfPresent(binlogEntry *binlogpb.GrpcLogEntry, grpcLogEntry *grpcLogEntry) {
	if binlogEntry.GetPeer() != nil {
		grpcLogEntry.Peer.Type = Type(binlogEntry.GetPeer().GetType())
		grpcLogEntry.Peer.Address = binlogEntry.GetPeer().GetAddress()
		grpcLogEntry.Peer.IpPort = binlogEntry.GetPeer().GetIpPort()
	}
}

var loggerTypeToEventLogger = map[binlogpb.GrpcLogEntry_Logger]Logger{
	binlogpb.GrpcLogEntry_LOGGER_UNKNOWN: LoggerUnknown,
	binlogpb.GrpcLogEntry_LOGGER_CLIENT:  Client,
	binlogpb.GrpcLogEntry_LOGGER_SERVER:  Server,
}

// figuring out what types are exported and what aren't -> determines what comments to write,
// so write comments at the end?

// all these enums and types - pull off Lidi's comments
// ClusterType is the type of cluster from a received CDS response.
type EventType int

const ( // these need to marshal, so I think you need to export type? What is least amount of exported things to make json.Marshal work
	// ClusterTypeEDS represents the EDS cluster type, which will delegate endpoint
	// discovery to the management server.
	EventTypeUnknown EventType = iota
	// ClusterTypeLogicalDNS represents the Logical DNS cluster type, which essentially
	// maps to the gRPC behavior of using the DNS resolver with pick_first LB policy.
	ClientHeader
	// ClusterTypeAggregate represents the Aggregate Cluster type, which provides a
	// prioritized list of clusters to use. It is used for failover between clusters
	// with a different configuration.
	ServerHeader
	ClientMessage
	ServerMessage
	ClientHalfClose
	ServerTrailer
	Cancel
)

func (t EventType) MarshalJSON() ([]byte, error) {
	// This gets called every logging call, this is slow? want it string.Builder? ugh
	buffer := bytes.NewBufferString(`"`)
	switch t {
	case EventTypeUnknown:
		buffer.WriteString("") // fill out once Eric responds
	case ClientHeader:
		buffer.WriteString("")
	case ServerHeader:
		buffer.WriteString("")
	case ClientMessage:
		buffer.WriteString("")
	case ServerMessage:
		buffer.WriteString("")
	case ClientHalfClose:
		buffer.WriteString("")
	case ServerTrailer:
		buffer.WriteString("")
	case Cancel:
		buffer.WriteString("")
	}
	buffer.WriteString(`"`)
	return buffer.Bytes(), nil
}

// I don't think you need UnmarshalJSON for anything? Cloud Logging should just
// take it into a json string, it doesn't need the type passed in.

type Logger int

const (
	LoggerUnknown Logger = iota
	Client
	Server
)

func (t Logger) MarshalJSON() ([]byte, error) {
	// This gets called every logging call, this is slow? want it string.Builder? ugh
	buffer := bytes.NewBufferString(`"`)
	switch t {
	case LoggerUnknown:
		buffer.WriteString("") // fill out once Eric responds
	case Client:
		buffer.WriteString("")
	case Server:
		buffer.WriteString("")
	}
	buffer.WriteString(`"`)
	return buffer.Bytes(), nil
}

type Payload struct {
	Metadata map[string]string `json:"metadata,omitempty"`
	// I've heard so much shit about Duration, and how complicated it is
	// is this the correct one?
	Timeout time.Duration `json:"timeout,omitempty"` // seems like only place where you might need special logic for MarshalJSON here, time.Duration...
	StatusCode uint32 `json:"statusCode,omitempty"`
	StatusMessage string `json:"statusMessage,omitempty"`
	StatusDetails []byte `json:"statusMessage,omitempty"` // bytes in proto, is this the go type this corresponds to?, I've seen/done this conversion before
	MessageLength uint32 `json:"messageLength,omitempty"`
	Message []byte `json:"message,omitempty"`
}

type Type int

const (
	TypeUnknown Type = iota
	IPV4 // `json:"TYPE_IPV4"`
	IPV6 // `json:"TYPE_IPV6"`
	UNIX // `json:"TYPE_UNIX"`
)

func (t Type) MarshalJSON() ([]byte, error) {
	// This gets called every logging call, this is slow? want it string.Builder? ugh
	buffer := bytes.NewBufferString(`"`)
	switch t {
	case TypeUnknown:
		buffer.WriteString("") // fill out once Eric responds, what to do for unknowns
	case IPV4:
		buffer.WriteString("TYPE_IPV4")
	case IPV6:
		buffer.WriteString("TYPE_IPV6")
	case UNIX:
		buffer.WriteString("TYPE_UNIX")
	}
	buffer.WriteString(`"`)
	return buffer.Bytes(), nil
}

type Address struct {
	Type Type `json:"type,omitempty"`
	Address string `json:"address,omitempty"`
	IpPort uint32 `json:"ipPort,omitempty"`
}

type grpcLogEntry struct { // exported or unexported? I think the fields do, wb types?
	// I think these need to be exported for json.Marshal to work
	CallId string `json:"callId,omitempty"`
	SequenceID uint64 `json:"sequenceId,omitempty"`
	Type EventType `json:"type,omitempty"`
	Logger Logger `json:"logger,omitempty"`

	Payload Payload `json:"payload,omitempty"`
	PayloadTruncated bool `json:"payloadTruncated,omitempty"`
	Peer Address `json:"peer,omitempty"`

	Authority string `json:"authority,omitempty"`
	ServiceName string `json:"serviceName,omitempty"`
	MethodName string `json:"methodName,omitempty"`
} // json annotations

// right, because you just pass in this struct into cloud logging and they
// handle calling this which emits raw json
func (gle *grpcLogEntry) MarshalJSON() ([]byte, error) {
	// A thought I just thought of: the enums, which are ints, need to have MarshalJSON defined on them,
	// similarly to DiscoveryMechanismType



	/*
	or something that marshals via the encoding/json package to a JSON object
	(and not any other type of JSON value).
	*/
	// json object or []byte which is what it says it returns from this function....

	// ex: For discovery mechanism type, makes a new buffer string and returns the bytes of that "EDS" -> bytes
	// Seems like major complication here is going to be when/how do I call enums wrt their JSON values?

	// Ends up as "{with the full config filled out}" not invalid like {5] this is the end result find something in the codebase that can give you an example of this.
	// anyway, just pass in your struct into Payload and make sure your struct has a MarshalJSON method on it and you're good!
	// json.Marshal()
	// return []byte(fmt.Sprintf(`[{%q: %s}]`, bc.Name, c)), nil
	return json.Marshal(gle) // no special logic here right, do I even need this? I think I'm done with MarshalJSON
}

// Just the functionality I need
type methodLoggerBuilder interface {
	Build(iblog.LogEntryConfig) *binlogpb.GrpcLogEntry // keep it lean, only define what you need
}

type binaryMethodLogger struct {
	callID, serviceName, methodName string

    mlb                          methodLoggerBuilder
    exporter                       loggingExporter
}

// Need to get metrics small PR out*

func (bml *binaryMethodLogger) Log(c iblog.LogEntryConfig) {
	// it'll always have if present, no need to check for nil
	binLogEntry := bml.mlb.Build(c)

	grpcLogEntry := &grpcLogEntry{
		CallId:     bml.callID,
		SequenceID: binLogEntry.GetSequenceIdWithinCall(),
		Logger:     loggerTypeToEventLogger[binLogEntry.Logger], // convert to right type wrt map with new struct as schema, do I really need this map
		// LogLevel is hardcoded and deleted, just hardcode it in cloud logging???
	}

	switch binLogEntry.GetType() { // this binary log pb mechanism is going to be kept regardless
	// the specific stream operations
	// I might have to do more logic here vs. Lidi's logic - do a pass on this to triage
	case binlogpb.GrpcLogEntry_EVENT_TYPE_UNKNOWN:
		grpcLogEntry.Type = EventTypeUnknown
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CLIENT_HEADER:
		grpcLogEntry.Type = ClientHeader
		if binLogEntry.GetClientHeader() != nil {
			methodName := binLogEntry.GetClientHeader().MethodName
			// Example method name: /grpc.testing.TestService/UnaryCall
			if strings.Contains(methodName, "/") {
				tokens := strings.Split(methodName, "/")
				if len(tokens) == 3 { // the first token is an empty string lol
					// Record service name and method name for all events. I think this still stays the same
					bml.serviceName = tokens[1]
					bml.methodName = tokens[2]
				} else {
					logger.Infof("Malformed method name: %v", methodName)
				}
			}
			grpcLogEntry.Payload.Timeout = binLogEntry.GetClientHeader().GetTimeout().AsDuration() // is this the right way to go?
			grpcLogEntry.Authority = binLogEntry.GetClientHeader().Authority
			grpcLogEntry.Payload.Metadata = translateMetadata(binLogEntry.GetClientHeader().GetMetadata())
		}
		grpcLogEntry.PayloadTruncated = binLogEntry.GetPayloadTruncated()
		setPeerIfPresent(binLogEntry, grpcLogEntry)
	case binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_HEADER:
		grpcLogEntry.Type = ServerHeader
		if binLogEntry.GetServerHeader() != nil {
			grpcLogEntry.Payload.Metadata = translateMetadata(binLogEntry.GetServerHeader().GetMetadata())
		}
		grpcLogEntry.PayloadTruncated = binLogEntry.GetPayloadTruncated()
		setPeerIfPresent(binLogEntry, grpcLogEntry)
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CLIENT_MESSAGE:
		grpcLogEntry.Type = ClientMessage
		grpcLogEntry.Payload.Message = binLogEntry.GetMessage().GetData()
		grpcLogEntry.Payload.MessageLength = binLogEntry.GetMessage().GetLength()
		grpcLogEntry.PayloadTruncated = binLogEntry.GetPayloadTruncated()
	case binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_MESSAGE:
		grpcLogEntry.Type = ServerMessage
		grpcLogEntry.Payload.Message = binLogEntry.GetMessage().GetData()
		grpcLogEntry.Payload.MessageLength = binLogEntry.GetMessage().GetLength()
		grpcLogEntry.PayloadTruncated = binLogEntry.GetPayloadTruncated()
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CLIENT_HALF_CLOSE:
		grpcLogEntry.Type = ClientHalfClose
	case binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_TRAILER:
		grpcLogEntry.Type = ServerTrailer
		grpcLogEntry.Payload.Metadata = translateMetadata(binLogEntry.GetTrailer().Metadata)
		grpcLogEntry.Payload.StatusCode = binLogEntry.GetTrailer().GetStatusCode()
		grpcLogEntry.Payload.StatusMessage = binLogEntry.GetTrailer().GetStatusMessage()
		grpcLogEntry.Payload.StatusDetails = binLogEntry.GetTrailer().GetStatusDetails()
		grpcLogEntry.PayloadTruncated = binLogEntry.GetPayloadTruncated()
		setPeerIfPresent(binLogEntry, grpcLogEntry)
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CANCEL:
		grpcLogEntry.Type = Cancel
	default:
		logger.Infof("Unknown event type: %v", binLogEntry.Type)
		return
	}
	// changes from binary logging entry
	// 1. Repeat method and authority on each log entry; for routing? this doesn't seem to be a part of Lidi's ***

	// 2. Separate method and service into independent fields; for easier routing (done)
	grpcLogEntry.ServiceName = bml.serviceName
	grpcLogEntry.MethodName = bml.methodName

	gcploggingEntry := gcplogging.Entry{
		Timestamp: binLogEntry.GetTimestamp().AsTime(),
		Severity:  100,
		Payload:   grpcLogEntry, // internal struct that marshals to a json object
		// Does anything else need to be stuck here? Eric said he'll explicitly define this
	}

	bml.exporter.EmitGcpLoggingEntry(gcploggingEntry) // test this layer
}

type eventConfig struct {
	ServiceMethod map[string]bool
	Services map[string]bool
	MatchAll bool

	// If true, won't log anything.
	Exclude bool
	HeaderBytes uint64
	MessageBytes uint64
}

type LoggerConfigObservability struct {
	EventConfigs []eventConfig
}

type binaryLogger struct {
	EventConfigs []eventConfig // pointer?
	exporter loggingExporter
}

func (bl *binaryLogger) GetMethodLogger(methodName string) iblog.MethodLogger {
	s, _, err := grpcutil.ParseMethod(methodName)
	if err != nil {
		logger.Infof("binarylogging: failed to parse %q: %v", methodName, err)
		return nil
	}
	for _, eventConfig := range bl.EventConfigs {
		if eventConfig.MatchAll || eventConfig.ServiceMethod[methodName] || eventConfig.Services[s] {
			if eventConfig.Exclude {
				return nil
			}

			return &binaryMethodLogger{
				exporter: bl.exporter,
				mlb: iblog.NewMethodLoggerImp(eventConfig.HeaderBytes, eventConfig.MessageBytes),
				callID: uuid.NewString(),
			}
		}
	}
	return nil
}

func registerClientRPCEvents(clientRPCEvents []clientRPCEvents, exporter loggingExporter) {
	if len(clientRPCEvents) == 0 {
		return
	}
	var eventConfigs []eventConfig
	for _, clientRPCEvent := range clientRPCEvents {
		eventConfig := eventConfig{} // do I want to make this a pointer?
		eventConfig.Exclude = clientRPCEvent.Exclude
		eventConfig.HeaderBytes = uint64(clientRPCEvent.MaxMetadataBytes)
		eventConfig.MessageBytes = uint64(clientRPCEvent.MaxMessageBytes)
		for _, method := range clientRPCEvent.Method {
			eventConfig.ServiceMethod = make(map[string]bool)
			eventConfig.Services = make(map[string]bool)
			if method == "*" {
				eventConfig.MatchAll = true
				continue
			}
			s, m, err := grpcutil.ParseMethod(method)
			if err != nil { // Shouldn't happen, already validated at this point.
				continue
			}
			if m == "*" {
				eventConfig.Services[s] = true
				continue
			}
			eventConfig.ServiceMethod[method] = true
		}
		eventConfigs = append(eventConfigs, eventConfig)
	}
	clientSideLogger := &binaryLogger{
		EventConfigs: eventConfigs,
		exporter: exporter,
	}
	internal.AddGlobalDialOptions.(func(opt ...grpc.DialOption))(grpc.WithBinaryLogger(clientSideLogger))
}

func registerServerRPCEvents(serverRPCEvents []serverRPCEvents, exporter loggingExporter) {
	if len(serverRPCEvents) == 0 {
		return
	}
	var eventConfigs []eventConfig
	for _, serverRPCEvent := range serverRPCEvents {
		eventConfig := eventConfig{} // do I want to make this a pointer?
		eventConfig.Exclude = serverRPCEvent.Exclude
		eventConfig.HeaderBytes = uint64(serverRPCEvent.MaxMetadataBytes)
		eventConfig.MessageBytes = uint64(serverRPCEvent.MaxMessageBytes)
		for _, method := range serverRPCEvent.Method {
			eventConfig.ServiceMethod = make(map[string]bool)
			eventConfig.Services = make(map[string]bool)
			if method == "*" {
				eventConfig.MatchAll = true
				continue
			}
			s, m, err := grpcutil.ParseMethod(method)
			if err != nil { // Shouldn't happen, already validated at this point.
				continue
			}
			if m == "*" {
				eventConfig.Services[s] = true
				continue
			}
			eventConfig.ServiceMethod[method] = true
		}
		eventConfigs = append(eventConfigs, eventConfig)
	}
	serverSideLogger := &binaryLogger{
		EventConfigs: eventConfigs,
		exporter: exporter,
	}
	internal.AddGlobalServerOptions.(func(opt ...grpc.ServerOption))(grpc.BinaryLogger(serverSideLogger))
}

func startLogging(ctx context.Context, config *config) error {
	if config == nil || config.CloudLogging == nil {
		return nil
	}
	var err error
	lExporter, err = newLoggingExporter(ctx, config) // mock this function, make this function global is how you inject, orthogonal to the problem for how you close
	if err != nil {
		return fmt.Errorf("unable to create CloudLogging exporter: %v", err)
	}

	cl := config.CloudLogging
	registerClientRPCEvents(cl.ClientRPCEvents, lExporter) // one exporter for client and server
	registerServerRPCEvents(cl.ServerRPCEvents, lExporter) // these don't return errors
	return nil
}

func stopLogging() {
	// Do we want to do this unconditionally? Unregistering? I feel like there's no harm calling this unconditionally, no nil checks or anything.
	internal.ClearGlobalDialOptions() // this happen as a part of this logging, and also as a part of opencensus. Do we want to move this to a shared cleanup? After testing, you can play around with what works and not
	internal.ClearGlobalServerOptions()
	// flush some type of buffer from design review
	// how to get ref to clear the buffer

	// do I need to close/cleanup everything in the client and server
	// binaryloggers as well? Do we need client/server? master just uses to
	// clean up exporter. If so, you need to hold a ref for it in this function
	// (master is global)

	// exporter.Close() <- single exporter, shared amongst client and server,
	// need to hold a ref, also this is important because this is what we're
	// mocking/hooking in for testing. How does it mock/hook in on master?

	// Close() is a function ON the logger. That's how it has a ref.
	// Globally exporter to mock/cleanup? exporter is shared amongst two and cleaned up at end

	if lExporter != nil {
		lExporter.Close()
	} // also needs a Close() to flush buffer, Lidi handled that for you (eric talked about how we need to flush buffer at the end), and we will lose data when logging buffer is full, non blocking

}

// server side: configure with some methods,
// will need to match this iterative list somehow
// No preprocessing

// One of the nodes is a never log node, should cause it to not log at all

// You want a single config or multiple cases

// the config configures both

// client             server

// this is specified in JSON


// Task list I can see:

// 1. Rewrite binary logger GetMethodLogger to be exported, reuse that logic by sticking as child of MethodLogger in o11y

// 2. Change the internal logging schema to an internal struct, this will have all the layerings of JSON
//          just need to finish EmitGrpcLogRecord

// 3. Rewrite the EmitGrpcLogRecord(gcplogging.Entry) rather than o11y proto (done, but need to test)
//          gcplogging.Entry <- populated with switched internal struct that defines MarshalJSON, cloudlogging library will then handle this for you

// 4. Test :D!


// Need to change his configs to new configs, new configs are tied to my test cases/overall idea of how to test this
//

// HookPoint EmitGrpcLoggingEntry() <- the downstream behaviors are all tied to this