package grpcapi

import (
	"github.com/golang/protobuf/proto"
)

// Legacy protobuf messages: keep it simple and codegen-free.

type CreateObjectRequest struct {
	TenantId    string `json:"tenant_id,omitempty"    protobuf:"bytes,1,opt,name=tenant_id,json=tenantId,proto3"`
	ContentType string `json:"content_type,omitempty" protobuf:"bytes,2,opt,name=content_type,json=contentType,proto3"`
	SizeBytes   int64  `json:"size_bytes,omitempty"   protobuf:"varint,3,opt,name=size_bytes,json=sizeBytes,proto3"`
}

func (*CreateObjectRequest) Reset()         {}
func (*CreateObjectRequest) String() string { return "CreateObjectRequest" }
func (*CreateObjectRequest) ProtoMessage()  {}

var _ proto.Message = (*CreateObjectRequest)(nil)

type CreateObjectResponse struct {
	ObjectId      string            `json:"object_id,omitempty"       protobuf:"bytes,1,opt,name=object_id,json=objectId,proto3"`
	ObjectKey     string            `json:"object_key,omitempty"      protobuf:"bytes,2,opt,name=object_key,json=objectKey,proto3"`
	UploadUrl     string            `json:"upload_url,omitempty"      protobuf:"bytes,3,opt,name=upload_url,json=uploadUrl,proto3"`
	Method        string            `json:"method,omitempty"          protobuf:"bytes,4,opt,name=method,proto3"`
	Headers       map[string]string `json:"headers,omitempty"         protobuf:"bytes,5,rep,name=headers,proto3"                             protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	ExpiresAtUnix int64             `json:"expires_at_unix,omitempty" protobuf:"varint,6,opt,name=expires_at_unix,json=expiresAtUnix,proto3"`
}

func (*CreateObjectResponse) Reset()         {}
func (*CreateObjectResponse) String() string { return "CreateObjectResponse" }
func (*CreateObjectResponse) ProtoMessage()  {}

var _ proto.Message = (*CreateObjectResponse)(nil)

type GetObjectRequest struct {
	TenantId string `json:"tenant_id,omitempty" protobuf:"bytes,1,opt,name=tenant_id,json=tenantId,proto3"`
	ObjectId string `json:"object_id,omitempty" protobuf:"bytes,2,opt,name=object_id,json=objectId,proto3"`
}

func (*GetObjectRequest) Reset()         {}
func (*GetObjectRequest) String() string { return "GetObjectRequest" }
func (*GetObjectRequest) ProtoMessage()  {}

var _ proto.Message = (*GetObjectRequest)(nil)

type GetObjectResponse struct {
	ObjectId      string `json:"object_id,omitempty"       protobuf:"bytes,1,opt,name=object_id,json=objectId,proto3"`
	ObjectKey     string `json:"object_key,omitempty"      protobuf:"bytes,2,opt,name=object_key,json=objectKey,proto3"`
	Bucket        string `json:"bucket,omitempty"          protobuf:"bytes,3,opt,name=bucket,proto3"`
	ContentType   string `json:"content_type,omitempty"    protobuf:"bytes,4,opt,name=content_type,json=contentType,proto3"`
	SizeBytes     int64  `json:"size_bytes,omitempty"      protobuf:"varint,5,opt,name=size_bytes,json=sizeBytes,proto3"`
	Status        string `json:"status,omitempty"          protobuf:"bytes,6,opt,name=status,proto3"`
	DownloadUrl   string `json:"download_url,omitempty"    protobuf:"bytes,7,opt,name=download_url,json=downloadUrl,proto3"`
	ExpiresAtUnix int64  `json:"expires_at_unix,omitempty" protobuf:"varint,8,opt,name=expires_at_unix,json=expiresAtUnix,proto3"`
}

func (*GetObjectResponse) Reset()         {}
func (*GetObjectResponse) String() string { return "GetObjectResponse" }
func (*GetObjectResponse) ProtoMessage()  {}

var _ proto.Message = (*GetObjectResponse)(nil)

type CompleteObjectRequest struct {
	TenantId string `json:"tenant_id,omitempty" protobuf:"bytes,1,opt,name=tenant_id,json=tenantId,proto3"`
	ObjectId string `json:"object_id,omitempty" protobuf:"bytes,2,opt,name=object_id,json=objectId,proto3"`
}

func (*CompleteObjectRequest) Reset()         {}
func (*CompleteObjectRequest) String() string { return "CompleteObjectRequest" }
func (*CompleteObjectRequest) ProtoMessage()  {}

var _ proto.Message = (*CompleteObjectRequest)(nil)

type CompleteObjectResponse struct {
	Status string `json:"status,omitempty" protobuf:"bytes,1,opt,name=status,proto3"`
}

func (*CompleteObjectResponse) Reset()         {}
func (*CompleteObjectResponse) String() string { return "CompleteObjectResponse" }
func (*CompleteObjectResponse) ProtoMessage()  {}

var _ proto.Message = (*CompleteObjectResponse)(nil)

type InitiateMultipartRequest struct {
	TenantId    string `json:"tenant_id,omitempty"    protobuf:"bytes,1,opt,name=tenant_id,json=tenantId,proto3"`
	ContentType string `json:"content_type,omitempty" protobuf:"bytes,2,opt,name=content_type,json=contentType,proto3"`
	SizeBytes   int64  `json:"size_bytes,omitempty"   protobuf:"varint,3,opt,name=size_bytes,json=sizeBytes,proto3"`
}

func (*InitiateMultipartRequest) Reset()         {}
func (*InitiateMultipartRequest) String() string { return "InitiateMultipartRequest" }
func (*InitiateMultipartRequest) ProtoMessage()  {}

var _ proto.Message = (*InitiateMultipartRequest)(nil)

type InitiateMultipartResponse struct {
	ObjectId      string `json:"object_id,omitempty"       protobuf:"bytes,1,opt,name=object_id,json=objectId,proto3"`
	ObjectKey     string `json:"object_key,omitempty"      protobuf:"bytes,2,opt,name=object_key,json=objectKey,proto3"`
	UploadId      string `json:"upload_id,omitempty"       protobuf:"bytes,3,opt,name=upload_id,json=uploadId,proto3"`
	PartSize      int64  `json:"part_size,omitempty"       protobuf:"varint,4,opt,name=part_size,json=partSize,proto3"`
	ExpiresAtUnix int64  `json:"expires_at_unix,omitempty" protobuf:"varint,5,opt,name=expires_at_unix,json=expiresAtUnix,proto3"`
}

func (*InitiateMultipartResponse) Reset()         {}
func (*InitiateMultipartResponse) String() string { return "InitiateMultipartResponse" }
func (*InitiateMultipartResponse) ProtoMessage()  {}

var _ proto.Message = (*InitiateMultipartResponse)(nil)

type SignPartRequest struct {
	TenantId   string `json:"tenant_id,omitempty"   protobuf:"bytes,1,opt,name=tenant_id,json=tenantId,proto3"`
	UploadId   string `json:"upload_id,omitempty"   protobuf:"bytes,2,opt,name=upload_id,json=uploadId,proto3"`
	PartNumber int32  `json:"part_number,omitempty" protobuf:"varint,3,opt,name=part_number,json=partNumber,proto3"`
}

func (*SignPartRequest) Reset()         {}
func (*SignPartRequest) String() string { return "SignPartRequest" }
func (*SignPartRequest) ProtoMessage()  {}

var _ proto.Message = (*SignPartRequest)(nil)

type SignPartResponse struct {
	UploadUrl     string `json:"upload_url,omitempty"      protobuf:"bytes,1,opt,name=upload_url,json=uploadUrl,proto3"`
	Method        string `json:"method,omitempty"          protobuf:"bytes,2,opt,name=method,proto3"`
	ExpiresAtUnix int64  `json:"expires_at_unix,omitempty" protobuf:"varint,3,opt,name=expires_at_unix,json=expiresAtUnix,proto3"`
}

func (*SignPartResponse) Reset()         {}
func (*SignPartResponse) String() string { return "SignPartResponse" }
func (*SignPartResponse) ProtoMessage()  {}

var _ proto.Message = (*SignPartResponse)(nil)

type CompleteMultipartRequest struct {
	TenantId string                   `json:"tenant_id,omitempty" protobuf:"bytes,1,opt,name=tenant_id,json=tenantId,proto3"`
	UploadId string                   `json:"upload_id,omitempty" protobuf:"bytes,2,opt,name=upload_id,json=uploadId,proto3"`
	Parts    []*CompleteMultipartPart `json:"parts,omitempty"     protobuf:"bytes,3,rep,name=parts,proto3"`
}

func (*CompleteMultipartRequest) Reset()         {}
func (*CompleteMultipartRequest) String() string { return "CompleteMultipartRequest" }
func (*CompleteMultipartRequest) ProtoMessage()  {}

var _ proto.Message = (*CompleteMultipartRequest)(nil)

type CompleteMultipartPart struct {
	PartNumber int32  `json:"part_number,omitempty" protobuf:"varint,1,opt,name=part_number,json=partNumber,proto3"`
	Etag       string `json:"etag,omitempty"        protobuf:"bytes,2,opt,name=etag,proto3"`
}

func (*CompleteMultipartPart) Reset()         {}
func (*CompleteMultipartPart) String() string { return "CompleteMultipartPart" }
func (*CompleteMultipartPart) ProtoMessage()  {}

var _ proto.Message = (*CompleteMultipartPart)(nil)

type CompleteMultipartResponse struct {
	ObjectId string `json:"object_id,omitempty" protobuf:"bytes,1,opt,name=object_id,json=objectId,proto3"`
	Status   string `json:"status,omitempty"    protobuf:"bytes,2,opt,name=status,proto3"`
}

func (*CompleteMultipartResponse) Reset()         {}
func (*CompleteMultipartResponse) String() string { return "CompleteMultipartResponse" }
func (*CompleteMultipartResponse) ProtoMessage()  {}

var _ proto.Message = (*CompleteMultipartResponse)(nil)

type AbortMultipartRequest struct {
	TenantId string `json:"tenant_id,omitempty" protobuf:"bytes,1,opt,name=tenant_id,json=tenantId,proto3"`
	UploadId string `json:"upload_id,omitempty" protobuf:"bytes,2,opt,name=upload_id,json=uploadId,proto3"`
}

func (*AbortMultipartRequest) Reset()         {}
func (*AbortMultipartRequest) String() string { return "AbortMultipartRequest" }
func (*AbortMultipartRequest) ProtoMessage()  {}

var _ proto.Message = (*AbortMultipartRequest)(nil)

type AbortMultipartResponse struct {
	Status string `json:"status,omitempty" protobuf:"bytes,1,opt,name=status,proto3"`
}

func (*AbortMultipartResponse) Reset()         {}
func (*AbortMultipartResponse) String() string { return "AbortMultipartResponse" }
func (*AbortMultipartResponse) ProtoMessage()  {}

var _ proto.Message = (*AbortMultipartResponse)(nil)
