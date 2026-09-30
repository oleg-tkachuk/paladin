// Package usersettingsh implements the UserSettings RPC surface.
//
// Two access paths:
//
//  1. Self-service: a user reads/updates their own settings via GetMine /
//     UpdateMine. No Cedar round-trip — owning your settings is a baseline
//     capability that can never be revoked from the principal who owns them.
//
//  2. Admin: GetForUser / ListByTenant / DeleteForUser act on someone else's
//     settings, gated by `ReadUserSettings` / `ManageUserSettings` Cedar
//     actions. Tenant-isolation is enforced at the code level as
//     defense-in-depth — Cedar policies have historically drifted from
//     intent during migrations.
//
// Free-form `Preferences` is capped at 16 KiB on write to keep the row
// bounded. Web clients should treat preferences as best-effort: server may
// truncate, the schema is owned client-side.
package usersettingsh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// Settings is the domain projection of a user_settings row.
//
// Preferences carries arbitrary JSON the web UI uses for client-side state.
// The server treats the value as opaque bytes; callers MUST send valid JSON
// or Validate will reject the write.
type Settings struct {
	UserID          uuid.UUID
	TenantID        uuid.UUID
	Timezone        string
	Locale          string
	Theme           string
	Preferences     []byte // raw JSON
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Defaults applied when a user has no row yet. Mirrors the column defaults
// in migrations/001_initial_schema.sql so server-side fallback matches what
// the database would have produced on first write.
func DefaultsFor(userID, tenantID uuid.UUID) Settings {
	return Settings{
		UserID:      userID,
		TenantID:    tenantID,
		Timezone:    "UTC",
		Locale:      "en-US",
		Theme:       "system",
		Preferences: []byte("{}"),
	}
}

// Repository is the persistence interface satisfied by the postgres adapter.
type Repository interface {
	Get(ctx context.Context, userID uuid.UUID) (Settings, error)
	Upsert(ctx context.Context, s Settings) (Settings, error)
	ListByTenant(ctx context.Context, tenantID uuid.UUID, pageSize int32) ([]Settings, error)
	Delete(ctx context.Context, userID uuid.UUID) error
}

// ErrNotFound — returned by Repository.Get when no row exists. Handler
// translates to "serve defaults" for self-service reads.
var ErrNotFound = errors.New("user_settings not found")

// preferencesCap bounds the JSON preference payload. Picked at 16 KiB —
// large enough for layout state across many panels, small enough that a
// runaway tab can't pin-point-of-truth a giant blob into a hot row.
const preferencesCap = 16 * 1024

type Handler struct {
	repo   Repository
	users  authstore.UserRepository
	policy cedar.Authorizer
}

func NewHandler(repo Repository, users authstore.UserRepository, policy cedar.Authorizer) *Handler {
	if repo == nil {
		panic("usersettingsh: repo is required")
	}
	if policy == nil {
		panic("usersettingsh: policy authorizer is required")
	}
	return &Handler{repo: repo, users: users, policy: policy}
}

// GetMine returns the calling user's settings, or defaults when none have
// been persisted yet. Never returns NotFound — the row's absence is a normal
// state for a fresh user.
func (h *Handler) GetMine(ctx context.Context) (*Settings, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	uid, err := h.subjectToUserID(ctx, p)
	if err != nil {
		return nil, err
	}
	s, err := h.repo.Get(ctx, uid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			d := DefaultsFor(uid, p.TenantID)
			return &d, nil
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &s, nil
}

// UpdateMineInput is the patch payload for self-service updates. All fields
// are optional — only the ones present in UpdateMask are applied.
type UpdateMineInput struct {
	UpdateMask  []string
	Timezone    string
	Locale      string
	Theme       string
	Preferences []byte
}

// UpdateMine applies a partial update to the calling user's settings and
// returns the post-write row. Falls through to Upsert (no separate "create")
// because the row's existence is a database concern, not the caller's.
func (h *Handler) UpdateMine(ctx context.Context, in UpdateMineInput) (*Settings, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	uid, err := h.subjectToUserID(ctx, p)
	if err != nil {
		return nil, err
	}
	current, err := h.repo.Get(ctx, uid)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		current = DefaultsFor(uid, p.TenantID)
	}
	if err := applyMask(&current, in); err != nil {
		return nil, err
	}
	if err := validate(current); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	saved, err := h.repo.Upsert(ctx, current)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &saved, nil
}

// GetForUser is the admin-side read. Cedar gates cross-user access; the
// owning principal short-circuits via GetMine.
func (h *Handler) GetForUser(ctx context.Context, userID uuid.UUID) (*Settings, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	target, err := h.users.GetByID(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	// Same-tenant boundary, even for platform admins (they can override via
	// Cedar policy, but the default keeps tenants isolated).
	if !hasPlatformAdmin(p) && target.TenantID != p.TenantID {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}
	if err := h.authorize(ctx, p, cedar.ActionReadUserSettings, target); err != nil {
		return nil, err
	}
	s, err := h.repo.Get(auth.WithActingTenant(ctx, target.TenantID), userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			d := DefaultsFor(userID, target.TenantID)
			return &d, nil
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &s, nil
}

// ListByTenant returns all customized settings rows for a tenant — the
// uncustomized users are absent (callers should fall back to defaults for
// missing user_ids).
func (h *Handler) ListByTenant(ctx context.Context, tenantID uuid.UUID, pageSize int32) ([]Settings, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	if !hasPlatformAdmin(p) && tenantID != p.TenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("cross-tenant list denied"))
	}
	if err := h.authorizeTenant(ctx, p, cedar.ActionReadUserSettings, tenantID); err != nil {
		return nil, err
	}
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 100
	}
	return h.repo.ListByTenant(auth.WithActingTenant(ctx, tenantID), tenantID, pageSize)
}

// DeleteForUser drops a user's persisted settings row. The user reverts to
// defaults until they next call UpdateMine. Used by GDPR-erasure flows and
// support troubleshooting ("reset my UI to defaults").
func (h *Handler) DeleteForUser(ctx context.Context, userID uuid.UUID) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	target, err := h.users.GetByID(ctx, userID)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if !hasPlatformAdmin(p) && target.TenantID != p.TenantID {
		return connect.NewError(connect.CodeNotFound, errors.New("user not found"))
	}
	if err := h.authorize(ctx, p, cedar.ActionManageUserSettings, target); err != nil {
		return err
	}
	if err := h.repo.Delete(auth.WithActingTenant(ctx, target.TenantID), userID); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	return nil
}

// ─── helpers ────────────────────────────────────────────────────────────────

// subjectToUserID resolves the caller's JWT `sub` claim to the user_id
// primary key needed by the user_settings FK. The IAM issuer mints
// access tokens with `sub = UserID.String()` (see authh.MintAccess),
// so p.Subject is already the UUID — parse it directly. We retain a
// GetBySubject fallback for tokens minted by an older issuer that
// stored the human-readable subject in `sub`; once that path is
// confirmed dead the fallback can go.
func (h *Handler) subjectToUserID(ctx context.Context, p *auth.Principal) (uuid.UUID, error) {
	if h.users == nil {
		return uuid.Nil, connect.NewError(connect.CodeInternal,
			errors.New("user repository not wired"))
	}
	if id, perr := uuid.Parse(p.Subject); perr == nil {
		// Fast path — sub claim is the UUID.
		if u, err := h.users.GetByID(ctx, id); err == nil {
			return u.UserID, nil
		}
	}
	u, err := h.users.GetBySubject(ctx, p.TenantID, p.Subject)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated,
			fmt.Errorf("resolve user: %w", err))
	}
	return u.UserID, nil
}

func (h *Handler) authorize(ctx context.Context, p *auth.Principal, action string, target authstore.User) error {
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{
			TenantID:      target.TenantID,
			TargetUserID:  target.UserID,
			TargetSubject: target.Subject,
		},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

func (h *Handler) authorizeTenant(ctx context.Context, p *auth.Principal, action string, tenantID uuid.UUID) error {
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{TenantID: tenantID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}
	return nil
}

func applyMask(s *Settings, in UpdateMineInput) error {
	if len(in.UpdateMask) == 0 {
		// Empty mask = "replace all explicit fields" for ergonomic clients
		// that don't bother computing the mask. Preferences absent → keep.
		s.Timezone = in.Timezone
		s.Locale = in.Locale
		s.Theme = in.Theme
		if in.Preferences != nil {
			s.Preferences = in.Preferences
		}
		return nil
	}
	for _, field := range in.UpdateMask {
		switch field {
		case "timezone":
			s.Timezone = in.Timezone
		case "locale":
			s.Locale = in.Locale
		case "theme":
			s.Theme = in.Theme
		case "preferences":
			s.Preferences = in.Preferences
		default:
			return connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("unknown update_mask field %q", field))
		}
	}
	return nil
}

func validate(s Settings) error {
	if err := apiutil.ValidateTimezone(s.Timezone); err != nil {
		return err
	}
	if err := apiutil.ValidateLocale(s.Locale); err != nil {
		return err
	}
	if err := apiutil.ValidateTheme(s.Theme); err != nil {
		return err
	}
	if len(s.Preferences) > preferencesCap {
		return fmt.Errorf("preferences too large (%d bytes, max %d)", len(s.Preferences), preferencesCap)
	}
	if len(s.Preferences) == 0 {
		// Empty bytes round-trip cleanly as `{}` so the column NOT NULL
		// constraint never fires. Validate would otherwise fail at the
		// database layer with a less-friendly message.
		return nil
	}
	if !json.Valid(s.Preferences) {
		return errors.New("preferences must be valid JSON")
	}
	return nil
}

func hasPlatformAdmin(p *auth.Principal) bool {
	return p != nil && p.HasRole("platform.admin")
}
