// Lifecycle worker — periodic CEL evaluator that soft-deletes (and
// eventually transitions) objects per `bucket.lifecycle_rules`.
//
// v2 scope: expiration only. Transition rules are advisory until the S3
// lifecycle-config integration lands (slice 12+). Each tick:
//
//  1. ListBucketsWithLifecycle — only buckets with a non-empty rules array.
//  2. For each bucket, walk its bound object_keys and stream objects.
//  3. For each object, evaluate every rule whose CEL `match` compiles;
//     first matching `Expiration.after` whose age is exceeded triggers
//     soft-delete via the state machine.
//
// Idempotent: SoftDelete is a no-op on already-DELETED rows. Multiple
// replicas can run concurrently — the SQL state guards serialize.
package worker

import (
	"context"
	"time"

	celpkg "github.com/google/cel-go/cel"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

// LifecycleObjectIter exposes the read seam the worker needs: enumerate
// objects under a (tenant, object_key) scope. Implementations stream
// pages — the worker iterates lazily to keep memory bounded.
type LifecycleObjectIter interface {
	IterateObjects(ctx context.Context, tenantID uuid.UUID, objectKey string, cb func(LifecycleObjectRow) error) error
}

// LifecycleObjectRow is the minimal projection per object the worker reads.
type LifecycleObjectRow struct {
	ObjectID    uuid.UUID
	State       string
	CreatedAt   time.Time
	CommittedAt *time.Time
	SizeBytes   int64
	ContentType string
	Tags        map[string]string
	Metadata    map[string]string
}

// BucketLifecycleSource lists buckets that carry non-empty lifecycle rules.
// Plus enumerates the (tenant, object_key) pairs bound to each bucket so
// the worker can scope its scans.
type BucketLifecycleSource interface {
	ListBucketsWithLifecycle(ctx context.Context) ([]admindomain.Bucket, error)
	ListObjectKeyBindings(ctx context.Context, backendID, bucketName string) ([]ObjectKeyBinding, error)
}

type ObjectKeyBinding struct {
	TenantID  uuid.UUID
	ObjectKey string
}

// LifecycleWorker is the worker fan entry. SoftDeleter is the state-machine
// seam (just SoftDelete; the worker never touches HardDelete).
//
// CELEvaluator is optional. When set, rules with a non-empty `match`
// expression are evaluated against each Object before applying the
// expiration cutoff — letting policy authors write things like
// `tags["archive"] == "true" && size_bytes > 1048576`. When unset, rules
// with `match` are skipped (fail-closed).
type LifecycleWorker struct {
	Buckets      BucketLifecycleSource
	Objects      LifecycleObjectIter
	SoftDeleter  SoftDeleter
	CELEvaluator *cel.Evaluator
	Interval     time.Duration
	Logger       *zap.Logger

	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

// SoftDeleter is the state-machine seam. *statemachine.Transitioner
// satisfies this with SoftDelete(ctx, objectID, expectedVersion=0).
type SoftDeleter interface {
	SoftDelete(ctx context.Context, objectID uuid.UUID, expectedVersion int64) error
}

func (w *LifecycleWorker) Run(ctx context.Context) error {
	if w.Interval <= 0 {
		w.Interval = 30 * time.Minute
	}
	if w.Now == nil {
		w.Now = time.Now
	}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *LifecycleWorker) tick(ctx context.Context) {
	buckets, err := w.Buckets.ListBucketsWithLifecycle(ctx)
	if err != nil {
		w.log().Warn("failed to list buckets", zap.Error(err))
		return
	}
	for _, b := range buckets {
		if err := ctx.Err(); err != nil {
			return
		}
		w.processBucket(ctx, b)
	}
}

func (w *LifecycleWorker) processBucket(ctx context.Context, b admindomain.Bucket) {
	expirers := buildExpirers(b.LifecycleRules, w.Now(), w.CELEvaluator, w.log())
	if len(expirers) == 0 {
		return // no expiration rules → nothing to evaluate this tick
	}
	bindings, err := w.Buckets.ListObjectKeyBindings(ctx, b.BackendID, b.BucketName)
	if err != nil {
		w.log().Warn("failed to list object_key bindings",
			zap.String("backend", b.BackendID),
			zap.String("bucket", b.BucketName),
			zap.Error(err))
		return
	}
	for _, bind := range bindings {
		if err := ctx.Err(); err != nil {
			return
		}
		err := w.Objects.IterateObjects(ctx, bind.TenantID, bind.ObjectKey, func(row LifecycleObjectRow) error {
			if row.State != string(statemachine.StateAvailable) {
				return nil
			}
			for _, e := range expirers {
				ok, err := e.matches(row)
				if err != nil {
					w.log().Warn("failed to evaluate match",
						zap.String("rule", e.id),
						zap.String("object_id", row.ObjectID.String()),
						zap.Error(err))
					continue
				}
				if !ok {
					continue
				}
				if err := w.SoftDeleter.SoftDelete(ctx, row.ObjectID, 0); err != nil {
					w.log().Warn("failed to soft-delete object",
						zap.String("object_id", row.ObjectID.String()),
						zap.Error(err))
					return nil
				}
				w.log().Info("expired object",
					zap.String("rule", e.id),
					zap.String("object_id", row.ObjectID.String()))
				return nil
			}
			return nil
		})
		if err != nil {
			w.log().Warn("failed to iterate objects",
				zap.String("tenant", bind.TenantID.String()),
				zap.String("object_key", bind.ObjectKey),
				zap.Error(err))
		}
	}
}

func (w *LifecycleWorker) log() *zap.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return zap.NewNop()
}

// expirer is a compiled lifecycle rule projection. CEL `match` programs are
// pre-compiled here so the per-row hot path stays a single Eval call.
type expirer struct {
	id      string
	enabled bool
	cutoff  time.Time
	// match, when non-nil, gates expiration on a CEL evaluation against
	// the Object schema. nil → always-match (gate is purely the cutoff).
	match celpkg.Program
}

// matches reports whether the row should be expired by this rule. Two
// gates run in series: (1) age-based cutoff, (2) optional CEL match.
func (e expirer) matches(row LifecycleObjectRow) (bool, error) {
	if !e.enabled {
		return false, nil
	}
	created := row.CreatedAt
	if row.CommittedAt != nil {
		created = *row.CommittedAt
	}
	if !created.Before(e.cutoff) {
		return false, nil
	}
	if e.match == nil {
		return true, nil
	}
	return cel.Match(e.match, lifecycleRowToCELVars(row))
}

// buildExpirers compiles each rule's CEL `match` expression upfront.
//
// Compile failure is FATAL for that rule — the worker emits an Error log
// and skips the rule for this tick. Previously the path silently dropped
// the rule on a Warn, which meant a misconfig in production turned
// lifecycle off for the bucket without anyone noticing. Write-time
// validation in bucketh.SetLifecycleRules now rejects bad CEL synchronously,
// so reaching this branch implies either (a) data written before the
// validator landed, or (b) a schema migration introduced an incompatibility
// — both warrant Error-level visibility.
//
// Rules without a match always evaluate the time window only — preserving
// the slice-11 behaviour.
func buildExpirers(rules []admindomain.LifecycleRule, now time.Time, eval *cel.Evaluator, logger *zap.Logger) []expirer {
	out := make([]expirer, 0, len(rules))
	for _, r := range rules {
		if r.Expiration == nil || r.Expiration.After <= 0 {
			continue
		}
		var prog celpkg.Program
		if r.Match != "" {
			if eval == nil {
				logger.Error("rule has match but no CEL evaluator wired (configuration error)",
					zap.String("rule", r.ID))
				continue
			}
			compiled, err := eval.Compile(cel.ObjectSchema, r.Match)
			if err != nil {
				logger.Error("malformed CEL match — rule will not run; fix at write time via SetLifecycleRules",
					zap.String("rule", r.ID),
					zap.String("match", r.Match),
					zap.Error(err))
				continue
			}
			prog = compiled
		}
		out = append(out, expirer{
			id:      r.ID,
			enabled: r.Enabled,
			cutoff:  now.Add(-r.Expiration.After),
			match:   prog,
		})
	}
	return out
}

// lifecycleRowToCELVars projects a LifecycleObjectRow into the variable
// map shape expected by cel.ObjectSchema. Mirrors the projection used by
// the data-plane ListObjects filter so policies behave identically.
func lifecycleRowToCELVars(row LifecycleObjectRow) map[string]any {
	vars := map[string]any{
		"state":        row.State,
		"size_bytes":   row.SizeBytes,
		"content_type": row.ContentType,
		"tags":         coalesceMap(row.Tags),
		"metadata":     coalesceMap(row.Metadata),
	}
	if !row.CreatedAt.IsZero() {
		vars["created_at"] = row.CreatedAt
	}
	if row.CommittedAt != nil && !row.CommittedAt.IsZero() {
		vars["committed_at"] = *row.CommittedAt
	}
	return vars
}

// coalesceMap returns m unchanged when non-nil; an empty map otherwise.
// CEL's `tags["foo"]` semantics differ between nil-map and empty-map —
// always present an empty map to keep policies portable.
func coalesceMap(m map[string]string) map[string]string {
	if m != nil {
		return m
	}
	return map[string]string{}
}
