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
	"context"
	"encoding/base64"
	"fmt"
	"google.golang.org/grpc/internal/grpcutil"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/google/uuid"
	binlogpb "google.golang.org/grpc/binarylog/grpc_binarylog_v1"
	grpclogrecordpb "google.golang.org/grpc/gcp/observability/internal/logging"
	iblog "google.golang.org/grpc/internal/binarylog"
)

// translateMetadata translates the metadata from Binary Logging format to
// its GrpcLogRecord equivalent.
func translateMetadata(m *binlogpb.Metadata) *grpclogrecordpb.GrpcLogRecord_Metadata {
	var res grpclogrecordpb.GrpcLogRecord_Metadata
	res.Entry = make([]*grpclogrecordpb.GrpcLogRecord_MetadataEntry, len(m.Entry))
	for i, e := range m.Entry {
		res.Entry[i] = &grpclogrecordpb.GrpcLogRecord_MetadataEntry{
			Key:   e.Key,
			Value: e.Value,
		}
		// assuming multiple values:
		// switch this to [k]:{"v", "v", "v"}
		// and binary headers: [k]:["string representing base64 of 010101", "string representing base64 of 010101", "string representing base64 of 010101"]
		// base64.RawStdEncoding.EncodeToString(bits) and base64.RawStdEncoding.DecodeString(str)
	}
	return &res
}

func translateMetadata2(m *binlogpb.Metadata) map[string]string {
	metadata := make(map[string]string)

	for _, entry := range m.GetEntry() {

		// if binary header (ends with -bin):
		// same thing, except take the 010101010101 and base64 encode.
		// still same thing, either create or append , base64 encoded (how to merge all these logic cleanly)

		// []byte -> string

		// entry.GetValue()

		// str := base64.StdEncoding.EncodeToString(entry.GetValue()) // do I ever even have to decode

		// str is the same node as string(entry.GetValue)

		// create a string from both paths, then helper or inline iteration for
		// metadata - run this through tests, this should work
		entryKey := entry.GetKey()
		var newVal string
		if strings.HasSuffix(entryKey, "-bin") { // bin header
			newVal = base64.StdEncoding.EncodeToString(entry.GetValue())
		} else { // normal header
			newVal = string(entry.GetValue())
		}

		var val string
		var ok bool
		if val, ok = metadata[entryKey]; !ok {
			metadata[entryKey] = newVal
			continue
		}
		// = old string + , + newstring
		// metadata[entry.GetKey()] = append(metadata[entry.GetKey()], ",")
		metadata[entryKey] = metadata[entryKey] + "," + val





		// can value be ""
		/*var value string
		var ok bool
		if value, ok = metadata[entry.GetKey()]; !ok {
			metadata[entry.GetKey()] = string(entry.GetValue()) // string([]byte) - is this correct?
			continue
		}
		// value append (+) ", entry.GetValue()"
		// Build out map using these keys and values, using the logic I outlined above
		entry.GetKey()
		entry.GetValue()
		*/
	}


	return metadata
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

var loggerTypeToEventLogger = map[binlogpb.GrpcLogEntry_Logger]grpclogrecordpb.GrpcLogRecord_Logger {
	binlogpb.GrpcLogEntry_LOGGER_UNKNOWN: grpclogrecordpb.GrpcLogRecord_UNKNOWN,
	binlogpb.GrpcLogEntry_LOGGER_CLIENT:  grpclogrecordpb.GrpcLogRecord_CLIENT,
	binlogpb.GrpcLogEntry_LOGGER_SERVER:  grpclogrecordpb.GrpcLogRecord_SERVER,
}

type binaryMethodLogger struct {
	rpcID, serviceName, methodName string
	originalMethodLogger           iblog.MethodLogger
	childMethodLogger              iblog.MethodLogger
	exporter                       loggingExporter
}

type binaryMethodLogger2 struct { // This also includes Eric's changes
	rpcID, serviceName, methodName string
	childMethodLogger iblog.MethodLogger // switch created function to exported
	exporter loggingExporter
}

// within this new proto: define this new proto, compile it, use that for populating? What is different or should it just be
// the same

func (ml *binaryMethodLogger) Log(c iblog.LogEntryConfig) {
	// Invoke the original MethodLogger to maintain backward compatibility
	if ml.originalMethodLogger != nil {
		ml.originalMethodLogger.Log(c) // binary log sink
	}

	// three things that implement the interfaces
	// 1. this wrapper wraps
	//     2. the original binary logging that writes to a sink
	//     3. the new observability...? crap that exports to an exporter

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
	// This is the exported Build binary log exported thing

	// sink.Write(o.Build(c)), this doesn't write to the sink, takes the
	// binLogEntry -> converts it to GrpcLogRecord

	binlogEntry = o.Build(c) // gets binary log entry from observability path, translates that
	// to o11y schema to export to cloud logging

	// Translate to GrpcLogRecord
	grpcLogRecord := &grpclogrecordpb.GrpcLogRecord{ // internal format - Doug wants me to switch this to JSON. The bin log stuff too?
		Timestamp:   binlogEntry.GetTimestamp(),
		RpcId:       ml.rpcID,
		SequenceId:  binlogEntry.GetSequenceIdWithinCall(),
		EventLogger: loggerTypeToEventLogger[binlogEntry.Logger],
		// Making DEBUG the default LogLevel
		LogLevel: grpclogrecordpb.GrpcLogRecord_LOG_LEVEL_DEBUG,
	}

	switch binlogEntry.GetType() { // gets the binlog format, converts it to grpcLogRecord
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
	ml.exporter.EmitGrpcLogRecord(grpcLogRecord) // export to stackdriver - part of wrapped, this is the extra functionality using bin logging infra, this function will have to change
}

func (ml *binaryMethodLogger2) Log(c iblog.LogEntryConfig) {
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

	grpcLogRecord := &grpclogrecordpb.GrpcLogRecord{

	}

	// See how the proto changes, then see if any logic compared
	// to what Lidi had changes based on Eric's document.
	grpcLogRecord := &grpclogrecordpb.GrpcLogRecord{ // internal format - Doug wants me to switch this to JSON. The bin log stuff too?
		// Timestamp:   binlogEntry.GetTimestamp(), // 1. we're getting rid of this, Timestamp for cloud logging, which is already there, use cloud logging timestamp instead
		CallId:       ml.rpcID, // is this correct? Both of these
		SequenceId:  binlogEntry.GetSequenceIdWithinCall(),
		EventLogger: loggerTypeToEventLogger[binlogEntry.Logger],
		// Making DEBUG the default LogLevel
		// LogLevel: grpclogrecordpb.GrpcLogRecord_LOG_LEVEL_DEBUG,
	}
	// 2. message Metadata -> inline repeated MetadataEntry

	// 3. Make metadata values string (in both bin logging and in o11y package
	// it's []byte s/ string), manually Base64 encode -bin headers. "Base 64
	// encode" I'm assuming means take the groups of characters make them bytes,
	// then take the representation of the bytes six bits each why it's string

	// string - lol -> 24 bits, six bits each with character set of 64 possibilities
	// 24 bits -> bG9s

	// 4. change call_id to UUID, is this what we already have?

	// 5. Repeat method and authority on each log entry, for routing

	// 6. Separate method and service into independent fields; for easier routing

	// 7. Rename "message" to "rpc_message", "message" means "log message"
	// (unnecessary if using message Payload later)

	// 8. Change "sequence_id_within_call" to "sequence_id" to save the bytes.

	// Right, so these heuristics are how Eric defined this conversion, just go ahead and do it.
	// and ending changed

	// Conversion here - pull from Lidi's


	// Compile new proto and put here and that'll make it easier


	// Are these different from what Lidi has?


	// rewrite this to be cleaner
	switch binlogEntry.GetType() { // gets the binlog format, converts it to grpcLogRecord
	case binlogpb.GrpcLogEntry_EVENT_TYPE_UNKNOWN:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_NONE
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CLIENT_HEADER:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_CLIENT_HEADER
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
			// why is this complicated?
			grpcLogRecord.Payload.Timeout = binlogEntry.GetClientHeader().Timeout /// did I delete this?
			grpcLogRecord.Authority = binlogEntry.GetClientHeader().Authority
			grpcLogRecord.Payload.Metadata = translateMetadata2(binlogEntry.GetClientHeader().Metadata)
		}
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
		setPeerIfPresent(binlogEntry, grpcLogRecord)
	case binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_HEADER:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_SERVER_HEADER
		grpcLogRecord.Payload.Metadata = translateMetadata2(binlogEntry.GetServerHeader().Metadata)
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
		setPeerIfPresent(binlogEntry, grpcLogRecord)
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CLIENT_MESSAGE:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_CLIENT_MESSAGE
		grpcLogRecord.Payload.Message = binlogEntry.GetMessage().GetData()
		grpcLogRecord.PayloadSize = binlogEntry.GetMessage().GetLength()
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
	case binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_MESSAGE:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_SERVER_MESSAGE
		grpcLogRecord.Payload.Message = binlogEntry.GetMessage().GetData()
		grpcLogRecord.PayloadSize = binlogEntry.GetMessage().GetLength()
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CLIENT_HALF_CLOSE:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_CLIENT_HALF_CLOSE
	case binlogpb.GrpcLogEntry_EVENT_TYPE_SERVER_TRAILER:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_SERVER_TRAILER
		grpcLogRecord.Payload.Metadata = translateMetadata2(binlogEntry.GetTrailer().Metadata)
		grpcLogRecord.Payload.StatusCode = binlogEntry.GetTrailer().GetStatusCode()
		grpcLogRecord.Payload.StatusMessage = binlogEntry.GetTrailer().GetStatusMessage()
		grpcLogRecord.Payload.StatusDetails = binlogEntry.GetTrailer().GetStatusDetails()
		grpcLogRecord.PayloadTruncated = binlogEntry.GetPayloadTruncated()
		setPeerIfPresent(binlogEntry, grpcLogRecord)
	case binlogpb.GrpcLogEntry_EVENT_TYPE_CANCEL:
		grpcLogRecord.EventType = grpclogrecordpb.GrpcLogRecord_CANCEL
	default:
		logger.Infof("Unknown event type: %v", binlogEntry.Type)
		return
	}
	grpcLogRecord.ServiceName = ml.serviceName
	grpcLogRecord.MethodName = ml.methodName



	ml.exporter.EmitGrpcLogRecord(grpcLogRecord) // even this I think gets changed
}

// new binaryLogger at the binaryLogger level with new config
// needs the exporter (needs to only build exporter when either client rpc events or server rpc events will actually export)

// myMethodLogger (in binary log package?) exporter

// func (mml *myMethodLogger) Log(c iblog.LogEntryConfig) {
//         binlogEntry = o.Build(c)
//         convert to Eric's proto similar to Lidi's - switch proto first, then fill out a new helper based on Lidi's
//         outside of the schema changes to grpcLogRecord, also need to map changes from binary logging slide to Lidi's code and my code.
//         binLogEntry -> grpcLogRecord
//         mml.exporter.EmitGrpcLogRecord(grpcLogRecord)
// }

/*
type loggingExporter interface {
    EmitGrpcLogRecord(*logging.GrpcLogRecord)
    Close() error
}
*/


// 1. Rewriting this whole module to take new config, will help you figure out vvv



// 2. When to create exporter/how to send it down to the binlog layer to persist to log on Log()...exporter.EmitGrpcLogRecord()

// Once I've figured out 2, I can do vvv
// steps: Eric's schema changes, this changes the layer of method logger
// grpcLogRecord defined in method above will have names switched, and also
// there's specific funny annoying logic that got added.

// Problem with 2 and 3, GetMethodLogger() (which I guess reuses the binary logging logic layer in it's wrapper)
// and Log(), touches grpc-proto type, which is scoped to this package, would have to export...but I guess this package is
// that would be a circular dependency, binaryLog -> `grpcproto`, `exporter`.EmitGrpcLogRecord(grpcLogRecord)

// the types are scoped to this package and you can't pass them downward.

// (binarylog literally cannot import proto). This package already uses the logger interface, so can I just define them here? and have them implement the iblog.Interface definitions (talk to Doug about that change - he should agree)
// internal.AddExtraDialOptions(iblog.Logger) <- this implements interface already, so you're fine

type EventConfig struct {
	// these three matchers are just (match || not match) - no precedence needed
	ServiceMethod map[string]bool
	Services map[string]bool // need to create this
	// If set, this will always match
	MatchAll bool

	// If true, won't log anything, "excluded from logging" from Feng's design.
	Negation bool // or exclude?
	HeaderBytes uint64 // can never be negative, use uint64 like in codebase?
	MessageBytes uint64
} // orrr persist something and build at a later date, conversion will always be needed

type LoggerConfigObservability struct { // now that's here, it's an internal detail, I don't think you need to export anymore
	EventConfigs []EventConfig
}

type binaryLogger2 struct {
	EventConfigs []EventConfig // pointer or not?
	exporter loggingExporter/*Lidi had an unsafe pointer?*/
}

func (bl *binaryLogger2) GetMethodLogger(methodName string) iblog.MethodLogger {
	s, m, err := grpcutil.ParseMethod(methodName)
	if err != nil {
		logger.Infof("binarylogging: failed to parse %q: %v", methodName, err)
		return nil
	}
	for _, eventConfig := range bl.EventConfigs {
		// three ifs for matching or just one big considated if
		/*if eventConfig.matchAll {
			return newMethodLogger(eventConfig.headerBytes, eventConfig.messageBytes)
		}
		// /service/method comes in
		if eventConfig.serviceMethod["/" + s + "/" + m] { // matches against service/method, or just use methodName
			return newMethodLogger(eventConfig.headerBytes, eventConfig.messageBytes)
		}

		if eventConfig.services[s] {
			return newMethodLogger(eventConfig.headerBytes, eventConfig.messageBytes)
		}*/

		if eventConfig.MatchAll || eventConfig.ServiceMethod["/" + s + "/" + m] || eventConfig.Services[s] {
			if eventConfig.Negation {
				return nil
			}
			ml := newMethodLogger(eventConfig.HeaderBytes, eventConfig.MessageBytes) // switch this to return correct method logger type, the one with wrapped data you need
			return &binaryMethodLogger2{
				childMethodLogger: ml,
				exporter: bl.exporter,
			}
		}
	}
	// if it does hit a node, return {h, b} for that node

	return nil // equivalent to logging nothing - have at end of function and also if hits a negation
}

// For this new method logger to actually get invoked, it needs to be plumbed in
// through global dial/newServer options.

// Instead of stream calling global.Log()...,
// locallyScoped.GetMethodLogger()..., locallyScoped.Log() on each operation

// ^^^ still scoped from global dial/server options/but still


type binaryLogger struct { // this binaryLogger type is a wrapper of the original logger.
	// originalLogger is needed to ensure binary logging users won't be impacted
	// by this plugin. Users are allowed to subscribe to a completely different
	// set of methods.
	originalLogger iblog.Logger // calls into normal bin logging
	// exporter is a loggingExporter and the handle for uploading collected data
	// to backends.
	exporter unsafe.Pointer // loggingExporter
	// logger is a iblog.Logger wrapped for reusing the pattern matching logic
	// and the method logger creating logic.
	logger unsafe.Pointer // iblog.Logger
}

func (l *binaryLogger) loadExporter() loggingExporter {
	ptrPtr := atomic.LoadPointer(&l.exporter)
	if ptrPtr == nil {
		return nil
	}
	exporterPtr := (*loggingExporter)(ptrPtr)
	return *exporterPtr
}

func (l *binaryLogger) loadLogger() iblog.Logger {
	ptrPtr := atomic.LoadPointer(&l.logger)
	if ptrPtr == nil {
		return nil
	}
	loggerPtr := (*iblog.Logger)(ptrPtr)
	return *loggerPtr
}

func (l *binaryLogger) GetMethodLogger(methodName string) iblog.MethodLogger {
	var ol iblog.MethodLogger

	if l.originalLogger != nil { // wrapper on what was already in codebase -
		ol = l.originalLogger.GetMethodLogger(methodName) // backwards compatibility with a user already using binlog
	}

	// Prevent logging from logging, traces, and metrics API calls.
	if strings.HasPrefix(methodName, "/google.logging.v2.LoggingServiceV2/") || strings.HasPrefix(methodName, "/google.monitoring.v3.MetricService/") ||
		strings.HasPrefix(methodName, "/google.devtools.cloudtrace.v2.TraceService/") {
		return ol
	}

	// If no exporter is specified, there is no point creating a method
	// logger. We don't have any chance to inject exporter after its
	// creation.
	exporter := l.loadExporter()
	if exporter == nil {
		return ol
	}

	// If no logger is specified, e.g., during init period, do nothing.
	binLogger := l.loadLogger()
	if binLogger == nil {
		return ol
	}

	// If this method is not picked by LoggerConfig, do nothing.
	// gets binary logging for the observability extras.
	// bin logger is configured? differently
	ml := binLogger.GetMethodLogger(methodName) // observability plugin - gets binary logging
	if ml == nil {
		return ol
	}

	return &binaryMethodLogger{
		originalMethodLogger: ol,
		childMethodLogger:    ml,
		rpcID:                uuid.NewString(), // is this related to Eric's logging schema change?
		exporter:             exporter, // This happens at the wrapper layer, how do we plumb this all the way down? Does the config you send to binary logger need this? Doug doesn't want global, so it as an object needs to hold onto it (config, exporter) or ((config (contains exporter)))
	}
}

func (l *binaryLogger) Close() {
	if l == nil {
		return
	}
	ePtr := atomic.LoadPointer(&l.exporter)
	if ePtr != nil {
		exporter := (*loggingExporter)(ePtr)
		if err := (*exporter).Close(); err != nil {
			logger.Infof("Failed to close logging exporter: %v", err)
		}
	}
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

// FLIP OUTLIER DETECTION DEFAULT TO BE TRUE
func createBinaryLoggerConfig3() iblog.LoggerConfigObservability {
	var clientRPCEvents []clientRPCEvents // plumb this in - this will become a binary logging object held by client side
	var eventConfigs []iblog.EventConfig
	for _, clientRPCEvent := range clientRPCEvents { // or append
		eventConfig := iblog.EventConfig{}

		eventConfig.Negation = clientRPCEvent.Exclude
		eventConfig.HeaderBytes = uint64(clientRPCEvent.MaxMetadataBytes) // do we want to convert here or just type this as a uint64?
		eventConfig.MessageBytes = uint64(clientRPCEvent.MaxMessageBytes)
		for _, method := range clientRPCEvent.Method {
			eventConfig.ServiceMethod = make(map[string]bool) // this gets read in binarylog.go, so always create (make)
			eventConfig.Services = make(map[string]bool)
			if method == "*" {
				eventConfig.MatchAll = true // defaults to false
				continue
			}
			// are we validating it's a valid method name? isn't it validated at
			// this point
			/*
			All necessary validation of the configuration data would happen at
			the observability initialization time, if any error happens, the
			observability initialization method must return an error status to
			the caller and all initialization should be noop. Validation at observability
			initialization time.
			*/
			s, m, err := grpcutil.ParseMethod(method)
			if err != nil { // Shouldn't happen, already validated
				continue
			}
			if m == "*" {
				eventConfig.Services[s] = true
				continue
			}

			// parse it, if it is /, *, set that field in the set, can you get /, * from parser?, yeah you can get /, *

			eventConfig.ServiceMethod[method] = true
		}
	}
	clientSideConfig := iblog.LoggerConfigObservability{
		EventConfigs: eventConfigs,
	}
	// returns a binLog that holds onto this config
	clientSideLogger := iblog.NewLoggerFromConfig(clientSideConfig) // NewLoggerFromConfig, binaryLogger[config] - once it holds onto it, iterates through it for GetMethodLogger().

	// package.AddGlobalDialOptions(WithBinaryLogger(clientSideLogger))

	// var clientSideConfig iblog.LoggerConfigObservability // not a global object, an object you pass around to internal Dial Options (holds a ref to heap memory)
	// clientSideConfig.EventConfigs // []EventConfig, 1:1 with clientRPCEvents

	var serverRPCEvents []serverRPCEvents // plumb this in - this will become a binary logging object held by server side - what object on server side?
	// same flow as client events
	// package.AddGlobalServerOptions
}

func createBinaryLoggerConfig2() (iblog.LoggerConfig) {
	// a list of client rpc events
	//           and each event has a list of methods - which I feel like is equivalent to what's below

	// client and server - how does that map, also do you want []LoggerConfig?

	var clientRPCEvents []clientRPCEvents

	for _, clientRPCEvent := range clientRPCEvents {
		for _, method := range clientRPCEvent.Method {
			method // string, how is this plumbed into LoggerConfig?
		}
		clientRPCEvent.MaxMessageBytes // int - how to plumb this into logger config?
		clientRPCEvent.MaxMetadataBytes // int
		clientRPCEvent.Exclude // bool, directly tied to ^^^ - how is this plumbed into logger config? Set one of the Blacklist?
		/*
		If the value is true, the wildcard (*) cannot be used as the whole value in the client_rpc_events[].method[].
		Put this in a validation section?
		*/
	}
	// two things, client side (outgoing RPC's from binary) ^^^, server side (incoming RPC's to binary) vvv
	var serverRPCEvents []serverRPCEvents
	for _, serverRPCEvent := range serverRPCEvents {
		for _, method := range serverRPCEvent.Method {
			method // string
		}
		serverRPCEvent.MaxMessageBytes // int
		serverRPCEvent.MaxMetadataBytes // int
		serverRPCEvent.Exclude // same thing as client side, tied to that method iteration ^^^
	}

	// behaviors defined in his config document map to the existing binary logging config,
	// do we need to change binary logging config? Esp wrt the "most specific" logic.

	/* this is what I'm eventually filling out - do I need to rework so not exact match?
	type LoggerConfig struct {
	    All       *MethodLoggerConfig
	    Services  map[string]*MethodLoggerConfig
	    Methods   map[string]*MethodLoggerConfig
	    Blacklist map[string]struct{}
	}
	*/

	// Can the behaviors listed by Feng actually be configured through this
	// binary logging proto?

	// I think exclusion is this `-` thing this is through binary logging configuration

	// The thing I'm questioning is client/server partition,
	// and the iteration through the events and first one that matches
	// How do I stick that precedence in LoggerConfig?

	/* And this line I don't get how you do either:
	The client_rpc_events configs are evaluated in text order, the first one
	matched is used. If a RPC doesn’t match an entry, it will continue on the
	next entry in the list.
	*/

	loggerConfig := iblog.LoggerConfig{ // does this come coupled with that exact match thingy?

	}

	// Used in binary logging as such (maybe the other logic comes with building out the config):
	// Methods first return methodLogger configured with metadata and message bytes information
	// Blacklist next
	// Services next

	methodLoggerConfig := &iblog.MethodLoggerConfig{
		Header: /*MaxMetadataBytes*/,
		Message: /*MaxMessageBytes*/,
	}

	// return is it one binary logging config for both the client and server?
}

func createBinaryLoggerConfig(filters []logFilter) iblog.LoggerConfig {
	// this is the big helper function I have to deal with.

	// do we pass in both or just one?


	// best match wins vs.

	// metadata bytes and message bytes for []string (wildcards etc. are kept
	// constant), this is []{string, metadata bytes, message bytes}

	config := iblog.LoggerConfig{
		Services: make(map[string]*iblog.MethodLoggerConfig), // max length of header and message
		Methods:  make(map[string]*iblog.MethodLoggerConfig),
	}
	// Try matching the filters one by one, pick the first match. The
	// correctness of the log filter pattern is ensured by config.go.
	/*
	filter gets expanded to have invert,
	plus one for client and one for server - how does this map to what we have
	to not allowing * and invert

	// what is this? client or server
	// what we are going to support:
	// server rpc events: a list of server_rpc_events configs, represents the config for
	*** incoming RPCs to the server. ***
	evaluated in text order, the first one matched is used. If it doesn't match, continues to the next entry in list.

	// client rpc events: a list of client_rpcs_events configs, represents config for outgoing RPCs from server.
	// for *** outgoing RPCs from the server ***

	// both from the server - do we just register both?
	// is what we have now for the server? yeah, is this registered for a service and incoming is server rpc events?
	// and outgoing is client rpc events? Where are these hooked in?

	// ALSO REMOVE LOGGING PB AND make internal JSON

	*/

	// client and server filtering is looking in the proto itself

	// iterative -> precedence thing, preprocessing

	// while processing a filter, remove anything that might process a previous filter

	// how does this precedence thing even work?

	// client_rpc_events[] -> (each node has a []method {header bytes, message
	// bytes for the whole []string which represents method with *} this
	// relationship is taken care of by setting each header bytes/message bytes
	// for each specific method) - A list of strings which can select a group of methods.

	// So if it hits any []method, it doesn't matter, they all have the same header bytes
	// and message bytes


	// expand client_rpc_events[] and this iterative plurality (first one you
	// hit, use) to the precedence list of specificity

	// within each client/server event it doesn't matter since they all share the same message/header bytes.
	// otherwise, check every single entry post

	// how to check for collisions

	// change way of filtering to enable that, or accept cost, do the filtering on our end
	// Convert between most specific and iteration one by one

	// This precedence ordering of filling out a config for a method name
	// is reused, expect * can't happen if exclude is true (in validation or here?)

	// splitting client and server - how are we going to do that

	// taking his filters and building out precedence list
	for _, filter := range filters { // field for client or server
		if filter.Pattern == "*" { // is this pattern the same? Also, now we have client and server
			// Match a "*"
			if !validateExistingMethodLoggerConfig(config.All, filter) { // also, how does this plumb in wrt to the hot restart crap of overriding config - "new methodLogger with same method overrides the previous one", can't set override
				continue
			}
			config.All = &iblog.MethodLoggerConfig{Header: uint64(filter.HeaderBytes), Message: uint64(filter.MessageBytes)}
			continue
		}
		tokens := strings.SplitN(filter.Pattern, "/", 2) // is this different to our filtering? I think our filtering described as the / being in the first part - service name, not method name
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

	// createBinaryLoggerConfig(filters) <- this filter list is what changes


	// the binary logger tied to o11y - configured with filters
	// which will now be reworked to a new logical configuration, the will affect
	// not the standard binLogger backwards compatibility, but the o11y
	// specific binLogger configured with o11y configuration.

	// Also, the schema actually updated to cloud logging changes.


	// the extra obv. bin logging - yup, converted from the filters this is big
	// thing you'll have to figure out, esp with the dimensionality/plurality of
	// the configs.
	binLogger := iblog.NewLoggerFromConfig(createBinaryLoggerConfig(filters)) // right here ib.NewLoggerFromConfig(createBinaryLoggerConfig(filters) <- conversion function that is interesting to me) -
	if binLogger != nil {
		atomic.StorePointer(&l.logger, unsafe.Pointer(&binLogger))
	}
	atomic.StorePointer(&l.exporter, unsafe.Pointer(&exporter))
	logger.Info("Start gRPC Observability logger")
	return nil
}

// the config is already verified by the time it reaches here
func (l *binaryLogger) Start(ctx context.Context, config *config) error {
	if config == nil || !config.EnableCloudLogging {
		return nil
	}
	if config.DestinationProjectID == "" { // this has already been validated, this will never hit
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
	return &binaryLogger{ // wrapper of binary logger
		originalLogger: iblogger,
	}
}

var defaultLogger *binaryLogger

func prepareLogging() {
	// yeah gets the global, wraps it in the wrapper global, this doesn't need to happen, ib log already registered so old plumbing is already there
	// ib.GetLogger = normal binary logger, newBinaryLogger(normal binary logger)
	defaultLogger = newBinaryLogger(iblog.GetLogger()) // sets the global logger to the binlog wrapper. This global logger holds onto a bin log method logger etc.
	iblog.SetLogger(defaultLogger) // sets global to the thing
}

// we're switching this whole flow from binLog{original binLog, o11y binLog}
// to leave the old codepath alone, (just needs to make sure original binary logging is still there...)

// new codepath - goal is internal global dial option/internal global NewServer option

// registerClientRPCEvents registers clientRPCEvents as a global Dial Option.
func registerClientRPCEvents(clientRPCEvents []clientRPCEvents, exporter loggingExporter) {
	if len(clientRPCEvents) == 0 {
		return
	}
	var eventConfigs []EventConfig
	for _, clientRPCEvent := range clientRPCEvents {
		eventConfig := EventConfig{}
		eventConfig.Negation = clientRPCEvent.Exclude
		eventConfig.HeaderBytes = uint64(clientRPCEvent.MaxMetadataBytes)
		eventConfig.MessageBytes = uint64(clientRPCEvent.MaxMessageBytes)
		for _, method := range clientRPCEvent.Method {
			eventConfig.ServiceMethod = make(map[string]bool)
			eventConfig.Services = make(map[string]bool)
			if method == "*" {
				eventConfig.MatchAll = true // defaults to false
				continue
			}
			// are we validating it's a valid method name? isn't it validated at
			// this point
			/*
				All necessary validation of the configuration data would happen at
				the observability initialization time, if any error happens, the
				observability initialization method must return an error status to
				the caller and all initialization should be noop. Validation at observability
				initialization time.
			*/
			s, m, err := grpcutil.ParseMethod(method)
			if err != nil { // Shouldn't happen, already validated
				continue
			}
			if m == "*" {
				eventConfig.Services[s] = true
				continue
			}
			eventConfig.ServiceMethod[method] = true
		}
	}
	clientSideConfig := iblog.LoggerConfigObservability{
		EventConfigs: eventConfigs,
	}
	// returns a binLog that holds onto this config
	// clientSideLogger := iblog.NewLoggerFromConfig(clientSideConfig, loggingExporter) // NewLoggerFromConfig, binaryLogger[config] - once it holds onto it, iterates through it for GetMethodLogger().
	clientSideLogger := &binaryLogger2{
		EventConfigs: eventConfigs,
		exporter: exporter,
	}
	// package.AddGlobalClientSideOptions(WithBinaryLogger(clientSideLogger))
}

func registerServerRPCEvents(serverRPCEvents []serverRPCEvents, exporter loggingExporter) {
	if len(serverRPCEvents) == 0 {
		return
	}
	var eventConfigs []EventConfig
	for _, serverRPCEvent := range serverRPCEvents {
		eventConfig := EventConfig{}
		eventConfig.Negation = serverRPCEvent.Exclude
		eventConfig.HeaderBytes = uint64(serverRPCEvent.MaxMetadataBytes)
		eventConfig.MessageBytes = uint64(serverRPCEvent.MaxMessageBytes)
		for _, method := range serverRPCEvent.Method {
			eventConfig.ServiceMethod = make(map[string]bool)
			eventConfig.Services = make(map[string]bool)
			if method == "*" {
				eventConfig.MatchAll = true
				continue
			}
			// are we validating it's a valid method name? isn't it validated at
			// this point
			/*
				All necessary validation of the configuration data would happen at
				the observability initialization time, if any error happens, the
				observability initialization method must return an error status to
				the caller and all initialization should be noop. Validation at observability
				initialization time.
			*/
			s, m, err := grpcutil.ParseMethod(method)
			if err != nil { // Shouldn't happen, already validated
				continue
			}
			if m == "*" {
				eventConfig.Services[s] = true
				continue
			}
			eventConfig.ServiceMethod[method] = true
		}
	}
	/*serverSideConfig := iblog.LoggerConfigObservability{
		EventConfigs: eventConfigs,
	}*/
	// returns a binLog that holds onto this config
	// serverSideLogger := iblog.NewLoggerFromConfig(serverSideConfig) // NewLoggerFromConfig, binaryLogger[config] - once it holds onto it, iterates through it for GetMethodLogger().
	serverSideLogger := &binaryLogger2{
		EventConfigs: eventConfigs,
		exporter: exporter,
	}
	// package.AddGlobalServerOptions(WithBinaryLogger(serverSideLogger))
}


// on client side and server side, {original binLog}, client (some object on client side).ClientSideBinLog != nil, server (some object on server side).ServerSideBinLog != nil
func startLogging(ctx context.Context, config *newConfig) error { // does this even need an error?

	if config == nil || config.CloudLogging == nil {
		return nil
	}

	exporter, err := newCloudLoggingExporter2(ctx, config)
	if err != nil {
		return fmt.Errorf("unable to create CloudLogging exporter: %v", err)
	}
	// this is our own type, can send it downward? and define interface in binarylog.go?
	exporter.EmitGrpcLogRecord()
	exporter.Close()


	cl := config.CloudLogging // what part of this config are we validating?
	// Goal: client side global DialOption registered - when does it actually need to register?
	registerClientRPCEvents(cl.ClientRPCEvents, exporter)
	// Goal: server side global NewServerOption registered
	registerServerRPCEvents(cl.ServerRPCEvents, exporter)
}

// cleanup new config path, binary logging

// then plumb the global dial options/server options around