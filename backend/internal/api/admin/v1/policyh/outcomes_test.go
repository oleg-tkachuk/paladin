package policyh

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// A page holds at most size versions, and names the version it stopped at
// only when the store found one more.
func TestPageOfVersions(t *testing.T) {
	const size = 2
	full := pageOf(history(size+1), size)
	if len(full.Versions) != size || full.NextPageToken != "2" {
		t.Errorf("one more than the page = %d versions, token %q; want %d and \"2\"", len(full.Versions), full.NextPageToken, size)
	}
	last := pageOf(history(size), size)
	if len(last.Versions) != size || last.NextPageToken != "" {
		t.Errorf("exactly a page = %d versions, token %q; want %d and none", len(last.Versions), last.NextPageToken, size)
	}
}

// Every read that fails for no reason the caller can fix answers internal and
// says why in the log.
func TestInternalFailuresAreLogged(t *testing.T) {
	tid := uuid.New()
	boom := errors.New("connection reset")
	failingStore := &fakeStore{fetchFn: func(context.Context, uuid.UUID, string) (cedar.Layers, []byte, string, error) {
		return cedar.Layers{}, nil, "", boom
	}}
	for _, tc := range []struct {
		name string
		call func(*Handler) error
	}{
		{"effective policy", func(h *Handler) error {
			_, err := h.GetEffectivePolicy(authedCtx(tid), "tenants/"+tid.String(), tid)
			return err
		}},
		{"policy versions", func(h *Handler) error {
			h.SetVersionStore(&fakeVersions{err: boom})
			_, err := h.ListPolicyVersions(authedCtx(tid), "tenants/"+tid.String(), 0, "")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.ErrorLevel)
			h := NewHandler(allowEngine(), failingStore)
			h.SetLogger(zap.New(core))
			if err := tc.call(h); connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("err = %v, want internal", err)
			}
			if logs.Len() != 1 || logs.All()[0].ContextMap()["error"] == nil {
				t.Fatalf("want one error line with the cause, got %v", logs.All())
			}
		})
	}
}
