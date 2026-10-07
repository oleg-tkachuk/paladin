//go:build integration

package components

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A multipart object's composite checksum and part size are written to the
// PENDING row and read back with the object; a row past PENDING is not
// rewritten, so a late or repeated completion cannot replace what was read.
func TestMultipartCompositeChecksumRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	repo := adapters.NewMultipartRepo(sqlc.New(pool), pool)

	const (
		partSize  = 5 << 20
		composite = "D3qZqhMCO0j+h+RS7RQR8UFdX4mTed1iexltHESNUwA=-2"
		later     = "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=-2"
	)
	objectID := uuid.Must(uuid.NewV7())
	backendID, bucket, err := repo.LookupBucket(ctx, f.tenantID, f.collection, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.InitiateSession(ctx, multiparth.InitiateArgs{
		TenantID: f.tenantID, Collection: f.collection, Key: "mpu-" + uuid.NewString()[:8],
		ContentType: "application/octet-stream", TotalParts: 2, PartSizeBytes: partSize, SizeHint: partSize + 1,
		ChecksumAlgo: "SHA256",
	}, objectID, "storage-upload-composite", backendID, bucket); err != nil {
		t.Fatalf("InitiateSession: %v", err)
	}

	if err := repo.RecordCompositeChecksum(ctx, objectID, composite, partSize); err != nil {
		t.Fatalf("RecordCompositeChecksum: %v", err)
	}
	obj, err := repo.Object(ctx, f.tenantID, objectID)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Checksum != composite || obj.ChecksumPartSizeBytes != partSize {
		t.Fatalf("read back checksum %q over %d-byte parts, want %q over %d", obj.Checksum, obj.ChecksumPartSizeBytes, composite, partSize)
	}

	mustExec(t, ctx, pool, `UPDATE objects SET state = 'AVAILABLE' WHERE id = $1`, objectID)
	if err := repo.RecordCompositeChecksum(ctx, objectID, later, partSize); err != nil {
		t.Fatalf("RecordCompositeChecksum on an available row: %v", err)
	}
	if obj, err = repo.Object(ctx, f.tenantID, objectID); err != nil || obj.Checksum != composite {
		t.Fatalf("an available row's checksum became %q (err %v), want it kept as %q", obj.Checksum, err, composite)
	}
}
