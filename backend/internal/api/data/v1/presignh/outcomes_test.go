package presignh

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// An object lookup the store fails is an outage, not an absent object: it
// used to answer NotFound, which a client reads as "it is gone".
func TestLookupFailureIsNotNotFound(t *testing.T) {
	tid := uuid.New()
	for _, tc := range []struct {
		name string
		err  error
		want connect.Code
	}{
		{"not there", fmt.Errorf("object: %w", ErrObjectNotFound), connect.CodeNotFound},
		{"store down", errors.New("connection reset"), connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{lookupObjectFn: func(context.Context, uuid.UUID, string, uuid.UUID) (ObjectRef, error) {
				return ObjectRef{}, tc.err
			}}
			h := NewHandler(repo, &fakeStorage{}, allowPolicy(), testConfig(t))
			_, _, _, getErr := h.PresignGet(authedCtx(tid), "obj", validObjectID, 0, "", false)
			wantCode(t, getErr, tc.want)
			_, putErr := h.RegenerateUploadURL(authedCtx(tid), "obj", validObjectID, 0)
			wantCode(t, putErr, tc.want)
		})
	}
}

// A presigner that fails a GET is internal, as it is for a PUT; it used to
// reach the caller with no code at all.
func TestPresignGetStorageFailureIsInternal(t *testing.T) {
	h := NewHandler(&fakeRepo{}, &fakeStorage{err: errors.New("signer unavailable")}, allowPolicy(), testConfig(t))
	_, _, _, err := h.PresignGet(authedCtx(uuid.New()), "obj", validObjectID, 0, "", false)
	wantCode(t, err, connect.CodeInternal)
}

// Each presign is logged by how it ended: issued at debug, refused at warn,
// failed at error.
func TestPresignOutcomesAreLogged(t *testing.T) {
	tid := uuid.New()
	for _, tc := range []struct {
		name    string
		repo    *fakeRepo
		storage *fakeStorage
		level   zapcore.Level
	}{
		{"issued", &fakeRepo{}, &fakeStorage{}, zapcore.DebugLevel},
		{"refused", &fakeRepo{lookupObjectFn: func(context.Context, uuid.UUID, string, uuid.UUID) (ObjectRef, error) {
			return ObjectRef{}, ErrObjectNotFound
		}}, &fakeStorage{}, zapcore.WarnLevel},
		{"failed", &fakeRepo{}, &fakeStorage{err: errors.New("signer unavailable")}, zapcore.ErrorLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			h := NewHandler(tc.repo, tc.storage, allowPolicy(), testConfig(t))
			h.SetLogger(zap.New(core))
			_, _, _, _ = h.PresignGet(authedCtx(tid), "obj", validObjectID, time.Minute, "", false)
			lines := logs.FilterLevelExact(tc.level).All()
			if len(lines) != 1 || lines[0].ContextMap()["op"] != "presign get" || lines[0].ContextMap()["tenant_id"] != tid.String() {
				t.Fatalf("want one %v line naming op and tenant, got %v", tc.level, logs.All())
			}
		})
	}
}
