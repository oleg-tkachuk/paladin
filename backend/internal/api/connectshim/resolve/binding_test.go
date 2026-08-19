package resolve

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/internal/auth"
)

// fakeBindings is a DefaultBindingLookup stub. When found is false the tenant
// has no default binding.
type fakeBindings struct {
	backend, bucket string
	found           bool
	err             error
	calls           int
}

func (f *fakeBindings) TenantDefaultBinding(_ context.Context, _ uuid.UUID) (string, string, bool, error) {
	f.calls++
	return f.backend, f.bucket, f.found, f.err
}

func ctxWithTenant(tid uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u", TenantID: tid})
}

// TestResolveWithBinding_BareEnriched: a bare name is completed to canonical
// using the tenant's default (backend, bucket).
func TestResolveWithBinding_BareEnriched(t *testing.T) {
	tid := uuid.New()
	b := &fakeBindings{backend: "primary", bucket: "acme-eu", found: true}

	ref, err := ResolveObjectKeyNameWithBinding(ctxWithTenant(tid), "invoices/q1", b)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ref.Shape != ShapeBare {
		t.Errorf("shape = %v, want bare", ref.Shape)
	}
	if ref.TenantID != tid || ref.ObjectKey != "invoices/q1" {
		t.Errorf("ref = {%s, %q}, want {%s, invoices/q1}", ref.TenantID, ref.ObjectKey, tid)
	}
	if ref.BackendID != "primary" || ref.BucketName != "acme-eu" {
		t.Errorf("binding = (%q, %q), want (primary, acme-eu)", ref.BackendID, ref.BucketName)
	}
}

// TestResolveWithBinding_BareNoBinding: bare name + no default binding →
// ErrNoDefaultBinding.
func TestResolveWithBinding_BareNoBinding(t *testing.T) {
	tid := uuid.New()
	b := &fakeBindings{found: false}
	if _, err := ResolveObjectKeyNameWithBinding(ctxWithTenant(tid), "just-a-key", b); !errors.Is(err, ErrNoDefaultBinding) {
		t.Fatalf("err = %v, want ErrNoDefaultBinding", err)
	}
}

// TestResolveWithBinding_CanonicalAndTenantUntouched: A and C shapes already
// carry (or don't need) a binding, so the lookup is never consulted.
func TestResolveWithBinding_CanonicalAndTenantUntouched(t *testing.T) {
	tid := uuid.New()

	// C-shape: no lookup, no binding populated (matches ResolveObjectKeyName).
	b1 := &fakeBindings{found: true, backend: "x", bucket: "y"}
	refC, err := ResolveObjectKeyNameWithBinding(ctxWithTenant(tid), "tenants/"+tid.String()+"/objectKeys/inv", b1)
	if err != nil {
		t.Fatalf("C resolve: %v", err)
	}
	if refC.Shape != ShapeTenant || b1.calls != 0 {
		t.Errorf("C: shape=%v lookups=%d, want tenant/0", refC.Shape, b1.calls)
	}
	if refC.BackendID != "" || refC.BucketName != "" {
		t.Errorf("C must not be enriched; got (%q, %q)", refC.BackendID, refC.BucketName)
	}

	// A-shape: carries its own binding; lookup not consulted.
	b2 := &fakeBindings{found: true, backend: "x", bucket: "y"}
	refA, err := ResolveObjectKeyNameWithBinding(ctxWithTenant(tid),
		"storageBackends/primary/buckets/acme-eu/tenants/"+tid.String()+"/objectKeys/inv", b2)
	if err != nil {
		t.Fatalf("A resolve: %v", err)
	}
	if refA.Shape != ShapeCanonical || b2.calls != 0 {
		t.Errorf("A: shape=%v lookups=%d, want canonical/0", refA.Shape, b2.calls)
	}
	if refA.BackendID != "primary" || refA.BucketName != "acme-eu" {
		t.Errorf("A binding = (%q, %q), want (primary, acme-eu)", refA.BackendID, refA.BucketName)
	}
}

// TestResolveWithBinding_LookupError propagates a store error (not
// ErrNoDefaultBinding).
func TestResolveWithBinding_LookupError(t *testing.T) {
	tid := uuid.New()
	b := &fakeBindings{err: errors.New("db down")}
	_, err := ResolveObjectKeyNameWithBinding(ctxWithTenant(tid), "bare", b)
	if err == nil || errors.Is(err, ErrNoDefaultBinding) {
		t.Fatalf("err = %v, want a wrapped store error", err)
	}
}
