package admin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/audith"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// exportingAudit answers ExportAuditLog with result.
type exportingAudit struct {
	failingAudit
	result *audith.ExportAuditLogResult
}

func (e exportingAudit) ExportAuditLog(context.Context, string, string) (*audith.ExportAuditLogResult, error) {
	return e.result, nil
}

// The export runs synchronously, so its operation comes back finished, with
// the result attached: a client polling one that read Done=false would wait
// forever on an operation nothing is running.
func TestExportAuditLogIsAFinishedOperation(t *testing.T) {
	const rows = 2
	srv := &AuditServer{H: exportingAudit{result: &audith.ExportAuditLogResult{
		GeneratedAt: time.Unix(1_767_225_600, 0).UTC(),
		RowCount:    rows,
	}}}
	resp, err := srv.ExportAuditLog(context.Background(), connect.NewRequest(&pb.ExportAuditLogRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Msg.GetDone() {
		t.Error("the export is not done")
	}
	var got audith.ExportAuditLogResult
	if err := json.Unmarshal(resp.Msg.GetResponse().GetValue(), &got); err != nil {
		t.Fatalf("the result does not decode: %v", err)
	}
	if got.RowCount != rows {
		t.Errorf("row count = %d, want %d", got.RowCount, rows)
	}
}

// An entry whose stored before-image is not JSON cannot be written out; the
// export fails rather than answering with a truncated or empty result.
func TestExportAuditLogRefusesAResultItCannotEncode(t *testing.T) {
	srv := &AuditServer{H: exportingAudit{result: &audith.ExportAuditLogResult{
		Entries: []audith.ExportAuditLogEntry{{Before: json.RawMessage(`{`)}},
	}}}
	_, err := srv.ExportAuditLog(context.Background(), connect.NewRequest(&pb.ExportAuditLogRequest{}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Errorf("err = %v, want Internal", err)
	}
}
