// Copyright 2022 The gRPC Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.0
// 	protoc        v3.15.3
// source: logging/logging.proto

package logging

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	durationpb "google.golang.org/protobuf/types/known/durationpb"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// List of event types
type GrpcLogRecord_EventType int32

const (
	// Unknown event type
	GrpcLogRecord_NONE GrpcLogRecord_EventType = 0
	// Header sent from client to server
	GrpcLogRecord_CLIENT_HEADER GrpcLogRecord_EventType = 1
	// Header sent from server to client
	GrpcLogRecord_SERVER_HEADER GrpcLogRecord_EventType = 2
	// Message sent from client to server
	GrpcLogRecord_CLIENT_MESSAGE GrpcLogRecord_EventType = 3
	// Message sent from server to client
	GrpcLogRecord_SERVER_MESSAGE GrpcLogRecord_EventType = 4
	// A signal that client is done sending
	GrpcLogRecord_CLIENT_HALF_CLOSE GrpcLogRecord_EventType = 5
	// Trailer indicates the end of the gRPC call
	GrpcLogRecord_SERVER_TRAILER GrpcLogRecord_EventType = 6
	// A signal that the rpc is canceled
	GrpcLogRecord_CANCEL GrpcLogRecord_EventType = 7
)

// Enum value maps for GrpcLogRecord_EventType.
var (
	GrpcLogRecord_EventType_name = map[int32]string{
		0: "NONE",
		1: "CLIENT_HEADER",
		2: "SERVER_HEADER",
		3: "CLIENT_MESSAGE",
		4: "SERVER_MESSAGE",
		5: "CLIENT_HALF_CLOSE",
		6: "SERVER_TRAILER",
		7: "CANCEL",
	}
	GrpcLogRecord_EventType_value = map[string]int32{
		"NONE":              0,
		"CLIENT_HEADER":     1,
		"SERVER_HEADER":     2,
		"CLIENT_MESSAGE":    3,
		"SERVER_MESSAGE":    4,
		"CLIENT_HALF_CLOSE": 5,
		"SERVER_TRAILER":    6,
		"CANCEL":            7,
	}
)

func (x GrpcLogRecord_EventType) Enum() *GrpcLogRecord_EventType {
	p := new(GrpcLogRecord_EventType)
	*p = x
	return p
}

func (x GrpcLogRecord_EventType) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (GrpcLogRecord_EventType) Descriptor() protoreflect.EnumDescriptor {
	return file_logging_logging_proto_enumTypes[0].Descriptor()
}

func (GrpcLogRecord_EventType) Type() protoreflect.EnumType {
	return &file_logging_logging_proto_enumTypes[0]
}

func (x GrpcLogRecord_EventType) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use GrpcLogRecord_EventType.Descriptor instead.
func (GrpcLogRecord_EventType) EnumDescriptor() ([]byte, []int) {
	return file_logging_logging_proto_rawDescGZIP(), []int{1, 0}
}

// The entity that generates the log entry
type GrpcLogRecord_Logger int32

const (
	GrpcLogRecord_UNKNOWN GrpcLogRecord_Logger = 0 // Don't do this inline - even if Doug makes you switch, this makes it so much cleaner as an intermediate step
	GrpcLogRecord_CLIENT  GrpcLogRecord_Logger = 1
	GrpcLogRecord_SERVER  GrpcLogRecord_Logger = 2
)

// Enum value maps for GrpcLogRecord_Logger.
var (
	GrpcLogRecord_Logger_name = map[int32]string{
		0: "UNKNOWN",
		1: "CLIENT",
		2: "SERVER",
	}
	GrpcLogRecord_Logger_value = map[string]int32{
		"UNKNOWN": 0,
		"CLIENT":  1,
		"SERVER":  2,
	}
)

func (x GrpcLogRecord_Logger) Enum() *GrpcLogRecord_Logger {
	p := new(GrpcLogRecord_Logger)
	*p = x
	return p
}

func (x GrpcLogRecord_Logger) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (GrpcLogRecord_Logger) Descriptor() protoreflect.EnumDescriptor {
	return file_logging_logging_proto_enumTypes[1].Descriptor()
}

func (GrpcLogRecord_Logger) Type() protoreflect.EnumType {
	return &file_logging_logging_proto_enumTypes[1]
}

func (x GrpcLogRecord_Logger) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use GrpcLogRecord_Logger.Descriptor instead.
func (GrpcLogRecord_Logger) EnumDescriptor() ([]byte, []int) {
	return file_logging_logging_proto_rawDescGZIP(), []int{1, 1}
}

// The log severity level of the log entry
type GrpcLogRecord_LogLevel int32

const (
	GrpcLogRecord_LOG_LEVEL_UNKNOWN  GrpcLogRecord_LogLevel = 0
	GrpcLogRecord_LOG_LEVEL_TRACE    GrpcLogRecord_LogLevel = 1
	GrpcLogRecord_LOG_LEVEL_DEBUG    GrpcLogRecord_LogLevel = 2
	GrpcLogRecord_LOG_LEVEL_INFO     GrpcLogRecord_LogLevel = 3
	GrpcLogRecord_LOG_LEVEL_WARN     GrpcLogRecord_LogLevel = 4
	GrpcLogRecord_LOG_LEVEL_ERROR    GrpcLogRecord_LogLevel = 5
	GrpcLogRecord_LOG_LEVEL_CRITICAL GrpcLogRecord_LogLevel = 6
)

// Enum value maps for GrpcLogRecord_LogLevel.
var (
	GrpcLogRecord_LogLevel_name = map[int32]string{
		0: "LOG_LEVEL_UNKNOWN",
		1: "LOG_LEVEL_TRACE",
		2: "LOG_LEVEL_DEBUG",
		3: "LOG_LEVEL_INFO",
		4: "LOG_LEVEL_WARN",
		5: "LOG_LEVEL_ERROR",
		6: "LOG_LEVEL_CRITICAL",
	}
	GrpcLogRecord_LogLevel_value = map[string]int32{
		"LOG_LEVEL_UNKNOWN":  0,
		"LOG_LEVEL_TRACE":    1,
		"LOG_LEVEL_DEBUG":    2,
		"LOG_LEVEL_INFO":     3,
		"LOG_LEVEL_WARN":     4,
		"LOG_LEVEL_ERROR":    5,
		"LOG_LEVEL_CRITICAL": 6,
	}
)

func (x GrpcLogRecord_LogLevel) Enum() *GrpcLogRecord_LogLevel {
	p := new(GrpcLogRecord_LogLevel)
	*p = x
	return p
}

func (x GrpcLogRecord_LogLevel) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (GrpcLogRecord_LogLevel) Descriptor() protoreflect.EnumDescriptor {
	return file_logging_logging_proto_enumTypes[2].Descriptor()
}

func (GrpcLogRecord_LogLevel) Type() protoreflect.EnumType {
	return &file_logging_logging_proto_enumTypes[2]
}

func (x GrpcLogRecord_LogLevel) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use GrpcLogRecord_LogLevel.Descriptor instead.
func (GrpcLogRecord_LogLevel) EnumDescriptor() ([]byte, []int) {
	return file_logging_logging_proto_rawDescGZIP(), []int{1, 2}
}

type GrpcLogRecord_Address_Type int32

const (
	GrpcLogRecord_Address_TYPE_UNKNOWN GrpcLogRecord_Address_Type = 0
	GrpcLogRecord_Address_TYPE_IPV4    GrpcLogRecord_Address_Type = 1 // in 1.2.3.4 form
	GrpcLogRecord_Address_TYPE_IPV6    GrpcLogRecord_Address_Type = 2 // IPv6 canonical form (RFC5952 section 4)
	GrpcLogRecord_Address_TYPE_UNIX    GrpcLogRecord_Address_Type = 3 // UDS string
)

// Enum value maps for GrpcLogRecord_Address_Type.
var (
	GrpcLogRecord_Address_Type_name = map[int32]string{
		0: "TYPE_UNKNOWN",
		1: "TYPE_IPV4",
		2: "TYPE_IPV6",
		3: "TYPE_UNIX",
	}
	GrpcLogRecord_Address_Type_value = map[string]int32{
		"TYPE_UNKNOWN": 0,
		"TYPE_IPV4":    1,
		"TYPE_IPV6":    2,
		"TYPE_UNIX":    3,
	}
)

func (x GrpcLogRecord_Address_Type) Enum() *GrpcLogRecord_Address_Type {
	p := new(GrpcLogRecord_Address_Type)
	*p = x
	return p
}

func (x GrpcLogRecord_Address_Type) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (GrpcLogRecord_Address_Type) Descriptor() protoreflect.EnumDescriptor {
	return file_logging_logging_proto_enumTypes[3].Descriptor()
}

func (GrpcLogRecord_Address_Type) Type() protoreflect.EnumType {
	return &file_logging_logging_proto_enumTypes[3]
}

func (x GrpcLogRecord_Address_Type) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use GrpcLogRecord_Address_Type.Descriptor instead.
func (GrpcLogRecord_Address_Type) EnumDescriptor() ([]byte, []int) {
	return file_logging_logging_proto_rawDescGZIP(), []int{1, 2, 0}
}

type Payload struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// now - map<string, string> metadata = 1; "inline the repeated Metadata entry" is this the same?
	// I think it's same iteration of key values but populates a map? rather than a list of {key, values}, value switched from []byte -> string, need to encode by (string -> []byte -> Base64)?
	Metadata map[string]string `protobuf:"bytes,1,rep,name=metadata,proto3" json:"metadata,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"` // are the values v1, v2, v3, v4 or individual?
	// the RPC timeout value - Eric mentioned this duration will be a headache.
	Timeout *durationpb.Duration `protobuf:"bytes,2,opt,name=timeout,proto3" json:"timeout,omitempty"`
	// The gRPC status code
	StatusCode uint32 `protobuf:"varint,3,opt,name=status_code,json=statusCode,proto3" json:"status_code,omitempty"`
	// The gRPC status message
	StatusMessage string `protobuf:"bytes,4,opt,name=status_message,json=statusMessage,proto3" json:"status_message,omitempty"`
	// The value of the grpc-status-details-bin metadata key, if any.
	// This is always an encoded google.rpc.Status message
	StatusDetails []byte `protobuf:"bytes,5,opt,name=status_details,json=statusDetails,proto3" json:"status_details,omitempty"`
	MessageLength uint32 `protobuf:"varint,6,opt,name=message_length,json=messageLength,proto3" json:"message_length,omitempty"` // messageLength
	// Used by message event
	Message []byte `protobuf:"bytes,7,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *Payload) Reset() {
	*x = Payload{}
	if protoimpl.UnsafeEnabled {
		mi := &file_logging_logging_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Payload) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Payload) ProtoMessage() {}

func (x *Payload) ProtoReflect() protoreflect.Message {
	mi := &file_logging_logging_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Payload.ProtoReflect.Descriptor instead.
func (*Payload) Descriptor() ([]byte, []int) {
	return file_logging_logging_proto_rawDescGZIP(), []int{0}
}

func (x *Payload) GetMetadata() map[string]string {
	if x != nil {
		return x.Metadata
	}
	return nil
}

func (x *Payload) GetTimeout() *durationpb.Duration {
	if x != nil {
		return x.Timeout
	}
	return nil
}

func (x *Payload) GetStatusCode() uint32 {
	if x != nil {
		return x.StatusCode
	}
	return 0
}

func (x *Payload) GetStatusMessage() string {
	if x != nil {
		return x.StatusMessage
	}
	return ""
}

func (x *Payload) GetStatusDetails() []byte {
	if x != nil {
		return x.StatusDetails
	}
	return nil
}

func (x *Payload) GetMessageLength() uint32 {
	if x != nil {
		return x.MessageLength
	}
	return 0
}

func (x *Payload) GetMessage() []byte {
	if x != nil {
		return x.Message
	}
	return nil
}

// DO GRAD CHECKIN, get my package, do laundry, swim
type GrpcLogRecord struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// I switched this from RPC_ID. Is this the same? I think so
	CallId string `protobuf:"bytes,2,opt,name=call_id,json=callId,proto3" json:"call_id,omitempty"` // callId in JSON
	// The entry sequence ID for this call. The first message has a value of 1,
	// to disambiguate from an unset value. The purpose of this field is to
	// detect missing entries in environments where durability or ordering is
	// not guaranteed.
	SequenceId  uint64                  `protobuf:"varint,3,opt,name=sequence_id,json=sequenceId,proto3" json:"sequence_id,omitempty"`                                                            // sequenceId
	EventType   GrpcLogRecord_EventType `protobuf:"varint,4,opt,name=event_type,json=eventType,proto3,enum=grpc.observability.logging.v1.GrpcLogRecord_EventType" json:"event_type,omitempty"`    // one of the above EventType enum
	EventLogger GrpcLogRecord_Logger    `protobuf:"varint,5,opt,name=event_logger,json=eventLogger,proto3,enum=grpc.observability.logging.v1.GrpcLogRecord_Logger" json:"event_logger,omitempty"` // one of the above EventLogger enum
	Payload     *Payload                `protobuf:"bytes,6,opt,name=payload,proto3" json:"payload,omitempty"`
	// true if message or metadata field is either truncated or omitted due
	// to config options
	PayloadTruncated bool `protobuf:"varint,7,opt,name=payload_truncated,json=payloadTruncated,proto3" json:"payload_truncated,omitempty"` // payloadTruncated
	// Peer address information. On client side, peer is logged on server
	// header event or trailer event (if trailer-only). On server side, peer
	// is always logged on the client header event.
	Peer *GrpcLogRecord_Address `protobuf:"bytes,8,opt,name=peer,proto3" json:"peer,omitempty"`
	// A single process may be used to run multiple virtual servers with
	// different identities.
	// The authority is the name of such a server identify. It is typically a
	// portion of the URI in the form of <host> or <host>:<port>.
	Authority string `protobuf:"bytes,10,opt,name=authority,proto3" json:"authority,omitempty"`
	// the name of the service
	ServiceName string `protobuf:"bytes,11,opt,name=service_name,json=serviceName,proto3" json:"service_name,omitempty"` // serviceName
	// the name of the RPC method
	MethodName string `protobuf:"bytes,12,opt,name=method_name,json=methodName,proto3" json:"method_name,omitempty"` // methodName
	// Size of the message or metadata, depending on the event type,
	// regardless of whether the full message or metadata is being logged
	// (i.e. could be truncated or omitted).
	PayloadSize uint32 `protobuf:"varint,13,opt,name=payload_size,json=payloadSize,proto3" json:"payload_size,omitempty"`
}

func (x *GrpcLogRecord) Reset() {
	*x = GrpcLogRecord{}
	if protoimpl.UnsafeEnabled {
		mi := &file_logging_logging_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GrpcLogRecord) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GrpcLogRecord) ProtoMessage() {}

func (x *GrpcLogRecord) ProtoReflect() protoreflect.Message {
	mi := &file_logging_logging_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GrpcLogRecord.ProtoReflect.Descriptor instead.
func (*GrpcLogRecord) Descriptor() ([]byte, []int) {
	return file_logging_logging_proto_rawDescGZIP(), []int{1}
}

func (x *GrpcLogRecord) GetCallId() string {
	if x != nil {
		return x.CallId
	}
	return ""
}

func (x *GrpcLogRecord) GetSequenceId() uint64 {
	if x != nil {
		return x.SequenceId
	}
	return 0
}

func (x *GrpcLogRecord) GetEventType() GrpcLogRecord_EventType {
	if x != nil {
		return x.EventType
	}
	return GrpcLogRecord_NONE
}

func (x *GrpcLogRecord) GetEventLogger() GrpcLogRecord_Logger {
	if x != nil {
		return x.EventLogger
	}
	return GrpcLogRecord_UNKNOWN
}

func (x *GrpcLogRecord) GetPayload() *Payload {
	if x != nil {
		return x.Payload
	}
	return nil
}

func (x *GrpcLogRecord) GetPayloadTruncated() bool {
	if x != nil {
		return x.PayloadTruncated
	}
	return false
}

func (x *GrpcLogRecord) GetPeer() *GrpcLogRecord_Address {
	if x != nil {
		return x.Peer
	}
	return nil
}

func (x *GrpcLogRecord) GetAuthority() string {
	if x != nil {
		return x.Authority
	}
	return ""
}

func (x *GrpcLogRecord) GetServiceName() string {
	if x != nil {
		return x.ServiceName
	}
	return ""
}

func (x *GrpcLogRecord) GetMethodName() string {
	if x != nil {
		return x.MethodName
	}
	return ""
}

func (x *GrpcLogRecord) GetPayloadSize() uint32 {
	if x != nil {
		return x.PayloadSize
	}
	return 0
}

// A list of metadata pairs
type GrpcLogRecord_Metadata struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Entry []*GrpcLogRecord_MetadataEntry `protobuf:"bytes,1,rep,name=entry,proto3" json:"entry,omitempty"`
}

func (x *GrpcLogRecord_Metadata) Reset() {
	*x = GrpcLogRecord_Metadata{}
	if protoimpl.UnsafeEnabled {
		mi := &file_logging_logging_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GrpcLogRecord_Metadata) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GrpcLogRecord_Metadata) ProtoMessage() {}

func (x *GrpcLogRecord_Metadata) ProtoReflect() protoreflect.Message {
	mi := &file_logging_logging_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GrpcLogRecord_Metadata.ProtoReflect.Descriptor instead.
func (*GrpcLogRecord_Metadata) Descriptor() ([]byte, []int) {
	return file_logging_logging_proto_rawDescGZIP(), []int{1, 0}
}

func (x *GrpcLogRecord_Metadata) GetEntry() []*GrpcLogRecord_MetadataEntry {
	if x != nil {
		return x.Entry
	}
	return nil
}

// One metadata key value pair
type GrpcLogRecord_MetadataEntry struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Key   string `protobuf:"bytes,1,opt,name=key,proto3" json:"key,omitempty"`
	Value []byte `protobuf:"bytes,2,opt,name=value,proto3" json:"value,omitempty"`
}

func (x *GrpcLogRecord_MetadataEntry) Reset() {
	*x = GrpcLogRecord_MetadataEntry{}
	if protoimpl.UnsafeEnabled {
		mi := &file_logging_logging_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GrpcLogRecord_MetadataEntry) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GrpcLogRecord_MetadataEntry) ProtoMessage() {}

func (x *GrpcLogRecord_MetadataEntry) ProtoReflect() protoreflect.Message {
	mi := &file_logging_logging_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GrpcLogRecord_MetadataEntry.ProtoReflect.Descriptor instead.
func (*GrpcLogRecord_MetadataEntry) Descriptor() ([]byte, []int) {
	return file_logging_logging_proto_rawDescGZIP(), []int{1, 1}
}

func (x *GrpcLogRecord_MetadataEntry) GetKey() string {
	if x != nil {
		return x.Key
	}
	return ""
}

func (x *GrpcLogRecord_MetadataEntry) GetValue() []byte {
	if x != nil {
		return x.Value
	}
	return nil
}

// Address information
type GrpcLogRecord_Address struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Type    GrpcLogRecord_Address_Type `protobuf:"varint,1,opt,name=type,proto3,enum=grpc.observability.logging.v1.GrpcLogRecord_Address_Type" json:"type,omitempty"`
	Address string                     `protobuf:"bytes,2,opt,name=address,proto3" json:"address,omitempty"`
	// only for TYPE_IPV4 and TYPE_IPV6
	IpPort uint32 `protobuf:"varint,3,opt,name=ip_port,json=ipPort,proto3" json:"ip_port,omitempty"`
}

func (x *GrpcLogRecord_Address) Reset() {
	*x = GrpcLogRecord_Address{}
	if protoimpl.UnsafeEnabled {
		mi := &file_logging_logging_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GrpcLogRecord_Address) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GrpcLogRecord_Address) ProtoMessage() {}

func (x *GrpcLogRecord_Address) ProtoReflect() protoreflect.Message {
	mi := &file_logging_logging_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GrpcLogRecord_Address.ProtoReflect.Descriptor instead.
func (*GrpcLogRecord_Address) Descriptor() ([]byte, []int) {
	return file_logging_logging_proto_rawDescGZIP(), []int{1, 2}
}

func (x *GrpcLogRecord_Address) GetType() GrpcLogRecord_Address_Type {
	if x != nil {
		return x.Type
	}
	return GrpcLogRecord_Address_TYPE_UNKNOWN
}

func (x *GrpcLogRecord_Address) GetAddress() string {
	if x != nil {
		return x.Address
	}
	return ""
}

func (x *GrpcLogRecord_Address) GetIpPort() uint32 {
	if x != nil {
		return x.IpPort
	}
	return 0
}

var File_logging_logging_proto protoreflect.FileDescriptor

var file_logging_logging_proto_rawDesc = []byte{
	0x0a, 0x15, 0x6c, 0x6f, 0x67, 0x67, 0x69, 0x6e, 0x67, 0x2f, 0x6c, 0x6f, 0x67, 0x67, 0x69, 0x6e,
	0x67, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x1d, 0x67, 0x72, 0x70, 0x63, 0x2e, 0x6f, 0x62,
	0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x6c, 0x6f, 0x67, 0x67,
	0x69, 0x6e, 0x67, 0x2e, 0x76, 0x31, 0x1a, 0x1e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x64, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0xfd, 0x02, 0x0a, 0x07, 0x50, 0x61, 0x79, 0x6c, 0x6f,
	0x61, 0x64, 0x12, 0x50, 0x0a, 0x08, 0x6d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0x18, 0x01,
	0x20, 0x03, 0x28, 0x0b, 0x32, 0x34, 0x2e, 0x67, 0x72, 0x70, 0x63, 0x2e, 0x6f, 0x62, 0x73, 0x65,
	0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x6c, 0x6f, 0x67, 0x67, 0x69, 0x6e,
	0x67, 0x2e, 0x76, 0x31, 0x2e, 0x50, 0x61, 0x79, 0x6c, 0x6f, 0x61, 0x64, 0x2e, 0x4d, 0x65, 0x74,
	0x61, 0x64, 0x61, 0x74, 0x61, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x52, 0x08, 0x6d, 0x65, 0x74, 0x61,
	0x64, 0x61, 0x74, 0x61, 0x12, 0x33, 0x0a, 0x07, 0x74, 0x69, 0x6d, 0x65, 0x6f, 0x75, 0x74, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x19, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x44, 0x75, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e,
	0x52, 0x07, 0x74, 0x69, 0x6d, 0x65, 0x6f, 0x75, 0x74, 0x12, 0x1f, 0x0a, 0x0b, 0x73, 0x74, 0x61,
	0x74, 0x75, 0x73, 0x5f, 0x63, 0x6f, 0x64, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0d, 0x52, 0x0a,
	0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x43, 0x6f, 0x64, 0x65, 0x12, 0x25, 0x0a, 0x0e, 0x73, 0x74,
	0x61, 0x74, 0x75, 0x73, 0x5f, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x04, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0d, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x4d, 0x65, 0x73, 0x73, 0x61, 0x67,
	0x65, 0x12, 0x25, 0x0a, 0x0e, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x5f, 0x64, 0x65, 0x74, 0x61,
	0x69, 0x6c, 0x73, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x0d, 0x73, 0x74, 0x61, 0x74, 0x75,
	0x73, 0x44, 0x65, 0x74, 0x61, 0x69, 0x6c, 0x73, 0x12, 0x25, 0x0a, 0x0e, 0x6d, 0x65, 0x73, 0x73,
	0x61, 0x67, 0x65, 0x5f, 0x6c, 0x65, 0x6e, 0x67, 0x74, 0x68, 0x18, 0x06, 0x20, 0x01, 0x28, 0x0d,
	0x52, 0x0d, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x4c, 0x65, 0x6e, 0x67, 0x74, 0x68, 0x12,
	0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x07, 0x20, 0x01, 0x28, 0x0c,
	0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x1a, 0x3b, 0x0a, 0x0d, 0x4d, 0x65, 0x74,
	0x61, 0x64, 0x61, 0x74, 0x61, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65,
	0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x03, 0x6b, 0x65, 0x79, 0x12, 0x14, 0x0a, 0x05,
	0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x76, 0x61, 0x6c,
	0x75, 0x65, 0x3a, 0x02, 0x38, 0x01, 0x22, 0x91, 0x0a, 0x0a, 0x0d, 0x47, 0x72, 0x70, 0x63, 0x4c,
	0x6f, 0x67, 0x52, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x12, 0x17, 0x0a, 0x07, 0x63, 0x61, 0x6c, 0x6c,
	0x5f, 0x69, 0x64, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x63, 0x61, 0x6c, 0x6c, 0x49,
	0x64, 0x12, 0x1f, 0x0a, 0x0b, 0x73, 0x65, 0x71, 0x75, 0x65, 0x6e, 0x63, 0x65, 0x5f, 0x69, 0x64,
	0x18, 0x03, 0x20, 0x01, 0x28, 0x04, 0x52, 0x0a, 0x73, 0x65, 0x71, 0x75, 0x65, 0x6e, 0x63, 0x65,
	0x49, 0x64, 0x12, 0x55, 0x0a, 0x0a, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x5f, 0x74, 0x79, 0x70, 0x65,
	0x18, 0x04, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x36, 0x2e, 0x67, 0x72, 0x70, 0x63, 0x2e, 0x6f, 0x62,
	0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x6c, 0x6f, 0x67, 0x67,
	0x69, 0x6e, 0x67, 0x2e, 0x76, 0x31, 0x2e, 0x47, 0x72, 0x70, 0x63, 0x4c, 0x6f, 0x67, 0x52, 0x65,
	0x63, 0x6f, 0x72, 0x64, 0x2e, 0x45, 0x76, 0x65, 0x6e, 0x74, 0x54, 0x79, 0x70, 0x65, 0x52, 0x09,
	0x65, 0x76, 0x65, 0x6e, 0x74, 0x54, 0x79, 0x70, 0x65, 0x12, 0x56, 0x0a, 0x0c, 0x65, 0x76, 0x65,
	0x6e, 0x74, 0x5f, 0x6c, 0x6f, 0x67, 0x67, 0x65, 0x72, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0e, 0x32,
	0x33, 0x2e, 0x67, 0x72, 0x70, 0x63, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69,
	0x6c, 0x69, 0x74, 0x79, 0x2e, 0x6c, 0x6f, 0x67, 0x67, 0x69, 0x6e, 0x67, 0x2e, 0x76, 0x31, 0x2e,
	0x47, 0x72, 0x70, 0x63, 0x4c, 0x6f, 0x67, 0x52, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x2e, 0x4c, 0x6f,
	0x67, 0x67, 0x65, 0x72, 0x52, 0x0b, 0x65, 0x76, 0x65, 0x6e, 0x74, 0x4c, 0x6f, 0x67, 0x67, 0x65,
	0x72, 0x12, 0x40, 0x0a, 0x07, 0x70, 0x61, 0x79, 0x6c, 0x6f, 0x61, 0x64, 0x18, 0x06, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x26, 0x2e, 0x67, 0x72, 0x70, 0x63, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76,
	0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x6c, 0x6f, 0x67, 0x67, 0x69, 0x6e, 0x67, 0x2e,
	0x76, 0x31, 0x2e, 0x50, 0x61, 0x79, 0x6c, 0x6f, 0x61, 0x64, 0x52, 0x07, 0x70, 0x61, 0x79, 0x6c,
	0x6f, 0x61, 0x64, 0x12, 0x2b, 0x0a, 0x11, 0x70, 0x61, 0x79, 0x6c, 0x6f, 0x61, 0x64, 0x5f, 0x74,
	0x72, 0x75, 0x6e, 0x63, 0x61, 0x74, 0x65, 0x64, 0x18, 0x07, 0x20, 0x01, 0x28, 0x08, 0x52, 0x10,
	0x70, 0x61, 0x79, 0x6c, 0x6f, 0x61, 0x64, 0x54, 0x72, 0x75, 0x6e, 0x63, 0x61, 0x74, 0x65, 0x64,
	0x12, 0x48, 0x0a, 0x04, 0x70, 0x65, 0x65, 0x72, 0x18, 0x08, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x34,
	0x2e, 0x67, 0x72, 0x70, 0x63, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c,
	0x69, 0x74, 0x79, 0x2e, 0x6c, 0x6f, 0x67, 0x67, 0x69, 0x6e, 0x67, 0x2e, 0x76, 0x31, 0x2e, 0x47,
	0x72, 0x70, 0x63, 0x4c, 0x6f, 0x67, 0x52, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x2e, 0x41, 0x64, 0x64,
	0x72, 0x65, 0x73, 0x73, 0x52, 0x04, 0x70, 0x65, 0x65, 0x72, 0x12, 0x1c, 0x0a, 0x09, 0x61, 0x75,
	0x74, 0x68, 0x6f, 0x72, 0x69, 0x74, 0x79, 0x18, 0x0a, 0x20, 0x01, 0x28, 0x09, 0x52, 0x09, 0x61,
	0x75, 0x74, 0x68, 0x6f, 0x72, 0x69, 0x74, 0x79, 0x12, 0x21, 0x0a, 0x0c, 0x73, 0x65, 0x72, 0x76,
	0x69, 0x63, 0x65, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x0b, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b,
	0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x4e, 0x61, 0x6d, 0x65, 0x12, 0x1f, 0x0a, 0x0b, 0x6d,
	0x65, 0x74, 0x68, 0x6f, 0x64, 0x5f, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x0c, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x0a, 0x6d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x4e, 0x61, 0x6d, 0x65, 0x12, 0x21, 0x0a, 0x0c,
	0x70, 0x61, 0x79, 0x6c, 0x6f, 0x61, 0x64, 0x5f, 0x73, 0x69, 0x7a, 0x65, 0x18, 0x0d, 0x20, 0x01,
	0x28, 0x0d, 0x52, 0x0b, 0x70, 0x61, 0x79, 0x6c, 0x6f, 0x61, 0x64, 0x53, 0x69, 0x7a, 0x65, 0x1a,
	0x5c, 0x0a, 0x08, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0x12, 0x50, 0x0a, 0x05, 0x65,
	0x6e, 0x74, 0x72, 0x79, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x3a, 0x2e, 0x67, 0x72, 0x70,
	0x63, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e,
	0x6c, 0x6f, 0x67, 0x67, 0x69, 0x6e, 0x67, 0x2e, 0x76, 0x31, 0x2e, 0x47, 0x72, 0x70, 0x63, 0x4c,
	0x6f, 0x67, 0x52, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x2e, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74,
	0x61, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x52, 0x05, 0x65, 0x6e, 0x74, 0x72, 0x79, 0x1a, 0x37, 0x0a,
	0x0d, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x12, 0x10,
	0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x03, 0x6b, 0x65, 0x79,
	0x12, 0x14, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0c, 0x52,
	0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x1a, 0xd2, 0x01, 0x0a, 0x07, 0x41, 0x64, 0x64, 0x72, 0x65,
	0x73, 0x73, 0x12, 0x4d, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0e,
	0x32, 0x39, 0x2e, 0x67, 0x72, 0x70, 0x63, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62,
	0x69, 0x6c, 0x69, 0x74, 0x79, 0x2e, 0x6c, 0x6f, 0x67, 0x67, 0x69, 0x6e, 0x67, 0x2e, 0x76, 0x31,
	0x2e, 0x47, 0x72, 0x70, 0x63, 0x4c, 0x6f, 0x67, 0x52, 0x65, 0x63, 0x6f, 0x72, 0x64, 0x2e, 0x41,
	0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x2e, 0x54, 0x79, 0x70, 0x65, 0x52, 0x04, 0x74, 0x79, 0x70,
	0x65, 0x12, 0x18, 0x0a, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x17, 0x0a, 0x07, 0x69,
	0x70, 0x5f, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0d, 0x52, 0x06, 0x69, 0x70,
	0x50, 0x6f, 0x72, 0x74, 0x22, 0x45, 0x0a, 0x04, 0x54, 0x79, 0x70, 0x65, 0x12, 0x10, 0x0a, 0x0c,
	0x54, 0x59, 0x50, 0x45, 0x5f, 0x55, 0x4e, 0x4b, 0x4e, 0x4f, 0x57, 0x4e, 0x10, 0x00, 0x12, 0x0d,
	0x0a, 0x09, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x49, 0x50, 0x56, 0x34, 0x10, 0x01, 0x12, 0x0d, 0x0a,
	0x09, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x49, 0x50, 0x56, 0x36, 0x10, 0x02, 0x12, 0x0d, 0x0a, 0x09,
	0x54, 0x59, 0x50, 0x45, 0x5f, 0x55, 0x4e, 0x49, 0x58, 0x10, 0x03, 0x22, 0x9a, 0x01, 0x0a, 0x09,
	0x45, 0x76, 0x65, 0x6e, 0x74, 0x54, 0x79, 0x70, 0x65, 0x12, 0x08, 0x0a, 0x04, 0x4e, 0x4f, 0x4e,
	0x45, 0x10, 0x00, 0x12, 0x11, 0x0a, 0x0d, 0x43, 0x4c, 0x49, 0x45, 0x4e, 0x54, 0x5f, 0x48, 0x45,
	0x41, 0x44, 0x45, 0x52, 0x10, 0x01, 0x12, 0x11, 0x0a, 0x0d, 0x53, 0x45, 0x52, 0x56, 0x45, 0x52,
	0x5f, 0x48, 0x45, 0x41, 0x44, 0x45, 0x52, 0x10, 0x02, 0x12, 0x12, 0x0a, 0x0e, 0x43, 0x4c, 0x49,
	0x45, 0x4e, 0x54, 0x5f, 0x4d, 0x45, 0x53, 0x53, 0x41, 0x47, 0x45, 0x10, 0x03, 0x12, 0x12, 0x0a,
	0x0e, 0x53, 0x45, 0x52, 0x56, 0x45, 0x52, 0x5f, 0x4d, 0x45, 0x53, 0x53, 0x41, 0x47, 0x45, 0x10,
	0x04, 0x12, 0x15, 0x0a, 0x11, 0x43, 0x4c, 0x49, 0x45, 0x4e, 0x54, 0x5f, 0x48, 0x41, 0x4c, 0x46,
	0x5f, 0x43, 0x4c, 0x4f, 0x53, 0x45, 0x10, 0x05, 0x12, 0x12, 0x0a, 0x0e, 0x53, 0x45, 0x52, 0x56,
	0x45, 0x52, 0x5f, 0x54, 0x52, 0x41, 0x49, 0x4c, 0x45, 0x52, 0x10, 0x06, 0x12, 0x0a, 0x0a, 0x06,
	0x43, 0x41, 0x4e, 0x43, 0x45, 0x4c, 0x10, 0x07, 0x22, 0x2d, 0x0a, 0x06, 0x4c, 0x6f, 0x67, 0x67,
	0x65, 0x72, 0x12, 0x0b, 0x0a, 0x07, 0x55, 0x4e, 0x4b, 0x4e, 0x4f, 0x57, 0x4e, 0x10, 0x00, 0x12,
	0x0a, 0x0a, 0x06, 0x43, 0x4c, 0x49, 0x45, 0x4e, 0x54, 0x10, 0x01, 0x12, 0x0a, 0x0a, 0x06, 0x53,
	0x45, 0x52, 0x56, 0x45, 0x52, 0x10, 0x02, 0x22, 0xa0, 0x01, 0x0a, 0x08, 0x4c, 0x6f, 0x67, 0x4c,
	0x65, 0x76, 0x65, 0x6c, 0x12, 0x15, 0x0a, 0x11, 0x4c, 0x4f, 0x47, 0x5f, 0x4c, 0x45, 0x56, 0x45,
	0x4c, 0x5f, 0x55, 0x4e, 0x4b, 0x4e, 0x4f, 0x57, 0x4e, 0x10, 0x00, 0x12, 0x13, 0x0a, 0x0f, 0x4c,
	0x4f, 0x47, 0x5f, 0x4c, 0x45, 0x56, 0x45, 0x4c, 0x5f, 0x54, 0x52, 0x41, 0x43, 0x45, 0x10, 0x01,
	0x12, 0x13, 0x0a, 0x0f, 0x4c, 0x4f, 0x47, 0x5f, 0x4c, 0x45, 0x56, 0x45, 0x4c, 0x5f, 0x44, 0x45,
	0x42, 0x55, 0x47, 0x10, 0x02, 0x12, 0x12, 0x0a, 0x0e, 0x4c, 0x4f, 0x47, 0x5f, 0x4c, 0x45, 0x56,
	0x45, 0x4c, 0x5f, 0x49, 0x4e, 0x46, 0x4f, 0x10, 0x03, 0x12, 0x12, 0x0a, 0x0e, 0x4c, 0x4f, 0x47,
	0x5f, 0x4c, 0x45, 0x56, 0x45, 0x4c, 0x5f, 0x57, 0x41, 0x52, 0x4e, 0x10, 0x04, 0x12, 0x13, 0x0a,
	0x0f, 0x4c, 0x4f, 0x47, 0x5f, 0x4c, 0x45, 0x56, 0x45, 0x4c, 0x5f, 0x45, 0x52, 0x52, 0x4f, 0x52,
	0x10, 0x05, 0x12, 0x16, 0x0a, 0x12, 0x4c, 0x4f, 0x47, 0x5f, 0x4c, 0x45, 0x56, 0x45, 0x4c, 0x5f,
	0x43, 0x52, 0x49, 0x54, 0x49, 0x43, 0x41, 0x4c, 0x10, 0x06, 0x42, 0x77, 0x0a, 0x1d, 0x69, 0x6f,
	0x2e, 0x67, 0x72, 0x70, 0x63, 0x2e, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c,
	0x69, 0x74, 0x79, 0x2e, 0x6c, 0x6f, 0x67, 0x67, 0x69, 0x6e, 0x67, 0x42, 0x19, 0x4f, 0x62, 0x73,
	0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69, 0x74, 0x79, 0x4c, 0x6f, 0x67, 0x67, 0x69, 0x6e,
	0x67, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x39, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65,
	0x2e, 0x67, 0x6f, 0x6c, 0x61, 0x6e, 0x67, 0x2e, 0x6f, 0x72, 0x67, 0x2f, 0x67, 0x72, 0x70, 0x63,
	0x2f, 0x67, 0x63, 0x70, 0x2f, 0x6f, 0x62, 0x73, 0x65, 0x72, 0x76, 0x61, 0x62, 0x69, 0x6c, 0x69,
	0x74, 0x79, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x6c, 0x6f, 0x67, 0x67,
	0x69, 0x6e, 0x67, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_logging_logging_proto_rawDescOnce sync.Once
	file_logging_logging_proto_rawDescData = file_logging_logging_proto_rawDesc
)

func file_logging_logging_proto_rawDescGZIP() []byte {
	file_logging_logging_proto_rawDescOnce.Do(func() {
		file_logging_logging_proto_rawDescData = protoimpl.X.CompressGZIP(file_logging_logging_proto_rawDescData)
	})
	return file_logging_logging_proto_rawDescData
}

var file_logging_logging_proto_enumTypes = make([]protoimpl.EnumInfo, 4)
var file_logging_logging_proto_msgTypes = make([]protoimpl.MessageInfo, 6)
var file_logging_logging_proto_goTypes = []interface{}{
	(GrpcLogRecord_EventType)(0),        // 0: grpc.observability.logging.v1.GrpcLogRecord.EventType
	(GrpcLogRecord_Logger)(0),           // 1: grpc.observability.logging.v1.GrpcLogRecord.Logger
	(GrpcLogRecord_LogLevel)(0),         // 2: grpc.observability.logging.v1.GrpcLogRecord.LogLevel
	(GrpcLogRecord_Address_Type)(0),     // 3: grpc.observability.logging.v1.GrpcLogRecord.Address.Type
	(*Payload)(nil),                     // 4: grpc.observability.logging.v1.Payload
	(*GrpcLogRecord)(nil),               // 5: grpc.observability.logging.v1.GrpcLogRecord
	nil,                                 // 6: grpc.observability.logging.v1.Payload.MetadataEntry
	(*GrpcLogRecord_Metadata)(nil),      // 7: grpc.observability.logging.v1.GrpcLogRecord.Metadata
	(*GrpcLogRecord_MetadataEntry)(nil), // 8: grpc.observability.logging.v1.GrpcLogRecord.MetadataEntry
	(*GrpcLogRecord_Address)(nil),       // 9: grpc.observability.logging.v1.GrpcLogRecord.Address
	(*durationpb.Duration)(nil),         // 10: google.protobuf.Duration
}
var file_logging_logging_proto_depIdxs = []int32{
	6,  // 0: grpc.observability.logging.v1.Payload.metadata:type_name -> grpc.observability.logging.v1.Payload.MetadataEntry
	10, // 1: grpc.observability.logging.v1.Payload.timeout:type_name -> google.protobuf.Duration
	0,  // 2: grpc.observability.logging.v1.GrpcLogRecord.event_type:type_name -> grpc.observability.logging.v1.GrpcLogRecord.EventType
	1,  // 3: grpc.observability.logging.v1.GrpcLogRecord.event_logger:type_name -> grpc.observability.logging.v1.GrpcLogRecord.Logger
	4,  // 4: grpc.observability.logging.v1.GrpcLogRecord.payload:type_name -> grpc.observability.logging.v1.Payload
	9,  // 5: grpc.observability.logging.v1.GrpcLogRecord.peer:type_name -> grpc.observability.logging.v1.GrpcLogRecord.Address
	8,  // 6: grpc.observability.logging.v1.GrpcLogRecord.Metadata.entry:type_name -> grpc.observability.logging.v1.GrpcLogRecord.MetadataEntry
	3,  // 7: grpc.observability.logging.v1.GrpcLogRecord.Address.type:type_name -> grpc.observability.logging.v1.GrpcLogRecord.Address.Type
	8,  // [8:8] is the sub-list for method output_type
	8,  // [8:8] is the sub-list for method input_type
	8,  // [8:8] is the sub-list for extension type_name
	8,  // [8:8] is the sub-list for extension extendee
	0,  // [0:8] is the sub-list for field type_name
}

func init() { file_logging_logging_proto_init() }
func file_logging_logging_proto_init() {
	if File_logging_logging_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_logging_logging_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Payload); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_logging_logging_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GrpcLogRecord); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_logging_logging_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GrpcLogRecord_Metadata); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_logging_logging_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GrpcLogRecord_MetadataEntry); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_logging_logging_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GrpcLogRecord_Address); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_logging_logging_proto_rawDesc,
			NumEnums:      4,
			NumMessages:   6,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_logging_logging_proto_goTypes,
		DependencyIndexes: file_logging_logging_proto_depIdxs,
		EnumInfos:         file_logging_logging_proto_enumTypes,
		MessageInfos:      file_logging_logging_proto_msgTypes,
	}.Build()
	File_logging_logging_proto = out.File
	file_logging_logging_proto_rawDesc = nil
	file_logging_logging_proto_goTypes = nil
	file_logging_logging_proto_depIdxs = nil
}
