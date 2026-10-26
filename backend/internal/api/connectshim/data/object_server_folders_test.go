package data

import (
	"context"
	"slices"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// folderHandler answers a list and its folders, and keeps what it was asked.
type folderHandler struct {
	failingHandler
	listed      objecth.ListObjectsInput
	prefixCalls int
}

func (f *folderHandler) ListObjects(_ context.Context, in objecth.ListObjectsInput) ([]objecth.Object, string, error) {
	f.listed = in
	return nil, "", nil
}

func (f *folderHandler) ListCommonPrefixes(_ context.Context, in objecth.ListObjectsInput) ([]string, bool, error) {
	f.prefixCalls++
	return []string{in.Prefix + "b/"}, true, nil
}

func TestListObjectsAnswersTheFolders(t *testing.T) {
	ctx := ctxTenant(tenantA)
	parent := "tenants/" + tenantA.String() + "/collections/c1"

	h := &folderHandler{}
	out, err := (&ObjectServer{H: h}).ListObjects(ctx, &pb.ListObjectsRequest{
		Parent: parent, Prefix: "a/", Delimiter: "/",
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.listed.Prefix != "a/" || h.listed.Delimiter != "/" {
		t.Errorf("handler got prefix %q delimiter %q", h.listed.Prefix, h.listed.Delimiter)
	}
	if !slices.Equal(out.GetCommonPrefixes(), []string{"a/b/"}) || !out.GetCommonPrefixesTruncated() {
		t.Errorf("response folders = %v truncated %v", out.GetCommonPrefixes(), out.GetCommonPrefixesTruncated())
	}

	// A later page carries objects only: the folders came with the first.
	h = &folderHandler{}
	out, err = (&ObjectServer{H: h}).ListObjects(ctx, &pb.ListObjectsRequest{
		Parent: parent, Prefix: "a/", Delimiter: "/",
		Page: &commonpb.PageRequest{PageToken: "next"},
	})
	if err != nil || h.prefixCalls != 0 || len(out.GetCommonPrefixes()) != 0 {
		t.Errorf("second page: folders %v, asked %d times, err %v", out.GetCommonPrefixes(), h.prefixCalls, err)
	}
}

func (f *folderHandler) CountObjectFacets(context.Context, objecth.ListObjectsInput) (*objecth.ObjectFacetCounts, error) {
	return nil, nil
}
