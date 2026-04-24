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
		Bucket:        m.GetBucket(),
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
		Bucket:    m.GetBucket(),
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
		Bucket: req.Msg.GetBucket(),
		Filter: req.Msg.GetFilter(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.CountObjectsResponse{
		ApproximateCount: out.ApproximateCount,
		Exact:            out.Exact,
	}), nil
}

var _ paladinv1connect.ObjectServiceHandler = (*ObjectServer)(nil)
