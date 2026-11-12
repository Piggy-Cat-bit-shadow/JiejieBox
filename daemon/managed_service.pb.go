package daemon

import (
	reflect "reflect"
	sync "sync"
	unsafe "unsafe"

	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type DebugCrashRequest_Type int32

const (
	DebugCrashRequest_GO     DebugCrashRequest_Type = 0
	DebugCrashRequest_NATIVE DebugCrashRequest_Type = 1
)

// Enum value maps for DebugCrashRequest_Type.
var (
	DebugCrashRequest_Type_name = map[int32]string{
		0: "GO",
		1: "NATIVE",
	}
	DebugCrashRequest_Type_value = map[string]int32{
		"GO":     0,
		"NATIVE": 1,
	}
)

func (x DebugCrashRequest_Type) Enum() *DebugCrashRequest_Type {
	p := new(DebugCrashRequest_Type)
	*p = x
	return p
}

func (x DebugCrashRequest_Type) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (DebugCrashRequest_Type) Descriptor() protoreflect.EnumDescriptor {
	return file_daemon_managed_service_proto_enumTypes[0].Descriptor()
}

func (DebugCrashRequest_Type) Type() protoreflect.EnumType {
	return &file_daemon_managed_service_proto_enumTypes[0]
}

func (x DebugCrashRequest_Type) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use DebugCrashRequest_Type.Descriptor instead.
func (DebugCrashRequest_Type) EnumDescriptor() ([]byte, []int) {
	return file_daemon_managed_service_proto_rawDescGZIP(), []int{4, 0}
}

type ReloadServiceRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The caller's identity for THIS attempt. It is echoed in the response, so a confirmation
	// that arrives after the caller moved on cannot settle a different attempt.
	RequestId string `protobuf:"bytes,1,opt,name=requestId,proto3" json:"requestId,omitempty"`
	// The instance the caller last saw, or empty when it does not track instances. A mismatch
	// means the reload was decided against a state the caller had not seen.
	ExpectedInstanceId string `protobuf:"bytes,2,opt,name=expectedInstanceId,proto3" json:"expectedInstanceId,omitempty"`
	unknownFields      protoimpl.UnknownFields
	sizeCache          protoimpl.SizeCache
}

func (x *ReloadServiceRequest) Reset() {
	*x = ReloadServiceRequest{}
	mi := &file_daemon_managed_service_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ReloadServiceRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ReloadServiceRequest) ProtoMessage() {}

func (x *ReloadServiceRequest) ProtoReflect() protoreflect.Message {
	mi := &file_daemon_managed_service_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ReloadServiceRequest.ProtoReflect.Descriptor instead.
func (*ReloadServiceRequest) Descriptor() ([]byte, []int) {
	return file_daemon_managed_service_proto_rawDescGZIP(), []int{0}
}

func (x *ReloadServiceRequest) GetRequestId() string {
	if x != nil {
		return x.RequestId
	}
	return ""
}

func (x *ReloadServiceRequest) GetExpectedInstanceId() string {
	if x != nil {
		return x.ExpectedInstanceId
	}
	return ""
}

type ReloadServiceResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// The identity from the request.
	RequestId string `protobuf:"bytes,1,opt,name=requestId,proto3" json:"requestId,omitempty"`
	// The instance running after the attempt. Empty when no instance is running.
	InstanceId string `protobuf:"bytes,2,opt,name=instanceId,proto3" json:"instanceId,omitempty"`
	// The profile revision serving after the attempt, or empty when nothing is running.
	ActiveRevision string `protobuf:"bytes,3,opt,name=activeRevision,proto3" json:"activeRevision,omitempty"`
	// The caller's expectedInstanceId did not match when the request arrived. The reload is still
	// performed: refusing would leave the caller unable to reach a state it can see.
	Stale bool `protobuf:"varint,4,opt,name=stale,proto3" json:"stale,omitempty"`
	// What the attempt left: "restarted" when a new instance is running, "stopped" when none is.
	Status        string `protobuf:"bytes,5,opt,name=status,proto3" json:"status,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ReloadServiceResponse) Reset() {
	*x = ReloadServiceResponse{}
	mi := &file_daemon_managed_service_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ReloadServiceResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ReloadServiceResponse) ProtoMessage() {}

func (x *ReloadServiceResponse) ProtoReflect() protoreflect.Message {
	mi := &file_daemon_managed_service_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ReloadServiceResponse.ProtoReflect.Descriptor instead.
func (*ReloadServiceResponse) Descriptor() ([]byte, []int) {
	return file_daemon_managed_service_proto_rawDescGZIP(), []int{1}
}

func (x *ReloadServiceResponse) GetRequestId() string {
	if x != nil {
		return x.RequestId
	}
	return ""
}

func (x *ReloadServiceResponse) GetInstanceId() string {
	if x != nil {
		return x.InstanceId
	}
	return ""
}

func (x *ReloadServiceResponse) GetActiveRevision() string {
	if x != nil {
		return x.ActiveRevision
	}
	return ""
}

func (x *ReloadServiceResponse) GetStale() bool {
	if x != nil {
		return x.Stale
	}
	return false
}

func (x *ReloadServiceResponse) GetStatus() string {
	if x != nil {
		return x.Status
	}
	return ""
}

type SystemProxyStatus struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Available     bool                   `protobuf:"varint,1,opt,name=available,proto3" json:"available,omitempty"`
	Enabled       bool                   `protobuf:"varint,2,opt,name=enabled,proto3" json:"enabled,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *SystemProxyStatus) Reset() {
	*x = SystemProxyStatus{}
	mi := &file_daemon_managed_service_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SystemProxyStatus) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SystemProxyStatus) ProtoMessage() {}

func (x *SystemProxyStatus) ProtoReflect() protoreflect.Message {
	mi := &file_daemon_managed_service_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SystemProxyStatus.ProtoReflect.Descriptor instead.
func (*SystemProxyStatus) Descriptor() ([]byte, []int) {
	return file_daemon_managed_service_proto_rawDescGZIP(), []int{2}
}

func (x *SystemProxyStatus) GetAvailable() bool {
	if x != nil {
		return x.Available
	}
	return false
}

func (x *SystemProxyStatus) GetEnabled() bool {
	if x != nil {
		return x.Enabled
	}
	return false
}

type SetSystemProxyEnabledRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Enabled       bool                   `protobuf:"varint,1,opt,name=enabled,proto3" json:"enabled,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *SetSystemProxyEnabledRequest) Reset() {
	*x = SetSystemProxyEnabledRequest{}
	mi := &file_daemon_managed_service_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SetSystemProxyEnabledRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SetSystemProxyEnabledRequest) ProtoMessage() {}

func (x *SetSystemProxyEnabledRequest) ProtoReflect() protoreflect.Message {
	mi := &file_daemon_managed_service_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SetSystemProxyEnabledRequest.ProtoReflect.Descriptor instead.
func (*SetSystemProxyEnabledRequest) Descriptor() ([]byte, []int) {
	return file_daemon_managed_service_proto_rawDescGZIP(), []int{3}
}

func (x *SetSystemProxyEnabledRequest) GetEnabled() bool {
	if x != nil {
		return x.Enabled
	}
	return false
}

type DebugCrashRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Type          DebugCrashRequest_Type `protobuf:"varint,1,opt,name=type,proto3,enum=daemon.DebugCrashRequest_Type" json:"type,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DebugCrashRequest) Reset() {
	*x = DebugCrashRequest{}
	mi := &file_daemon_managed_service_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DebugCrashRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DebugCrashRequest) ProtoMessage() {}

func (x *DebugCrashRequest) ProtoReflect() protoreflect.Message {
	mi := &file_daemon_managed_service_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DebugCrashRequest.ProtoReflect.Descriptor instead.
func (*DebugCrashRequest) Descriptor() ([]byte, []int) {
	return file_daemon_managed_service_proto_rawDescGZIP(), []int{4}
}

func (x *DebugCrashRequest) GetType() DebugCrashRequest_Type {
	if x != nil {
		return x.Type
	}
	return DebugCrashRequest_GO
}

var File_daemon_managed_service_proto protoreflect.FileDescriptor

const file_daemon_managed_service_proto_rawDesc = "" +
	"\n" +
	"\x1cdaemon/managed_service.proto\x12\x06daemon\x1a\x1bgoogle/protobuf/empty.proto\"d\n" +
	"\x14ReloadServiceRequest\x12\x1c\n" +
	"\trequestId\x18\x01 \x01(\tR\trequestId\x12.\n" +
	"\x12expectedInstanceId\x18\x02 \x01(\tR\x12expectedInstanceId\"\xab\x01\n" +
	"\x15ReloadServiceResponse\x12\x1c\n" +
	"\trequestId\x18\x01 \x01(\tR\trequestId\x12\x1e\n" +
	"\n" +
	"instanceId\x18\x02 \x01(\tR\n" +
	"instanceId\x12&\n" +
	"\x0eactiveRevision\x18\x03 \x01(\tR\x0eactiveRevision\x12\x14\n" +
	"\x05stale\x18\x04 \x01(\bR\x05stale\x12\x16\n" +
	"\x06status\x18\x05 \x01(\tR\x06status\"K\n" +
	"\x11SystemProxyStatus\x12\x1c\n" +
	"\tavailable\x18\x01 \x01(\bR\tavailable\x12\x18\n" +
	"\aenabled\x18\x02 \x01(\bR\aenabled\"8\n" +
	"\x1cSetSystemProxyEnabledRequest\x12\x18\n" +
	"\aenabled\x18\x01 \x01(\bR\aenabled\"c\n" +
	"\x11DebugCrashRequest\x122\n" +
	"\x04type\x18\x01 \x01(\x0e2\x1e.daemon.DebugCrashRequest.TypeR\x04type\"\x1a\n" +
	"\x04Type\x12\x06\n" +
	"\x02GO\x10\x00\x12\n" +
	"\n" +
	"\x06NATIVE\x10\x012\xa1\x04\n" +
	"\x0eManagedService\x12=\n" +
	"\vStopService\x12\x16.google.protobuf.Empty\x1a\x16.google.protobuf.Empty\x12?\n" +
	"\rReloadService\x12\x16.google.protobuf.Empty\x1a\x16.google.protobuf.Empty\x12Y\n" +
	"\x18ReloadServiceWithRequest\x12\x1c.daemon.ReloadServiceRequest\x1a\x1d.daemon.ReloadServiceResponse\"\x00\x12K\n" +
	"\x14GetSystemProxyStatus\x12\x16.google.protobuf.Empty\x1a\x19.daemon.SystemProxyStatus\"\x00\x12W\n" +
	"\x15SetSystemProxyEnabled\x12$.daemon.SetSystemProxyEnabledRequest\x1a\x16.google.protobuf.Empty\"\x00\x12H\n" +
	"\x11TriggerDebugCrash\x12\x19.daemon.DebugCrashRequest\x1a\x16.google.protobuf.Empty\"\x00\x12D\n" +
	"\x10TriggerOOMReport\x12\x16.google.protobuf.Empty\x1a\x16.google.protobuf.Empty\"\x00B%Z#github.com/sagernet/sing-box/daemonb\x06proto3"

var (
	file_daemon_managed_service_proto_rawDescOnce sync.Once
	file_daemon_managed_service_proto_rawDescData []byte
)

func file_daemon_managed_service_proto_rawDescGZIP() []byte {
	file_daemon_managed_service_proto_rawDescOnce.Do(func() {
		file_daemon_managed_service_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_daemon_managed_service_proto_rawDesc), len(file_daemon_managed_service_proto_rawDesc)))
	})
	return file_daemon_managed_service_proto_rawDescData
}

var file_daemon_managed_service_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_daemon_managed_service_proto_msgTypes = make([]protoimpl.MessageInfo, 5)
var file_daemon_managed_service_proto_goTypes = []any{
	(DebugCrashRequest_Type)(0),          // 0: daemon.DebugCrashRequest.Type
	(*ReloadServiceRequest)(nil),         // 1: daemon.ReloadServiceRequest
	(*ReloadServiceResponse)(nil),        // 2: daemon.ReloadServiceResponse
	(*SystemProxyStatus)(nil),            // 3: daemon.SystemProxyStatus
	(*SetSystemProxyEnabledRequest)(nil), // 4: daemon.SetSystemProxyEnabledRequest
	(*DebugCrashRequest)(nil),            // 5: daemon.DebugCrashRequest
	(*emptypb.Empty)(nil),                // 6: google.protobuf.Empty
}
var file_daemon_managed_service_proto_depIdxs = []int32{
	0, // 0: daemon.DebugCrashRequest.type:type_name -> daemon.DebugCrashRequest.Type
	6, // 1: daemon.ManagedService.StopService:input_type -> google.protobuf.Empty
	6, // 2: daemon.ManagedService.ReloadService:input_type -> google.protobuf.Empty
	1, // 3: daemon.ManagedService.ReloadServiceWithRequest:input_type -> daemon.ReloadServiceRequest
	6, // 4: daemon.ManagedService.GetSystemProxyStatus:input_type -> google.protobuf.Empty
	4, // 5: daemon.ManagedService.SetSystemProxyEnabled:input_type -> daemon.SetSystemProxyEnabledRequest
	5, // 6: daemon.ManagedService.TriggerDebugCrash:input_type -> daemon.DebugCrashRequest
	6, // 7: daemon.ManagedService.TriggerOOMReport:input_type -> google.protobuf.Empty
	6, // 8: daemon.ManagedService.StopService:output_type -> google.protobuf.Empty
	6, // 9: daemon.ManagedService.ReloadService:output_type -> google.protobuf.Empty
	2, // 10: daemon.ManagedService.ReloadServiceWithRequest:output_type -> daemon.ReloadServiceResponse
	3, // 11: daemon.ManagedService.GetSystemProxyStatus:output_type -> daemon.SystemProxyStatus
	6, // 12: daemon.ManagedService.SetSystemProxyEnabled:output_type -> google.protobuf.Empty
	6, // 13: daemon.ManagedService.TriggerDebugCrash:output_type -> google.protobuf.Empty
	6, // 14: daemon.ManagedService.TriggerOOMReport:output_type -> google.protobuf.Empty
	8, // [8:15] is the sub-list for method output_type
	1, // [1:8] is the sub-list for method input_type
	1, // [1:1] is the sub-list for extension type_name
	1, // [1:1] is the sub-list for extension extendee
	0, // [0:1] is the sub-list for field type_name
}

func init() { file_daemon_managed_service_proto_init() }
func file_daemon_managed_service_proto_init() {
	if File_daemon_managed_service_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_daemon_managed_service_proto_rawDesc), len(file_daemon_managed_service_proto_rawDesc)),
			NumEnums:      1,
			NumMessages:   5,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_daemon_managed_service_proto_goTypes,
		DependencyIndexes: file_daemon_managed_service_proto_depIdxs,
		EnumInfos:         file_daemon_managed_service_proto_enumTypes,
		MessageInfos:      file_daemon_managed_service_proto_msgTypes,
	}.Build()
	File_daemon_managed_service_proto = out.File
	file_daemon_managed_service_proto_goTypes = nil
	file_daemon_managed_service_proto_depIdxs = nil
}
