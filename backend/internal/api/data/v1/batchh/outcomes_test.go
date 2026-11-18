package batchh

import (
	"testing"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// A batch reaches thousands of objects, so each one submitted says what it
// is, which operation carries it and for whom.
func TestSubmittedBatchIsLogged(t *testing.T) {
	tid, opID := uuid.New(), uuid.New()
	core, logs := observer.New(zapcore.InfoLevel)
	h := newHandler(&fakeSubmitter{id: opID}, allowAll())
	h.SetLogger(zap.New(core))
	if _, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{Collection: "c", ObjectIDs: ids(3)}); err != nil {
		t.Fatalf("BatchDelete: %v", err)
	}
	lines := logs.FilterMessage("batch submitted").All()
	if len(lines) != 1 {
		t.Fatalf("want one line, got %v", logs.All())
	}
	f := lines[0].ContextMap()
	if f["op_type"] != OpBatchDelete || f["operation_id"] != opID.String() || f["tenant_id"] != tid.String() {
		t.Errorf("line = %v", f)
	}
}
