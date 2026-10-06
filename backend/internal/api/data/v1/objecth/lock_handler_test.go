package objecth

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// The database enforces what a lock means once written; these tests cover the
// decisions made before the write, which the database cannot see: who is
// allowed to ask, whether the bucket opted in, and whether there is a version
// to attach anything to.

type lockRepoStub struct {
	setRetentionArgs SetRetentionArgs
	setRetentionErr  error
	setRetentionOut  ObjectLock

	legalHoldCalls []bool
	legalHoldErr   error

	getOut ObjectLock
	getErr error

	defaults []struct {
		mode      string
		retention time.Duration
	}
}

func (s *lockRepoStub) SetRetention(_ context.Context, args SetRetentionArgs) (ObjectLock, error) {
	s.setRetentionArgs = args
	return s.setRetentionOut, s.setRetentionErr
}

func (s *lockRepoStub) SetLegalHold(_ context.Context, _, _ uuid.UUID, hold bool) (ObjectLock, error) {
	s.legalHoldCalls = append(s.legalHoldCalls, hold)
	return ObjectLock{LegalHold: hold}, s.legalHoldErr
}

func (s *lockRepoStub) GetByVersion(context.Context, uuid.UUID) (ObjectLock, error) {
	return s.getOut, s.getErr
}

func (s *lockRepoStub) ApplyBucketDefault(_ context.Context, _, _ uuid.UUID, mode string, retention time.Duration) error {
	s.defaults = append(s.defaults, struct {
		mode      string
		retention time.Duration
	}{mode, retention})
	return nil
}

// lockVersionStub supplies the current version the lock attaches to.
type lockVersionStub struct {
	current uuid.UUID
	err     error
}

func (s *lockVersionStub) Insert(context.Context, ObjectVersion) error { panic("not used") }
func (s *lockVersionStub) Get(context.Context, uuid.UUID) (ObjectVersion, error) {
	panic("not used")
}
func (s *lockVersionStub) List(context.Context, uuid.UUID, int32, string) ([]ObjectVersion, string, error) {
	panic("not used")
}
func (s *lockVersionStub) CurrentVersionID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return s.current, s.err
}
func (s *lockVersionStub) SetCurrentVersionID(context.Context, uuid.UUID, uuid.UUID) error {
	panic("not used")
}

// lockObjectStub is the minimum Repository the lock handler touches.
type lockObjectStub struct {
	fakeObjectRepo
	obj     Object
	objErr  error
	meta    BucketMeta
	metaErr error
}

func (s *lockObjectStub) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	return s.obj, s.objErr
}

func (s *lockObjectStub) LookupBucketMeta(context.Context, uuid.UUID, string, bool) (BucketMeta, error) {
	return s.meta, s.metaErr
}

type denyAll struct{}

func (denyAll) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionDeny, nil
}

// actionRecorder captures which Cedar action the handler asked about, which is
// the whole point of giving object lock its own actions.
type actionRecorder struct{ last cedar.Action }

func (a *actionRecorder) IsAuthorized(_ context.Context, _ *cedar.Principal, action cedar.Action, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.last = action
	return cedar.DecisionAllow, nil
}

func lockCtx(tenantID uuid.UUID, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(),
		&auth.Principal{Subject: "u1", TenantID: tenantID, Roles: roles})
}

func newLockHandler(t *testing.T, locks *lockRepoStub, policy cedar.Authorizer, lockEnabled bool) (*LockHandler, uuid.UUID) {
	t.Helper()
	tenantID := uuid.New()
	objects := &lockObjectStub{
		obj:  Object{ObjectID: uuid.New(), TenantID: tenantID, Collection: "docs", Key: "a.pdf"},
		meta: BucketMeta{BackendID: "be", BucketName: "bk", ObjectLockEnabled: lockEnabled},
	}
	versions := &lockVersionStub{current: uuid.Must(uuid.NewV7())}
	return NewLockHandler(objects, versions, locks, policy), tenantID
}

func lockCode(err error) connect.Code { return connect.CodeOf(err) }

// TestSetRetentionRejectsBadInput pins the two arguments that cannot be
// repaired downstream: an unknown mode, and a window that has already closed.
// Storing either would leave a row implying protection it does not give.
func TestSetRetentionRejectsBadInput(t *testing.T) {
	locks := &lockRepoStub{}
	h, tid := newLockHandler(t, locks, &actionRecorder{}, true)

	for _, tc := range []struct {
		name string
		in   SetRetentionInput
	}{
		{"unknown mode", SetRetentionInput{Collection: "docs", ObjectID: "o", Mode: "FOREVER", RetainUntil: time.Now().Add(time.Hour)}},
		{"empty mode", SetRetentionInput{Collection: "docs", ObjectID: "o", RetainUntil: time.Now().Add(time.Hour)}},
		{"retain_until in the past", SetRetentionInput{Collection: "docs", ObjectID: "o", Mode: "GOVERNANCE", RetainUntil: time.Now().Add(-time.Hour)}},
		{"retain_until now", SetRetentionInput{Collection: "docs", ObjectID: "o", Mode: "GOVERNANCE"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.SetRetention(lockCtx(tid), tc.in); lockCode(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument", lockCode(err))
			}
		})
	}
	if locks.setRetentionArgs.Mode != "" {
		t.Error("a rejected request still reached the repository")
	}
}

// TestSetRetentionRequiresBucketOptIn pins that a lock cannot be created in a
// bucket whose operator never enabled object lock. Otherwise any writer could
// pin storage the bucket owner never agreed to hold.
func TestSetRetentionRequiresBucketOptIn(t *testing.T) {
	locks := &lockRepoStub{}
	h, tid := newLockHandler(t, locks, &actionRecorder{}, false) // lock disabled

	_, err := h.SetRetention(lockCtx(tid), SetRetentionInput{
		Collection: "docs", ObjectID: "o", Mode: "GOVERNANCE",
		RetainUntil: time.Now().Add(time.Hour),
	})
	if lockCode(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", lockCode(err))
	}
	if !errors.Is(err, ErrObjectLockNotEnabled) {
		t.Errorf("error = %v, want ErrObjectLockNotEnabled", err)
	}
}

// TestSetRetentionBypassNeedsARole is the gate that separates "may set a
// lock" from "may weaken one". A caller with write access to the collection
// gets the first; only lock.governance.bypass or platform.admin gets the
// second.
func TestSetRetentionBypassNeedsARole(t *testing.T) {
	in := SetRetentionInput{
		Collection: "docs", ObjectID: "o", Mode: "GOVERNANCE",
		RetainUntil: time.Now().Add(time.Hour), BypassGovernance: true,
	}

	t.Run("refused without the role", func(t *testing.T) {
		locks := &lockRepoStub{}
		h, tid := newLockHandler(t, locks, &actionRecorder{}, true)
		if _, err := h.SetRetention(lockCtx(tid), in); lockCode(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v, want PermissionDenied", lockCode(err))
		}
		if locks.setRetentionArgs.BypassGovernance {
			t.Error("the refused bypass still reached the repository")
		}
	})

	for _, role := range []string{"lock.governance.bypass", "platform.admin"} {
		t.Run("allowed with "+role, func(t *testing.T) {
			locks := &lockRepoStub{}
			h, tid := newLockHandler(t, locks, &actionRecorder{}, true)
			if _, err := h.SetRetention(lockCtx(tid, role), in); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !locks.setRetentionArgs.BypassGovernance {
				t.Error("the bypass flag was not forwarded")
			}
		})
	}

	t.Run("no role needed when not bypassing", func(t *testing.T) {
		locks := &lockRepoStub{}
		h, tid := newLockHandler(t, locks, &actionRecorder{}, true)
		plain := in
		plain.BypassGovernance = false
		if _, err := h.SetRetention(lockCtx(tid), plain); err != nil {
			t.Fatalf("setting a lock required a bypass role: %v", err)
		}
	})
}

// TestLockRPCsUseTheirOwnCedarActions pins that object lock does not ride on
// UpdateObject. Sharing an action would mean write access to a collection
// carries the power to make its objects permanently undeletable.
func TestLockRPCsUseTheirOwnCedarActions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		call   func(h *LockHandler, ctx context.Context) error
		action cedar.Action
	}{
		{"retention", func(h *LockHandler, ctx context.Context) error {
			_, err := h.SetRetention(ctx, SetRetentionInput{
				Collection: "docs", ObjectID: "o", Mode: "GOVERNANCE",
				RetainUntil: time.Now().Add(time.Hour),
			})
			return err
		}, cedar.ActionSetObjectRetention},
		{"legal hold", func(h *LockHandler, ctx context.Context) error {
			_, err := h.SetLegalHold(ctx, "docs", "o", true)
			return err
		}, cedar.ActionSetObjectLegalHold},
		{"read", func(h *LockHandler, ctx context.Context) error {
			_, err := h.GetLock(ctx, "docs", "o")
			return err
		}, cedar.ActionReadObjectLock},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &actionRecorder{}
			h, tid := newLockHandler(t, &lockRepoStub{}, rec, true)
			if err := tc.call(h, lockCtx(tid)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rec.last != tc.action {
				t.Errorf("cedar action = %q, want %q", rec.last, tc.action)
			}
		})
	}
}

// TestLockRPCsHonourPolicyDenial pins that a Cedar deny is a deny on all three.
func TestLockRPCsHonourPolicyDenial(t *testing.T) {
	h, tid := newLockHandler(t, &lockRepoStub{}, denyAll{}, true)
	ctx := lockCtx(tid)

	if _, err := h.SetRetention(ctx, SetRetentionInput{
		Collection: "docs", ObjectID: "o", Mode: "GOVERNANCE", RetainUntil: time.Now().Add(time.Hour),
	}); lockCode(err) != connect.CodePermissionDenied {
		t.Errorf("retention: code = %v, want PermissionDenied", lockCode(err))
	}
	if _, err := h.SetLegalHold(ctx, "docs", "o", true); lockCode(err) != connect.CodePermissionDenied {
		t.Errorf("legal hold: code = %v, want PermissionDenied", lockCode(err))
	}
	if _, err := h.GetLock(ctx, "docs", "o"); lockCode(err) != connect.CodePermissionDenied {
		t.Errorf("read: code = %v, want PermissionDenied", lockCode(err))
	}
}

// TestReleasingLegalHoldDoesNotNeedBucketOptIn pins the asymmetry that keeps a
// misconfigured bucket recoverable: placing a hold requires the bucket to have
// object lock on, releasing one does not. Otherwise turning the bucket setting
// off would strand every hold with no way to lift it.
func TestReleasingLegalHoldDoesNotNeedBucketOptIn(t *testing.T) {
	locks := &lockRepoStub{}
	h, tid := newLockHandler(t, locks, &actionRecorder{}, false) // lock disabled

	if _, err := h.SetLegalHold(lockCtx(tid), "docs", "o", true); lockCode(err) != connect.CodeFailedPrecondition {
		t.Fatalf("placing a hold on an opted-out bucket: code = %v, want FailedPrecondition", lockCode(err))
	}
	if _, err := h.SetLegalHold(lockCtx(tid), "docs", "o", false); err != nil {
		t.Fatalf("releasing a hold was refused on an opted-out bucket: %v", err)
	}
	if len(locks.legalHoldCalls) != 1 || locks.legalHoldCalls[0] {
		t.Errorf("repository saw %v, want exactly one release", locks.legalHoldCalls)
	}
}

// TestLockNeedsACurrentVersion pins that an object with no version reports a
// precondition failure rather than quietly succeeding. Reporting success would
// tell the caller their object is retained when nothing holds it.
func TestLockNeedsACurrentVersion(t *testing.T) {
	tenantID := uuid.New()
	objects := &lockObjectStub{
		obj:  Object{ObjectID: uuid.New(), TenantID: tenantID, Collection: "docs", Key: "a.pdf"},
		meta: BucketMeta{ObjectLockEnabled: true},
	}
	h := NewLockHandler(objects, &lockVersionStub{current: uuid.Nil}, &lockRepoStub{}, &actionRecorder{})

	_, err := h.SetRetention(lockCtx(tenantID), SetRetentionInput{
		Collection: "docs", ObjectID: "o", Mode: "GOVERNANCE", RetainUntil: time.Now().Add(time.Hour),
	})
	if lockCode(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", lockCode(err))
	}
	if !errors.Is(err, ErrNoCurrentVersion) {
		t.Errorf("error = %v, want ErrNoCurrentVersion", err)
	}
}

// TestWeakeningIsAPreconditionNotAnInternalError pins the mapping of the
// repository's refusal. An operator who tries to shorten a COMPLIANCE window
// should be told why, not handed a 500.
func TestWeakeningIsAPreconditionNotAnInternalError(t *testing.T) {
	locks := &lockRepoStub{setRetentionErr: ErrRetentionWeakened}
	h, tid := newLockHandler(t, locks, &actionRecorder{}, true)

	_, err := h.SetRetention(lockCtx(tid), SetRetentionInput{
		Collection: "docs", ObjectID: "o", Mode: "GOVERNANCE", RetainUntil: time.Now().Add(time.Hour),
	})
	if lockCode(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", lockCode(err))
	}
}

// TestUnauthenticatedIsRefusedBeforeAnythingElse pins that identity comes
// first — the handler must not read the object, let alone write a lock, for a
// caller it cannot name.
func TestUnauthenticatedIsRefusedBeforeAnythingElse(t *testing.T) {
	locks := &lockRepoStub{}
	h, _ := newLockHandler(t, locks, &actionRecorder{}, true)

	if _, err := h.SetRetention(context.Background(), SetRetentionInput{
		Collection: "docs", ObjectID: "o", Mode: "GOVERNANCE", RetainUntil: time.Now().Add(time.Hour),
	}); lockCode(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", lockCode(err))
	}
	if locks.setRetentionArgs.Mode != "" {
		t.Error("an unauthenticated request reached the repository")
	}
}
