package data

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// recordingHandler keeps what the shim asked of the handler, and fails, so a
// test reads the request mapping without a response to build.
type recordingHandler struct {
	failingHandler
	upload *objecth.UploadObjectInput
	copied *objecth.CopyObjectInput
	listed *objecth.ListObjectsInput
}

func newRecordingHandler() *recordingHandler {
	return &recordingHandler{failingHandler: failingHandler{err: errBoom}}
}

func (r *recordingHandler) UploadObject(ctx context.Context, in objecth.UploadObjectInput) (*objecth.UploadObjectOutput, error) {
	r.upload = &in
	return r.failingHandler.UploadObject(ctx, in)
}

func (r *recordingHandler) CopyObject(ctx context.Context, in objecth.CopyObjectInput) (*objecth.Object, error) {
	r.copied = &in
	return r.failingHandler.CopyObject(ctx, in)
}

func (r *recordingHandler) ListObjects(ctx context.Context, in objecth.ListObjectsInput) ([]objecth.Object, string, error) {
	r.listed = &in
	return r.failingHandler.ListObjects(ctx, in)
}

// The request fields that change what the handler does reach it as the
// client set them, and stay off when the client did not set them.
func TestObjectRequestsMapOntoTheHandler(t *testing.T) {
	ctx := ctxTenant(tenantA)
	parent := "tenants/" + tenantA.String() + "/collections/c1"
	objName := parent + "/objects/" + objUUID.String()

	t.Run("ListObjects sort order", func(t *testing.T) {
		for order, desc := range map[commonpb.SortOrder]bool{
			commonpb.SortOrder_SORT_ORDER_DESC:        true,
			commonpb.SortOrder_SORT_ORDER_ASC:         false,
			commonpb.SortOrder_SORT_ORDER_UNSPECIFIED: false,
		} {
			h := newRecordingHandler()
			_, _ = (&ObjectServer{H: h}).ListObjects(ctx, connect.NewRequest(&pb.ListObjectsRequest{Parent: parent, SortOrder: order}))
			if h.listed == nil || h.listed.SortDesc != desc {
				t.Errorf("%v: SortDesc = %+v, want %v", order, h.listed, desc)
			}
		}
	})

	t.Run("UploadObject transport", func(t *testing.T) {
		for transport, post := range map[pb.PresignTransport]bool{
			pb.PresignTransport_PRESIGN_TRANSPORT_POST:        true,
			pb.PresignTransport_PRESIGN_TRANSPORT_UNSPECIFIED: false,
		} {
			h := newRecordingHandler()
			_, _ = (&ObjectServer{H: h}).UploadObject(ctx, connect.NewRequest(&pb.UploadObjectRequest{Parent: parent, Transport: transport}))
			if h.upload == nil || h.upload.TransportPOST != post {
				t.Errorf("%v: TransportPOST = %+v, want %v", transport, h.upload, post)
			}
		}
	})

	t.Run("CopyObject overrides", func(t *testing.T) {
		h := newRecordingHandler()
		_, _ = (&ObjectServer{H: h}).CopyObject(ctx, connect.NewRequest(&pb.CopyObjectRequest{
			SourceName: objName, DestinationCollection: parent, DestinationKey: "copy",
			MetadataOverride: &pb.MetadataOverride{Metadata: map[string]string{"m": "1"}},
			TagsOverride:     &pb.TagsOverride{Tags: map[string]string{"t": "1"}},
		}))
		if h.copied == nil || h.copied.Metadata["m"] != "1" || h.copied.Tags["t"] != "1" {
			t.Errorf("overrides not passed: %+v", h.copied)
		}

		h = newRecordingHandler()
		_, _ = (&ObjectServer{H: h}).CopyObject(ctx, connect.NewRequest(&pb.CopyObjectRequest{
			SourceName: objName, DestinationCollection: parent, DestinationKey: "copy",
		}))
		if h.copied == nil || h.copied.Metadata != nil || h.copied.Tags != nil {
			t.Errorf("no override set, yet the copy replaces metadata or tags: %+v", h.copied)
		}
	})
}
