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
	"google.golang.org/grpc"
	"google.golang.org/grpc/internal"
	"google.golang.org/grpc/internal/grpcutil"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/google/uuid"
	binlogpb "google.golang.org/grpc/binarylog/grpc_binarylog_v1"
	grpclogrecordpb "google.golang.org/grpc/gcp/observability/internal/logging"
	iblog "google.golang.org/grpc/internal/binarylog"
)

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
}

func setPeerIfPresent(binlogEntry *binlogpb.GrpcLogEntry, grpcLogRecord *grpclogrecordpb.GrpcLogRecord) {
	if binlogEntry.GetPeer() != nil {
		grpcLogRecord.PeerAddress = &grpclogrecordpb.GrpcLogRecord_Address{
			Type:    grpclogrecordpb.GrpcLogRecord_Address_Type(binlogEntry.Peer.Type),
			Address: binlogEntry.Peer.Address,
			IpPort:  binlogEntry.Peer.IpPort,
		}
	}
}

var loggerTypeToEventLogger = map[binlogpb.GrpcLogEntry_Logger]Logger{
	binlogpb.GrpcLogEntry_LOGGER_UNKNOWN: LoggerUnknown,
	binlogpb.GrpcLogEntry_LOGGER_CLIENT:  Client,
	binlogpb.GrpcLogEntry_LOGGER_SERVER:  Server,
}
/*
type binaryMethodLogger struct {
	rpcID, serviceName, methodName string
	originalMethodLogger           iblog.MethodLogger
	childMethodLogger              iblog.MethodLogger
	exporter                       loggingExporter
}

func (ml *binaryMethodLogger) Log(c iblog.LogEntryConfig) {
	// Invoke the original MethodLogger to maintain backward compatibility
	if ml.originalMethodLogger != nil {
		ml.originalMethodLogger.Log(c)
	}

	// Fetch the compiled binary logging log entry
	if ml.childMethodLogger == nil {
		logger.Info("No wrapped method logger found")
		return
	}
	var binlogEntry *binlogpb.GrpcLogEntry
	o, ok := ml.childMethodLogger.(interface {
		Build(iblog.LogEntryConfig) *binlogpb.GrpcLogEntry
	})
	if !ok {
		logger.Error("Failed to locate the Build method in wrapped method logger")
		return
	}
	binlogEntry = o.Build(c)

	// Translate to GrpcLogRecord
	grpcLogRecord := &grpclogrecordpb.GrpcLogRecord{
		Timestamp:   binlogEntry.GetTimestamp(),
		RpcId:       ml.rpcID,
		SequenceId:  binlogEntry.GetSequenceIdWithinCall(),
		EventLogger: loggerTypeToEventLogger[binlogEntry.Logger],
		// Making DEBUG the default LogLevel
		LogLevel: grpclogrecordpb.GrpcLogRecord_LOG_LEVEL_DEBUG,
	}

	switch binlogEntry.GetType() {
	case binlogpb.GrpcLogEntry_EVENT_TYPE_UNKNOWN:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_GRPC_CALL_UNKNOWN
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CLIENT_HEADER:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_GRPC_CALL_REQUEST_HEADER
		if binlogEntry.GetClientHeader() != nil {
			methodName := binlogEntry.GetClientHeader().MethodName
			// Example method name: /grpc.testing.TestService/UnaryCall
			if strings.Contains(methodName, "/") {
				tokens := strings.Split(methodName, "/")
				if len(tokens) == 3 {
					// Record service name and method name for all events
					ml.serviceName = tokens[1]
					ml.methodName = tokens[2]
				} else {
					logger.Infof("Malformed method name: %v", methodName)
				}
			}
			grpcLogRecord.Timeout = binlogEntry.GetClientHeader().Timeout
			grpcLogRecord.Authority = binlogEntry.GetClientHeader().Authority
			grpcLogRecord.Metadata = translateMetadata(binlogEntry.GetClientHeader().Metadata)
		}
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
		setPeerIfPresent(binlogEntry, grpcLogRecord)
	case binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_HEADER:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_GRPC_CALL_RESPONSE_HEADER
		grpcLogRecord.Metadata = translateMetadata(binlogEntry.GetServerHeader().Metadata)
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
		setPeerIfPresent(binlogEntry, grpcLogRecord)
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CLIENT_MESSAGE:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_GRPC_CALL_REQUEST_MESSAGE
		grpcLogRecord.Message = binlogEntry.GetMessage().GetData()
		grpcLogRecord.PayloadSize = binlogEntry.GetMessage().GetLength()
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
	case binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_MESSAGE:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_GRPC_CALL_RESPONSE_MESSAGE
		grpcLogRecord.Message = binlogEntry.GetMessage().GetData()
		grpcLogRecord.PayloadSize = binlogEntry.GetMessage().GetLength()
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CLIENT_HALF_CLOSE:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_GRPC_CALL_HALF_CLOSE
	case binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_TRAILER:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_GRPC_CALL_TRAILER
		grpcLogRecord.Metadata = translateMetadata(binlogEntry.GetTrailer().Metadata)
		grpcLogRecord.StatusCode = binlogEntry.GetTrailer().GetStatusCode()
		grpcLogRecord.StatusMessage = binlogEntry.GetTrailer().GetStatusMessage()
		grpcLogRecord.StatusDetails = binlogEntry.GetTrailer().GetStatusDetails()
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
		setPeerIfPresent(binlogEntry, grpcLogRecord)
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CANCEL:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_GRPC_CALL_CANCEL
	default:
		logger.Infof("Unknown event type: %v", binlogEntry.Type)
		return
	}
	grpcLogRecord.ServiceName = ml.serviceName
	grpcLogRecord.MethodName = ml.methodName
	ml.exporter.EmitGrpcLogRecord(grpcLogRecord)
}

func validateExistingMethodLoggerConfig(existing *iblog.MethodLoggerConfig, filter logFilter) bool {
	// In future, we could add more validations. Currently, we only check if the
	// new filter configs are different than the existing one, if so, we log a
	// warning.
	if existing != nil && (existing.Header != uint64(filter.HeaderBytes) || existing.Message != uint64(filter.MessageBytes)) {
		logger.Warningf("Ignored log_filter config: %+v", filter)
	}
	return existing == nil
}

func createBinaryLoggerConfig(filters []logFilter) iblog.LoggerConfig {
	config := iblog.LoggerConfig{
		Services: make(map[string]*iblog.MethodLoggerConfig),
		Methods:  make(map[string]*iblog.MethodLoggerConfig),
	}
	// Try matching the filters one by one, pick the first match. The
	// correctness of the log filter pattern is ensured by config.go.
	for _, filter := range filters {
		if filter.Pattern == "*" {
			// Match a "*"
			if !validateExistingMethodLoggerConfig(config.All, filter) {
				continue
			}
			config.All = &iblog.MethodLoggerConfig{Header: uint64(filter.HeaderBytes), Message: uint64(filter.MessageBytes)}
			continue
		}
		tokens := strings.SplitN(filter.Pattern, "/", 2)
		filterService := tokens[0]
		filterMethod := tokens[1]
		if filterMethod == "*" {
			// Handle "p.s/*" case
			if !validateExistingMethodLoggerConfig(config.Services[filterService], filter) {
				continue
			}
			config.Services[filterService] = &iblog.MethodLoggerConfig{Header: uint64(filter.HeaderBytes), Message: uint64(filter.MessageBytes)}
			continue
		}
		// Exact match like "p.s/m"
		if !validateExistingMethodLoggerConfig(config.Methods[filter.Pattern], filter) {
			continue
		}
		config.Methods[filter.Pattern] = &iblog.MethodLoggerConfig{Header: uint64(filter.HeaderBytes), Message: uint64(filter.MessageBytes)}
	}
	return config
}

// start is the core logic for setting up the custom binary logging logger, and
// it's also useful for testing.
func (l *binaryLogger) start(config *config, exporter loggingExporter) error {
	filters := config.LogFilters
	if len(filters) == 0 || exporter == nil {
		// Doing nothing is allowed
		if exporter != nil {
			// The exporter is owned by binaryLogger, so we should close it if
			// we are not planning to use it.
			exporter.Close()
		}
		logger.Info("Skipping gRPC Observability logger: no config")
		return nil
	}

	binLogger := iblog.NewLoggerFromConfig(createBinaryLoggerConfig(filters))
	if binLogger != nil {
		atomic.StorePointer(&l.logger, unsafe.Pointer(&binLogger))
	}
	atomic.StorePointer(&l.exporter, unsafe.Pointer(&exporter))
	logger.Info("Start gRPC Observability logger")
	return nil
}

func (l *binaryLogger) Start(ctx context.Context, config *config) error {
	if config == nil || !config.EnableCloudLogging {
		return nil
	}
	if config.DestinationProjectID == "" {
		return fmt.Errorf("failed to enable CloudLogging: empty destination_project_id")
	}
	exporter, err := newCloudLoggingExporter(ctx, config)
	if err != nil {
		return fmt.Errorf("unable to create CloudLogging exporter: %v", err)
	}
	l.start(config, exporter)
	return nil
}

func newBinaryLogger(iblogger iblog.Logger) *binaryLogger {
	return &binaryLogger{
		originalLogger: iblogger,
	}
}

var defaultLogger *binaryLogger

func prepareLogging() {
	defaultLogger = newBinaryLogger(iblog.GetLogger())
	iblog.SetLogger(defaultLogger)
}*/

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

type Logger int

const (
	LoggerUnknown Logger = iota
	Client
	Server
)

type Payload struct {
	Metadata map[string]string `json:"metadata,omitempty"`
	// I've heard so much shit about Duration, and how complicated it is
	// is this the correct one?
	Timeout time.Duration `json:"timeout,omitempty"`
	StatusCode uint32 `json:"statusCode,omitempty"`
	StatusMessage string `json:"statusMessage,omitempty"`
	StatusDetails []byte `json:"statusMessage,omitempty"` // bytes in proto, is this the go type this corresponds to?, I've seen/done this conversion before
	MessageLength uint32 `json:"messageLength,omitempty"`
	Message []byte `json:"message,omitempty"`
}

type Type int

const (
	TypeUnknown Type = iota
	IPV4
	IPV6
	UNIX
)

type Address struct {
	Type Type `json:"type,omitempty"`
	Address string `json:"address,omitempty"`
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
	/*
	or something that marshals via the encoding/json package to a JSON object
	(and not any other type of JSON value).
	*/
}

// Just the functionality I need
type methodLoggerBuilder interface {
	Build(iblog.LogEntryConfig) *binlogpb.GrpcLogEntry // keep it lean, only define what you need
}

type binaryMethodLogger struct {
	// s rpcID to callID?
	rpcID, serviceName, methodName string
    // childMethodLogger - do we want this? we're reusing the logic for build, but we don't want to call into package...
    // Logs an operation something that implements toproto, and handles message/header bytes

    // toProto -> grpcBinLog -> chopping off headers is the logic we're reusing
    // in the binary logging package - configured only with the knobs h and b

    // If you export the concrete type, do you just need the method
    // exported (already done) or the type exported?

    // If so, just export method and try that out, I will run into that in tests

    mlb                          methodLoggerBuilder/*Only the interface that I need*/
    exporter                       loggingExporter
}

// Outlier Detection, metrics small PR, gcp next blog post consistent with
// Documentation Oct 11, only 10 users anyway

func (bml *binaryMethodLogger) Log(c iblog.LogEntryConfig) {
	// json.Marshaler()
	// The question here comes from (stream operation -> (conversion + chopping
	// off header and message bytes) binary log proto) -> o11y grpcLogRecord

	// The parenthesis are currently happening (and reason we need to import iblog.LogEntryConfig) in internal/binlog

	// Reuses logic configured through old path, calls Log() with same data and
	// same way on same operations as previous, I think this is fineeee, logical
	// reusablity


	// what we want to convert to, keep it as is against master,
	// fork the changes later
	// parenthesis is what's going on in binary logger package, reused with binary logging itself - why we're exporting in first place
	// s from (iblog.LogEntryConfig -> binlogpb.GrpcLogEntry) -> grpclogrecordpb.GrpcLogRecord
	// to     (iblog.LogEntryConfig -> binlogpb.GrpcLogEntry) -> struct representing Eric's new schema which can marshal to JSON
	binlogEntry := &binlogpb.GrpcLogEntry{}


	grpcLogRecord := &grpcLogEntry{
		CallId:     bml.rpcID, // uuid generated at GetMethodLogger time
		SequenceID: binlogEntry.GetSequenceIdWithinCall(),
		Logger:     loggerTypeToEventLogger[binlogEntry.Logger], // convert to right type wrt map with new struct as schema
		// LogLevel is hardcoded and deleted, just hardcode it in cloud logging???
	}

	switch binlogEntry.GetType() { // this binary log pb mechanism is going to be kept regardless
	// the specific stream operations
	// I might have to do more logic here vs. Lidi's logic - do a pass on this to triage
	case binlogpb.GrpcLogEntry_EVENT_TYPE_UNKNOWN:
		grpcLogRecord.Type = EventTypeUnknown
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CLIENT_HEADER:
		grpcLogRecord.Type = ClientHeader
		if binlogEntry.GetClientHeader() != nil {
			methodName := binlogEntry.GetClientHeader().MethodName
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
			grpcLogRecord.Payload.Timeout = binlogEntry.GetClientHeader().GetTimeout().AsDuration() // is this the right way to go?
			grpcLogRecord.Authority = binlogEntry.GetClientHeader().Authority
			grpcLogRecord.Payload.Metadata = translateMetadata(binlogEntry.GetClientHeader().GetMetadata())/*new conversion I wrote - pull from my other PR*/
		}
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
		// setPeerIfPresent() - how does this map?
	case binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_HEADER:
		grpcLogRecord.Type = ServerHeader
		if binlogEntry.GetServerHeader() != nil {
			grpcLogRecord.Payload.Metadata = translateMetadata(binlogEntry.GetServerHeader().GetMetadata())
		}
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
		// setPeerIfPresent() - how does this map?
	// ugh also need to do exported methods and OD stuff
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CLIENT_MESSAGE:
		grpcLogRecord.Type = ClientMessage
		grpcLogRecord.Payload.Message = binlogEntry.GetMessage().GetData() // so it is []bytes
		grpcLogRecord.Payload.MessageLength = binlogEntry.GetMessage().GetLength()
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
	case binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_MESSAGE:
		grpcLogRecord.Type = ServerMessage
		grpcLogRecord.Payload.Message = binlogEntry.GetMessage().GetData() // so it is []bytes
		grpcLogRecord.Payload.MessageLength = binlogEntry.GetMessage().GetLength()
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CLIENT_HALF_CLOSE:
		grpcLogRecord.Type = ClientHalfClose
	case binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_TRAILER:
		grpcLogRecord.Type = ServerTrailer
		grpcLogRecord.Payload.Metadata = translateMetadata(binlogEntry.GetTrailer().Metadata)
		grpcLogRecord.Payload.StatusCode = binlogEntry.GetTrailer().GetStatusCode()
		grpcLogRecord.Payload.StatusMessage = binlogEntry.GetTrailer().GetStatusMessage()
		grpcLogRecord.Payload.StatusDetails = binlogEntry.GetTrailer().GetStatusDetails()
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
		// setPeerIfPresent() - how does this map?
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CANCEL:
		grpcLogRecord.Type = Cancel
	default:
		logger.Infof("Unknown event type: %v", binlogEntry.Type)
		return
	}
	// changes from binary logging entry
	// 1. Repeat method and authority on each log entry; for routing? this doesn't seem to be a part of Lidi's ***

	// 2. Separate method and service into independent fields; for easier routing
	grpcLogRecord.ServiceName = bml.serviceName // where are these written
	grpcLogRecord.MethodName = bml.methodName

	// what is the thing that eric was talking about wrt hardcoding for each
	// entry?

	// same flow with updated logic from eric's schema such as bin headers, i had already thought about this some

	// take grpcLogRecord and build a cloudlogging entry
	// to send to cloud logging, intercept that call to verify
	// expected downstream effects of system
	// binlogEntry.Timestamp // populate cloud logging entry with this instead
	// stick internal struct in there, is there anything else I need to put in there

	gcploggingEntry := gcplogging.Entry{
		Timestamp: binlogEntry.GetTimestamp().AsTime(),
		Severity: 100,
		Payload: grpcLogRecord,
	}

	bml.exporter.EmitGrpcLogRecordd(gcploggingEntry) // test this layer
}

type eventConfig struct { // to check for equality - I like e2e style tests anyway?
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
	s, m, err := grpcutil.ParseMethod(methodName)
	if err != nil {
		logger.Infof("binarylogging: failed to parse %q: %v", methodName, err)
		return nil
	}
	for _, eventConfig := range bl.EventConfigs {
		if eventConfig.MatchAll || eventConfig.ServiceMethod["/" + s + "/" + m] || eventConfig.Services[s] {
			if eventConfig.Exclude {
				return nil
			}
			// wait we don't want to import that package anymore, is there any way to just
			// bring in that functionality.
			// operations on a stream
			// operation1.Log(an object that converts to proto)

			// previously := (second configured binary
			// logger).GetMethodLogger(), handled the calling of getMethodLogger
			// methodLogger with headers and bytes.

			// How do I change now then?
			// returns a concrete type? Maybe type it as a Builder to persist instead of child method logger
			return &binaryMethodLogger{
				exporter: bl.exporter,
				mlb: iblog.NewMethodLoggerImp(eventConfig.HeaderBytes, eventConfig.MessageBytes),
			}
		}
	}

	// rpcID (aka call ID) is just built as a uuid.NewString()
	// this gets attached to every log record emitted

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
			eventConfig.ServiceMethod[method] = true // called with /service/method anyway, so this is correct
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
			eventConfig.ServiceMethod[method] = true // called with /service/method anyway, so this is correct
		}
		eventConfigs = append(eventConfigs, eventConfig)
	}
	serverSideLogger := &binaryLogger{
		EventConfigs: eventConfigs,
		exporter: exporter,
	}
	internal.AddGlobalServerOptions.(func(opt ...grpc.ServerOption))(grpc.BinaryLogger(serverSideLogger)) // don't want to import binlog package right
}

func startLogging(ctx context.Context, config *config) error {
	if config == nil || config.CloudLogging == nil {
		return nil
	}
	exporter, err := newCloudLoggingExporter(ctx, config)
	if err != nil {
		return fmt.Errorf("unable to create CloudLogging exporter: %v", err)
	}

	cl := config.CloudLogging // what part of this config are we validating?
	// Goal: client side global DialOption registered - when does it actually need to register?
	registerClientRPCEvents(cl.ClientRPCEvents, exporter)
	// Goal: server side global NewServerOption registered
	registerServerRPCEvents(cl.ServerRPCEvents, exporter)
}

func stopLogging() {
	// Do we want to do this conditionally? Unregistering?
	internal.ClearGlobalDialOptions() // this happen as a part of this logging, and also as a part of opencensus. Do we want to move this?
	internal.ClearGlobalServerOptions()
	// flush some type of buffer from design review
}

// testing: do we do same thing and mock exporter?
// can't verify the results of EmitGrpcLogRecord() -> CloudLogging.Log()

// can do the same thing he did and check EmitGrpcLogRecord()...
// events, but then this would change over time esp with Eric's schema changes
// you're literally testing the emission of the o11y schema

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

// 3. Rewrite the EmitGrpcLogRecord(gcplogging.Entry) rather than o11y proto
//          gcplogging.Entry <- populated with switched internal struct that defines MarshalJSON, cloudlogging library will then handle this for you