// Package bootstrap implements one-shot startup steps that prepare a fresh
// Paladin cluster for first-time use. Each step is opt-in via config, runs once
// per process boot, and is idempotent — restarting the server doesn't
// duplicate state.
//
// The currently-implemented step is `EnsureAdmin`, an ArgoCD-style
// platform-admin provisioning flow: read a password from a Kubernetes
// Secret (mounted as an env var), create the dedicated tenant if missing,
// then either create the admin user or — on operator request — rotate its
// password_hash.
package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	v1admindomain "github.com/oleg-tkachuk/paladin-private/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin-private/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin-private/internal/auth/store"
	"github.com/oleg-tkachuk/paladin-private/internal/config"
	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres/sqlc"
)

// AuditWriter is the subset of the audit-log adapter we depend on. Kept
// narrow so tests don't need the full repo.
type AuditWriter interface {
	Insert(ctx context.Context, e v1admindomain.AuditEntry) error
}

// TenantStore is the subset of sqlc.Queries we use for tenant lookup +
// creation. Declared as an interface so unit tests can inject an in-memory
// fake; *sqlc.Queries satisfies it implicitly via its concrete methods.
type TenantStore interface {
	GetTenantBySlug(ctx context.Context, slug string) (sqlc.GetTenantBySlugRow, error)
	CreateTenant(
		ctx context.Context,
		tenantID pgtype.UUID,
		slug string,
		displayName string,
		labels []byte,
		inheritedCedarPolicy string,
		storageLayout string,
	) error
}

// Deps groups everything EnsureAdmin needs from the surrounding process.
// The caller wires concrete sqlc.Queries / repo / logger; tests inject
// fakes via the interfaces.
type Deps struct {
	Tenants TenantStore
	Users   authstore.UserRepository
	Audit   AuditWriter
	Logger  *zap.Logger
	// Mode mirrors cfg.Runtime.Mode and gates production-grade password
	// length enforcement. Empty defaults to "release".
	Mode string
}

// audit action names — kept here so log scrapers and runbooks have a
// stable string to grep on.
const (
	auditActionCreate = "iam.bootstrap_admin.create"
	auditActionReset  = "iam.bootstrap_admin.reset"
	auditActorSubject = "system:bootstrap"
	auditAudience     = "paladin-iam"
)

// EnsureAdmin provisions the platform-admin user described in cfg. Returns
// nil when the step is disabled or already converged. Returns a non-nil
// error only on cases the operator must see (missing env var, tenant
// creation failure, DB error).
func EnsureAdmin(ctx context.Context, cfg config.BootstrapAdmin, deps Deps) error {
	if !cfg.Enabled {
		return nil
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	log := deps.Logger.With(
		zap.String("subject", cfg.Subject),
		zap.String("tenant_slug", cfg.TenantSlug),
	)

	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("bootstrap.admin config: %w", err)
	}

	pw, err := readPassword(cfg, deps.Mode)
	if err != nil {
		return err
	}

	tenantID, tenantCreated, err := ensureTenant(ctx, deps.Tenants, cfg)
	if err != nil {
		return fmt.Errorf("bootstrap.admin: ensure tenant: %w", err)
	}
	if tenantCreated {
		log.Info("bootstrap.admin: created tenant", zap.String("tenant_id", tenantID.String()))
	}

	hash, err := auth.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("bootstrap.admin: hash password: %w", err)
	}

	roles := cfg.Roles
	if len(roles) == 0 {
		roles = []string{apiutil.RolePlatformAdmin}
	}
	displayName := cfg.DisplayName
	if displayName == "" {
		displayName = cfg.Subject
	}

	existing, err := deps.Users.GetBySubject(ctx, tenantID, cfg.Subject)
	switch {
	case errors.Is(err, authstore.ErrNotFound):
		// Create the user.
		created, err := deps.Users.Create(ctx, authstore.User{
			TenantID:     tenantID,
			Subject:      cfg.Subject,
			DisplayName:  displayName,
			PasswordHash: hash,
			Roles:        roles,
		})
		if err != nil {
			return fmt.Errorf("bootstrap.admin: create user: %w", err)
		}
		log.Info("bootstrap.admin: created user", zap.String("user_id", created.UserID.String()))
		writeAudit(ctx, deps, auditActionCreate, tenantID, created.UserID, "")
		return nil

	case err != nil:
		return fmt.Errorf("bootstrap.admin: lookup user: %w", err)

	default:
		// User exists.
		if !cfg.ForceReset {
			log.Info("bootstrap.admin: user already exists, skipping (force_reset=false)",
				zap.String("user_id", existing.UserID.String()))
			return nil
		}
		if err := deps.Users.UpdatePasswordHash(ctx, existing.UserID, hash); err != nil {
			return fmt.Errorf("bootstrap.admin: rotate password: %w", err)
		}
		log.Warn("bootstrap.admin: rotated password (force_reset=true) — flip back to false after the next deploy",
			zap.String("user_id", existing.UserID.String()))
		writeAudit(ctx, deps, auditActionReset, tenantID, existing.UserID, "")
		return nil
	}
}

// ─── internal helpers ───────────────────────────────────────────────────────

func validateConfig(cfg config.BootstrapAdmin) error {
	if cfg.Subject == "" {
		return errors.New("subject must be set")
	}
	if cfg.TenantSlug == "" {
		return errors.New("tenant_slug must be set")
	}
	if err := apiutil.ValidateTenantSlug(cfg.TenantSlug); err != nil {
		return fmt.Errorf("tenant_slug invalid: %w", err)
	}
	return nil
}

// readPassword pulls the password out of cfg.Password — populated either
// inline (debug) or by the K8sSecretResolver from cfg.PasswordSecret. The
// caller has already cleared PasswordSecret by the time this runs. The
// raw plaintext never gets logged.
func readPassword(cfg config.BootstrapAdmin, mode string) (string, error) {
	pw := cfg.Password
	if pw == "" {
		return "", errors.New(
			"bootstrap.admin: password is empty (set bootstrap.admin.password_secret " +
				"to a Secret containing the password, or in debug mode set " +
				"bootstrap.admin.password inline)",
		)
	}
	min := cfg.MinPasswordLength
	if min == 0 {
		// Per-mode defaults: production-style length in release, lax in
		// debug/test so dev fixtures don't trip the gate.
		if mode == "" || mode == "release" {
			min = 16
		} else {
			min = 8
		}
	}
	if len(pw) < min {
		return "", fmt.Errorf("bootstrap.admin: password is shorter than %d characters (mode=%s)",
			min, mode)
	}
	return pw, nil
}

// ensureTenant returns the tenant_id for cfg.TenantSlug, creating the row
// if it doesn't exist. The second return value is true iff a fresh row was
// inserted (used only for logging — callers don't branch on it).
func ensureTenant(
	ctx context.Context,
	q TenantStore,
	cfg config.BootstrapAdmin,
) (uuid.UUID, bool, error) {
	row, err := q.GetTenantBySlug(ctx, cfg.TenantSlug)
	switch {
	case err == nil:
		// Already there. Convert pgtype.UUID → uuid.UUID.
		return uuidFromPg(row.Tenant.TenantID), false, nil

	case errors.Is(err, pgx.ErrNoRows):
		// Continue to create below.

	default:
		return uuid.Nil, false, fmt.Errorf("get tenant by slug: %w", err)
	}

	tenantID, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("generate tenant_id: %w", err)
	}
	display := cfg.TenantDisplayName
	if display == "" {
		display = cfg.TenantSlug
	}
	labels, _ := json.Marshal(map[string]string{
		"managed_by": "paladin-bootstrap",
	})
	if err := q.CreateTenant(
		ctx,
		pgtype.UUID{Bytes: tenantID, Valid: true},
		cfg.TenantSlug,
		display,
		labels,
		"",       // inherited_cedar_policy — empty default
		"shared", // storage_layout — the platform tenant is shared
	); err != nil {
		return uuid.Nil, false, fmt.Errorf("create tenant: %w", err)
	}
	return tenantID, true, nil
}

func writeAudit(
	ctx context.Context,
	deps Deps,
	action string,
	tenantID uuid.UUID,
	userID uuid.UUID,
	errorMsg string,
) {
	if deps.Audit == nil {
		return
	}
	entry := v1admindomain.AuditEntry{
		EntryID:       uuid.Must(uuid.NewV7()),
		ActorSubject:  auditActorSubject,
		ActorTenantID: tenantID,
		ActorAudience: auditAudience,
		Action:        action,
		ResourceName:  fmt.Sprintf("tenants/%s/users/%s", tenantID, userID),
		ErrorMessage:  errorMsg,
	}
	if err := deps.Audit.Insert(ctx, entry); err != nil {
		// Audit failure must not block boot — the admin is already
		// created/updated and the operator needs the cluster usable.
		deps.Logger.Warn("bootstrap.admin: audit insert failed", zap.Error(err))
	}
}

func uuidFromPg(u pgtype.UUID) uuid.UUID {
	if !u.Valid {
		return uuid.Nil
	}
	return u.Bytes
}
