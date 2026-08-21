// Package backendh implements the admin BackendService — CRUD over storage
// backends. Mutations require role `platform.admin`. Reads are also allowed
// for `tenant.admin` and `bucket.admin` (so they can pick a target backend
// for new buckets / collections).
package backendh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// EventProducer is the ADR-0003 outbox seam: DispatchTx writes the event's
// outbox rows on the caller's tx so they commit atomically with the mutation.
// *worker.Dispatcher implements it.
type EventProducer interface {
	DispatchTx(ctx context.Context, tx pgx.Tx, tenantID string, evt worker.Event) (int, error)
}

// Repository is the admindomain BackendRepository plus the ADR-0003 tx seam
// (RunInTx + the *Tx mutations the rotate-with-event path needs). Kept local
// so admindomain stays pgx-free; the concrete *BackendRepoV2 satisfies both.
type Repository interface {
	admindomain.BackendRepository
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error
	RotateCredentialsTx(ctx context.Context, tx pgx.Tx, backendID, secretRef string, graceSeconds int64) error
	GetTx(ctx context.Context, tx pgx.Tx, backendID string) (admindomain.StorageBackend, error)
}

const (
	rolePlatformAdmin = "platform.admin"
	roleBucketAdmin   = "bucket.admin"
	roleTenantAdmin   = "tenant.admin"
)

// Cedar action names — must match policies/schema.cedarschema. Centralised
// here so handler call-sites can't typo them silently.
const (
	actionManageBackend = "ManageBackend"
	actionReadBackend   = "ReadBackend"
)

// BackendProber runs a read-only reachability + auth probe against a backend's
// storage endpoint (a ListBuckets). Implemented in the app layer: config
// backends use their pre-built client; a dynamic backend is resolved from its
// row (endpoint/region + credentials_secret_ref). Takes the whole row so the
// dynamic path has everything it needs. nil-safe at the handler.
type BackendProber interface {
	Probe(ctx context.Context, backend admindomain.StorageBackend) error
}

type Handler struct {
	repo   Repository
	policy cedar.Authorizer
	// prober runs TestBackend's connectivity check. nil → TestBackend
	// reports unreachable with an explanatory message instead of probing.
	prober BackendProber
	// events is the optional outbox producer; nil → events are skipped.
	events EventProducer
	log    *zap.Logger
}

func NewHandler(r Repository, policyEngine cedar.Authorizer) *Handler {
	if policyEngine == nil {
		panic("backendh: policy authorizer is required")
	}
	return &Handler{repo: r, policy: policyEngine, log: zap.NewNop()}
}

// SetProber wires the TestBackend connectivity prober. Opt-in: an unset
// prober makes TestBackend return reachable=false with a clear note rather
// than panicking, so unit tests and probe-less deployments still work.
func (h *Handler) SetProber(p BackendProber) { h.prober = p }

// SetEventProducer attaches the optional outbox producer. nil is silent (same
// contract as tenanth/bucketh) — deployments without the dispatcher leave it
// unset and rotation simply emits no event.
func (h *Handler) SetEventProducer(p EventProducer) { h.events = p }

// SetLogger attaches a non-nop logger so wiring diagnostics surface.
func (h *Handler) SetLogger(l *zap.Logger) {
	if l != nil {
		h.log = l
	}
}

// backendResourceName is the canonical StorageBackend resource string
// subscribers route on (`storageBackends/{backend_id}`).
func backendResourceName(backendID string) string {
	return "storageBackends/" + backendID
}

// dispatchEventTx fans a backend lifecycle event into the outbox on the
// caller's tx (ADR-0003). Backends are tenant-agnostic infra, so the event is
// platform-scoped (empty tenant). Returns the error so the caller rolls back.
func (h *Handler) dispatchEventTx(ctx context.Context, tx pgx.Tx, eventType, resourceName string, payload map[string]any) error {
	if h.events == nil {
		return nil
	}
	actor := ""
	if p, err := auth.PrincipalFromContext(ctx); err == nil {
		actor = p.Subject
	}
	_, err := h.events.DispatchTx(ctx, tx, "", worker.Event{
		Type:         eventType,
		At:           time.Now().UTC(),
		TenantID:     "",
		ResourceName: resourceName,
		ActorSubject: actor,
		Payload:      payload,
	})
	return err
}

// authorize runs Cedar against the StorageBackend resource (`r.BackendID` is
// the natural key — backends are tenant-agnostic infra).
func (h *Handler) authorize(ctx context.Context, action, backendID string) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{BackendID: backendID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("denied by policy"))
	}
	return nil
}

// ─── Create ─────────────────────────────────────────────────────────────────

func (h *Handler) CreateBackend(ctx context.Context, b admindomain.StorageBackend) (*admindomain.StorageBackend, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionManageBackend, b.BackendID); err != nil {
		return nil, err
	}
	if b.BackendID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("backend_id required"))
	}
	if b.Kind == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("kind required"))
	}
	if err := h.repo.Upsert(ctx, b); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create backend: %w", err))
	}
	got, err := h.repo.Get(ctx, b.BackendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

// ─── Read ───────────────────────────────────────────────────────────────────

func (h *Handler) GetBackend(ctx context.Context, backendID string) (*admindomain.StorageBackend, error) {
	if err := requireAnyRole(ctx, rolePlatformAdmin, roleBucketAdmin, roleTenantAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionReadBackend, backendID); err != nil {
		return nil, err
	}
	b, err := h.repo.Get(ctx, backendID)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	// Tenant/bucket admins get a redacted view — both the active and the
	// previous (grace-window) secret refs are secrets.
	p, _ := auth.PrincipalFromContext(ctx)
	if !p.HasRole(rolePlatformAdmin) {
		b.CredentialsSecretRef = "[REDACTED]"
		if b.PreviousCredentialsSecretRef != "" {
			b.PreviousCredentialsSecretRef = "[REDACTED]"
		}
	}
	return &b, nil
}

func (h *Handler) ListBackends(ctx context.Context, pageSize int32, afterID string) ([]admindomain.StorageBackend, string, error) {
	if err := requireAnyRole(ctx, rolePlatformAdmin, roleBucketAdmin, roleTenantAdmin); err != nil {
		return nil, "", err
	}
	if err := h.authorize(ctx, cedar.ActionReadBackend, ""); err != nil {
		return nil, "", err
	}
	out, next, err := h.repo.List(ctx, pageSize, afterID)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err)
	}
	p, _ := auth.PrincipalFromContext(ctx)
	if !p.HasRole(rolePlatformAdmin) {
		for i := range out {
			out[i].CredentialsSecretRef = "[REDACTED]"
			if out[i].PreviousCredentialsSecretRef != "" {
				out[i].PreviousCredentialsSecretRef = "[REDACTED]"
			}
		}
	}
	return out, next, nil
}

// ─── Update ─────────────────────────────────────────────────────────────────

func (h *Handler) UpdateBackend(ctx context.Context, b admindomain.StorageBackend, expectedVersion int64, mask []string) (*admindomain.StorageBackend, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionManageBackend, b.BackendID); err != nil {
		return nil, err
	}
	if err := h.repo.Update(ctx, b, expectedVersion, mask); err != nil {
		return nil, apiutil.MapError(err)
	}
	got, err := h.repo.Get(ctx, b.BackendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

// ─── SetBackendEnabled ───────────────────────────────────────────────────────

// SetBackendEnabled flips the backend's enable/disable state. It is a
// Set* mutation (not Create*), so it is OCC-guarded via expectedVersion
// rather than an idempotency key, and is naturally idempotent: setting
// the state a backend already has succeeds as a no-op (the repo UPDATE
// still matches the row; the bump_rv trigger advances resource_version).
//
// The default-backend guard (refuse disabling the configured default)
// is added in a follow-up so the constructor can carry the default id.
func (h *Handler) SetBackendEnabled(ctx context.Context, backendID string, enabled bool, expectedVersion int64) (*admindomain.StorageBackend, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionManageBackend, backendID); err != nil {
		return nil, err
	}
	// No default-backend guard: there is no default backend to protect. An
	// operator disabling a backend that still holds buckets is expected to
	// drain (read-only) first; operations resolving to a disabled backend fail
	// loudly, which is the honest, explicit behaviour.
	if err := h.repo.SetEnabled(ctx, backendID, enabled, expectedVersion); err != nil {
		return nil, apiutil.MapError(err)
	}
	got, err := h.repo.Get(ctx, backendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

// ─── SetBackendReadOnly ──────────────────────────────────────────────────────

// SetBackendReadOnly flips the backend's read-only "drain" state (migration
// 047). Same platform-admin + Cedar gate, OCC, and idempotency as
// SetBackendEnabled. Unlike disable, draining the configured default backend
// is ALLOWED: it's a deliberate migration step (reads keep working), and
// refusing it would make the default backend un-drainable.
func (h *Handler) SetBackendReadOnly(ctx context.Context, backendID string, readOnly bool, expectedVersion int64) (*admindomain.StorageBackend, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionManageBackend, backendID); err != nil {
		return nil, err
	}
	if err := h.repo.SetReadOnly(ctx, backendID, readOnly, expectedVersion); err != nil {
		return nil, apiutil.MapError(err)
	}
	got, err := h.repo.Get(ctx, backendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

// ─── SetBackendMaintenance ───────────────────────────────────────────────────

// SetBackendMaintenance raises/clears the operator-set maintenance flag
// (the schema baseline (001_initial_schema.sql)). Same platform-admin + Cedar gate, OCC, and idempotency as
// SetBackendReadOnly. Advisory only — it does not gate operations, and (like
// drain) the configured default backend may be flagged.
func (h *Handler) SetBackendMaintenance(ctx context.Context, backendID string, maintenance bool, expectedVersion int64) (*admindomain.StorageBackend, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionManageBackend, backendID); err != nil {
		return nil, err
	}
	if err := h.repo.SetMaintenance(ctx, backendID, maintenance, expectedVersion); err != nil {
		return nil, apiutil.MapError(err)
	}
	got, err := h.repo.Get(ctx, backendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &got, nil
}

func (h *Handler) RotateCredentials(ctx context.Context, backendID, secretRef string, grace time.Duration) (*admindomain.StorageBackend, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionRotateBackendCredentials, backendID); err != nil {
		return nil, err
	}
	if grace < 0 {
		grace = 0
	}
	// Rotation + paladin.backend.credentials_rotated in one tx (ADR-0003): a crash
	// can't leave the credential swap without its event, nor the reverse. The
	// event carries the grace horizon so consumers know how long the previous
	// secret must stay valid at the storage backend before it's safe to purge.
	rotatedAt := time.Now().UTC()
	var got admindomain.StorageBackend
	if err := h.repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := h.repo.RotateCredentialsTx(ctx, tx, backendID, secretRef, int64(grace.Seconds())); e != nil {
			return e
		}
		var e error
		if got, e = h.repo.GetTx(ctx, tx, backendID); e != nil {
			return e
		}
		payload := map[string]any{
			"backend_id":    backendID,
			"grace_seconds": int64(grace.Seconds()),
			"rotated_at":    rotatedAt.Format(time.RFC3339),
		}
		if grace > 0 {
			payload["previous_valid_until"] = rotatedAt.Add(grace).Format(time.RFC3339)
		}
		return h.dispatchEventTx(ctx, tx, "paladin.backend.credentials_rotated",
			backendResourceName(backendID), payload)
	}); err != nil {
		return nil, apiutil.MapError(err)
	}
	return &got, nil
}

// ─── Delete ─────────────────────────────────────────────────────────────────

func (h *Handler) DeleteBackend(ctx context.Context, backendID string, expectedVersion int64, force bool) error {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return err
	}
	if err := h.authorize(ctx, actionManageBackend, backendID); err != nil {
		return err
	}
	if err := h.repo.Delete(ctx, backendID, expectedVersion, force); err != nil {
		return apiutil.MapError(err)
	}
	return nil
}

// ─── TestBackend ────────────────────────────────────────────────────────────

// TestBackendOutput is the result of a connectivity probe.
type TestBackendOutput struct {
	Reachable    bool
	ErrorMessage string
	LatencyMs    int32
}

func (h *Handler) TestBackend(ctx context.Context, backendID string) (*TestBackendOutput, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, actionReadBackend, backendID); err != nil {
		return nil, err
	}
	// Existence first — a probe against an unknown backend id is a 404, not
	// an "unreachable" result. The row also feeds the dynamic probe path.
	got, err := h.repo.Get(ctx, backendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	// Read-only — no audit row (per the BackendService contract).
	if h.prober == nil {
		return &TestBackendOutput{
			Reachable:    false,
			ErrorMessage: "connectivity probe not wired in this deployment",
		}, nil
	}
	// Bound the probe so a wedged endpoint can't hang the RPC.
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	start := time.Now()
	perr := h.prober.Probe(pctx, got)
	out := &TestBackendOutput{LatencyMs: int32(time.Since(start).Milliseconds())}
	status := "ok"
	if perr != nil {
		out.Reachable = false
		out.ErrorMessage = perr.Error()
		status = "error"
	} else {
		out.Reachable = true
	}
	// Persist the probe outcome as the backend's derived health (migration
	// 048), surfaced in the UI. Best-effort: a health-write failure must not
	// fail the probe response, and it uses a fresh (non-probe-timeout) context
	// so a slow probe doesn't also lose the recording. Writes a separate table
	// — no resource_version churn, so TestBackend stays read-only w.r.t. config.
	if err := h.repo.SetHealth(ctx, backendID, status, out.ErrorMessage, time.Now().UTC()); err != nil {
		logger.FromContext(ctx).Warn("failed to record backend health probe outcome",
			zap.String("backend_id", backendID), zap.String("status", status), zap.Error(err))
	}
	return out, nil
}

// ─── role helpers ──────────────────────────────────────────────────────────

func requireRole(ctx context.Context, role string) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	if !p.HasRole(role) {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("role %q required", role))
	}
	return nil
}

func requireAnyRole(ctx context.Context, roles ...string) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	for _, r := range roles {
		if p.HasRole(r) {
			return nil
		}
	}
	return connect.NewError(connect.CodePermissionDenied, errors.New("insufficient role"))
}
