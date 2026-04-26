package connectshim

import (
	"context"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
)

// ObjectServer bridges generated Connect to object.Handler. Only the handler
// methods that exist on the current business layer are implemented; the rest
// are inherited from UnimplementedObjectServiceHandler and return
// CodeUnimplemented so partial rollouts remain usable.
type ObjectServer struct {
	paladinv1connect.UnimplementedObjectServiceHandler
	H *object.Handler
}

func NewObjectServer(h *object.Handler) *ObjectServer { return &ObjectServer{H: h} }

func (s *ObjectServer) UploadObject(ctx context.Context, req *connect.Request[pb.UploadObjectRequest]) (*connect.Response[pb.UploadObjectResponse], error) {
	m := req.Msg
	out, err := s.H.UploadObject(ctx, object.UploadObjectInput{
		ObjectKey:     m.GetObjectKey(),
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

	presigned := &pb.PresignedUrl{
		Method:          out.Method,
		RequiredHeaders: out.Headers,
		ExpiresAt:       tsProto(out.ExpiresAt),
	}
	if out.PostAction != "" {
		presigned.PostPolicy = &pb.PresignedPostPolicy{
			Action: out.PostAction,
			Fields: out.PostFields,
		}
	} else {
		presigned.Url = out.URL
	}
	return connect.NewResponse(&pb.UploadObjectResponse{
		Object:         objectToProto(&out.Object),
		UploadUrl:      presigned,
		CompletionMode: completionModeProto(out.CompletionMode),
	}), nil
}

func (s *ObjectServer) CompleteObject(ctx context.Context, req *connect.Request[pb.CompleteObjectRequest]) (*connect.Response[pb.Object], error) {
	obj, err := s.H.CompleteObject(ctx, object.CompleteObjectInput{
		Name:     req.Msg.GetName(),
		ETag:     req.Msg.GetEtag(),
		Checksum: req.Msg.GetChecksum(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(obj)), nil
}

func (s *ObjectServer) ListObjects(ctx context.Context, req *connect.Request[pb.ListObjectsRequest]) (*connect.Response[pb.ListObjectsResponse], error) {
	m := req.Msg
	objs, next, err := s.H.ListObjects(ctx, object.ListObjectsInput{
		ObjectKey: m.GetObjectKey(),
		PageSize:  m.GetPageSize(),
		PageToken: m.GetPageToken(),
		Filter:    m.GetFilter(),
		OrderBy:   m.GetOrderBy(),
		SortDesc:  m.GetSortOrder() == pb.SortOrder_SORT_ORDER_DESC,
	})
	if err != nil {
		return nil, err
	}
	out := &pb.ListObjectsResponse{NextPageToken: next}
	for i := range objs {
		out.Objects = append(out.Objects, objectToProto(&objs[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *ObjectServer) CountObjects(ctx context.Context, req *connect.Request[pb.CountObjectsRequest]) (*connect.Response[pb.CountObjectsResponse], error) {
	out, err := s.H.CountObjects(ctx, object.CountObjectsInput{
		ObjectKey: req.Msg.GetObjectKey(),
		Filter:    req.Msg.GetFilter(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.CountObjectsResponse{
		ApproximateCount: out.ApproximateCount,
		Exact:            out.Exact,
	}), nil
}

func (s *ObjectServer) GetObject(ctx context.Context, req *connect.Request[pb.GetObjectRequest]) (*connect.Response[pb.Object], error) {
	obj, err := s.H.GetObject(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(obj)), nil
}

func (s *ObjectServer) LookupObject(ctx context.Context, req *connect.Request[pb.LookupObjectRequest]) (*connect.Response[pb.Object], error) {
	obj, err := s.H.LookupObject(ctx, req.Msg.GetObjectKey(), req.Msg.GetKey())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(obj)), nil
}

func (s *ObjectServer) DownloadObject(ctx context.Context, req *connect.Request[pb.DownloadObjectRequest]) (*connect.Response[pb.DownloadObjectResponse], error) {
	out, err := s.H.DownloadObject(ctx, req.Msg.GetName(), req.Msg.GetTtl().AsDuration(), req.Msg.GetContentDisposition())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DownloadObjectResponse{
		Object: objectToProto(&out.Object),
		DownloadUrl: &pb.PresignedUrl{
			Url:             out.URL,
			Method:          "GET",
			RequiredHeaders: out.Headers,
			ExpiresAt:       tsProto(out.ExpiresAt),
		},
	}), nil
}

func (s *ObjectServer) UpdateObject(ctx context.Context, req *connect.Request[pb.UpdateObjectRequest]) (*connect.Response[pb.Object], error) {
	m := req.Msg
	rv, _ := parseInt64Local(m.GetResourceVersion())
	var fields []string
	if mask := m.GetUpdateMask(); mask != nil {
		fields = mask.GetPaths()
	}
	obj, err := s.H.UpdateObject(ctx, object.UpdateObjectInput{
		Name:            m.GetName(),
		ResourceVersion: rv,
		UpdatedFields:   fields,
		Metadata:        m.GetMetadata(),
		Tags:            m.GetTags(),
		ContentType:     m.GetContentType(),
		ExternalRef:     m.GetExternalRef(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(obj)), nil
}

func (s *ObjectServer) DeleteObject(ctx context.Context, req *connect.Request[pb.DeleteObjectRequest]) (*connect.Response[pb.Object], error) {
	m := req.Msg
	if err := s.H.DeleteObject(ctx, m.GetName(), m.GetResourceVersion(), m.GetPermanent()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.Object{Name: m.GetName()}), nil
}

func (s *ObjectServer) RestoreObject(ctx context.Context, req *connect.Request[pb.RestoreObjectRequest]) (*connect.Response[pb.Object], error) {
	obj, err := s.H.RestoreObject(ctx, req.Msg.GetName())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(obj)), nil
}

func (s *ObjectServer) CopyObject(ctx context.Context, req *connect.Request[pb.CopyObjectRequest]) (*connect.Response[pb.Object], error) {
	m := req.Msg
	in := object.CopyObjectInput{
		SourceName:    m.GetSourceName(),
		DestObjectKey: m.GetDestinationBucket(),
		DestKey:       m.GetDestinationKey(),
	}
	if mo := m.GetMetadataOverride(); mo != nil {
		in.Metadata = mo.GetMetadata()
	}
	if to := m.GetTagsOverride(); to != nil {
		in.Tags = to.GetTags()
	}
	obj, err := s.H.CopyObject(ctx, in)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(objectToProto(obj)), nil
}

// parseInt64Local mirrors handler.parseInt64 for use in the shim. Returns 0
// on empty/invalid input (skips OCC), matching the handler's convention.
func parseInt64Local(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, nil
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

var _ paladinv1connect.ObjectServiceHandler = (*ObjectServer)(nil)
