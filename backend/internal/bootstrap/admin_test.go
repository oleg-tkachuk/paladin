package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	v1admindomain "github.com/oleg-tkachuk/paladin-private/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin-private/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin-private/internal/auth/store"
	"github.com/oleg-tkachuk/paladin-private/internal/config"
	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres/sqlc"
)

// ─── Fakes ──────────────────────────────────────────────────────────────────

type fakeTenants struct {
	bySlug  map[string]sqlc.Tenant
	creates int
}

func (f *fakeTenants) GetTenantBySlug(_ context.Context, slug string) (sqlc.GetTenantBySlugRow, error) {
	t, ok := f.bySlug[slug]
	if !ok {
		return sqlc.GetTenantBySlugRow{}, pgx.ErrNoRows
	}
	return sqlc.GetTenantBySlugRow{Tenant: t}, nil
}

func (f *fakeTenants) CreateTenant(
	_ context.Context,
	tenantID pgtype.UUID,
	slug string,
	displayName string,
	_ []byte,
	_ string,
	_ string,
) error {
	if f.bySlug == nil {
		f.bySlug = map[string]sqlc.Tenant{}
	}
	if _, exists := f.bySlug[slug]; exists {
		return errors.New("duplicate slug")
	}
	f.bySlug[slug] = sqlc.Tenant{
		TenantID:    tenantID,
		Slug:        slug,
		DisplayName: displayName,
	}
	f.creates++
	return nil
}

type fakeUsers struct {
	byKey   map[string]*authstore.User
	creates int
	resets  int
}

func userKey(tenantID uuid.UUID, subject string) string {
	return tenantID.String() + "::" + subject
}

func (f *fakeUsers) Create(_ context.Context, u authstore.User) (authstore.User, error) {
	if f.byKey == nil {
		f.byKey = map[string]*authstore.User{}
	}
	if u.UserID == uuid.Nil {
		u.UserID = uuid.New()
	}
	u.CreatedAt = time.Now()
	if _, exists := f.byKey[userKey(u.TenantID, u.Subject)]; exists {
		return authstore.User{}, authstore.ErrSubjectTaken
	}
	cp := u
	f.byKey[userKey(u.TenantID, u.Subject)] = &cp
	f.creates++
	return cp, nil
}

func (f *fakeUsers) GetBySubject(_ context.Context, tenantID uuid.UUID, subject string) (authstore.User, error) {
	u, ok := f.byKey[userKey(tenantID, subject)]
	if !ok {
		return authstore.User{}, authstore.ErrNotFound
	}
	return *u, nil
}

func (f *fakeUsers) UpdatePasswordHash(_ context.Context, id uuid.UUID, hash []byte) error {
	for _, u := range f.byKey {
		if u.UserID == id {
			u.PasswordHash = hash
			f.resets++
			return nil
		}
	}
	return authstore.ErrNotFound
}

// Unused interface methods — return zero values; these aren't exercised by the
// bootstrap path but the interface forces us to satisfy them.
func (f *fakeUsers) GetByID(context.Context, uuid.UUID) (authstore.User, error) {
	return authstore.User{}, authstore.ErrNotFound
}
func (f *fakeUsers) FindBySubjectGlobal(context.Context, string) ([]authstore.User, error) {
	return nil, nil
}
func (f *fakeUsers) Update(context.Context, authstore.User, int64) (authstore.User, error) {
	return authstore.User{}, nil
}
func (f *fakeUsers) Delete(context.Context, uuid.UUID, int64) error { return nil }
func (f *fakeUsers) List(context.Context, authstore.ListUsersArgs) ([]authstore.User, string, error) {
	return nil, "", nil
}
func (f *fakeUsers) TouchLogin(context.Context, uuid.UUID, time.Time) error { return nil }

type fakeAudit struct{ entries []v1admindomain.AuditEntry }

func (f *fakeAudit) Insert(_ context.Context, e v1admindomain.AuditEntry) error {
	f.entries = append(f.entries, e)
	return nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func newDeps() (*fakeTenants, *fakeUsers, *fakeAudit, Deps) {
	t := &fakeTenants{}
	u := &fakeUsers{}
	a := &fakeAudit{}
	return t, u, a, Deps{
		Tenants: t,
		Users:   u,
		Audit:   a,
		Logger:  zap.NewNop(),
		Mode:    "release",
	}
}

func defaultCfg() config.BootstrapAdmin {
	return config.BootstrapAdmin{
		Enabled:           true,
		Subject:           "admin",
		TenantSlug:        "platform",
		TenantDisplayName: "Platform",
		Roles:             []string{"platform.admin"},
	}
}

// ─── Tests ──────────────────────────────────────────────────────────────────

func TestEnsureAdmin_DisabledIsNoop(t *testing.T) {
	tenants, users, _, deps := newDeps()
	cfg := defaultCfg()
	cfg.Enabled = false
	cfg.Password = "irrelevant-but-unused-value"

	require.NoError(t, EnsureAdmin(context.Background(), cfg, deps))
	require.Equal(t, 0, tenants.creates, "no tenant should be created")
	require.Equal(t, 0, users.creates, "no user should be created")
}

func TestEnsureAdmin_ErrorsWhenPasswordEmpty(t *testing.T) {
	_, _, _, deps := newDeps()
	cfg := defaultCfg()
	cfg.Password = ""

	err := EnsureAdmin(context.Background(), cfg, deps)
	require.Error(t, err)
	require.Contains(t, err.Error(), "password is empty")
}

func TestEnsureAdmin_ErrorsWhenPasswordTooShortInRelease(t *testing.T) {
	_, _, _, deps := newDeps()
	cfg := defaultCfg()
	cfg.Password = "shortpw"

	err := EnsureAdmin(context.Background(), cfg, deps)
	require.Error(t, err)
	require.Contains(t, err.Error(), "shorter than")
}

func TestEnsureAdmin_AcceptsShortPasswordInDebug(t *testing.T) {
	tenants, users, _, deps := newDeps()
	deps.Mode = "debug"
	cfg := defaultCfg()
	cfg.Password = "devpass1" // 8 chars: meets debug minimum

	require.NoError(t, EnsureAdmin(context.Background(), cfg, deps))
	require.Equal(t, 1, tenants.creates)
	require.Equal(t, 1, users.creates)
}

func TestEnsureAdmin_FreshCreate(t *testing.T) {
	tenants, users, audit, deps := newDeps()
	cfg := defaultCfg()
	cfg.Password = "longenough-bootstrap-pw"

	require.NoError(t, EnsureAdmin(context.Background(), cfg, deps))

	// Tenant created, user created, audit recorded.
	require.Equal(t, 1, tenants.creates)
	require.Equal(t, 1, users.creates)
	require.Len(t, audit.entries, 1)
	require.Equal(t, auditActionCreate, audit.entries[0].Action)

	// Password hash is bcrypt — verify the round-trip works.
	for _, u := range users.byKey {
		require.NoError(t, auth.CheckPassword(u.PasswordHash, "longenough-bootstrap-pw"))
	}
}

func TestEnsureAdmin_IdempotentWhenForceResetFalse(t *testing.T) {
	tenants, users, audit, deps := newDeps()
	cfg := defaultCfg()
	cfg.Password = "longenough-bootstrap-pw"

	// First boot — creates user.
	require.NoError(t, EnsureAdmin(context.Background(), cfg, deps))
	firstHash := firstUser(users).PasswordHash

	// Second boot — must not change anything. Use a different password to
	// prove we don't re-hash.
	cfg.Password = "another-very-long-passwd"
	require.NoError(t, EnsureAdmin(context.Background(), cfg, deps))

	require.Equal(t, 1, tenants.creates, "tenant create must run only once")
	require.Equal(t, 1, users.creates, "user create must run only once")
	require.Equal(t, 0, users.resets, "no rotation when force_reset=false")
	require.Len(t, audit.entries, 1, "no second audit entry")
	require.Equal(t, firstHash, firstUser(users).PasswordHash)
}

func TestEnsureAdmin_ForceResetRotatesPassword(t *testing.T) {
	tenants, users, audit, deps := newDeps()
	cfg := defaultCfg()
	cfg.Password = "first-bootstrap-pw-long"

	// First boot.
	require.NoError(t, EnsureAdmin(context.Background(), cfg, deps))
	originalID := firstUser(users).UserID
	originalHash := firstUser(users).PasswordHash

	// Second boot with force_reset and a new password.
	cfg.ForceReset = true
	cfg.Password = "second-bootstrap-pw-long"
	require.NoError(t, EnsureAdmin(context.Background(), cfg, deps))

	require.Equal(t, 1, tenants.creates)
	require.Equal(t, 1, users.creates, "user is updated, not re-created")
	require.Equal(t, 1, users.resets)
	require.Len(t, audit.entries, 2)
	require.Equal(t, auditActionReset, audit.entries[1].Action)

	// Same user_id, new hash, new password validates.
	require.Equal(t, originalID, firstUser(users).UserID)
	require.NotEqual(t, originalHash, firstUser(users).PasswordHash)
	require.NoError(t, auth.CheckPassword(firstUser(users).PasswordHash, "second-bootstrap-pw-long"))
}

func TestEnsureAdmin_RejectsInvalidSlug(t *testing.T) {
	_, _, _, deps := newDeps()
	cfg := defaultCfg()
	cfg.TenantSlug = "Invalid_Slug" // upper-case + underscore both rejected
	cfg.Password = "longenough-bootstrap-pw"

	err := EnsureAdmin(context.Background(), cfg, deps)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tenant_slug invalid")
}

func TestEnsureAdmin_ReusesExistingTenant(t *testing.T) {
	tenants, users, _, deps := newDeps()
	// Pre-populate the tenant so EnsureAdmin should not call CreateTenant.
	existingID := uuid.New()
	tenants.bySlug = map[string]sqlc.Tenant{
		"platform": {
			TenantID: pgtype.UUID{Bytes: existingID, Valid: true},
			Slug:     "platform",
		},
	}
	cfg := defaultCfg()
	cfg.Password = "longenough-bootstrap-pw"

	require.NoError(t, EnsureAdmin(context.Background(), cfg, deps))
	require.Equal(t, 0, tenants.creates, "existing tenant must be reused")
	require.Equal(t, 1, users.creates)
	require.Equal(t, existingID, firstUser(users).TenantID)
}

func firstUser(f *fakeUsers) authstore.User {
	for _, u := range f.byKey {
		return *u
	}
	return authstore.User{}
}
