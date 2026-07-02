//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// TestMultipartSessionAnchorsBackend proves migration 053 + the session
// anchoring: InitiateSession persists the (backend_id, bucket_name) it was
// given, and GetSession reads back exactly those — NOT the object_key's
// current binding. This is what keeps complete/abort/presign-part (and the
// reaper) targeting where the parts actually live after an operator rebinds
// the object_key to another backend mid-upload.
func TestMultipartSessionAnchorsBackend(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	repo := adapters.NewMultipartRepo(sqlc.New(pool), pool)

	// Anchor values deliberately distinct from the fixture's object_key
	// binding, so a match on read can only come from the stored session, not
	// a re-resolution of the current binding.
	const anchorBackend, anchorBucket = "be-anchored", "bkt-anchored"
	objectID := uuid.Must(uuid.NewV7())
	sess, err := repo.InitiateSession(ctx, multipart.InitiateArgs{
		TenantID:      f.tenantID,
		ObjectKey:     f.objectKey,
		Key:           "mpu-" + uuid.NewString()[:8],
		ContentType:   "application/octet-stream",
		TotalParts:    2,
		PartSizeBytes: 5 << 20,
		SizeHint:      6 << 20,
	}, objectID, "storage-upload-xyz", anchorBackend, anchorBucket)
	if err != nil {
		t.Fatalf("InitiateSession: %v", err)
	}
	if sess.BackendID != anchorBackend || sess.Bucket != anchorBucket {
		t.Fatalf("InitiateSession returned backend/bucket = %q/%q, want %q/%q",
			sess.BackendID, sess.Bucket, anchorBackend, anchorBucket)
	}

	got, err := repo.GetSession(ctx, sess.UploadID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.BackendID != anchorBackend {
		t.Fatalf("GetSession backend = %q, want %q (anchored at initiate, not the current binding)", got.BackendID, anchorBackend)
	}
	if got.Bucket != anchorBucket {
		t.Fatalf("GetSession bucket = %q, want %q (anchored at initiate)", got.Bucket, anchorBucket)
	}
}
