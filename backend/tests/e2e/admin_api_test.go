//go:build e2e

// End-to-end coverage for the admin Connect API. Walks every CRUD
// surface that Phase 0 (tenant identity hardening) + the
// tenant_default_bindings work introduced, plus a representative
// slice of the rest of the admin plane so a regression on the
// connectshim layer surfaces here before the UI sees it.
//
// Run against a deployed cluster:
//
//	PALADIN_ADMIN_URL=http://localhost:8090 \
//	PALADIN_JWT_SECRET=dev-secret-change-me-32-bytes-min \
//	PALADIN_JWT_ISSUER=paladin-dev \
//	go test -tags=e2e ./tests/e2e/...
//
// Or via port-forward:
//
//	kubectl port-forward -n paladin svc/paladin-admin 8090:8090
//	PALADIN_ADMIN_URL=http://localhost:8090 go test -tags=e2e ./tests/e2e/...
//
// When PALADIN_ADMIN_URL is unset the test SKIPs — keeps `go test ./...`
// green in CI without a cluster.
//
// Each test creates its own scoped resources and tears them down on
// exit. Resource names embed a per-run nonce so concurrent runs (e.g.
// CI shards) don't collide on the UNIQUE constraints.
package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
)

// envOrSkip reads a required env var, t.Skip'ing when absent so the
// suite is opt-in. Returns the value with whitespace trimmed.
func envOrSkip(t *testing.T, name string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		t.Skipf("%s not set — set it to enable E2E", name)
	}
	return v
}

// fixture bundles the shared state every subtest reaches for: the
// Connect clients, the JWT, and a short random nonce that scopes
// resource names to this run.
type fixture struct {
	ctx    context.Context
	cancel context.CancelFunc

	adminURL string
	nonce    string

	// JWT-minting parameters kept so the fixture can re-mint a JWT
	// scoped to a different tenant_id after we create one. The
	// ObjectKey handler uses the caller's tenant_id as the resource
	// owner (apiutil.CallerContext) — so writing into a newly-created
	// tenant needs a JWT minted against THAT tenant's UUID, not the
	// fixture's bootstrap tenant.
	jwtSecret string
	jwtIssuer string

	tenants    paladinadminv1connect.TenantServiceClient
	backends   paladinadminv1connect.BackendServiceClient
	buckets    paladinadminv1connect.BucketServiceClient
	objectKeys paladinadminv1connect.ObjectKeyServiceClient
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	adminURL := envOrSkip(t, "PALADIN_ADMIN_URL")
	secret := os.Getenv("PALADIN_JWT_SECRET")
	if secret == "" {
		secret = "dev-secret-change-me-32-bytes-min"
	}
	issuer := os.Getenv("PALADIN_JWT_ISSUER")
	if issuer == "" {
		issuer = "paladin-dev"
	}

	// Mint a platform.admin JWT scoped to a fresh tenant_id. The tenant
	// in the claim is the *caller's* tenant; the test creates fresh
	// platform-scoped resources via this admin role.
	jwt := mintJWT(t, secret, issuer, "paladin-admin", uuid.New().String(),
		"e2e-runner", []string{"platform.admin"}, 30*time.Minute)

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &authedTransport{
			jwt:  jwt,
			base: http.DefaultTransport,
		},
	}

	nonce := randomNonce(t, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	return &fixture{
		ctx:       ctx,
		cancel:    cancel,
		adminURL:  adminURL,
		nonce:     nonce,
		jwtSecret: secret,
		jwtIssuer: issuer,
		tenants:   paladinadminv1connect.NewTenantServiceClient(httpClient, adminURL),
		backends:  paladinadminv1connect.NewBackendServiceClient(httpClient, adminURL),
		buckets:   paladinadminv1connect.NewBucketServiceClient(httpClient, adminURL),
		objectKeys: paladinadminv1connect.NewObjectKeyServiceClient(
			httpClient, adminURL),
	}
}

// clientForTenant returns a fresh ObjectKey client whose JWT is
// scoped to `tenantID`. Use for ObjectKey CRUD against a tenant
// other than the fixture's bootstrap one — the ObjectKey handler
// reads the caller's tenant_id off the principal and uses it as
// the resource owner.
func (f *fixture) objectKeysAsTenant(t *testing.T, tenantID string) paladinadminv1connect.ObjectKeyServiceClient {
	t.Helper()
	jwt := mintJWT(t, f.jwtSecret, f.jwtIssuer, "paladin-admin",
		tenantID, "e2e-runner", []string{"platform.admin"}, 30*time.Minute)
	hc := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &authedTransport{jwt: jwt, base: http.DefaultTransport},
	}
	return paladinadminv1connect.NewObjectKeyServiceClient(hc, f.adminURL)
}

// authedTransport injects the bearer JWT on every request without
// requiring callers to thread it through each Connect call site.
type authedTransport struct {
	jwt  string
	base http.RoundTripper
}

func (t *authedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.jwt)
	return t.base.RoundTrip(req)
}

// randomNonce returns `n` bytes of random hex. Used to scope test
// resource names so concurrent runs don't trip UNIQUE constraints.
func randomNonce(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("randomNonce: %v", err)
	}
	return hex.EncodeToString(b)
}

// mintJWT signs an HS256 token matching the dev config's verifier
// (auth.issuer = paladin-dev, audiences include paladin-admin).
func mintJWT(t *testing.T, secret, issuer, audience, tenantID, subject string,
	roles []string, ttl time.Duration,
) string {
	t.Helper()
	now := time.Now()
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":    issuer,
		"aud":    audience,
		"sub":    subject,
		"tenant": tenantID, // matches internal/auth/jwt.go jwtClaims.Tenant
		"roles":  roles,
		"iat":    now.Unix(),
		"exp":    now.Add(ttl).Unix(),
		"nbf":    now.Add(-30 * time.Second).Unix(),
	}
	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(headerJSON) + "." + enc.EncodeToString(claimsJSON)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	sig := enc.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

// ── Tests ──────────────────────────────────────────────────────────────

// TestAdminAPI_E2E walks the full admin API surface in dependency
// order: register backend → create bucket → create tenant (with
// default binding) → create ObjectKey → exercise reads/lists/updates →
// reverse-order cleanup. Each phase asserts shape + invariants of
// the responses; negative subtests pin error semantics (slug
// immutability, duplicate detection, missing-binding rejection).
//
// Single top-level test because the phases depend on each other —
// running them as t.Run subtests of one parent ensures cleanup order
// is right and gives clean output grouping.
func TestAdminAPI_E2E(t *testing.T) {
	f := newFixture(t)

	backendID := "e2e-" + f.nonce
	bucketName := "paladin-e2e-" + f.nonce
	tenantSlug := "e2e-" + f.nonce
	// Multi-segment per migration 030. Every segment must satisfy the
	// per-segment kebab-case CHECK (`^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$`)
	// — minimum 3 chars when the optional middle group is present, so
	// segments like "q1" (2 chars) get rejected. Use ≥3-char segments.
	objectKeyName := "e2e/" + f.nonce + "/2026"

	var (
		createdTenantID string
		tenantRV        string
	)

	// Cleanup at the end — reverse order. Each step ignores
	// not-found / version-mismatch errors so a partial run still
	// drains state. The deferred t.Cleanup pattern would scatter
	// the same intent across subtests; consolidating here keeps the
	// teardown auditable in one place.
	t.Cleanup(func() {
		if createdTenantID != "" {
			oks := f.objectKeysAsTenant(t, createdTenantID)
			_, _ = oks.DeleteObjectKey(f.ctx,
				connect.NewRequest(&pb.DeleteObjectKeyRequest{
					Name: "tenants/" + createdTenantID + "/objectKeys/" + objectKeyName,
				}))
			_, _ = f.tenants.DeleteTenant(f.ctx,
				connect.NewRequest(&pb.DeleteTenantRequest{
					Name:  "tenants/" + createdTenantID,
					Force: true,
				}))
		}
		_, _ = f.buckets.DeleteBucket(f.ctx,
			connect.NewRequest(&pb.DeleteBucketRequest{
				Name: bucketResourceName(backendID, bucketName),
			}))
		_, _ = f.backends.DeleteBackend(f.ctx,
			connect.NewRequest(&pb.DeleteBackendRequest{
				Name: "storageBackends/" + backendID,
			}))
	})

	// ─── Backend ─────────────────────────────────────────────────
	t.Run("BackendService_CreateAndGet", func(t *testing.T) {
		got, err := f.backends.CreateBackend(f.ctx, connect.NewRequest(
			&pb.CreateBackendRequest{
				BackendId: backendID,
				Backend: &pb.StorageBackend{
					BackendId:            backendID,
					DisplayName:          "E2E " + f.nonce,
					Kind:                 pb.StorageKind_STORAGE_KIND_S3_COMPATIBLE,
					Endpoint:             "http://seaweedfs-filer.storage.svc.cluster.local:8333",
					Region:               "e2e",
					ForcePathStyle:       true,
					CredentialsSecretRef: "vault://kv/paladin/e2e",
				},
			}))
		if err != nil {
			t.Fatalf("CreateBackend: %v", err)
		}
		if got.Msg.GetBackendId() != backendID {
			t.Errorf("BackendId: got %q want %q",
				got.Msg.GetBackendId(), backendID)
		}

		read, err := f.backends.GetBackend(f.ctx, connect.NewRequest(
			&pb.GetBackendRequest{Name: "storageBackends/" + backendID}))
		if err != nil {
			t.Fatalf("GetBackend: %v", err)
		}
		if read.Msg.GetEndpoint() == "" {
			t.Errorf("Endpoint is empty on read")
		}
	})

	t.Run("BackendService_List", func(t *testing.T) {
		res, err := f.backends.ListBackends(f.ctx, connect.NewRequest(
			&pb.ListBackendsRequest{}))
		if err != nil {
			t.Fatalf("ListBackends: %v", err)
		}
		if !containsBackend(res.Msg.GetBackends(), backendID) {
			t.Errorf("ListBackends missing %q", backendID)
		}
	})

	// ─── Bucket ──────────────────────────────────────────────────
	t.Run("BucketService_CreateAndGet", func(t *testing.T) {
		got, err := f.buckets.CreateBucket(f.ctx, connect.NewRequest(
			&pb.CreateBucketRequest{
				Parent:     "storageBackends/" + backendID,
				BucketName: bucketName,
				Bucket: &pb.Bucket{
					BucketName:  bucketName,
					DisplayName: "E2E Bucket " + f.nonce,
					Region:      "e2e",
				},
			}))
		if err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		if got.Msg.GetBucketName() != bucketName {
			t.Errorf("BucketName: got %q want %q",
				got.Msg.GetBucketName(), bucketName)
		}

		read, err := f.buckets.GetBucket(f.ctx, connect.NewRequest(
			&pb.GetBucketRequest{
				Name: bucketResourceName(backendID, bucketName),
			}))
		if err != nil {
			t.Fatalf("GetBucket: %v", err)
		}
		if read.Msg.GetBackendId() != backendID {
			t.Errorf("BackendId: got %q want %q",
				read.Msg.GetBackendId(), backendID)
		}
	})

	// ─── Tenant ──────────────────────────────────────────────────
	t.Run("TenantService_CreateWithDefaultBinding", func(t *testing.T) {
		got, err := f.tenants.CreateTenant(f.ctx, connect.NewRequest(
			&pb.CreateTenantRequest{
				Tenant: &pb.Tenant{
					Slug:        tenantSlug,
					DisplayName: "E2E Tenant " + f.nonce,
				},
				DefaultBucket: bucketResourceName(backendID, bucketName),
			}))
		if err != nil {
			t.Fatalf("CreateTenant: %v", err)
		}
		if got.Msg.GetSlug() != tenantSlug {
			t.Errorf("Slug: got %q want %q",
				got.Msg.GetSlug(), tenantSlug)
		}
		if got.Msg.GetTenantId() == "" {
			t.Fatal("TenantId is empty — server should have minted UUIDv7")
		}
		if _, err := uuid.Parse(got.Msg.GetTenantId()); err != nil {
			t.Errorf("TenantId is not a valid UUID: %v", err)
		}
		createdTenantID = got.Msg.GetTenantId()
		tenantRV = got.Msg.GetResourceVersion()
	})

	t.Run("TenantService_RejectEmptySlug", func(t *testing.T) {
		_, err := f.tenants.CreateTenant(f.ctx, connect.NewRequest(
			&pb.CreateTenantRequest{
				Tenant:        &pb.Tenant{Slug: "" /* missing */},
				DefaultBucket: bucketResourceName(backendID, bucketName),
			}))
		assertCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("TenantService_RejectDuplicateSlug", func(t *testing.T) {
		_, err := f.tenants.CreateTenant(f.ctx, connect.NewRequest(
			&pb.CreateTenantRequest{
				Tenant: &pb.Tenant{
					Slug:        tenantSlug, // already taken
					DisplayName: "Different " + f.nonce,
				},
				DefaultBucket: bucketResourceName(backendID, bucketName),
			}))
		assertCode(t, err, connect.CodeAlreadyExists)
	})

	t.Run("TenantService_RejectMismatchedBinding", func(t *testing.T) {
		_, err := f.tenants.CreateTenant(f.ctx, connect.NewRequest(
			&pb.CreateTenantRequest{
				Tenant: &pb.Tenant{
					Slug:        "e2e-mismatch-" + f.nonce,
					DisplayName: "Mismatch " + f.nonce,
				},
				// Bucket name doesn't exist on this backend → FK fails.
				DefaultBucket: bucketResourceName(backendID, "does-not-exist-"+f.nonce),
			}))
		assertCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("TenantService_GetByUUIDAndSlug", func(t *testing.T) {
		// Both forms should resolve to the same row.
		byUUID, err := f.tenants.GetTenant(f.ctx, connect.NewRequest(
			&pb.GetTenantRequest{Name: "tenants/" + createdTenantID}))
		if err != nil {
			t.Fatalf("GetTenant by UUID: %v", err)
		}
		bySlug, err := f.tenants.GetTenant(f.ctx, connect.NewRequest(
			&pb.GetTenantRequest{Name: "tenants/" + tenantSlug}))
		if err != nil {
			t.Fatalf("GetTenant by slug: %v", err)
		}
		if byUUID.Msg.GetTenantId() != bySlug.Msg.GetTenantId() {
			t.Errorf("UUID-form and slug-form returned different tenants: %s vs %s",
				byUUID.Msg.GetTenantId(), bySlug.Msg.GetTenantId())
		}
	})

	t.Run("TenantService_UpdateDisplayName", func(t *testing.T) {
		newName := "E2E Updated " + f.nonce
		got, err := f.tenants.UpdateTenant(f.ctx, connect.NewRequest(
			&pb.UpdateTenantRequest{
				Name:            "tenants/" + createdTenantID,
				ResourceVersion: tenantRV,
				UpdateMask:      maskOf("display_name"),
				Tenant:          &pb.Tenant{DisplayName: newName},
			}))
		if err != nil {
			t.Fatalf("UpdateTenant: %v", err)
		}
		if got.Msg.GetDisplayName() != newName {
			t.Errorf("DisplayName: got %q want %q",
				got.Msg.GetDisplayName(), newName)
		}
		tenantRV = got.Msg.GetResourceVersion()
	})

	t.Run("TenantService_RejectSlugMutation", func(t *testing.T) {
		// Slug in the field mask must be rejected — immutable per
		// Phase 0. The DB trigger is the last line of defence; the
		// connectshim should refuse before any DB write.
		_, err := f.tenants.UpdateTenant(f.ctx, connect.NewRequest(
			&pb.UpdateTenantRequest{
				Name:            "tenants/" + createdTenantID,
				ResourceVersion: tenantRV,
				UpdateMask:      maskOf("slug"),
				Tenant:          &pb.Tenant{Slug: "evil-" + f.nonce},
			}))
		assertCode(t, err, connect.CodeInvalidArgument)
	})

	// ─── ObjectKey ───────────────────────────────────────────────
	// ObjectKey handler uses the caller's tenant_id (from JWT) as the
	// resource owner — re-mint a JWT scoped to the just-created
	// tenant so the FK on object_keys.tenant_id resolves to a real row.
	t.Run("ObjectKeyService_CreateMultiSegment", func(t *testing.T) {
		oks := f.objectKeysAsTenant(t, createdTenantID)
		got, err := oks.CreateObjectKey(f.ctx, connect.NewRequest(
			&pb.CreateObjectKeyRequest{
				Parent:    "tenants/" + createdTenantID,
				ObjectKey: objectKeyName,
				ObjectKeyResource: &pb.ObjectKey{
					ObjectKey:   objectKeyName,
					DisplayName: "E2E OK " + f.nonce,
					Bucket:      bucketResourceName(backendID, bucketName),
				},
			}))
		if err != nil {
			t.Fatalf("CreateObjectKey: %v", err)
		}
		if got.Msg.GetObjectKey() != objectKeyName {
			t.Errorf("ObjectKey: got %q want %q",
				got.Msg.GetObjectKey(), objectKeyName)
		}
		// Multi-segment path round-trip — migration 030 guarantees the
		// constraint accepts `a/b/c` shapes; the connectshim parser
		// (admin/object_key_server.go) anchors on `/objectKeys/` so
		// slashes inside the body don't get mistaken for resource-name
		// separators.
		if !strings.Contains(got.Msg.GetObjectKey(), "/") {
			t.Errorf("expected multi-segment object_key, got %q",
				got.Msg.GetObjectKey())
		}
	})

	t.Run("ObjectKeyService_GetAndList", func(t *testing.T) {
		oks := f.objectKeysAsTenant(t, createdTenantID)
		read, err := oks.GetObjectKey(f.ctx, connect.NewRequest(
			&pb.GetObjectKeyRequest{
				Name: "tenants/" + createdTenantID + "/objectKeys/" + objectKeyName,
			}))
		if err != nil {
			t.Fatalf("GetObjectKey: %v", err)
		}
		if read.Msg.GetBucket() != bucketResourceName(backendID, bucketName) {
			t.Errorf("Bucket: got %q want %q",
				read.Msg.GetBucket(),
				bucketResourceName(backendID, bucketName))
		}

		list, err := oks.ListObjectKeys(f.ctx, connect.NewRequest(
			&pb.ListObjectKeysRequest{
				Parent: "tenants/" + createdTenantID,
			}))
		if err != nil {
			t.Fatalf("ListObjectKeys: %v", err)
		}
		if !containsObjectKey(list.Msg.GetObjectKeys(), objectKeyName) {
			t.Errorf("ListObjectKeys missing %q", objectKeyName)
		}
	})

	t.Run("ListTenants_IncludesCreated", func(t *testing.T) {
		list, err := f.tenants.ListTenants(f.ctx, connect.NewRequest(
			&pb.ListTenantsRequest{}))
		if err != nil {
			t.Fatalf("ListTenants: %v", err)
		}
		found := false
		for _, tt := range list.Msg.GetTenants() {
			if tt.GetTenantId() == createdTenantID {
				found = true
				if tt.GetSlug() != tenantSlug {
					t.Errorf("Slug on list response: got %q want %q",
						tt.GetSlug(), tenantSlug)
				}
				break
			}
		}
		if !found {
			t.Errorf("ListTenants missing tenant %q", createdTenantID)
		}
	})
}

// ── helpers ────────────────────────────────────────────────────────────

func bucketResourceName(backend, bucket string) string {
	return "storageBackends/" + backend + "/buckets/" + bucket
}

func containsBackend(list []*pb.StorageBackend, id string) bool {
	for _, b := range list {
		if b.GetBackendId() == id {
			return true
		}
	}
	return false
}

func containsObjectKey(list []*pb.ObjectKey, key string) bool {
	for _, ok := range list {
		if ok.GetObjectKey() == key {
			return true
		}
	}
	return false
}

func maskOf(paths ...string) *fieldmaskpb.FieldMask {
	return &fieldmaskpb.FieldMask{Paths: paths}
}

func assertCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", want)
	}
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected *connect.Error, got %T: %v", err, err)
	}
	if ce.Code() != want {
		t.Fatalf("expected code %s, got %s (%v)", want, ce.Code(), err)
	}
}
