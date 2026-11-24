package multiparth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

type recordingVersions struct {
	got []VersionedObject
}

func (r *recordingVersions) OnPromote(_ context.Context, obj VersionedObject) error {
	r.got = append(r.got, obj)
	return nil
}

type recordingQuota struct {
	sizes []int64
	err   error
}

func (r *recordingQuota) OnObjectPromoted(_ context.Context, _, _ uuid.UUID, size int64) error {
	r.sizes = append(r.sizes, size)
	return r.err
}

// A completion that promoted the object records its version and its quota; a
// retry that found it promoted records neither, and a quota that cannot be
// credited does not fail the completion.
func TestCompleteRecordsVersionAndQuotaOnce(t *testing.T) {
	tid := uuid.New()
	for name, tc := range map[string]struct {
		promoted bool
		quotaErr error
		want     int
	}{
		"promoted":                {true, nil, 1},
		"already promoted":        {false, nil, 0},
		"quota cannot be counted": {true, errors.New("db down"), 1},
	} {
		t.Run(name, func(t *testing.T) {
			versions, quota := &recordingVersions{}, &recordingQuota{err: tc.quotaErr}
			h := NewHandler(sessionRepo(tid), &fakeStorage{}, allow(), promotedSM{&fakeSM{}, tc.promoted}, testTTLPolicy(), testUploadLimits)
			h.SetVersionRecorder(versions)
			h.SetQuotaUpdater(quota)
			if _, err := h.CompleteMultipartUpload(authedCtx(tid), CompleteArgs{UploadID: "up-1", Parts: completeParts()}); err != nil {
				t.Fatalf("complete: %v", err)
			}
			if len(versions.got) != tc.want || len(quota.sizes) != tc.want {
				t.Fatalf("versions %d, quota %d; want %d each", len(versions.got), len(quota.sizes), tc.want)
			}
			if tc.want == 1 {
				v := versions.got[0]
				if v.ETag != "etag-1" || v.SizeBytes != 123 || v.Checksum == "" || v.ChecksumPartSizeBytes != testPartSize {
					t.Errorf("version recorded %+v, want the assembled object with its composite", v)
				}
				if quota.sizes[0] != 123 {
					t.Errorf("quota credited %d bytes, want 123", quota.sizes[0])
				}
			}
		})
	}
}

// promotedSM answers a promotion as this call's transition or as one that had
// already happened.
type promotedSM struct {
	*fakeSM
	changed bool
}

func (p promotedSM) PromoteToAvailable(context.Context, uuid.UUID, *statemachine.Placement, string, int64, string, string, statemachine.Source) (bool, error) {
	return p.changed, nil
}

// A session that cannot be deleted after an abort is left to the discard that
// follows; the abort still succeeds.
func TestAbortSucceedsWhenTheSessionIsNotDeleted(t *testing.T) {
	tid := uuid.New()
	repo := sessionRepo(tid)
	repo.deleteSessionFn = func(context.Context, string) error { return errors.New("db down") }
	sm := &fakeSM{}
	h := NewHandler(repo, &fakeStorage{}, allow(), sm, testTTLPolicy(), testUploadLimits)
	if err := h.AbortMultipartUpload(authedCtx(tid), "up-1", SessionRef{}); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if sm.failedID == uuid.Nil() {
		t.Error("the object was not discarded")
	}
}
