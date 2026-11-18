package operationh

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// A read the store fails is an outage, not an absent operation, and a cancel
// the store fails is not a refusal to cancel: both used to read as the
// caller's answer.
func TestStoreFailuresAreInternal(t *testing.T) {
	tid, opID := uuid.New(), uuid.New()
	boom := errors.New("connection reset")
	for _, tc := range []struct {
		name string
		repo *fakeRepo
		call func(*Handler) error
		want connect.Code
	}{
		{"get: not there", &fakeRepo{getFn: func(context.Context, uuid.UUID, uuid.UUID) (Operation, error) {
			return Operation{}, fmt.Errorf("op: %w", ErrOperationNotFound)
		}}, func(h *Handler) error { _, err := h.GetOperation(authedCtx(tid), opID); return err }, connect.CodeNotFound},
		{"get: store down", &fakeRepo{getFn: func(context.Context, uuid.UUID, uuid.UUID) (Operation, error) {
			return Operation{}, boom
		}}, func(h *Handler) error { _, err := h.GetOperation(authedCtx(tid), opID); return err }, connect.CodeInternal},
		{"cancel: store down", &fakeRepo{cancelFn: func(context.Context, uuid.UUID, uuid.UUID) error {
			return boom
		}}, func(h *Handler) error { return h.CancelOperation(authedCtx(tid), opID) }, connect.CodeInternal},
		{"cancel: not there", &fakeRepo{cancelFn: func(context.Context, uuid.UUID, uuid.UUID) error {
			return fmt.Errorf("op: %w", ErrOperationNotFound)
		}}, func(h *Handler) error { return h.CancelOperation(authedCtx(tid), opID) }, connect.CodeNotFound},
		{"cancel: already finished", &fakeRepo{cancelFn: func(context.Context, uuid.UUID, uuid.UUID) error {
			return ErrNotCancellable
		}}, func(h *Handler) error { return h.CancelOperation(authedCtx(tid), opID) }, connect.CodeFailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.ErrorLevel)
			h := NewHandler(tc.repo, allowAuthorizer())
			h.SetLogger(zap.New(core))
			err := tc.call(h)
			wantCode(t, err, tc.want)
			wantLines := 0
			if tc.want == connect.CodeInternal {
				wantLines = 1
			}
			if logs.Len() != wantLines {
				t.Fatalf("want %d error lines, got %v", wantLines, logs.All())
			}
		})
	}
}
