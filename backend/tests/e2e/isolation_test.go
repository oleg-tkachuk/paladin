//go:build e2e

package e2e

// Tenant isolation at the edge, against the stack: what Cedar delegates to the
// request path — the tenant a token names, the plane its audience is for, the
// signature that makes it Paladin's. Each probe crafts a token that tries to
// cross one of those lines and asserts the call is refused; a control proves
// the same object is readable by its own tenant, so a refusal is the line
// holding rather than the object missing.
//
// It replaced a shell probe that drove the API by hand and stopped running
// when the API it called was renamed; here it fails the stack gate instead.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// envDataURL names the data plane's address, as scripts/stack-ports.sh
// exports it.
const envDataURL = "PALADIN_E2E_DATA_URL"

const (
	audienceData  = "paladin-data"
	audienceAdmin = "paladin-admin"
	// probeTokenTTL is how long a probe's token lives: the whole test.
	probeTokenTTL = 10 * time.Minute
	// forgedSecret signs a token Paladin did not issue.
	forgedSecret = "not-the-real-secret-1234567890ab"
	probeObject  = "secret.txt"
	// bucketReady is the provision_state of a bucket the backend holds.
	bucketReady = "ready"
	// stackBucket is the bucket the stack's object store is created with
	// (seaweedfs-setup in the compose file, PALADIN_E2E_S3_BUCKET).
	stackBucket = "paladin-e2e"
	probeBody   = "secret"
	probeType   = "text/plain"
)

// tokenClaims is what a probe puts in its token; the slug is optional, as
// Paladin's own issuer sets it and a forger may set any.
type tokenClaims struct {
	audience, tenant, slug, subject string
	roles                           []string
}

// signToken is mintJWT with the claims a probe spoofs and the key it signs
// with chosen by the probe.
func signToken(t *testing.T, secret, issuer string, c tokenClaims) string {
	t.Helper()
	now := time.Now()
	claims := map[string]any{
		"iss": issuer, "aud": c.audience, "sub": c.subject,
		"tenant": c.tenant, "roles": c.roles,
		"iat": now.Unix(), "exp": now.Add(probeTokenTTL).Unix(),
		"nbf": now.Add(-30 * time.Second).Unix(),
	}
	if c.slug != "" {
		claims["tenant_slug"] = c.slug
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	body, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	input := enc.EncodeToString(header) + "." + enc.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	return input + "." + enc.EncodeToString(mac.Sum(nil))
}

// dataPlane is the data plane as a token's bearer sees it.
func dataPlane(t *testing.T, url, token string) *paladin.DataPlane {
	t.Helper()
	p, err := paladin.Connect(paladin.Endpoints{Data: url}, paladin.WithBearerToken(token))
	if err != nil {
		t.Fatalf("data plane client: %v", err)
	}
	return p.Data
}

func TestTenantIsolation_E2E(t *testing.T) {
	f := newFixture(t)
	dataURL := envOrSkip(t, envDataURL)
	ctx := f.ctx

	// The stack's bucket, which both tenants are bound to by default and so
	// granted (ADR-0032).
	bucket, registered := f.probeBucket(t)
	mk := func(prefix string) *pb.Tenant {
		t.Helper()
		slug := prefix + "-" + f.nonce
		tn, err := f.tenants.CreateTenant(ctx, &pb.CreateTenantRequest{
			Tenant:        &pb.Tenant{Slug: slug, DisplayName: slug},
			DefaultBucket: bucket,
		})
		if err != nil {
			t.Fatalf("create tenant %s: %v", slug, err)
		}
		return tn
	}
	victim, attacker := mk("vict"), mk("attk")

	collections := f.collectionsAsTenant(t, victim.GetTenantId())
	parent := "tenants/" + victim.GetTenantId() + "/collections/docs"
	if _, err := collections.CreateCollection(ctx, &pb.CreateCollectionRequest{
		Parent: "tenants/" + victim.GetTenantId(), Collection: "docs",
		CollectionResource: &pb.Collection{},
	}); err != nil {
		t.Fatalf("create the victim's collection: %v", err)
	}
	token := func(c tokenClaims) string { return signToken(t, f.jwtSecret, f.jwtIssuer, c) }
	owner := token(tokenClaims{audienceData, victim.GetTenantId(), victim.GetSlug(), "victim", []string{"tenant.admin"}})
	ownerData := dataPlane(t, dataURL, owner)
	obj, err := paladin.Upload(ctx, ownerData, paladin.UploadInput{
		Parent: parent, Key: probeObject, ContentType: probeType,
		Size: int64(len(probeBody)), Body: bytes.NewReader([]byte(probeBody)),
	}, paladin.UploadOptions{})
	if err != nil {
		t.Fatalf("upload the victim's object: %v", err)
	}
	// Children first: a collection holding an object, a tenant holding a
	// collection and a bucket holding either are each refused a delete.
	// A permanent delete is a platform admin's; the victim's own admin may
	// only move the object to the trash.
	purger := dataPlane(t, dataURL, token(tokenClaims{audienceData, victim.GetTenantId(), victim.GetSlug(), "e2e-runner", []string{"platform.admin"}}))
	t.Cleanup(func() {
		if _, err := paladin.Delete(context.Background(), purger, obj.GetName(), paladin.DeleteOptions{Permanent: true}); err != nil {
			t.Logf("cleanup: object %s: %v", obj.GetName(), err)
		}
		f.removeProbeTenants(t, collections, parent, victim, attacker)
		if registered {
			f.unregisterBucket(t, bucket)
		}
	})

	read := func(tok string) error {
		_, err := dataPlane(t, dataURL, tok).Presign.PresignDownload(ctx,
			&datav1.PresignDownloadRequest{Name: obj.GetName()})
		return err
	}
	refused := func(name string, err error) {
		t.Helper()
		var ce *connect.Error
		if !errors.As(err, &ce) {
			t.Errorf("%s: %v, want a refusal", name, err)
		}
	}

	t.Run("another tenant cannot read the object", func(t *testing.T) {
		refused("attacker", read(token(tokenClaims{audienceData, attacker.GetTenantId(), attacker.GetSlug(), "attacker", []string{"tenant.admin"}})))
	})
	t.Run("claiming the victim's slug does not make it the victim", func(t *testing.T) {
		refused("slug spoof", read(token(tokenClaims{audienceData, attacker.GetTenantId(), victim.GetSlug(), "attacker", []string{"tenant.admin"}})))
	})
	t.Run("a data-plane token is refused on the admin plane", func(t *testing.T) {
		hc := &http.Client{Timeout: 30 * time.Second, Transport: &authedTransport{jwt: owner, base: http.DefaultTransport}}
		admin := paladinadminv1connect.NewTenantServiceClient(connect.NewClient(connecthttp.NewTransport(hc, f.adminURL)))
		_, err := admin.GetTenant(ctx, &pb.GetTenantRequest{Name: "tenants/" + victim.GetTenantId()})
		refused("data audience on admin", err)
	})
	t.Run("an admin-plane token is refused on the data plane", func(t *testing.T) {
		refused("admin audience on data", read(token(tokenClaims{audienceAdmin, victim.GetTenantId(), victim.GetSlug(), "victim", []string{"tenant.admin"}})))
	})
	t.Run("a token Paladin did not sign is refused", func(t *testing.T) {
		forged := signToken(t, forgedSecret, f.jwtIssuer, tokenClaims{audienceData, victim.GetTenantId(), victim.GetSlug(), "forger", []string{"platform.admin"}})
		refused("forged", read(forged))
	})
	// The control: every refusal above is of an object its own tenant reads.
	t.Run("the victim's own member reads it", func(t *testing.T) {
		if err := read(token(tokenClaims{audienceData, victim.GetTenantId(), victim.GetSlug(), "member", []string{"tenant.user"}})); err != nil {
			t.Fatalf("own member: %v", err)
		}
	})
}

// removeProbeTenants takes the run's object, collection and tenants away, and
// says what it could not remove rather than failing a run that passed.
func (f *fixture) removeProbeTenants(t *testing.T, collections paladinadminv1connect.CollectionServiceClient,
	collection string, tenants ...*pb.Tenant,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := collections.DeleteCollection(ctx, &pb.DeleteCollectionRequest{
		Name: collection, SkipVersionCheck: true,
	}); err != nil {
		t.Logf("cleanup: collection %s: %v", collection, err)
	}
	for _, tn := range tenants {
		name := "tenants/" + tn.GetTenantId()
		if _, err := f.tenants.DeleteTenant(ctx, &pb.DeleteTenantRequest{
			Name: name, ResourceVersion: f.tenantVersion(tn.GetTenantId()),
		}); err != nil {
			t.Logf("cleanup: delete %s: %v", name, err)
		}
		if _, err := f.tenants.PurgeTenant(ctx, &pb.PurgeTenantRequest{Name: name}); err != nil {
			t.Logf("cleanup: purge %s: %v", name, err)
		}
	}
}

// probeBucket is the stack's physical bucket, and whether this run
// registered it: as the platform's, when no run had. Taken under management
// rather than provisioned, since the stack runs no worker and a bucket it is
// asked to create stays pending. Later suites register it for themselves, so a
// registration this run made is removed after it — never the bucket's bytes.
func (f *fixture) probeBucket(t *testing.T) (string, bool) {
	t.Helper()
	name := bucketResourceName(configuredBackendID, stackBucket)
	b, err := f.buckets.GetBucket(f.ctx, &pb.GetBucketRequest{Name: name})
	registered := connect.CodeOf(err) == connect.CodeNotFound
	if registered {
		b, err = f.buckets.CreateBucket(f.ctx, &pb.CreateBucketRequest{
			Parent:   "storageBackends/" + configuredBackendID,
			BucketId: stackBucket,
			Bucket:   &pb.Bucket{BucketId: stackBucket, OwnerTenantId: f.platformTenantID},
		})
	}
	if err != nil {
		t.Fatalf("the stack's bucket %s: %v", name, err)
	}
	if b.GetProvisionState() != bucketReady {
		t.Fatalf("the stack's bucket %s is %q, want %q", name, b.GetProvisionState(), bucketReady)
	}
	return name, registered
}

// unregisterBucket removes the run's registration of a bucket, leaving the
// bucket and its contents on the backend.
func (f *fixture) unregisterBucket(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := f.buckets.DeleteBucket(ctx, &pb.DeleteBucketRequest{Name: name, SkipVersionCheck: true}); err != nil {
		t.Logf("cleanup: unregister %s: %v", name, err)
	}
}
