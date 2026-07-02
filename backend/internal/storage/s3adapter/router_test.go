package s3adapter

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
)

// TestObjectRouter_UnknownBackendPropagates proves the router routes on the
// backend id carried by the call and surfaces an unknown-backend error without
// touching any client — a wrong id never silently falls through to the default
// store. (testRegistry lives in registry_test.go.)
func TestObjectRouter_UnknownBackendPropagates(t *testing.T) {
	reg, builds := testRegistry()
	rt := NewObjectRouter(reg)
	ctx := context.Background()

	if _, _, _, _, err := rt.Head(ctx, "nope", "bkt", uuid.New(), "ok", "k"); err == nil {
		t.Fatal("Head to unknown backend: want error")
	}
	if err := rt.DeleteObject(ctx, "nope", "bkt", uuid.New(), "ok", "k"); err == nil {
		t.Fatal("DeleteObject to unknown backend: want error")
	}
	if _, _, _, err := rt.PresignGet(ctx, object.PresignGetArgs{BackendID: "nope"}); err == nil {
		t.Fatal("PresignGet to unknown backend: want error")
	}
	if n := atomic.LoadInt32(builds); n != 0 {
		t.Fatalf("no client should be built for an unknown backend, got %d", n)
	}
}

// TestObjectRouter_CrossBackendCopyRejected proves a copy whose source and
// destination are on different backends is refused up front (Phase 3 will add
// the stream-through), before any client is built.
func TestObjectRouter_CrossBackendCopyRejected(t *testing.T) {
	reg, builds := testRegistry()
	rt := NewObjectRouter(reg)

	err := rt.CopyObject(context.Background(),
		object.Location{BackendID: "primary", Bucket: "a"},
		object.Location{BackendID: "secondary", Bucket: "b"})
	if err == nil {
		t.Fatal("cross-backend copy: want error")
	}
	if !strings.Contains(err.Error(), "cross-backend") {
		t.Fatalf("error should mention cross-backend, got %v", err)
	}
	if n := atomic.LoadInt32(builds); n != 0 {
		t.Fatalf("cross-backend copy must fail before building any client, got %d builds", n)
	}
}

// TestProvisionerRouter_UnknownBackendPropagates mirrors the object router for
// the provisioner path (its backend id is an explicit parameter).
func TestProvisionerRouter_UnknownBackendPropagates(t *testing.T) {
	reg, _ := testRegistry()
	rt := NewProvisionerRouter(reg)
	ctx := context.Background()

	if err := rt.CreateBucket(ctx, "nope", "bkt", "us-east-1"); err == nil {
		t.Fatal("CreateBucket to unknown backend: want error")
	}
	if err := rt.DeleteBucket(ctx, "nope", "bkt"); err == nil {
		t.Fatal("DeleteBucket to unknown backend: want error")
	}
}
