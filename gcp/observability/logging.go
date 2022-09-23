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
	}
	return &res
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

var loggerTypeToEventLogger = map[binlogpb.GrpcLogEntry_Logger]grpclogrecordpb.GrpcLogRecord_EventLogger{
	binlogpb.GrpcLogEntry_LOGGER_UNKNOWN: grpclogrecordpb.GrpcLogRecord_LOGGER_UNKNOWN,
	binlogpb.GrpcLogEntry_LOGGER_CLIENT:  grpclogrecordpb.GrpcLogRecord_LOGGER_CLIENT,
	binlogpb.GrpcLogEntry_LOGGER_SERVER:  grpclogrecordpb.GrpcLogRecord_LOGGER_SERVER,
}

type binaryMethodLogger struct {
	rpcID, serviceName, methodName string
	originalMethodLogger           iblog.MethodLogger
	childMethodLogger              iblog.MethodLogger
	exporter                       loggingExporter
}

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
		rpcID:                uuid.NewString(),
		exporter:             exporter,
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
			the caller and all initialization should be noop.
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
		// eventConfig.MatchAll = true

		clientRPCEvent.Exclude // do we build out the whole matcher unconditionally - yeah might as well, unless we are optimizing for preprocessing, also after validation what is the state space of this?
		clientRPCEvent.MaxMetadataBytes
		clientRPCEvent.MaxMessageBytes
		clientRPCEvent.Method // []string - this is the only thing that gets converted, the others you can just convert inline
	}
	clientSideConfig := iblog.LoggerConfigObservability{
		EventConfigs: eventConfigs,
	}
	// var clientSideConfig iblog.LoggerConfigObservability // not a global object, an object you pass around to internal Dial Options (holds a ref to heap memory)
	// clientSideConfig.EventConfigs // []EventConfig, 1:1 with clientRPCEvents

	var serverRPCEvents []serverRPCEvents // plumb this in - this will become a binary logging object held by server side - what object on server side?
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
	return &binaryLogger{ // wrapper of binary logger
		originalLogger: iblogger,
	}
}

var defaultLogger *binaryLogger

func prepareLogging() {
	// ib.GetLogger = normal binary logger, newBinaryLogger(normal binary logger)
	defaultLogger = newBinaryLogger(iblog.GetLogger()) // sets the global logger to the binlog wrapper. This global logger holds onto a bin log method logger etc.
	iblog.SetLogger(defaultLogger) // sets global to the thing
}
