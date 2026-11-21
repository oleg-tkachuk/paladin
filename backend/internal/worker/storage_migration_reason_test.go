package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"
)

// backendText is what an S3 client's error says that the reason a migration
// records must not: a URL, a request id, a status line.
const backendText = "operation error S3: HeadObject, https://s3.internal:3900/paladin-x/docs/a.txt, RequestID: 1F2E"

// leakedText is what no recorded reason carries: the driver's or the backend's
// own words.
var leakedText = []string{"SQLSTATE", "https://", "RequestID", "check constraint", "collections_"}

func assertProductReason(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("reason %q, want %q", got, want)
	}
	for _, s := range leakedText {
		if strings.Contains(got, s) {
			t.Errorf("reason %q carries %q", got, s)
		}
	}
}

// GetTenantStorageMigration returns the recorded reason to the admin caller,
// so a rebind the schema refused is recorded as the collection that cannot
// move, not as the Postgres error.
func TestStorageMigration_RefusedRebindRecordsTheCollection(t *testing.T) {
	pg := &pgconn.PgError{Severity: "ERROR", Code: "23514", Message: `new row for relation "collections" violates check constraint "collections_public_bucket"`}
	repo := &fakeMigRepo{
		mig:       StorageMigration{TenantID: uuid.New(), State: MigStateRebinding},
		rebindErr: RebindRefused("photos", pg),
	}
	if err := newWorker(repo, &fakeCopier{}).tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}
	assertProductReason(t, repo.reason, `rebind refused: collection "photos" cannot move to the dedicated bucket`)
}

// A refusal that does not say which collection still records a product
// reason.
func TestStorageMigration_RefusedRebindWithoutCollection(t *testing.T) {
	repo := &fakeMigRepo{
		mig:       StorageMigration{TenantID: uuid.New(), State: MigStateRebinding},
		rebindErr: errors.Join(ErrRebindRefused, &pgconn.PgError{Code: "23514"}),
	}
	if err := newWorker(repo, &fakeCopier{}).tick(context.Background()); err != nil {
		t.Fatalf("tick = %v, want nil", err)
	}
	assertProductReason(t, repo.reason, "rebind refused: a collection cannot move to the dedicated bucket")
}

// erroringHeader answers every HEAD with the backend's own error.
type erroringHeader struct{ err error }

func (h erroringHeader) HeadObject(context.Context, CopyLocation) (int64, error) {
	return -1, h.err
}

// A physical verify records which object failed and how, in its own words:
// the backend's error goes to the log.
func TestStorageMigration_VerifyFailureReason(t *testing.T) {
	for name, tc := range map[string]struct {
		header ObjectHeader
		want   string
	}{
		"backend error": {erroringHeader{errors.New(backendText)}, "physical verify: docs/a.txt could not be read from the target bucket"},
		"missing":       {erroringHeader{ErrObjectMissing}, "physical verify: docs/a.txt missing in the target bucket"},
		"size mismatch": {&fakeHeader{sizes: map[string]int64{"docs/a.txt": 50, "docs/b.txt": 7}}, "physical verify: docs/a.txt size mismatch: target 50 != source 100"},
	} {
		t.Run(name, func(t *testing.T) {
			repo, _ := rollbackFixture(MigStateVerifying)
			w := &StorageMigrationWorker{Repo: repo, Copier: &fakeCopier{}, Header: tc.header, CopyBatch: 2}
			if err := w.tick(context.Background()); err != nil {
				t.Fatalf("tick = %v, want nil", err)
			}
			assertProductReason(t, repo.reason, tc.want)
		})
	}
}
