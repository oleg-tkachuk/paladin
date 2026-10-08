// Package backendh implements the admin BackendService — CRUD over storage
// backends. Mutations require role `platform.admin`. Reads are also allowed
// for `tenant.admin` and `bucket.admin` (so they can pick a target backend
// for new buckets / collections).
package backendh

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/safecast"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	celpkg "github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
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

// BackendProber runs a read-only reachability + auth probe against a backend's
// storage endpoint (a ListBuckets). Implemented in the app layer: config
// backends use their pre-built client; a dynamic backend is resolved from its
// row (endpoint/region + credentials_secret_ref). Takes the whole row so the
// dynamic path has everything it needs. nil-safe at the handler.
type BackendProber interface {
	Probe(ctx context.Context, backend admindomain.StorageBackend) error
	// ProbeFeatures exercises every S3 feature Paladin uses (ADR-0026). The
	// error is only for a backend no client could be built for; what the
	// store does is in the results.
	ProbeFeatures(ctx context.Context, backend admindomain.StorageBackend) ([]features.Result, error)
}

const (
	// reachabilityProbeTimeout bounds TestBackend's reachability check, so a
	// wedged endpoint cannot hang the RPC.
	reachabilityProbeTimeout = 5 * time.Second
	// featureProbeTimeout bounds the feature probe that follows it. The two
	// together stay inside the admin listener's default 30s write timeout; a
	// feature the deadline cuts short is reported unknown.
	featureProbeTimeout = 20 * time.Second
)

type Handler struct {
	repo   Repository
	policy cedar.Authorizer
	// prober runs TestBackend's connectivity check. nil → TestBackend
	// reports unreachable with an explanatory message instead of probing.
	prober BackendProber
	// events is the optional outbox producer; nil → events are skipped.
	events EventProducer
	log    *zap.Logger
	// cel compiles and caches List filters (program cache only).
	cel *celpkg.Evaluator
}

func NewHandler(r Repository, policyEngine cedar.Authorizer) *Handler {
	if policyEngine == nil {
		panic("backendh: policy authorizer is required")
	}
	return &Handler{cel: celpkg.NewEvaluator(), repo: r, policy: policyEngine, log: zap.NewNop()}
}

// SetDeclaredBackends records the backend ids declared in storage.backends.
// Every backend the handler returns then says whether it is one of them, so a
// client offers bucket creation only where the server allows it.
func (h *Handler) SetDeclaredBackends(ids []string) {
	declared := make(map[string]bool, len(ids))
	for _, id := range ids {
		declared[id] = true
	}
	h.repo = declaringRepo{Repository: h.repo, declared: declared}
}

// declaringRepo marks each backend it reads with whether it is declared. Every
// response of the handler reads through Get, GetTx or List, so none is left
// unmarked.
type declaringRepo struct {
	Repository
	declared map[string]bool
}

func (r declaringRepo) Get(ctx context.Context, backendID string) (admindomain.StorageBackend, error) {
	b, err := r.Repository.Get(ctx, backendID)
	b.Declared = r.declared[b.BackendID]
	return b, err
}

func (r declaringRepo) GetTx(ctx context.Context, tx pgx.Tx, backendID string) (admindomain.StorageBackend, error) {
	b, err := r.Repository.GetTx(ctx, tx, backendID)
	b.Declared = r.declared[b.BackendID]
	return b, err
}

func (r declaringRepo) List(ctx context.Context, pageSize int32, afterID, filter string) ([]admindomain.StorageBackend, string, error) {
	out, next, err := r.Repository.List(ctx, pageSize, afterID, filter)
	for i := range out {
		out[i].Declared = r.declared[out[i].BackendID]
	}
	return out, next, err
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
func (h *Handler) authorize(ctx context.Context, action cedar.Action, backendID string) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		action,
		&cedar.Resource{BackendID: backendID},
		cedar.RequestContext{Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied,
			"denied by policy")
	}
	return nil
}

// ─── Create ─────────────────────────────────────────────────────────────────

func (h *Handler) CreateBackend(ctx context.Context, b admindomain.StorageBackend) (*admindomain.StorageBackend, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionManageBackend, b.BackendID); err != nil {
		return nil, err
	}
	if b.BackendID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "backend_id required")
	}
	if b.Kind == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "kind required")
	}
	// Create, not Upsert — and MapError, not a hardcoded Internal.
	//
	// This RPC used to route through the seeding path's upsert, so calling it
	// on an existing backend rewrote every field without an OCC check and
	// answered 200. UpdateBackend, right next to it, takes a REQUIRED
	// resource_version and says in the proto that it offers no bypass by
	// design; this was that bypass. The wholesale ON CONFLICT SET made it
	// worse than a missing guard: an omitted field was not left alone, it was
	// blanked, so re-running a provisioning script without `cedar_policy`
	// erased the policy.
	//
	// The hardcoded CodeInternal had to go with it, or ErrAlreadyExists would
	// have been flattened into a 500 the way a duplicate collection was.
	if err := h.repo.Create(ctx, b); err != nil {
		return nil, apiutil.MapError(err)
	}
	// The request carries the id, not a name, so the audit row would name
	// nothing.
	apiutil.StashResource(ctx, backendResourceName(b.BackendID))
	got, err := h.repo.Get(ctx, b.BackendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &got, nil
}

// ─── Read ───────────────────────────────────────────────────────────────────

func (h *Handler) GetBackend(ctx context.Context, backendID string) (*admindomain.StorageBackend, error) {
	if err := requireAnyRole(ctx, rolePlatformAdmin, roleBucketAdmin, roleTenantAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionReadBackend, backendID); err != nil {
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

// ListBackends returns one page of registered backends. filter is an optional
// CEL expression over StorageBackendSchema, applied after the fetch; the repo
// cursor is returned unchanged so paging survives a fully-filtered page.
func (h *Handler) ListBackends(ctx context.Context, pageSize int32, afterID, filter string) ([]admindomain.StorageBackend, string, error) {
	if err := requireAnyRole(ctx, rolePlatformAdmin, roleBucketAdmin, roleTenantAdmin); err != nil {
		return nil, "", err
	}
	if err := h.authorize(ctx, cedar.ActionReadBackend, ""); err != nil {
		return nil, "", err
	}
	out, next, err := h.repo.List(ctx, pageSize, afterID, filter)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
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
	out, err = celpkg.FilterPage(h.cel, celpkg.StorageBackendSchema, filter, out, backendRow)
	if err != nil {
		return nil, "", rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("filter: %w", err))
	}
	return out, next, nil
}

// backendRow projects a StorageBackend onto the variables
// StorageBackendSchema declares. Credential refs are deliberately absent —
// a filter must not become a way to probe them.
func backendRow(b admindomain.StorageBackend) map[string]any {
	return map[string]any{
		"backend_id":   b.BackendID,
		"display_name": b.DisplayName,
		"provider":     b.Provider,
		"endpoint":     b.Endpoint,
		"region":       b.Region,
		"enabled":      b.Enabled,
		"read_only":    b.ReadOnly,
		"maintenance":  b.Maintenance,
		"created_at":   b.CreatedAt,
		// Identical to ListStorageBackends' `search_like` clause; the pushdown
		// contract holds only while the two spellings agree.
		//
		// FOUR columns, not two. The console's backend search matched id,
		// display name, region AND endpoint, and a derived field covering only
		// the first two would have moved that search to the server while
		// quietly dropping half of what it used to find — the kind of
		// regression an operator reports as "it stopped finding my backend"
		// months later.
		"search": celpkg.SearchText(b.BackendID, b.DisplayName, b.Region, b.Endpoint),
	}
}

// ─── Update ─────────────────────────────────────────────────────────────────

func (h *Handler) UpdateBackend(ctx context.Context, b admindomain.StorageBackend, expectedVersion int64, mask []string) (*admindomain.StorageBackend, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionManageBackend, b.BackendID); err != nil {
		return nil, err
	}
	if err := h.repo.Update(ctx, b, expectedVersion, mask); err != nil {
		return nil, apiutil.MapError(err)
	}
	got, err := h.repo.Get(ctx, b.BackendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
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
	if err := h.authorize(ctx, cedar.ActionManageBackend, backendID); err != nil {
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
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
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
	if err := h.authorize(ctx, cedar.ActionManageBackend, backendID); err != nil {
		return nil, err
	}
	if err := h.repo.SetReadOnly(ctx, backendID, readOnly, expectedVersion); err != nil {
		return nil, apiutil.MapError(err)
	}
	got, err := h.repo.Get(ctx, backendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
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
	if err := h.authorize(ctx, cedar.ActionManageBackend, backendID); err != nil {
		return nil, err
	}
	if err := h.repo.SetMaintenance(ctx, backendID, maintenance, expectedVersion); err != nil {
		return nil, apiutil.MapError(err)
	}
	got, err := h.repo.Get(ctx, backendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
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

func (h *Handler) DeleteBackend(ctx context.Context, backendID string, expectedVersion int64) error {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return err
	}
	if err := h.authorize(ctx, cedar.ActionManageBackend, backendID); err != nil {
		return err
	}
	if err := h.repo.Delete(ctx, backendID, expectedVersion); err != nil {
		return apiutil.MapError(err)
	}
	return nil
}

// ─── TestBackend ────────────────────────────────────────────────────────────

// TestBackendOutput is the result of a connectivity probe and, when the
// backend answered, of the feature probe.
type TestBackendOutput struct {
	Reachable    bool
	ErrorMessage string
	LatencyMs    int32
	// Features has one result per catalog feature; nil when the backend was
	// unreachable, whose earlier results stay recorded.
	Features []features.Result
}

func (h *Handler) TestBackend(ctx context.Context, backendID string) (*TestBackendOutput, error) {
	if err := requireRole(ctx, rolePlatformAdmin); err != nil {
		return nil, err
	}
	if err := h.authorize(ctx, cedar.ActionReadBackend, backendID); err != nil {
		return nil, err
	}
	// Existence first — a probe against an unknown backend id is a 404, not
	// an "unreachable" result. The row also feeds the dynamic probe path.
	got, err := h.repo.Get(ctx, backendID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	}
	// Read-only — no audit row (per the BackendService contract).
	if h.prober == nil {
		return &TestBackendOutput{
			Reachable:    false,
			ErrorMessage: "connectivity probe not wired in this deployment",
		}, nil
	}
	// Bound the probe so a wedged endpoint can't hang the RPC.
	pctx, cancel := context.WithTimeout(ctx, reachabilityProbeTimeout)
	defer cancel()
	start := time.Now()
	perr := h.prober.Probe(pctx, got)
	out := &TestBackendOutput{LatencyMs: safecast.Int32From64(time.Since(start).Milliseconds())}
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
	if out.Reachable {
		out.Features = h.probeFeatures(ctx, got)
	}
	return out, nil
}

// probeFeatures runs the feature probe and records what it found (ADR-0026).
// Recording is best-effort like the health write: the caller still gets the
// results.
func (h *Handler) probeFeatures(ctx context.Context, b admindomain.StorageBackend) []features.Result {
	fctx, cancel := context.WithTimeout(ctx, featureProbeTimeout)
	defer cancel()
	results, err := h.prober.ProbeFeatures(fctx, b)
	if err != nil {
		now := time.Now().UTC()
		results = features.EveryFeature(nil)
		for i := range results {
			results[i].Message = err.Error()
			results[i].CheckedAt = now
		}
	}
	if err := h.repo.SetFeatures(ctx, b.BackendID, results); err != nil {
		logger.FromContext(ctx).Warn("failed to record backend feature probe outcome",
			zap.String("backend_id", b.BackendID), zap.Error(err))
	}
	return results
}

// ─── role helpers ──────────────────────────────────────────────────────────

func requireRole(ctx context.Context, role string) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	if !p.HasRole(role) {
		return connect.Errorf(connect.CodePermissionDenied,
			"role %q required", role)
	}
	return nil
}

func requireAnyRole(ctx context.Context, roles ...string) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	for _, r := range roles {
		if p.HasRole(r) {
			return nil
		}
	}
	return connect.NewError(connect.CodePermissionDenied, "insufficient role")
}
