// Storage-backend bootstrap step.
//
// The yaml `storage.backends.*` map drives the in-process S3 client
// (cmd/server/root.go's `s3adapter.New(ctx, backend)`). The DB table
// `storage_backends` is the FK target for `buckets` rows, and historically
// it was only ever populated through the BackendService.CreateBackend RPC.
// That meant a fresh cluster's yaml could declare `primary` without any
// row in `storage_backends`, and the next `INSERT INTO buckets` would
// trip `buckets_backend_id_fkey`.
//
// EnsureBackends mirrors yaml → DB at startup, idempotently. It mirrors
// the EnsureAdmin flow: read config, upsert into the canonical table,
// emit an audit row so the action is grep-able. Credentials are NEVER
// written to the DB — they live in env vars / Secrets, where the S3
// adapter picks them up at construction time. Only operational metadata
// (kind, endpoint, region, sse type, events target) is mirrored.

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"go.uber.org/zap"

	v1admindomain "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// BackendStore is the subset of admindomain.BackendRepository we actually
// need from the bootstrap path. Concrete *adapters.BackendRepoV2 satisfies
// it; tests can inject a fake.
type BackendStore interface {
	Upsert(ctx context.Context, b v1admindomain.StorageBackend) error
	Get(ctx context.Context, backendID string) (v1admindomain.StorageBackend, error)
}

// BackendDeps groups what EnsureBackends needs from the surrounding
// process. Audit + Logger are shared with EnsureAdmin.
type BackendDeps struct {
	Backends BackendStore
	Audit    AuditWriter
	Logger   *zap.Logger
}

const (
	auditActionBackendUpsert = "iam.bootstrap_backend.upsert"
	auditActionBackendNoop   = "iam.bootstrap_backend.noop"
)

// EnsureBackends iterates cfg.Backends and upserts each one into the DB.
// Returns a non-nil error only on configuration validation problems or
// repository errors — yaml-only fields without a DB analog are silently
// dropped.
//
// Empty cfg.Backends is treated as a no-op (operator opted out / using
// the BackendService RPC exclusively).
func EnsureBackends(ctx context.Context, cfg config.Storage, deps BackendDeps) error {
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	if deps.Backends == nil {
		return errors.New("bootstrap.backends: BackendStore is nil")
	}
	log := deps.Logger.With(zap.String("step", "ensure_backends"))

	if len(cfg.Backends) == 0 {
		log.Info("no storage.backends declared in yaml, nothing to mirror")
		return nil
	}

	// Sort for stable log ordering — makes diffing two boots easier.
	ids := make([]string, 0, len(cfg.Backends))
	for id := range cfg.Backends {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		yamlBackend := cfg.Backends[id]
		desired := domainBackendFromYAML(id, yamlBackend)

		existing, err := deps.Backends.Get(ctx, id)
		switch {
		case errors.Is(err, v1admindomain.ErrNotFound):
			// Brand-new — proceed with upsert below.
		case err != nil:
			return fmt.Errorf("bootstrap.backends: get %q: %w", id, err)
		default:
			// Already converged? Skip the write so we don't bump
			// resource_version on every restart.
			if equalForBootstrap(existing, desired) {
				log.Debug("backend already converged, skipping",
					zap.String("backend_id", id))
				writeBackendAudit(ctx, deps, auditActionBackendNoop, id)
				continue
			}
		}

		if err := deps.Backends.Upsert(ctx, desired); err != nil {
			return fmt.Errorf("bootstrap.backends: upsert %q: %w", id, err)
		}
		log.Info("upserted backend from yaml",
			zap.String("backend_id", id),
			zap.String("kind", desired.Kind),
			zap.String("endpoint", desired.Endpoint))
		writeBackendAudit(ctx, deps, auditActionBackendUpsert, id)
	}
	return nil
}

// domainBackendFromYAML translates the yaml-config representation of a
// backend into the admindomain shape used by the repo. Auth/credentials
// are intentionally dropped — the S3 adapter reads them straight from
// the yaml, never via the DB.
//
// NOTE (feature 002): `Enabled` is intentionally NOT set here. The
// enable/disable state is operator-managed via SetBackendEnabled, never
// mirrored from static config — config has no `enabled` key. Leaving it
// out (here + in equalForBootstrap + in the UpsertStorageBackendV2
// ON CONFLICT set) is what guarantees a disabled backend stays disabled
// across restarts (FR-012 / SC-005).
func domainBackendFromYAML(id string, b config.StorageBackend) v1admindomain.StorageBackend {
	return v1admindomain.StorageBackend{
		BackendID:      id,
		Kind:           b.Kind,
		Provider:       b.Provider,
		Endpoint:       b.Endpoint,
		PublicEndpoint: b.PublicEndpoint,
		Region:         b.Region,
		ForcePathStyle: b.ForcePathStyle,
		SSE: v1admindomain.ServerSideEncryption{
			Type:  sseTypeYAMLToDomain(b.SSE.Type),
			KeyID: b.SSE.KeyID,
		},
		Events: v1admindomain.EventSourceConfig{
			Enabled:      b.Events.Enabled,
			Target:       eventsTargetYAMLToDomain(b.Events.Target),
			QueueURL:     b.Events.QueueURL,
			PollInterval: b.Events.PollInterval,
		},
	}
}

// equalForBootstrap compares the yaml-derived fields only — RV /
// timestamps / cedar policy / credentials are not part of what bootstrap
// owns, so they don't count toward "converged".
func equalForBootstrap(a, b v1admindomain.StorageBackend) bool {
	return a.Kind == b.Kind &&
		a.Provider == b.Provider &&
		a.Endpoint == b.Endpoint &&
		a.PublicEndpoint == b.PublicEndpoint &&
		a.Region == b.Region &&
		a.ForcePathStyle == b.ForcePathStyle &&
		a.SSE == b.SSE &&
		a.Events == b.Events
}

// sseTypeYAMLToDomain folds the yaml vocabulary ("aws:kms") into the
// domain's ("KMS"). Empty in, empty out.
func sseTypeYAMLToDomain(yaml string) string {
	switch yaml {
	case "aws:kms":
		return v1admindomain.SSETypeKMS
	case "AES256":
		return "AES256"
	}
	return ""
}

// eventsTargetYAMLToDomain folds the yaml vocabulary ("none") into the
// domain's (""). The actual targets ("sqs", "redis") pass through
// unchanged.
func eventsTargetYAMLToDomain(yaml string) string {
	switch yaml {
	case "sqs":
		return "sqs"
	case "redis":
		return "redis"
	}
	return ""
}

func writeBackendAudit(ctx context.Context, deps BackendDeps, action, backendID string) {
	if deps.Audit == nil {
		return
	}
	entry := v1admindomain.AuditEntry{
		EntryID:       uuid.Must(uuid.NewV7()),
		ActorSubject:  auditActorSubject,
		ActorAudience: auditAudience,
		Action:        action,
		ResourceName:  fmt.Sprintf("storageBackends/%s", backendID),
	}
	if err := deps.Audit.Insert(ctx, entry); err != nil {
		deps.Logger.Warn("bootstrap.backends: audit insert failed",
			zap.String("backend_id", backendID), zap.Error(err))
	}
}
