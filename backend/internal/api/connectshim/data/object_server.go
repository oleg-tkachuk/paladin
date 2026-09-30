package data

import (
	"context"
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/object"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

type ObjectServer struct {
	paladindatav1connect.UnimplementedObjectServiceHandler
	H        objectHandler
	Versions versionHandler // optional; nil → versioning RPCs return Unimplemented
	Locks    lockHandler    // optional; nil → object-lock RPCs return Unimplemented
}

// The CONSTRUCTOR takes concrete types while the FIELDS are interfaces, and
// that asymmetry is deliberate.
//
// ProvideVersionHandler and ProvideLockHandler return a typed nil pointer when
// the feature is off. Assigned straight into an interface field that becomes a
// NON-nil interface holding a nil pointer, so `s.Locks != nil` is true and the
// shim calls a method on a nil receiver. Verified: it panics, turning "object
// lock is disabled, answer Unimplemented" into a dead server.
//
// Taking the concrete type here is what makes the nil detectable. Tests inject
// through the fields, which is where the seam is needed.
func NewObjectServer(h *object.Handler, versions *object.VersionHandler) *ObjectServer {
	s := &ObjectServer{H: h}
	if versions != nil {
		s.Versions = versions
	}
	return s
}

// WithLocks wires the object-lock handler. Separate from the constructor
// because object lock is opt-in per deployment and every existing caller of
// NewObjectServer predates it.
func (s *ObjectServer) WithLocks(locks *object.LockHandler) *ObjectServer {
	// Concrete parameter, guarded assignment — see NewObjectServer. A typed
	// nil here would make s.Locks non-nil and every object-lock RPC panic on a
	// deployment that has the feature off.
	if locks != nil {
		s.Locks = locks
	}
	return s
}

func (s *ObjectServer) UploadObject(ctx context.Context, req *connect.Request[pb.UploadObjectRequest]) (*connect.Response[pb.UploadObjectResponse], error) {
	m := req.Msg
	collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.UploadObject(ctx, object.UploadObjectInput{
		Collection:    collection,
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
	collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.DownloadObject(ctx, collection, objectID, m.GetTtl().AsDuration(), m.GetContentDisposition())
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
	collection, objectID, err := objectNameParts(ctx, req.Msg.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.GetObject(ctx, collection, objectID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(out)), nil
}

func (s *ObjectServer) LookupObject(ctx context.Context, req *connect.Request[pb.LookupObjectRequest]) (*connect.Response[pb.Object], error) {
	collection, err := collectionNameParts(ctx, req.Msg.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.LookupObject(ctx, collection, req.Msg.GetKey())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(out)), nil
}

func (s *ObjectServer) UpdateObject(ctx context.Context, req *connect.Request[pb.UpdateObjectRequest]) (*connect.Response[pb.Object], error) {
	m := req.Msg
	collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	out, err := s.H.UpdateObject(ctx, object.UpdateObjectInput{
		Collection:      collection,
		ObjectID:        objectID,
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
	collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.CompleteObject(ctx, object.CompleteObjectInput{
		Collection: collection,
		ObjectID:   objectID,
		ETag:       m.GetEtag(),
		Checksum:   m.GetChecksumValue(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(out)), nil
}

func (s *ObjectServer) DeleteObject(ctx context.Context, req *connect.Request[pb.DeleteObjectRequest]) (*connect.Response[pb.DeleteObjectResponse], error) {
	m := req.Msg
	collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	if err := s.H.DeleteObject(ctx, collection, objectID, m.GetResourceVersion(), m.GetPermanent(), m.GetBypassGovernanceRetention()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteObjectResponse{
		Object: &pb.Object{Name: m.GetName()},
	}), nil
}

func (s *ObjectServer) RestoreObject(ctx context.Context, req *connect.Request[pb.RestoreObjectRequest]) (*connect.Response[pb.Object], error) {
	collection, objectID, err := objectNameParts(ctx, req.Msg.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.RestoreObject(ctx, collection, objectID, req.Msg.GetResourceVersion())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(out)), nil
}

func (s *ObjectServer) CopyObject(ctx context.Context, req *connect.Request[pb.CopyObjectRequest]) (*connect.Response[pb.Object], error) {
	m := req.Msg
	srcCollection, srcObjectID, err := objectNameParts(ctx, m.GetSourceName())
	if err != nil {
		return nil, badName(fmt.Errorf("source: %w", err))
	}
	destCollection, err := collectionNameParts(ctx, m.GetDestinationCollection())
	if err != nil {
		return nil, badName(fmt.Errorf("destination: %w", err))
	}
	in := object.CopyObjectInput{
		SourceCollection: srcCollection,
		SourceObjectID:   srcObjectID,
		DestCollection:   destCollection,
		DestKey:          m.GetDestinationKey(),
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
	collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	objs, next, err := s.H.ListObjects(ctx, object.ListObjectsInput{
		Collection: collection,
		PageSize:   m.GetPage().GetPageSize(),
		PageToken:  m.GetPage().GetPageToken(),
		Filter:     m.GetFilter(),
		OrderBy:    m.GetOrderBy(),
		SortDesc:   m.GetSortOrder() == 2, // SORT_ORDER_DESC
	})
	if err != nil {
		return nil, err
	}
	out := &pb.ListObjectsResponse{Page: convx.PageResponseProto(next)}
	for i := range objs {
		out.Objects = append(out.Objects, objectToProto(&objs[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *ObjectServer) CountObjects(ctx context.Context, req *connect.Request[pb.CountObjectsRequest]) (*connect.Response[pb.CountObjectsResponse], error) {
	m := req.Msg
	collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.CountObjects(ctx, object.CountObjectsInput{
		Collection: collection,
		Filter:     m.GetFilter(),
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
	collection, objectID, err := objectNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	out, next, err := s.Versions.ListVersions(ctx, object.ListVersionsInput{
		Collection: collection,
		ObjectID:   objectID,
		PageSize:   m.GetPage().GetPageSize(),
		PageToken:  m.GetPage().GetPageToken(),
	})
	if err != nil {
		return nil, err
	}
	resp := &pb.ListObjectVersionsResponse{Page: convx.PageResponseProto(next)}
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
		return nil, badName(err)
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
	out, err := s.Versions.RestoreVersion(ctx, req.Msg.GetName(), req.Msg.GetResourceVersion())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(out)), nil
}

// ─── Object Lock (ADR-0013) ─────────────────────────────────────────────────

func (s *ObjectServer) SetObjectRetention(ctx context.Context, req *connect.Request[pb.SetObjectRetentionRequest]) (*connect.Response[pb.ObjectLockState], error) {
	if s.Locks == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("object lock not wired"))
	}
	m := req.Msg
	collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	if m.GetRetainUntil() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("retain_until is required"))
	}
	out, err := s.Locks.SetRetention(ctx, object.SetRetentionInput{
		Collection:       collection,
		ObjectID:         objectID,
		Mode:             m.GetMode(),
		RetainUntil:      m.GetRetainUntil().AsTime(),
		BypassGovernance: m.GetBypassGovernanceRetention(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(lockStateToProto(out)), nil
}

func (s *ObjectServer) SetObjectLegalHold(ctx context.Context, req *connect.Request[pb.SetObjectLegalHoldRequest]) (*connect.Response[pb.ObjectLockState], error) {
	if s.Locks == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("object lock not wired"))
	}
	collection, objectID, err := objectNameParts(ctx, req.Msg.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.Locks.SetLegalHold(ctx, collection, objectID, req.Msg.GetLegalHold())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(lockStateToProto(out)), nil
}

func (s *ObjectServer) GetObjectLock(ctx context.Context, req *connect.Request[pb.GetObjectLockRequest]) (*connect.Response[pb.ObjectLockState], error) {
	if s.Locks == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("object lock not wired"))
	}
	collection, objectID, err := objectNameParts(ctx, req.Msg.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.Locks.GetLock(ctx, collection, objectID)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(lockStateToProto(out)), nil
}

var _ paladindatav1connect.ObjectServiceHandler = (*ObjectServer)(nil)
