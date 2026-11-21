package multiparth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// sessionRPC is one per-session RPC, run against h.
type sessionRPC struct {
	name string
	op   string
	call func(h *Handler, ctx context.Context) error
}

func sessionRPCs() []sessionRPC {
	return []sessionRPC{
		{"complete", opComplete, func(h *Handler, ctx context.Context) error {
			_, err := h.CompleteMultipartUpload(ctx, CompleteArgs{UploadID: "up-1", Parts: completeParts()})
			return err
		}},
		{"abort", opAbort, func(h *Handler, ctx context.Context) error {
			return h.AbortMultipartUpload(ctx, "up-1", SessionRef{})
		}},
		{"presign part", opPresignPart, func(h *Handler, ctx context.Context) error {
			_, _, _, err := h.PresignPart(ctx, "up-1", 1, time.Minute, testPartChecksum, SessionRef{})
			return err
		}},
		{"list parts", opListParts, func(h *Handler, ctx context.Context) error {
			_, _, err := h.ListParts(ctx, "up-1", 0, "", SessionRef{})
			return err
		}},
	}
}

// A session read the store fails is an outage, not an absent upload: every
// per-session RPC used to answer NotFound for any error, which a client reads
// as "the upload is gone" and starts over.
func TestSessionLookupFailureIsNotNotFound(t *testing.T) {
	tid := uuid.New()
	for _, tc := range []struct {
		name string
		err  error
		want connect.Code
	}{
		{"not there", fmt.Errorf("upload up-1: %w", ErrUploadNotFound), connect.CodeNotFound},
		{"store down", errors.New("connection reset"), connect.CodeInternal},
	} {
		for _, rpc := range sessionRPCs() {
			t.Run(tc.name+"/"+rpc.name, func(t *testing.T) {
				repo := &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
					return Session{}, tc.err
				}}
				h := NewHandler(repo, &fakeStorage{}, allow(), &fakeSM{}, testTTLPolicy(), testUploadLimits)
				err := rpc.call(h, authedCtx(tid))
				wantCode(t, err, tc.want)
				if !errors.Is(err, tc.err) {
					t.Errorf("the cause was dropped: %v", err)
				}
			})
		}
	}
}

// Complete and Abort did not check the session's tenant, as PresignPart and
// ListParts do: row-level security hides another tenant's session from the
// read, and nothing in the handler backed it up.
func TestSessionOfAnotherTenantIsRefused(t *testing.T) {
	tid := uuid.New()
	for _, rpc := range sessionRPCs() {
		t.Run(rpc.name, func(t *testing.T) {
			sm := &fakeSM{}
			storage := &fakeStorage{}
			h := NewHandler(sessionRepo(uuid.New()), storage, allow(), sm, testTTLPolicy(), testUploadLimits)
			wantCode(t, rpc.call(h, authedCtx(tid)), connect.CodePermissionDenied)
			if storage.abortCalled || storage.lastComplete.storageUploadID != "" || sm.failedID != uuid.Nil() {
				t.Error("another tenant's upload was acted on")
			}
		})
	}
}

// A presigner that fails a part URL is internal; it used to reach the caller
// with no code at all.
func TestPresignPartStorageFailureIsInternal(t *testing.T) {
	tid := uuid.New()
	storage := &fakeStorage{presignFn: func() (string, map[string]string, time.Time, error) {
		return "", nil, time.Time{}, errors.New("signer unavailable")
	}}
	h := NewHandler(sessionRepo(tid), storage, allow(), &fakeSM{}, testTTLPolicy(), testUploadLimits)
	_, _, _, err := h.PresignPart(authedCtx(tid), "up-1", 1, time.Minute, testPartChecksum, SessionRef{})
	wantCode(t, err, connect.CodeInternal)
}

// Every RPC says how it ended: done at info (debug for the per-part reads),
// refused at warn, failed at error — naming the op, the tenant and the upload.
func TestMultipartOutcomesAreLogged(t *testing.T) {
	tid := uuid.New()
	success := map[string]zapcore.Level{
		opComplete:    zapcore.InfoLevel,
		opAbort:       zapcore.InfoLevel,
		opPresignPart: zapcore.DebugLevel,
		opListParts:   zapcore.DebugLevel,
	}
	for _, tc := range []struct {
		name  string
		err   error
		level func(op string) zapcore.Level
	}{
		{"done", nil, func(op string) zapcore.Level { return success[op] }},
		{"refused", ErrUploadNotFound, func(string) zapcore.Level { return zapcore.WarnLevel }},
		{"failed", errors.New("connection reset"), func(string) zapcore.Level { return zapcore.ErrorLevel }},
	} {
		for _, rpc := range sessionRPCs() {
			t.Run(tc.name+"/"+rpc.name, func(t *testing.T) {
				repo := sessionRepo(tid)
				if tc.err != nil {
					repo.getSessionFn = func(context.Context, string) (Session, error) { return Session{}, tc.err }
				}
				core, logs := observer.New(zapcore.DebugLevel)
				h := NewHandler(repo, &fakeStorage{}, allow(), &fakeSM{}, testTTLPolicy(), testUploadLimits)
				h.SetLogger(zap.New(core))
				_ = rpc.call(h, authedCtx(tid))
				lines := logs.FilterLevelExact(tc.level(rpc.op)).All()
				if len(lines) != 1 {
					t.Fatalf("want one %v line, got %v", tc.level(rpc.op), logs.All())
				}
				fields := lines[0].ContextMap()
				if fields["op"] != rpc.op || fields["tenant_id"] != tid.String() || fields["upload_id"] != "up-1" {
					t.Errorf("line lacks op, tenant or upload: %v", fields)
				}
				if len(logs.All()) != 1 {
					t.Errorf("one outcome logged more than once: %v", logs.All())
				}
			})
		}
	}
}

// An initiate names what it opened; its failure says which collection it
// was for.
func TestInitiateOutcomesAreLogged(t *testing.T) {
	tid := uuid.New()
	for _, tc := range []struct {
		name  string
		repo  *fakeRepo
		level zapcore.Level
	}{
		{"opened", &fakeRepo{}, zapcore.InfoLevel},
		{"failed", &fakeRepo{initiateSessionFn: func(context.Context, InitiateArgs, uuid.UUID, string, string, string) (Session, error) {
			return Session{}, errors.New("connection reset")
		}}, zapcore.ErrorLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			h := NewHandler(tc.repo, &fakeStorage{}, allow(), &fakeSM{}, testTTLPolicy(), testUploadLimits)
			h.SetLogger(zap.New(core))
			_, _ = h.InitiateMultipartUpload(authedCtx(tid), InitiateArgs{
				Collection: "photos", Key: "cat.jpg", SizeHint: testObjectSize, ChecksumAlgo: "SHA256",
			})
			lines := logs.FilterLevelExact(tc.level).All()
			if len(lines) != 1 {
				t.Fatalf("want one %v line, got %v", tc.level, logs.All())
			}
			fields := lines[0].ContextMap()
			if fields["op"] != opInitiate || fields["tenant_id"] != tid.String() || fields["collection"] != "photos" {
				t.Errorf("line lacks op, tenant or collection: %v", fields)
			}
		})
	}
}

// A part URL is a bearer credential: the line that says it was issued must
// not carry it, nor the headers signed into it.
func TestPresignPartLogCarriesNoURL(t *testing.T) {
	tid := uuid.New()
	core, logs := observer.New(zapcore.DebugLevel)
	h := NewHandler(sessionRepo(tid), &fakeStorage{}, allow(), &fakeSM{}, testTTLPolicy(), testUploadLimits)
	h.SetLogger(zap.New(core))
	if _, _, _, err := h.PresignPart(authedCtx(tid), "up-1", 1, time.Minute, testPartChecksum, SessionRef{}); err != nil {
		t.Fatal(err)
	}
	for _, line := range logs.All() {
		for k, v := range line.ContextMap() {
			if s := fmt.Sprint(v); strings.Contains(s, "https://") || strings.Contains(s, testPartChecksum) {
				t.Errorf("field %s carries the URL or its checksum: %s", k, s)
			}
		}
	}
}

// A Handler built without NewHandler logs nowhere rather than panicking.
func TestZeroHandlerLogsNowhere(t *testing.T) {
	h := &Handler{repo: &fakeRepo{getSessionFn: func(context.Context, string) (Session, error) {
		return Session{}, ErrUploadNotFound
	}}}
	wantCode(t, h.AbortMultipartUpload(authedCtx(uuid.New()), "up-1", SessionRef{}), connect.CodeNotFound)
}
