// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.25.0
// 	protoc        v3.14.0
// source: zach-example/zach-example.proto

package routeguide

import (
	proto "github.com/golang/protobuf/proto"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// This is a compile-time assertion that a sufficiently up-to-date version
// of the legacy proto package is being used.
const _ = proto.ProtoPackageIsVersion4

type NumberInput struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	NumberInput int32 `protobuf:"varint,1,opt,name=number_input,json=numberInput,proto3" json:"number_input,omitempty"`
}

func (x *NumberInput) Reset() {
	*x = NumberInput{}
	if protoimpl.UnsafeEnabled {
		mi := &file_zach_example_zach_example_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *NumberInput) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*NumberInput) ProtoMessage() {}

func (x *NumberInput) ProtoReflect() protoreflect.Message {
	mi := &file_zach_example_zach_example_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use NumberInput.ProtoReflect.Descriptor instead.
func (*NumberInput) Descriptor() ([]byte, []int) {
	return file_zach_example_zach_example_proto_rawDescGZIP(), []int{0}
}

func (x *NumberInput) GetNumberInput() int32 {
	if x != nil {
		return x.NumberInput
	}
	return 0
}

type NumberOutput struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	NumberOutput int32 `protobuf:"varint,1,opt,name=number_output,json=numberOutput,proto3" json:"number_output,omitempty"`
}

func (x *NumberOutput) Reset() {
	*x = NumberOutput{}
	if protoimpl.UnsafeEnabled {
		mi := &file_zach_example_zach_example_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *NumberOutput) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*NumberOutput) ProtoMessage() {}

func (x *NumberOutput) ProtoReflect() protoreflect.Message {
	mi := &file_zach_example_zach_example_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use NumberOutput.ProtoReflect.Descriptor instead.
func (*NumberOutput) Descriptor() ([]byte, []int) {
	return file_zach_example_zach_example_proto_rawDescGZIP(), []int{1}
}

func (x *NumberOutput) GetNumberOutput() int32 {
	if x != nil {
		return x.NumberOutput
	}
	return 0
}

var File_zach_example_zach_example_proto protoreflect.FileDescriptor

var file_zach_example_zach_example_proto_rawDesc = []byte{
	0x0a, 0x1f, 0x7a, 0x61, 0x63, 0x68, 0x2d, 0x65, 0x78, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x2f, 0x7a,
	0x61, 0x63, 0x68, 0x2d, 0x65, 0x78, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x12, 0x0b, 0x7a, 0x61, 0x63, 0x68, 0x65, 0x78, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x22, 0x30,
	0x0a, 0x0b, 0x4e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x49, 0x6e, 0x70, 0x75, 0x74, 0x12, 0x21, 0x0a,
	0x0c, 0x6e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x5f, 0x69, 0x6e, 0x70, 0x75, 0x74, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x05, 0x52, 0x0b, 0x6e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x49, 0x6e, 0x70, 0x75, 0x74,
	0x22, 0x33, 0x0a, 0x0c, 0x4e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x4f, 0x75, 0x74, 0x70, 0x75, 0x74,
	0x12, 0x23, 0x0a, 0x0d, 0x6e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x5f, 0x6f, 0x75, 0x74, 0x70, 0x75,
	0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x05, 0x52, 0x0c, 0x6e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x4f,
	0x75, 0x74, 0x70, 0x75, 0x74, 0x32, 0xc1, 0x02, 0x0a, 0x0b, 0x5a, 0x61, 0x63, 0x68, 0x45, 0x78,
	0x61, 0x6d, 0x70, 0x6c, 0x65, 0x12, 0x4d, 0x0a, 0x14, 0x4d, 0x61, 0x74, 0x68, 0x65, 0x6d, 0x61,
	0x74, 0x69, 0x63, 0x61, 0x6c, 0x46, 0x75, 0x6e, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x18, 0x2e,
	0x7a, 0x61, 0x63, 0x68, 0x65, 0x78, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x2e, 0x4e, 0x75, 0x6d, 0x62,
	0x65, 0x72, 0x49, 0x6e, 0x70, 0x75, 0x74, 0x1a, 0x19, 0x2e, 0x7a, 0x61, 0x63, 0x68, 0x65, 0x78,
	0x61, 0x6d, 0x70, 0x6c, 0x65, 0x2e, 0x4e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x4f, 0x75, 0x74, 0x70,
	0x75, 0x74, 0x22, 0x00, 0x12, 0x47, 0x0a, 0x0c, 0x5a, 0x65, 0x72, 0x6f, 0x54, 0x6f, 0x4e, 0x75,
	0x6d, 0x62, 0x65, 0x72, 0x12, 0x18, 0x2e, 0x7a, 0x61, 0x63, 0x68, 0x65, 0x78, 0x61, 0x6d, 0x70,
	0x6c, 0x65, 0x2e, 0x4e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x49, 0x6e, 0x70, 0x75, 0x74, 0x1a, 0x19,
	0x2e, 0x7a, 0x61, 0x63, 0x68, 0x65, 0x78, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x2e, 0x4e, 0x75, 0x6d,
	0x62, 0x65, 0x72, 0x4f, 0x75, 0x74, 0x70, 0x75, 0x74, 0x22, 0x00, 0x30, 0x01, 0x12, 0x48, 0x0a,
	0x0d, 0x53, 0x75, 0x6d, 0x41, 0x6c, 0x6c, 0x4e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x73, 0x12, 0x18,
	0x2e, 0x7a, 0x61, 0x63, 0x68, 0x65, 0x78, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x2e, 0x4e, 0x75, 0x6d,
	0x62, 0x65, 0x72, 0x49, 0x6e, 0x70, 0x75, 0x74, 0x1a, 0x19, 0x2e, 0x7a, 0x61, 0x63, 0x68, 0x65,
	0x78, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x2e, 0x4e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x4f, 0x75, 0x74,
	0x70, 0x75, 0x74, 0x22, 0x00, 0x28, 0x01, 0x12, 0x50, 0x0a, 0x13, 0x4e, 0x75, 0x6d, 0x62, 0x65,
	0x72, 0x73, 0x42, 0x61, 0x63, 0x6b, 0x41, 0x6e, 0x64, 0x46, 0x6f, 0x72, 0x74, 0x68, 0x12, 0x18,
	0x2e, 0x7a, 0x61, 0x63, 0x68, 0x65, 0x78, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x2e, 0x4e, 0x75, 0x6d,
	0x62, 0x65, 0x72, 0x49, 0x6e, 0x70, 0x75, 0x74, 0x1a, 0x19, 0x2e, 0x7a, 0x61, 0x63, 0x68, 0x65,
	0x78, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x2e, 0x4e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x4f, 0x75, 0x74,
	0x70, 0x75, 0x74, 0x22, 0x00, 0x28, 0x01, 0x30, 0x01, 0x42, 0x38, 0x5a, 0x36, 0x67, 0x6f, 0x6f,
	0x67, 0x6c, 0x65, 0x2e, 0x67, 0x6f, 0x6c, 0x61, 0x6e, 0x67, 0x2e, 0x6f, 0x72, 0x67, 0x2f, 0x67,
	0x72, 0x70, 0x63, 0x2f, 0x65, 0x78, 0x61, 0x6d, 0x70, 0x6c, 0x65, 0x73, 0x2f, 0x72, 0x6f, 0x75,
	0x74, 0x65, 0x5f, 0x67, 0x75, 0x69, 0x64, 0x65, 0x2f, 0x72, 0x6f, 0x75, 0x74, 0x65, 0x67, 0x75,
	0x69, 0x64, 0x65, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_zach_example_zach_example_proto_rawDescOnce sync.Once
	file_zach_example_zach_example_proto_rawDescData = file_zach_example_zach_example_proto_rawDesc
)

func file_zach_example_zach_example_proto_rawDescGZIP() []byte {
	file_zach_example_zach_example_proto_rawDescOnce.Do(func() {
		file_zach_example_zach_example_proto_rawDescData = protoimpl.X.CompressGZIP(file_zach_example_zach_example_proto_rawDescData)
	})
	return file_zach_example_zach_example_proto_rawDescData
}

var file_zach_example_zach_example_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_zach_example_zach_example_proto_goTypes = []interface{}{
	(*NumberInput)(nil),  // 0: zachexample.NumberInput
	(*NumberOutput)(nil), // 1: zachexample.NumberOutput
}
var file_zach_example_zach_example_proto_depIdxs = []int32{
	0, // 0: zachexample.ZachExample.MathematicalFunction:input_type -> zachexample.NumberInput
	0, // 1: zachexample.ZachExample.ZeroToNumber:input_type -> zachexample.NumberInput
	0, // 2: zachexample.ZachExample.SumAllNumbers:input_type -> zachexample.NumberInput
	0, // 3: zachexample.ZachExample.NumbersBackAndForth:input_type -> zachexample.NumberInput
	1, // 4: zachexample.ZachExample.MathematicalFunction:output_type -> zachexample.NumberOutput
	1, // 5: zachexample.ZachExample.ZeroToNumber:output_type -> zachexample.NumberOutput
	1, // 6: zachexample.ZachExample.SumAllNumbers:output_type -> zachexample.NumberOutput
	1, // 7: zachexample.ZachExample.NumbersBackAndForth:output_type -> zachexample.NumberOutput
	4, // [4:8] is the sub-list for method output_type
	0, // [0:4] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_zach_example_zach_example_proto_init() }
func file_zach_example_zach_example_proto_init() {
	if File_zach_example_zach_example_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_zach_example_zach_example_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*NumberInput); i {
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
		file_zach_example_zach_example_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*NumberOutput); i {
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
			RawDescriptor: file_zach_example_zach_example_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_zach_example_zach_example_proto_goTypes,
		DependencyIndexes: file_zach_example_zach_example_proto_depIdxs,
		MessageInfos:      file_zach_example_zach_example_proto_msgTypes,
	}.Build()
	File_zach_example_zach_example_proto = out.File
	file_zach_example_zach_example_proto_rawDesc = nil
	file_zach_example_zach_example_proto_goTypes = nil
	file_zach_example_zach_example_proto_depIdxs = nil
}
