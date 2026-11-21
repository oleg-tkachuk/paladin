package tenanth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestRetryTarget(t *testing.T) {
	failed := StorageMigration{State: StorageMigrationFailed, TargetBackendName: "b2"}
	for _, tc := range []struct {
		name      string
		prev      StorageMigration
		prevErr   error
		requested string
		want      string
		wantCode  connect.Code
	}{
		{"first migration keeps what was asked", StorageMigration{}, ErrStorageMigrationNotFound, "b1", "b1", 0},
		{"first migration, nothing asked", StorageMigration{}, ErrStorageMigrationNotFound, "", "", 0},
		{"the read failing is internal", StorageMigration{}, errors.New("down"), "b1", "", connect.CodeInternal},
		{"one still running is not retried", StorageMigration{State: "copying"}, nil, "b1", "", connect.CodeFailedPrecondition},
		{"a failed one is retried on its backend", failed, nil, "", "b2", 0},
		{"naming its backend is the same retry", failed, nil, "b2", "b2", 0},
		{"another backend is refused", failed, nil, "b3", "", connect.CodeFailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := retryTarget(tc.prev, tc.prevErr, tc.requested)
			if tc.wantCode != 0 {
				if connect.CodeOf(err) != tc.wantCode {
					t.Fatalf("err = %v, want %v", err, tc.wantCode)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("retryTarget = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestCleanupRetentionOrDefault(t *testing.T) {
	const asked = 3600
	if got := cleanupRetentionOrDefault(asked); got != asked {
		t.Errorf("asked %d, got %d", asked, got)
	}
	want := int64(DefaultMigrationCleanupRetention / time.Second)
	for _, unset := range []int64{0, -1} {
		if got := cleanupRetentionOrDefault(unset); got != want {
			t.Errorf("unset %d → %d, want %d", unset, got, want)
		}
	}
}

func TestDedicatedBucketNameNamesTheTenant(t *testing.T) {
	id := uuid.New()
	got := DedicatedBucketName(id)
	if !strings.HasSuffix(got, id.String()) || got == id.String() {
		t.Fatalf("DedicatedBucketName = %q", got)
	}
}

// Starting a migration says what happened at the level it deserves.
func TestStartStorageMigrationLogsItsOutcome(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		level    zapcore.Level
		wantCode connect.Code
	}{
		{"started", nil, zapcore.InfoLevel, 0},
		{"refused", ErrTenantNotShared, zapcore.WarnLevel, connect.CodeFailedPrecondition},
		{"failed", errors.New("connection reset"), zapcore.ErrorLevel, connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			repo := &fakeRepo{startMigrationFn: func(context.Context, StartStorageMigrationArgs) (StorageMigration, error) {
				return StorageMigration{}, tc.err
			}}
			h := NewHandler(repo, allow())
			h.SetLogger(zap.New(core))
			_, err := h.startStorageMigration(context.Background(), StartStorageMigrationArgs{TenantID: uuid.New()})
			if connect.CodeOf(err) != tc.wantCode && (tc.wantCode != 0 || err != nil) {
				t.Fatalf("err = %v, want code %v", err, tc.wantCode)
			}
			if logs.FilterLevelExact(tc.level).Len() != 1 {
				t.Fatalf("want one %v line, got %v", tc.level, logs.All())
			}
		})
	}
}
