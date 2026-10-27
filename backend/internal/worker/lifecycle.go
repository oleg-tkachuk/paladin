// Lifecycle worker — periodic CEL evaluator that soft-deletes objects per
// `bucket.lifecycle_rules`.
//
// Expiration only: SetLifecycleRules refuses transitions, and one stored
// before it did is skipped here. Each tick:
//
//  1. ListBucketsWithLifecycle — only buckets with a non-empty rules array.
//  2. For each bucket, walk its bound collections and stream objects.
//  3. For each object, evaluate every rule whose CEL `match` compiles;
//     first matching `Expiration.after` whose age is exceeded triggers
//     soft-delete via the state machine.
//
// Idempotent: SoftDelete is a no-op on already-DELETED rows. Multiple
// replicas can run concurrently — the SQL state guards serialize.
package worker

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	celpkg "cel.dev/cel-go/cel"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// LifecycleObjectIter exposes the read seam the worker needs: enumerate
// objects under a (tenant, collection) scope. Implementations stream
// pages — the worker iterates lazily to keep memory bounded.
type LifecycleObjectIter interface {
	IterateObjects(ctx context.Context, tenantID uuid.UUID, collection string, cb func(LifecycleObjectRow) error) error
}

// LifecycleObjectRow is the minimal projection per object the worker reads.
type LifecycleObjectRow struct {
	ObjectID    uuid.UUID
	Key         string
	State       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CommittedAt *time.Time
	SizeBytes   int64
	ContentType string
	ExternalRef string
	Tags        map[string]string
	Metadata    map[string]string
}

// BucketLifecycleSource lists buckets that carry non-empty lifecycle rules.
// Plus enumerates the (tenant, collection) pairs bound to each bucket so
// the worker can scope its scans.
type BucketLifecycleSource interface {
	ListBucketsWithLifecycle(ctx context.Context) ([]admindomain.Bucket, error)
	ListCollectionBindings(ctx context.Context, backendID, bucketName string) ([]CollectionBinding, error)
}

type CollectionBinding struct {
	TenantID   uuid.UUID
	Collection string
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
	return RunTicker(ctx, "lifecycle", w.Interval, w.tick)
}

// tick applies the rules of every bucket that has them, carrying on past one
// that fails, and returns the first failure.
func (w *LifecycleWorker) tick(ctx context.Context) error {
	buckets, err := w.Buckets.ListBucketsWithLifecycle(ctx)
	if err != nil {
		w.log().Warn("failed to list buckets", zap.Error(err))
		return err
	}
	var first error
	for _, b := range buckets {
		if ctx.Err() != nil {
			return first
		}
		keepFirst(&first, w.processBucket(ctx, b))
	}
	return first
}

// processBucket expires one bucket's objects and returns the first failure it
// logged. An object whose match fails to evaluate, or that fails to delete,
// does not stop the others.
func (w *LifecycleWorker) processBucket(ctx context.Context, b admindomain.Bucket) error {
	expirers, first := buildExpirers(b.LifecycleRules, w.Now(), w.CELEvaluator, w.log())
	if len(expirers) == 0 {
		return first // no runnable expiration rule → nothing to evaluate this tick
	}
	bindings, err := w.Buckets.ListCollectionBindings(ctx, b.BackendID, b.BucketName)
	if err != nil {
		w.log().Warn("failed to list collection bindings",
			zap.String(logKeyBackend, b.BackendID),
			zap.String(logKeyBucket, b.BucketName),
			zap.Error(err))
		keepFirst(&first, err)
		return first
	}
	for _, bind := range bindings {
		if ctx.Err() != nil {
			return first
		}
		log := w.log().With(
			zap.String(logKeyTenant, bind.TenantID.String()),
			zap.String(logKeyCollection, bind.Collection),
			zap.String(logKeyBackend, b.BackendID),
			zap.String(logKeyBucket, b.BucketName),
		)
		err := w.Objects.IterateObjects(ctx, bind.TenantID, bind.Collection, func(row LifecycleObjectRow) error {
			if row.State != string(statemachine.StateAvailable) {
				return nil
			}
			for _, e := range expirers {
				ok, err := e.matches(row)
				if err != nil {
					log.Warn("failed to evaluate match",
						zap.String(logKeyRule, e.id),
						zap.String(logKeyObjectID, row.ObjectID.String()),
						zap.String(logKeyKey, row.Key),
						zap.Error(err))
					keepFirst(&first, err)
					continue
				}
				if !ok {
					continue
				}
				if err := w.SoftDeleter.SoftDelete(ctx, row.ObjectID, 0); err != nil {
					log.Warn("failed to soft-delete object",
						zap.String(logKeyObjectID, row.ObjectID.String()),
						zap.String(logKeyKey, row.Key),
						zap.Error(err))
					// Carry on with the next object; the tick reports this one.
					keepFirst(&first, err)
					return nil
				}
				log.Info("expired object",
					zap.String(logKeyRule, e.id),
					zap.String(logKeyObjectID, row.ObjectID.String()),
					zap.String(logKeyKey, row.Key))
				return nil
			}
			return nil
		})
		if err != nil {
			log.Warn("failed to iterate objects", zap.Error(err))
			keepFirst(&first, err)
		}
	}
	return first
}

// Log keys of the per-object lines the lifecycle and replication workers
// write, so the two jobs (and their tests) spell them alike.
const (
	logKeyTenant     = "tenant"
	logKeyCollection = "collection"
	logKeyBackend    = "backend"
	logKeyBucket     = "bucket"
	logKeyDstBackend = "dst_backend"
	logKeyDstBucket  = "dst_bucket"
	logKeyObjectID   = "object_id"
	logKeyKey        = "key"
	logKeyRule       = "rule"
)

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
//
// The error is the first rule skipped, so the tick reports a bucket whose
// expiry stopped: logged alone, nothing raised the failing-worker alert.
func buildExpirers(rules []admindomain.LifecycleRule, now time.Time, eval *cel.Evaluator, logger *zap.Logger) ([]expirer, error) {
	out := make([]expirer, 0, len(rules))
	var first error
	for _, r := range rules {
		if r.Expiration == nil || r.Expiration.After <= 0 {
			continue
		}
		var prog celpkg.Program
		if r.Match != "" {
			if eval == nil {
				logger.Error("rule has match but no CEL evaluator wired (configuration error)",
					zap.String("rule", r.ID))
				keepFirst(&first, fmt.Errorf("lifecycle rule %s: %w", r.ID, errNoEvaluator))
				continue
			}
			compiled, err := eval.Compile(cel.ObjectSchema, r.Match)
			if err != nil {
				logger.Error("malformed CEL match — rule will not run; fix at write time via SetLifecycleRules",
					zap.String("rule", r.ID),
					zap.String("match", r.Match),
					zap.Error(err))
				keepFirst(&first, fmt.Errorf("lifecycle rule %s: %w", r.ID, err))
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
	return out, first
}

// errNoEvaluator is a rule with a match on a worker wired with no CEL
// evaluator to run it.
var errNoEvaluator = errors.New("match set but no CEL evaluator wired")

// lifecycleRowToCELVars projects a LifecycleObjectRow through
// cel.ObjectVars, the projection the data plane's ListObjects filter uses, so
// a rule matches exactly the objects the same expression lists. It used to be
// a copy that lacked `key`: every `key.startsWith(...)` rule failed to
// evaluate on every object and expired nothing.
func lifecycleRowToCELVars(row LifecycleObjectRow) map[string]any {
	return cel.ObjectVars(cel.ObjectRow{
		Key:         row.Key,
		State:       row.State,
		ContentType: row.ContentType,
		SizeBytes:   row.SizeBytes,
		Tags:        row.Tags,
		Metadata:    row.Metadata,
		ExternalRef: row.ExternalRef,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		CommittedAt: row.CommittedAt,
	})
}
