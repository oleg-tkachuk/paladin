// Idempotency middleware for Connect RPCs.
//
// Two responsibilities, both at the Connect interceptor layer:
//
//  1. **Enforcement.** When RequireOnCreate=true, every RPC whose
//     method name's trailing segment starts with `Create` OR
//     `Issue` MUST carry an `Idempotency-Key` header. Missing →
//     CodeInvalidArgument before the handler runs. The Create-vs-
//     Issue split is policy: Create matches AIP-canonical resource
//     creations (CreateTenant, CreateBucket, …); Issue matches the
//     token/credential variant (CapabilityService.Issue and
//     similar) — semantically identical double-submit hazard,
//     different domain noun.
//
//  2. **Memoize / replay.** When the header IS present, the first
//     request executes the handler normally, the serialized response
//     is cached per (tenant, method, key) with a TTL, and any
//     subsequent request with the same key returns the cached
//     response without re-executing the handler. This collapses
//     retries (network flake, client double-click) onto a single
//     resource.
//
// Replay decodes the cached bytes into the method's response type, read from
// the method's schema, so any memoised method replays from its first call on
// any replica: there is no registry to warm and nothing per method to wire.
//
// Errors are NOT cached — a transient failure must be retryable.
// Streaming RPCs are out of scope (Create* is always unary).

package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"time"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcmeta"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
)

const idempotencyHeader = paladin.HeaderIdempotencyKey

// IdempotencyRecord is one memoised call: the response it produced and the
// fingerprint of the request that produced it.
type IdempotencyRecord struct {
	Response []byte
	// RequestHash is requestFingerprint of the request. Empty for a row
	// written before fingerprints existed; such a row matches any request.
	RequestHash []byte
}

// IdempotencyStore persists and returns cached responses.
type IdempotencyStore interface {
	Get(ctx context.Context, tenantID uuid.UUID, method, key string) (rec IdempotencyRecord, found bool, err error)
	Put(ctx context.Context, tenantID uuid.UUID, method, key string, rec IdempotencyRecord, expiresAt time.Time) error
}

// errKeyReused answers a key already used, within its TTL, for a different
// request to the same method. Replaying would hand back a response to a
// question the caller did not ask — another object's download URL, another
// part's presigned PUT — and nothing downstream could tell.
var errKeyReused = connect.NewError(connect.CodeInvalidArgument,
	"idempotency: this Idempotency-Key was already used for a different request to this method; "+
		"use a new key for a new request")

// requestFingerprint identifies a request's content: SHA-256 over its
// deterministic protobuf encoding, so a JSON and a binary client sending the
// same message produce the same fingerprint.
func requestFingerprint(msg proto.Message) ([]byte, error) {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return sum[:], nil
}

// sameRequest reports whether a stored fingerprint admits this request. A row
// with no fingerprint predates them and is trusted, as it always was.
func sameRequest(stored, current []byte) bool {
	return len(stored) == 0 || bytes.Equal(stored, current)
}

// IdempotencyConfig tunes the interceptor.
type IdempotencyConfig struct {
	// TTL is how long stored responses survive in the cache. Tune to
	// the longest plausible client retry window; default 24h covers
	// every browser refresh + most worker requeues.
	TTL time.Duration
	// SkipMethods are full Connect procedures (e.g.
	// `/paladin.admin.v1.TenantService/CreateTenant`) that bypass BOTH
	// enforcement AND memoize. Use for internal seeding RPCs whose
	// upsert semantics already give idempotency.
	SkipMethods map[string]bool
	// RequireOnCreate rejects mutation-shaped RPCs that omit the
	// header. Matches method names with prefix `Create` (the AIP
	// canonical) AND `Issue` (the token/credential variant —
	// CapabilityService.Issue is the motivating case). Field name
	// is historical; see isMutationMethod() for the exact policy.
	// Default false to keep the constructor drop-in safe; flip when
	// clients are guaranteed to inject (admin UI BFF does this via
	// a transport interceptor; programmatic clients must opt in).
	RequireOnCreate bool
}

// NewIdempotencyInterceptor returns a Connect interceptor that
// enforces + memoizes the Idempotency-Key contract.
// NewIdempotencyInterceptor builds the interceptor. Credential minting is
// skipped unconditionally, whatever the caller passes.
//
// It used to be the caller's job — one `SkipMethods:
// middleware.CredentialMintingProcedures` line in the API listener's config
// literal, and nothing anywhere would notice its removal.
// TestCredentialMintersAreSkipped asserts what is IN the map, not that anyone
// passed it, and the admin listener already constructs this interceptor
// without it (correctly: it does not serve AuthService).
//
// That gap was unreachable while no client sent a key on an auth RPC. It
// stopped being unreachable when the clients moved onto idempotency_level:
// Login and RefreshToken declare IDEMPOTENCY_UNKNOWN, so every client now
// stamps them, and a dropped wiring line would make a rotated refresh token
// replayable. A safety property that depends on being remembered at each call
// site is not a safety property, so it no longer does.
func NewIdempotencyInterceptor(store IdempotencyStore, cfg IdempotencyConfig) connect.ServerInterceptor {
	if cfg.TTL == 0 {
		cfg.TTL = 24 * time.Hour
	}
	skip := make(map[string]bool, len(cfg.SkipMethods)+len(CredentialMintingProcedures))
	for p := range cfg.SkipMethods {
		skip[p] = true
	}
	for p := range CredentialMintingProcedures {
		skip[p] = true
	}
	cfg.SkipMethods = skip
	i := &idempotencyInterceptor{store: store, cfg: cfg}
	// Streaming RPCs don't participate in idempotency.
	return unary.Interceptor(i.wrap, nil)
}

type idempotencyInterceptor struct {
	store IdempotencyStore
	cfg   IdempotencyConfig
}

// idempotencyKeyCarrier is satisfied by any request message declaring an
// `idempotency_key` field — protoc-gen-go generates the getter.
type idempotencyKeyCarrier interface {
	GetIdempotencyKey() string
}

// idempotencyKey resolves the key from the header, falling back to the request
// body's idempotency_key field.
//
// The body field is why this exists. Two request messages declare one, and
// until now nothing read either: the interceptor looked only at the header, so
// a client that set the field — reasonably, since the schema offers it —
// got no idempotency at all and no indication of that. A retried
// InitiateMultipartUpload opened a second session.
//
// When both are present they must agree. Silently preferring one would make
// the effective key depend on a precedence rule nothing documents, and the
// caller disagreeing with itself is a bug worth surfacing.
func idempotencyKey(headers *connect.Header, req proto.Message) (string, error) {
	header := headers.Get(idempotencyHeader)
	var body string
	if c, ok := req.(idempotencyKeyCarrier); ok {
		body = c.GetIdempotencyKey()
	}
	switch {
	case header != "" && body != "" && header != body:
		return "", connect.NewError(connect.CodeInvalidArgument,
			"idempotency: Idempotency-Key header and idempotency_key field disagree")
	case header != "":
		return header, nil
	default:
		return body, nil
	}
}

func (i *idempotencyInterceptor) wrap(next unary.Func) unary.Func {
	return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
		method := spec.Procedure
		if i.cfg.SkipMethods[method] {
			return next(ctx, spec, req)
		}
		// A declared read is never memoized, whatever header arrives.
		//
		// The response to a read is a snapshot, and replaying an old one
		// answers a question the caller did not ask. Nothing enforced that
		// before the descriptors carried the claim: a client sending a key on
		// ListBuckets got its first page back for the rest of the TTL.
		//
		// Read from `option idempotency_level = NO_SIDE_EFFECTS`, so it covers
		// exactly the RPCs whose handlers were checked — see
		// internal/api/idempotency_contract_test.go, which fails the build if
		// one of them starts writing.
		if rpcmeta.IsDeclaredRead(method) {
			return next(ctx, spec, req)
		}
		key, err := idempotencyKey(unary.Info(ctx).RequestHeader(), req)
		if err != nil {
			return nil, err
		}
		if key == "" {
			if i.cfg.RequireOnCreate && isMutationMethod(method) {
				return nil, connect.NewError(connect.CodeInvalidArgument,
					"idempotency: missing Idempotency-Key header")
			}
			return next(ctx, spec, req)
		}
		// The tenant the request acts on: a platform admin's data-plane call
		// names another one (ActOnNamedTenant), and keying it on the admin's
		// own let one key collide across every tenant the admin worked in.
		tenantID, err := auth.EffectiveTenant(ctx)
		if err != nil {
			// Pre-auth or auth-failed: skip memoize, defer to next()
			// which will surface the auth error to the caller.
			return next(ctx, spec, req)
		}

		// Replay path: the cached response, decoded into the method's
		// response type, answers without invoking the handler.
		//
		// Side-effecting handlers (CreateTenant) are still expected to guard
		// their natural key with ON CONFLICT: a cached row that cannot be
		// decoded falls through to the handler, and so do two first calls
		// racing past an empty cache.
		fingerprint, ferr := requestFingerprint(req)
		if ferr != nil {
			// Cannot tell one request from another, so cannot memoise
			// safely; run the handler unmemoised.
			return next(ctx, spec, req)
		}
		rec, found, err := i.store.Get(ctx, tenantID, method, key)
		if err == nil && found && !sameRequest(rec.RequestHash, fingerprint) {
			metrics.RecordIdempotencyLookup(ctx, method, lookupKeyReused)
			return nil, errKeyReused
		}
		if err == nil && found {
			if replay, rerr := replayResponse(spec, rec.Response); rerr == nil {
				if err := refuseCredentialReplay(replay); err != nil {
					metrics.RecordIdempotencyLookup(ctx, method, lookupRefused)
					return nil, err
				}
				metrics.RecordIdempotencyLookup(ctx, method, "replayed")
				return replay, nil
			}
			// Cached, and not replayable — proto drift or a corrupt row: the
			// handler is about to run for a key that already succeeded once.
			// That is the only outcome here that can produce a duplicate side
			// effect, so it is counted.
			metrics.RecordIdempotencyLookup(ctx, method, "unreplayable")
		} else {
			metrics.RecordIdempotencyLookup(ctx, method, "miss")
		}

		resp, err := next(ctx, spec, req)
		if err != nil {
			// Don't memoize failures — the caller can retry and succeed.
			return resp, err
		}

		// Cache the response. The store Put is ON CONFLICT DO NOTHING
		// so concurrent first-time writers race harmlessly.
		body, merr := marshalResponse(resp)
		if merr != nil {
			// Marshal failure means we can't memoize this method.
			// The original response still flows back to the caller;
			// just log-and-skip (no logger plumbed here, so skip).
			return resp, nil
		}
		// Put errors are non-fatal — losing the cache write means
		// the next retry will re-run the handler. The caller still
		// gets their fresh response.
		_ = i.store.Put(ctx, tenantID, method, key,
			IdempotencyRecord{Response: body, RequestHash: fingerprint}, time.Now().Add(i.cfg.TTL))
		return resp, nil
	}
}

// marshalResponse serialises the response for the cache with every
// debug_redact field cleared: a minted API or capability token is returned
// once, to the first caller, and never written to idempotency_keys.
func marshalResponse(msg proto.Message) ([]byte, error) {
	return proto.Marshal(redacted(msg))
}

// replayResponse decodes a cached response into the method's response type.
func replayResponse(spec connect.Spec, body []byte) (proto.Message, error) {
	msg, err := unary.NewResponse(spec)
	if err != nil {
		return nil, err
	}
	if err := proto.Unmarshal(body, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// lookupRefused is the idempotency lookup outcome for a repeated key whose
// response carried a credential and is not replayed.
const lookupRefused = "refused"

// lookupKeyReused is the outcome for a key repeated with a different request.
const lookupKeyReused = "key_reused"

// errCredentialNotReplayed answers a repeated key whose response type carries
// a credential. The cached copy has it cleared, and a success without it
// would read as a token that is empty rather than one that was not kept.
var errCredentialNotReplayed = connect.NewError(connect.CodeAlreadyExists,
	"this Idempotency-Key already completed; its response carried a credential, "+
		"which is returned once and not stored — use a new key to issue another")

// refuseCredentialReplay returns errCredentialNotReplayed when replay is of a
// type that can carry a credential, and nil otherwise.
func refuseCredentialReplay(replay proto.Message) error {
	if carriesCredentials(replay.ProtoReflect().Descriptor()) {
		return errCredentialNotReplayed
	}
	return nil
}

// isMutationMethod reports whether the Connect procedure name
// corresponds to a resource-creating RPC that benefits from the
// Idempotency-Key contract. Scoped to the trailing method
// segment so a hypothetical service whose name contains
// "Create" or "Issue" (e.g. `CreateOrderHistoryService/ListOrders`
// or `IssueTrackerService/ListIssues`) is NOT caught — only
// METHODS whose name starts with one of the recognised prefixes.
//
// Connect procedure strings are always `/<package>.<Service>/
// <Method>` per the spec.
//
// Recognised prefixes:
//   - "Create" — the canonical AIP-style verb. CreateTenant,
//     CreateBucket, CreateCollection, CreateUser, …
//   - "Issue"  — the token/credential variant. CapabilityService.
//     Issue, etc. Logically these are creates with a different
//     domain noun, and double-submit hazards are identical.
//
// Adding a new prefix here is a deliberate policy expansion —
// document the reasoning inline so the next reader doesn't add
// "Submit" or "Post" without thinking through the semantic.
func isMutationMethod(procedure string) bool {
	if procedure == "" {
		return false
	}
	idx := strings.LastIndex(procedure, "/")
	if idx < 0 || idx == len(procedure)-1 {
		return false
	}
	method := procedure[idx+1:]
	return strings.HasPrefix(method, "Create") ||
		strings.HasPrefix(method, "Issue")
}
