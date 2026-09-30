package logger

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/utils"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestReplaceGlobals(t *testing.T) {
	testLog := zaptest.NewLogger(t)
	ReplaceGlobals(testLog)

	if zap.L() == nil {
		t.Error("Global logger was not replaced")
	}
	if AuditFromContext(context.Background()) == nil {
		t.Error("Audit logger is nil")
	}
}

func TestNewBootstrapLogger(t *testing.T) {
	l, err := NewBootstrapLogger()
	if err != nil {
		t.Fatalf("NewBootstrapLogger: %v", err)
	}
	if l == nil {
		t.Fatal("bootstrap logger is nil")
	}
}

func TestFromContext(t *testing.T) {
	ctx := context.Background()
	ctx = utils.WithTenantID(ctx, "test-tenant")
	ctx = context.WithValue(ctx, utils.RequestIDKey, "test-request-id")

	l := FromContext(ctx)
	if l == nil {
		t.Fatal("Logger from context is nil")
	}

	// We can't easily inspect zap fields without a custom core,
	// but we can at least verify it doesn't panic and returns a logger.
	l.Info("testing enrichment")
}
