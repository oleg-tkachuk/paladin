package data

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
)

type ObjectServer struct {
	paladindatav1connect.UnimplementedObjectServiceHandler
	H        *object.Handler
	Versions *object.VersionHandler // optional; nil → versioning RPCs return Unimplemented
}

func NewObjectServer(h *object.Handler, versions *object.VersionHandler) *ObjectServer {
	return &ObjectServer{H: h, Versions: versions}
}

func (s *ObjectServer) UploadObject(ctx context.Context, req *connect.Request[pb.UploadObjectRequest]) (*connect.Response[pb.UploadObjectResponse], error) {
	m := req.Msg
	objectKey, err := objectKeyNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.UploadObject(ctx, object.UploadObjectInput{
		ObjectKey:     objectKey,
		Key:           m.GetKey(),
		ContentType:   m.GetContentType(),
		SizeHint:      m.GetSizeHintBytes(),
		ChecksumAlgo:  checksumAlgoStr(m.GetChecksumAlgorithm()),
		Metadata:      m.GetMetadata(),
		Tags:          m.GetTags(),
		ExternalRef:   m.GetExternalRef(),
		TransportPOST: m.GetTransport() == pb.PresignTransport_PRESIGN_TRANSPORT_POST,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.UploadObjectResponse{
		Object: objectToProto(&out.Object),
		UploadUrl: presignedUrlProto(
			out.URL, out.Method, out.Headers, out.ExpiresAt,
			out.PostAction, out.PostFields,
		),
		CompletionMode: completionModeProto(out.CompletionMode),
	}), nil
}

func (s *ObjectServer) DownloadObject(ctx context.Context, req *connect.Request[pb.DownloadObjectRequest]) (*connect.Response[pb.DownloadObjectResponse], error) {
	m := req.Msg
	_, _, legacyName, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.DownloadObject(ctx, legacyName, m.GetTtl().AsDuration(), m.GetContentDisposition())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DownloadObjectResponse{
		Object: objectToProto(&out.Object),
		DownloadUrl: presignedUrlProto(
			out.URL, "GET", out.Headers, out.ExpiresAt, "", nil,
		),
	}), nil
}

func (s *ObjectServer) GetObject(ctx context.Context, req *connect.Request[pb.GetObjectRequest]) (*connect.Response[pb.Object], error) {
	_, _, legacyName, err := objectNameParts(ctx, req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.GetObject(ctx, legacyName)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(out)), nil
}

func (s *ObjectServer) LookupObject(ctx context.Context, req *connect.Request[pb.LookupObjectRequest]) (*connect.Response[pb.Object], error) {
	objectKey, err := objectKeyNameParts(ctx, req.Msg.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.LookupObject(ctx, objectKey, req.Msg.GetKey())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(out)), nil
}

func (s *ObjectServer) UpdateObject(ctx context.Context, req *connect.Request[pb.UpdateObjectRequest]) (*connect.Response[pb.Object], error) {
	m := req.Msg
	_, _, legacyName, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := parseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.UpdateObject(ctx, object.UpdateObjectInput{
		Name:            legacyName,
		ResourceVersion: rv,
		UpdatedFields:   m.GetUpdateMask().GetPaths(),
		Metadata:        m.GetMetadata(),
		Tags:            m.GetTags(),
		ContentType:     m.GetContentType(),
		ExternalRef:     m.GetExternalRef(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(out)), nil
}

func (s *ObjectServer) CompleteObject(ctx context.Context, req *connect.Request[pb.CompleteObjectRequest]) (*connect.Response[pb.Object], error) {
	m := req.Msg
	_, _, legacyName, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.CompleteObject(ctx, object.CompleteObjectInput{
		Name:     legacyName,
		ETag:     m.GetEtag(),
		Checksum: m.GetChecksumValue(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(out)), nil
}

func (s *ObjectServer) DeleteObject(ctx context.Context, req *connect.Request[pb.DeleteObjectRequest]) (*connect.Response[pb.DeleteObjectResponse], error) {
	m := req.Msg
	_, _, legacyName, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.H.DeleteObject(ctx, legacyName, m.GetResourceVersion(), m.GetPermanent(), m.GetBypassGovernanceRetention()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteObjectResponse{
		Object: &pb.Object{Name: m.GetName()},
	}), nil
}

func (s *ObjectServer) RestoreObject(ctx context.Context, req *connect.Request[pb.RestoreObjectRequest]) (*connect.Response[pb.Object], error) {
	_, _, legacyName, err := objectNameParts(ctx, req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.RestoreObject(ctx, legacyName, req.Msg.GetResourceVersion())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(out)), nil
}

func (s *ObjectServer) CopyObject(ctx context.Context, req *connect.Request[pb.CopyObjectRequest]) (*connect.Response[pb.Object], error) {
	m := req.Msg
	_, _, srcLegacy, err := objectNameParts(ctx, m.GetSourceName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("source: %w", err))
	}
	destObjectKey, err := objectKeyNameParts(ctx, m.GetDestinationObjectKey())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("destination: %w", err))
	}
	in := object.CopyObjectInput{
		SourceName:    srcLegacy,
		DestObjectKey: destObjectKey,
		DestKey:       m.GetDestinationKey(),
	}
	if mo := m.GetMetadataOverride(); mo != nil {
		in.Metadata = mo.GetMetadata()
	}
	if to := m.GetTagsOverride(); to != nil {
		in.Tags = to.GetTags()
	}
	out, err := s.H.CopyObject(ctx, in)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(out)), nil
}

func (s *ObjectServer) ListObjects(ctx context.Context, req *connect.Request[pb.ListObjectsRequest]) (*connect.Response[pb.ListObjectsResponse], error) {
	m := req.Msg
	objectKey, err := objectKeyNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	objs, next, err := s.H.ListObjects(ctx, object.ListObjectsInput{
		ObjectKey: objectKey,
		PageSize:  m.GetPage().GetPageSize(),
		PageToken: m.GetPage().GetPageToken(),
		Filter:    m.GetFilter(),
		OrderBy:   m.GetOrderBy(),
		SortDesc:  m.GetSortOrder() == 2, // SORT_ORDER_DESC
	})
	if err != nil {
		return nil, err
	}
	out := &pb.ListObjectsResponse{Page: pageResponseProto(next)}
	for i := range objs {
		out.Objects = append(out.Objects, objectToProto(&objs[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *ObjectServer) CountObjects(ctx context.Context, req *connect.Request[pb.CountObjectsRequest]) (*connect.Response[pb.CountObjectsResponse], error) {
	m := req.Msg
	objectKey, err := objectKeyNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.CountObjects(ctx, object.CountObjectsInput{
		ObjectKey: objectKey,
		Filter:    m.GetFilter(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.CountObjectsResponse{
		ApproximateCount: out.ApproximateCount,
		Exact:            out.Exact,
	}), nil
}

// ─── Versioning ─────────────────────────────────────────────────────────────
//
// History is read-only on the API side. Inserts happen via
// VersionHandler.RecordPromotion called from promotion paths when the parent
// bucket has versioning_enabled. Each version row is keyed by UUIDv7 so
// "newest first" sorts cleanly without joining created_at.

func (s *ObjectServer) ListObjectVersions(ctx context.Context, req *connect.Request[pb.ListObjectVersionsRequest]) (*connect.Response[pb.ListObjectVersionsResponse], error) {
	if s.Versions == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("versioning not wired"))
	}
	m := req.Msg
	out, next, err := s.Versions.ListVersions(ctx, object.ListVersionsInput{
		ParentName: parentObjectLegacyName(ctx, m.GetParent()),
		PageSize:   m.GetPage().GetPageSize(),
		PageToken:  m.GetPage().GetPageToken(),
	})
	if err != nil {
		return nil, err
	}
	resp := &pb.ListObjectVersionsResponse{Page: pageResponseProto(next)}
	for i := range out {
		resp.Versions = append(resp.Versions, versionToProto(m.GetParent(), &out[i]))
	}
	return connect.NewResponse(resp), nil
}

func (s *ObjectServer) GetObjectVersion(ctx context.Context, req *connect.Request[pb.GetObjectVersionRequest]) (*connect.Response[pb.ObjectVersion], error) {
	if s.Versions == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("versioning not wired"))
	}
	parent, err := stripVersionSuffix(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.Versions.GetVersion(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(versionToProto(parent, out)), nil
}

func (s *ObjectServer) RestoreObjectVersion(ctx context.Context, req *connect.Request[pb.RestoreObjectVersionRequest]) (*connect.Response[pb.Object], error) {
	if s.Versions == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("versioning not wired"))
	}
	out, err := s.Versions.RestoreVersion(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(out)), nil
}

var _ paladindatav1connect.ObjectServiceHandler = (*ObjectServer)(nil)
