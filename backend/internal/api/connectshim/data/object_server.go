package data

import (
	"context"
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

type ObjectServer struct {
	paladindatav1connect.UnimplementedObjectServiceHandler
	H        objectHandler
	Versions versionHandler // optional; nil → versioning RPCs return Unimplemented
	Locks    lockHandler    // optional; nil → object-lock RPCs return Unimplemented
	Taints   taintHandler   // optional; nil → SetObjectTaint returns Unimplemented
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
func NewObjectServer(h *objecth.Handler, versions *objecth.VersionHandler) *ObjectServer {
	s := &ObjectServer{H: h}
	if versions != nil {
		s.Versions = versions
	}
	return s
}

// WithLocks wires the object-lock handler. Separate from the constructor
// because object lock is opt-in per deployment and every existing caller of
// NewObjectServer predates it.
func (s *ObjectServer) WithLocks(locks *objecth.LockHandler) *ObjectServer {
	// Concrete parameter, guarded assignment — see NewObjectServer. A typed
	// nil here would make s.Locks non-nil and every object-lock RPC panic on a
	// deployment that has the feature off.
	if locks != nil {
		s.Locks = locks
	}
	return s
}

// WithTaints wires the taint handler. Concrete parameter, guarded
// assignment, for the typed-nil reason NewObjectServer gives.
func (s *ObjectServer) WithTaints(taints *objecth.TaintHandler) *ObjectServer {
	if taints != nil {
		s.Taints = taints
	}
	return s
}

// SetObjectTaint replaces an object's taint signals.
func (s *ObjectServer) SetObjectTaint(ctx context.Context, req *pb.SetObjectTaintRequest) (*pb.Object, error) {
	if s.Taints == nil {
		return nil, connect.Errorf(connect.CodeUnimplemented, "object taint not wired")
	}
	ctx, collection, objectID, err := objectNameParts(ctx, req.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.Taints.SetTaint(ctx, collection, objectID, taintFromProto(req.GetSignals()))
	if err != nil {
		return nil, err
	}
	return objectToProto(out), nil
}

func (s *ObjectServer) UploadObject(ctx context.Context, req *pb.UploadObjectRequest) (*pb.UploadObjectResponse, error) {
	m := req
	ctx, collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.UploadObject(ctx, objecth.UploadObjectInput{
		Collection:    collection,
		Key:           m.GetKey(),
		ContentType:   m.GetContentType(),
		SizeBytes:     m.GetSizeHintBytes(),
		ChecksumAlgo:  checksumAlgoStr(m.GetChecksumAlgorithm()),
		ChecksumValue: m.GetChecksumValue(),
		Metadata:      m.GetMetadata(),
		Tags:          m.GetTags(),
		ExternalRef:   m.GetExternalRef(),
		TransportPOST: m.GetTransport() == pb.PresignTransport_PRESIGN_TRANSPORT_POST,
	})
	if err != nil {
		return nil, err
	}
	return &pb.UploadObjectResponse{
		Object: objectToProto(&out.Object),
		UploadUrl: presignedUrlProto(
			out.URL, out.Method, out.Headers, out.ExpiresAt,
			out.PostAction, out.PostFields,
		),
		CompletionMode: completionModeProto(out.CompletionMode),
	}, nil
}

func (s *ObjectServer) DownloadObject(ctx context.Context, req *pb.DownloadObjectRequest) (*pb.DownloadObjectResponse, error) {
	m := req
	ctx, collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.DownloadObject(ctx, collection, objectID, m.GetTtl().AsDuration(), m.GetContentDisposition(), m.GetRequireEtagMatch())
	if err != nil {
		return nil, err
	}
	return &pb.DownloadObjectResponse{
		Object: objectToProto(&out.Object),
		DownloadUrl: presignedUrlProto(
			out.URL, "GET", out.Headers, out.ExpiresAt, "", nil,
		),
	}, nil
}

func (s *ObjectServer) GetObject(ctx context.Context, req *pb.GetObjectRequest) (*pb.Object, error) {
	ctx, collection, objectID, err := objectNameParts(ctx, req.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.GetObject(ctx, collection, objectID)
	if err != nil {
		return nil, err
	}
	return objectToProto(out), nil
}

func (s *ObjectServer) LookupObject(ctx context.Context, req *pb.LookupObjectRequest) (*pb.Object, error) {
	ctx, collection, err := collectionNameParts(ctx, req.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.LookupObject(ctx, collection, req.GetKey())
	if err != nil {
		return nil, err
	}
	return objectToProto(out), nil
}

// updateObjectPaths are the UpdateObjectRequest fields UpdateObject applies.
// content_type is on the request but nothing applies it, so it is refused
// rather than reported as updated.
var updateObjectPaths = []string{"metadata", "tags", "external_ref"}

func (s *ObjectServer) UpdateObject(ctx context.Context, req *pb.UpdateObjectRequest) (*pb.Object, error) {
	m := req
	ctx, collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	if err := convx.CheckMask(m.GetUpdateMask().GetPaths(), updateObjectPaths); err != nil {
		return nil, err
	}
	out, err := s.H.UpdateObject(ctx, objecth.UpdateObjectInput{
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
	return objectToProto(out), nil
}

func (s *ObjectServer) CompleteObject(ctx context.Context, req *pb.CompleteObjectRequest) (*pb.Object, error) {
	m := req
	ctx, collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.CompleteObject(ctx, objecth.CompleteObjectInput{
		Collection: collection,
		ObjectID:   objectID,
		ETag:       m.GetEtag(),
		Checksum:   m.GetChecksumValue(),
	})
	if err != nil {
		return nil, err
	}
	return objectToProto(out), nil
}

func (s *ObjectServer) DeleteObject(ctx context.Context, req *pb.DeleteObjectRequest) (*pb.DeleteObjectResponse, error) {
	m := req
	ctx, collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	if err := s.H.DeleteObject(ctx, collection, objectID, m.GetResourceVersion(), m.GetPermanent(), m.GetBypassGovernanceRetention()); err != nil {
		return nil, err
	}
	return &pb.DeleteObjectResponse{
		Object: &pb.Object{Name: m.GetName()},
	}, nil
}

func (s *ObjectServer) RestoreObject(ctx context.Context, req *pb.RestoreObjectRequest) (*pb.Object, error) {
	ctx, collection, objectID, err := objectNameParts(ctx, req.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.RestoreObject(ctx, collection, objectID, req.GetResourceVersion())
	if err != nil {
		return nil, err
	}
	return objectToProto(out), nil
}

func (s *ObjectServer) CopyObject(ctx context.Context, req *pb.CopyObjectRequest) (*pb.Object, error) {
	m := req
	ctx, srcCollection, srcObjectID, err := objectNameParts(ctx, m.GetSourceName())
	if err != nil {
		return nil, badName(fmt.Errorf("source: %w", err))
	}
	ctx, destCollection, err := collectionNameParts(ctx, m.GetDestinationCollection())
	if err != nil {
		return nil, badName(fmt.Errorf("destination: %w", err))
	}
	in := objecth.CopyObjectInput{
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
	return objectToProto(out), nil
}

func (s *ObjectServer) ListObjects(ctx context.Context, req *pb.ListObjectsRequest) (*pb.ListObjectsResponse, error) {
	m := req
	ctx, collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	objs, next, err := s.H.ListObjects(ctx, objecth.ListObjectsInput{
		Collection: collection,
		PageSize:   m.GetPage().GetPageSize(),
		PageToken:  m.GetPage().GetPageToken(),
		Filter:     m.GetFilter(),
		OrderBy:    m.GetOrderBy(),
		SortDesc:   m.GetSortOrder() == commonpb.SortOrder_SORT_ORDER_DESC,
	})
	if err != nil {
		return nil, err
	}
	out := &pb.ListObjectsResponse{Page: convx.PageResponseProto(next)}
	for i := range objs {
		out.Objects = append(out.Objects, objectToProto(&objs[i]))
	}
	return out, nil
}

func (s *ObjectServer) CountObjects(ctx context.Context, req *pb.CountObjectsRequest) (*pb.CountObjectsResponse, error) {
	m := req
	ctx, collection, err := collectionNameParts(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.H.CountObjects(ctx, objecth.CountObjectsInput{
		Collection: collection,
		Filter:     m.GetFilter(),
	})
	if err != nil {
		return nil, err
	}
	return &pb.CountObjectsResponse{
		ApproximateCount: out.ApproximateCount,
		Exact:            out.Exact,
	}, nil
}

// ─── Versioning ─────────────────────────────────────────────────────────────
//
// History is read-only on the API side. Inserts happen via
// VersionHandler.RecordPromotion called from promotion paths when the parent
// bucket has versioning_enabled. Each version row is keyed by UUIDv7 so
// "newest first" sorts cleanly without joining created_at.

func (s *ObjectServer) ListObjectVersions(ctx context.Context, req *pb.ListObjectVersionsRequest) (*pb.ListObjectVersionsResponse, error) {
	if s.Versions == nil {
		return nil, connect.Errorf(connect.CodeUnimplemented, "versioning not wired")
	}
	m := req
	ctx, parent, err := parseObjectName(ctx, m.GetParent())
	if err != nil {
		return nil, badName(err)
	}
	out, next, err := s.Versions.ListVersions(ctx, objecth.ListVersionsInput{
		Collection: parent.Collection,
		ObjectID:   parent.Object,
		PageSize:   m.GetPage().GetPageSize(),
		PageToken:  m.GetPage().GetPageToken(),
	})
	if err != nil {
		return nil, err
	}
	resp := &pb.ListObjectVersionsResponse{Page: convx.PageResponseProto(next)}
	for i := range out {
		resp.Versions = append(resp.Versions, versionToProto(parent, &out[i]))
	}
	return resp, nil
}

func (s *ObjectServer) GetObjectVersion(ctx context.Context, req *pb.GetObjectVersionRequest) (*pb.ObjectVersion, error) {
	if s.Versions == nil {
		return nil, connect.Errorf(connect.CodeUnimplemented, "versioning not wired")
	}
	ctx, parent, err := versionParent(ctx, req.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.Versions.GetVersion(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	return versionToProto(parent, out), nil
}

func (s *ObjectServer) RestoreObjectVersion(ctx context.Context, req *pb.RestoreObjectVersionRequest) (*pb.Object, error) {
	if s.Versions == nil {
		return nil, connect.Errorf(connect.CodeUnimplemented, "versioning not wired")
	}
	ctx, _, err := versionParent(ctx, req.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.Versions.RestoreVersion(ctx, req.GetName(), req.GetResourceVersion())
	if err != nil {
		return nil, err
	}
	return objectToProto(out), nil
}

// ─── Object Lock (ADR-0013) ─────────────────────────────────────────────────

func (s *ObjectServer) SetObjectRetention(ctx context.Context, req *pb.SetObjectRetentionRequest) (*pb.ObjectLockState, error) {
	if s.Locks == nil {
		return nil, connect.Errorf(connect.CodeUnimplemented, "object lock not wired")
	}
	m := req
	ctx, collection, objectID, err := objectNameParts(ctx, m.GetName())
	if err != nil {
		return nil, badName(err)
	}
	if m.GetRetainUntil() == nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "retain_until is required")
	}
	out, err := s.Locks.SetRetention(ctx, objecth.SetRetentionInput{
		Collection:       collection,
		ObjectID:         objectID,
		Mode:             m.GetMode(),
		RetainUntil:      m.GetRetainUntil().AsTime(),
		BypassGovernance: m.GetBypassGovernanceRetention(),
	})
	if err != nil {
		return nil, err
	}
	return lockStateToProto(out), nil
}

func (s *ObjectServer) SetObjectLegalHold(ctx context.Context, req *pb.SetObjectLegalHoldRequest) (*pb.ObjectLockState, error) {
	if s.Locks == nil {
		return nil, connect.Errorf(connect.CodeUnimplemented, "object lock not wired")
	}
	ctx, collection, objectID, err := objectNameParts(ctx, req.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.Locks.SetLegalHold(ctx, collection, objectID, req.GetLegalHold())
	if err != nil {
		return nil, err
	}
	return lockStateToProto(out), nil
}

func (s *ObjectServer) GetObjectLock(ctx context.Context, req *pb.GetObjectLockRequest) (*pb.ObjectLockState, error) {
	if s.Locks == nil {
		return nil, connect.Errorf(connect.CodeUnimplemented, "object lock not wired")
	}
	ctx, collection, objectID, err := objectNameParts(ctx, req.GetName())
	if err != nil {
		return nil, badName(err)
	}
	out, err := s.Locks.GetLock(ctx, collection, objectID)
	if err != nil {
		return nil, err
	}
	return lockStateToProto(out), nil
}

var _ paladindatav1connect.ObjectServiceHandler = (*ObjectServer)(nil)
