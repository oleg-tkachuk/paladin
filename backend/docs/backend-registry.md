# BackendRegistry — multi-backend S3 client routing (ADR-0011 Phase 2)

Status: proposed — detailed design for Phase 2 of
[ADR-0011](adr/0011-per-tenant-bucket-layout.md)
Owner: see ADR-0011
Last updated: 2026-07-02

> This is the component-level design for the "real unblock" in ADR-0011: the
> single per-process S3 client becomes one client **per backend**, keyed by
> `backend_id`. It carries no product decision — it is a prerequisite refactor
> that is safe and behavior-preserving on a single-backend deployment. Phase 1
> (dedicated bucket on the same backend) works without it; Phase 3 (cross-backend
> migration copy) depends on it.

## Goal

Let objects belonging to different tenants live on **different storage
backends** (region / account / endpoint), so per-tenant region pinning and
per-tenant IAM (ADR-0011 `dedicated` layout) become possible. Today
`internal/app/build_deps.go` builds a single `s3adapter.New(ctx, backend)` from
`config.Storage.DefaultBackend`; `resolveBucket(perCall)` swaps only the bucket
**name**, reusing one endpoint/credentials/region. Multi-backend routing is
schematically present (`object_keys.backend_id` FK) but unreachable through the
client wiring.

## The one principle

> A physical object location is **`(backendID, bucket)`**, not `bucket` alone.

Everything below follows from making that pair explicit where only `bucket`
flows today.

## Component boundaries

Three parts with single responsibilities:

| Component | Responsibility | Source of truth |
|---|---|---|
| **Resolver** (`LookupBucket` / `LookupBucketMeta`) | `(tenant, object_key)` → `(backendID, bucket, enabled, read_only, provision_state)` + the disabled/read-only policy gate | DB `storage_backends` / `buckets` |
| **BackendRegistry** | `backendID` → `*s3adapter.Client` (lazy build + cache) | `config.Storage.Backends` |
| **Router** | implements the existing narrow `wire.Storage` interfaces; dispatches each call to `registry.For(backendID)` | — |

The resolver stays the **only** chokepoint for `enabled` / `read_only`
(`ErrBackendDisabled` / `ErrBackendReadOnly`,
`internal/api/v1/object/handler.go`). The registry does **not** re-check them —
it is purely "give me a client for backend X", and a disabled backend must
still yield a client so a drain/migration can read from it.

### Current-state facts (grounded, do not re-derive)

- `bucket.Provisioner` **already** takes `backendID`
  (`CreateBucket(ctx, backendID, bucketName, region)`,
  `internal/api/v1/bucket/handler.go`) — the impl just ignores it (uses the one
  client). Provisioner routing is a signature no-op.
- `object.BucketMeta` **already** carries `BackendID` and `EventsEnabled`
  (`internal/api/v1/object/handler.go`), so the resolver already knows the
  backend and the completion-mode input.
- `object.Location{TenantID, Bucket, ObjectKey, Key}` has **no** `BackendID`;
  presign args (`PresignPutArgs`, …) carry `Bucket` but no `BackendID`.
- `config.Storage{DefaultBackend string, Backends map[string]StorageBackend}`
  is already a map; each `StorageBackend` has its own `Auth` block. DB
  `storage_backends` mirrors connection metadata from config at boot
  (`bootstrap.EnsureBackends`) and owns the operational flags.

## Component 1 — `BackendRegistry`

Lives in `internal/storage/s3adapter/registry.go` (needs `New` and `*Client`).
Same pooled-resource shape as `SQSClientPool` / `RabbitMQConnPool`.

```go
type BackendRegistry struct {
	mu      sync.Mutex
	clients map[string]*Client
	cfg     config.Storage
	// build is New, overridable in tests so the registry hands back a fake
	// client without touching AWS credential resolution.
	build func(ctx context.Context, b config.StorageBackend) (*Client, error)
}

func NewBackendRegistry(cfg config.Storage) *BackendRegistry {
	return &BackendRegistry{clients: map[string]*Client{}, cfg: cfg, build: New}
}

// For returns the cached client for backendID, building it lazily from
// cfg.Backends[backendID]. Empty id → the default backend (back-compat with
// single-client callers). Unknown id → error — never silently default a wrong
// id, which would place a tenant's bytes on the wrong store.
func (r *BackendRegistry) For(ctx context.Context, backendID string) (*Client, error) {
	id := backendID
	if id == "" {
		id = r.cfg.DefaultBackend
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.clients[id]; ok {
		return c, nil
	}
	bcfg, ok := r.cfg.Backends[id]
	if !ok {
		return nil, fmt.Errorf("unknown storage backend %q", id)
	}
	c, err := r.build(ctx, bcfg)
	if err != nil {
		return nil, fmt.Errorf("build backend %q: %w", id, err)
	}
	r.clients[id] = c
	return c, nil
}

// Warmup eagerly builds the default (a boot misconfig fails loudly, exactly
// like today's single New at boot) and optionally every configured backend.
func (r *BackendRegistry) Warmup(ctx context.Context, all bool) error {
	if _, err := r.For(ctx, r.cfg.DefaultBackend); err != nil {
		return err
	}
	if all {
		for id := range r.cfg.Backends {
			if _, err := r.For(ctx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// Invalidate drops a cached client so the next For rebuilds with freshly
// resolved credentials (rotation hook — see "Credentials").
func (r *BackendRegistry) Invalidate(backendID string) {
	r.mu.Lock()
	delete(r.clients, backendID)
	r.mu.Unlock()
}
```

Design decisions:

- **Warmup on boot.** `New` does I/O (AWS config, possibly STS AssumeRole /
  WebIdentity). `Warmup(ctx, true)` builds all configured backends at boot so
  the request path never pays first-build latency; the default is always eager
  so a misconfig is loud. Matches today's single-`New`-at-boot behavior.
- **Coarse mutex.** Concurrent first-use of the *same* backend serializes
  (prevents a double-build); it also briefly blocks other backends' first-use.
  For a handful of backends built once per lifetime this is negligible. If the
  backend count ever grows large, switch to `singleflight` keyed by id. Not
  done now.
- **No Close.** S3 clients are stateless HTTP (same as `SQSClientPool`).

## Component 2 — Router (implements the narrow interfaces)

Lives in `internal/wire` (already home to `bucketProvisionerAdapter`). Each of
the five `wire.Storage` interfaces gets a router wrapper that reads `backendID`
and dispatches:

```go
type objectStorageRouter struct{ reg *s3adapter.BackendRegistry }

func (rt objectStorageRouter) PresignGet(ctx context.Context, a object.PresignGetArgs) (string, map[string]string, time.Time, error) {
	c, err := rt.reg.For(ctx, a.BackendID)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return c.PresignGet(ctx, a)
}

func (rt objectStorageRouter) CopyObject(ctx context.Context, src, dst object.Location) error {
	return copyAcross(ctx, rt.reg, src, dst) // same-backend fast path / cross-backend stream-through
}
// Head / DeleteObject / InitiateMultipart / … — same shape: For(backendID) → delegate.
```

Handlers are **unchanged** — they still depend on `object.Storage` /
`presign.Storage` / … (fake-able in tests exactly as today). Routing is
centralized and tested in one place.

## Component 3 — thread `BackendID` where `Bucket` already flows

Minimal signature delta: `BackendID` becomes a sibling of `Bucket` everywhere
`Bucket` already appears.

```go
// object.Location — the physical location is now complete:
type Location struct {
	BackendID string    // NEW — "" → default (back-compat)
	TenantID  uuid.UUID
	Bucket    string
	ObjectKey string
	Key       string
}

// presign args structs gain BackendID next to the existing Bucket:
type PresignGetArgs struct { BackendID string; TenantID uuid.UUID; Bucket string; /* … */ }

// positional methods gain backendID before bucket:
Head(ctx, backendID, bucket string, tenantID uuid.UUID, objectKey, key string) (...)
```

Who fills `BackendID`: the resolver already knows it.

- `LookupBucketMeta` **already** returns `BucketMeta.BackendID` — no change.
- `LookupBucket` returns `(bucket_name, enabled, read_only)` today
  (`internal/store/postgres/adapters/presign.go`); add `backend_id` to the
  SELECT list (it is already in the JOIN).

Handlers pass the `backendID` they already receive from resolution into the
args/params right where they set `bucket`.

## Wiring (`build_deps.go`)

```go
// Was: one client bundled into wire.Storage.
//   s3c, _ := s3adapter.New(ctx, backend)
//   storage := wire.Storage{Object: s3c, Multipart: s3c, Presign: s3c.Presign(), …}

// Now: registry + router wrappers.
reg := s3adapter.NewBackendRegistry(cfg.Storage)
if err := reg.Warmup(ctx, true); err != nil { // fail-loud on boot
	return err
}
storage := wire.Storage{
	Object:      objectStorageRouter{reg},
	Multipart:   multipartRouter{reg},
	Presign:     presignRouter{reg},
	Stream:      streamRouter{reg},
	Provisioner: provisionerRouter{reg}, // For(backendID) — signature already carries backendID
}
```

## Credentials

- **Connection / auth params** come from `config.Storage.Backends[id].Auth`
  (each backend has its own `Auth`: static keys / AssumeRole / WebIdentity).
  This is where **per-backend IAM** lives.
- **Operational flags** (`enabled` / `read_only` / `maintenance`) come from DB
  `storage_backends`, read by the resolver — **not** the registry.
- **Rotation.** `storage_backends` carries `previous_credentials_secret_ref` /
  `previous_credentials_valid_until` (migrations 038). Baseline matches today:
  credentials resolve at boot, rotation = restart. Improvement:
  `Invalidate(backendID)` triggered by the control plane on a credentials-ref
  change; the natural driver is a `LISTEN` on a `storage_backends` change
  channel, mirroring the Cedar-Watch reconnect (ADR / PR #118). Out of Phase 2
  core scope.

## Cross-backend `CopyObject` (the migration enabler)

With two clients available, `CopyObject(src, dst Location)` can span backends:

```go
func copyAcross(ctx context.Context, reg *s3adapter.BackendRegistry, src, dst object.Location) error {
	if src.BackendID == dst.BackendID { // incl. "" == ""
		c, err := reg.For(ctx, dst.BackendID)
		if err != nil {
			return err
		}
		return c.CopyObject(ctx, src, dst) // server-side copy on one client (today's fast path)
	}
	// Different backends (different endpoint / account) — a server-side copy is
	// impossible: stream-through GET(src) → PUT(dst), multipart for large. (Phase 3)
	return streamThrough(ctx, reg, src, dst)
}
```

Phase 2 core implements the **branch + same-backend fast path**;
`streamThrough` is Phase 3 (the shared→dedicated migration job). The registry
is what makes it expressible — two clients, `For(src)` and `For(dst)`.

## Incidental simplification — `CompletionMode`

`CompletionMode(objectKey)` derives from the single client's `events_enabled`
today. In a multi-backend world it depends on **which** backend the objectKey
is on — but `BucketMeta.EventsEnabled` already carries that from resolution. So
compute completion mode from `BucketMeta`, and **drop `CompletionMode` from the
storage interface** (one fewer lookup, and no backend dependency leaking into
the interface).

## Edge cases / failure modes

- **Unknown `backendID`** → `For` errors explicitly (never silently defaults —
  a wrong default would cross a tenant onto another store). Handlers map it to
  `CodeInternal` / `FailedPrecondition`.
- **Per-backend presign public endpoint** — the presign URL embeds
  `public_endpoint`; each client is built from its own
  `config.StorageBackend.PublicEndpoint`, and `New`'s dual-client logic already
  handles it.
- **Disabled backend during drain** — the resolver returns `ErrBackendReadOnly`;
  the registry still builds a client (needed to read data off during migration).
  The responsibility split holds.
- **Empty `BackendID` from un-migrated call sites** → default backend. This is
  the back-compat bridge that lets the change land incrementally.

## Testing plan

- **Registry (unit):** `For` builds once per id (build counter), unknown→error,
  `""`→default, concurrent `For` of the same id builds once (`-race`), `Warmup`
  eagerly builds the default, `Invalidate` forces a rebuild.
- **Router (unit):** a fake registry keyed by backendID; two backends yield
  distinct clients; each method dispatches to the right one.
- **Integration:** two MinIO testcontainers as backends A/B; an object on A and
  one on B → presign each hits the correct endpoint. Cross-backend
  `CopyObject` stream-through (with Phase 3).

## Incremental landing (a single-backend deploy never breaks)

Each step is its own PR, each green on one backend:

1. Add `BackendRegistry` + `Warmup` **alongside** the existing wiring (registry
   builds the default → identical behavior).
2. Thread `BackendID` into `Location`, the presign args, and the resolver
   (default `""` = current behavior). Every existing call site passes `""` →
   default.
3. Switch `wire.Storage` to the router wrappers. One backend in config →
   registry returns the same client → zero behavioral change.
4. Only then can Phase 1 (`dedicated` bucket) place objects on a non-default
   backend.

Multi-backend behavior activates only when a second backend appears in config.

## Scope

**In Phase 2:** `BackendRegistry` + `Warmup` / `Invalidate`, the router
wrappers, `BackendID` threading, the resolver returning `backend_id`, the
wiring swap, the `CompletionMode` simplification, and unit + one/two-backend
integration tests.

**Out (Phase 3+):** `streamThrough` cross-backend copy, `LISTEN`-driven
credential invalidation, and the per-tenant backend-selection policy at
`CreateObjectKey`.

**Follow-ups surfaced while landing Phase 2 — now DONE:**

- **Maintenance-worker routing (done).** The bucket reconciler, reconciler
  probe (`HeadProber`), lifecycle hard-deleter (`StorageDeleter`), and
  multipart reaper (`MultipartAborter`) now carry a backend id and route
  through the registry. Their feeding queries materialize the backend
  (`LookupObjectByID` / `ListHardDeletable` already selected it;
  `ListStaleMultipartUploads` now does, preferring the session anchor).
- **Multipart session backend (done).** `multipart_uploads` gains
  `backend_id` / `bucket_name` (migration 053); `InitiateSession` anchors the
  physical location resolved at initiate time and `GetSession` reads it back,
  so complete / abort / presign-part and the reaper target where the parts
  actually live even after a `BindObjectKeyToBucket` rebind. Legacy in-flight
  rows fall back to re-resolution.
