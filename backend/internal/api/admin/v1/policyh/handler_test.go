package policyh

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// fakeEngine is a configurable cedar.Authorizer. It records every
// IsAuthorized call so tests can assert the principal / resource / action the
// handler forwards, and decides the outcome via authzFn (which may branch on
// the action, letting a test allow the InspectPolicy gate but deny the
// simulated action).
type fakeEngine struct {
	authzFn func(action string, r *cedar.Resource, p *cedar.Principal) (cedar.Decision, error)
	calls   []engineCall
}

type engineCall struct {
	action   string
	resource *cedar.Resource
	princ    *cedar.Principal
}

func (f *fakeEngine) IsAuthorized(_ context.Context, p *cedar.Principal, action string, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	f.calls = append(f.calls, engineCall{action: action, resource: r, princ: p})
	if f.authzFn != nil {
		return f.authzFn(action, r, p)
	}
	return cedar.DecisionAllow, nil
}

// allowEngine authorises every call.
func allowEngine() *fakeEngine { return &fakeEngine{} }

// denyEngine denies every call (drives the PermissionDenied branch of the
// InspectPolicy gate).
func denyEngine() *fakeEngine {
	return &fakeEngine{authzFn: func(string, *cedar.Resource, *cedar.Principal) (cedar.Decision, error) {
		return cedar.DecisionDeny, nil
	}}
}

// errEngine fails the InspectPolicy gate with an engine-level error (drives
// the Internal branch).
func errEngine() *fakeEngine {
	return &fakeEngine{authzFn: func(string, *cedar.Resource, *cedar.Principal) (cedar.Decision, error) {
		return cedar.DecisionDeny, errors.New("engine boom")
	}}
}

// fakeStore is a configurable cedar.Store; only Fetch is exercised by the
// handler. Watch satisfies the interface and is never called here.
type fakeStore struct {
	fetchFn func(ctx context.Context, tenantID uuid.UUID, collection string) (cedar.Layers, []byte, string, error)
}

func (f *fakeStore) Fetch(ctx context.Context, tenantID uuid.UUID, collection string) (cedar.Layers, []byte, string, error) {
	return f.fetchFn(ctx, tenantID, collection)
}

func (f *fakeStore) Watch(context.Context) (<-chan cedar.ChangeEvent, error) { return nil, nil }

func authedCtx(tid uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tid})
}

func wantCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %v, got nil", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("error code: got %v, want %v (err=%v)", got, want, err)
	}
}

func TestNewHandler(t *testing.T) {
	t.Run("nil engine panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic on nil engine")
			}
		}()
		NewHandler(nil, &fakeStore{})
	})

	t.Run("nil store panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic on nil store")
			}
		}()
		NewHandler(allowEngine(), nil)
	})

	t.Run("both set returns handler", func(t *testing.T) {
		if NewHandler(allowEngine(), &fakeStore{}) == nil {
			t.Fatal("expected non-nil handler")
		}
	})
}

func TestValidatePolicy(t *testing.T) {
	tid := uuid.New()
	const validPolicy = `permit (
  principal,
  action,
  resource
);`

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(allowEngine(), &fakeStore{})
		_, _, err := h.ValidatePolicy(context.Background(), validPolicy)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("authz denied → permission denied", func(t *testing.T) {
		h := NewHandler(denyEngine(), &fakeStore{})
		_, _, err := h.ValidatePolicy(authedCtx(tid), validPolicy)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("engine error → internal", func(t *testing.T) {
		h := NewHandler(errEngine(), &fakeStore{})
		_, _, err := h.ValidatePolicy(authedCtx(tid), validPolicy)
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("valid policy compiles → (true, empty, nil)", func(t *testing.T) {
		h := NewHandler(allowEngine(), &fakeStore{})
		ok, msg, err := h.ValidatePolicy(authedCtx(tid), validPolicy)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !ok || msg != "" {
			t.Fatalf("valid policy: got ok=%v msg=%q, want true/empty", ok, msg)
		}
	})

	t.Run("invalid policy → (false, parser-text, nil), not a protocol error", func(t *testing.T) {
		h := NewHandler(allowEngine(), &fakeStore{})
		ok, msg, err := h.ValidatePolicy(authedCtx(tid), "this is not cedar")
		if err != nil {
			t.Fatalf("parser failure must come back through the bool/string, not err: %v", err)
		}
		if ok {
			t.Fatal("expected invalid=false")
		}
		if msg == "" {
			t.Fatal("expected non-empty parser error text")
		}
	})

	t.Run("gate uses InspectPolicy action with empty resource", func(t *testing.T) {
		fe := allowEngine()
		h := NewHandler(fe, &fakeStore{})
		if _, _, err := h.ValidatePolicy(authedCtx(tid), validPolicy); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(fe.calls) != 1 {
			t.Fatalf("expected exactly 1 authz call, got %d", len(fe.calls))
		}
		if fe.calls[0].action != cedar.ActionInspectPolicy {
			t.Fatalf("gate action: got %q want %q", fe.calls[0].action, cedar.ActionInspectPolicy)
		}
		if fe.calls[0].resource.TenantID != uuid.Nil || fe.calls[0].resource.Collection != "" {
			t.Fatalf("ValidatePolicy must pass an empty resource, got %+v", fe.calls[0].resource)
		}
	})
}

func TestSimulateAuthz(t *testing.T) {
	tid := uuid.New()

	t.Run("invalid resource name → parse error before authz", func(t *testing.T) {
		fe := allowEngine()
		h := NewHandler(fe, &fakeStore{})
		_, err := h.SimulateAuthz(authedCtx(tid), SimulateAuthzInput{
			ResourceName: "tenants/not-a-uuid/collections/k",
		})
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected parse error, got %v", err)
		}
		if len(fe.calls) != 0 {
			t.Fatalf("authz must not run before the resource parses: %d calls", len(fe.calls))
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(allowEngine(), &fakeStore{})
		_, err := h.SimulateAuthz(context.Background(), SimulateAuthzInput{
			ResourceName: "tenants/" + tid.String(),
		})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("gate denied → permission denied", func(t *testing.T) {
		h := NewHandler(denyEngine(), &fakeStore{})
		_, err := h.SimulateAuthz(authedCtx(tid), SimulateAuthzInput{
			ResourceName: "tenants/" + tid.String(),
		})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("engine error on the simulated call is wrapped", func(t *testing.T) {
		fe := &fakeEngine{authzFn: func(action string, _ *cedar.Resource, _ *cedar.Principal) (cedar.Decision, error) {
			if action == cedar.ActionInspectPolicy {
				return cedar.DecisionAllow, nil // gate passes
			}
			return cedar.DecisionDeny, errors.New("kaboom")
		}}
		h := NewHandler(fe, &fakeStore{})
		_, err := h.SimulateAuthz(authedCtx(tid), SimulateAuthzInput{
			Action:       cedar.ActionGetObject,
			ResourceName: "tenants/" + tid.String(),
		})
		if err == nil || !strings.Contains(err.Error(), "authz") {
			t.Fatalf("expected wrapped authz error, got %v", err)
		}
	})

	t.Run("allowed forwards principal + resource + action from parsed name", func(t *testing.T) {
		objTenant := uuid.New()
		fe := allowEngine()
		h := NewHandler(fe, &fakeStore{})
		in := SimulateAuthzInput{
			PrincipalSubject: "svc-1",
			PrincipalRoles:   []string{"tenant.admin"},
			Action:           cedar.ActionGetObject,
			ResourceName:     "tenants/" + objTenant.String() + "/collections/logs",
		}
		out, err := h.SimulateAuthz(authedCtx(tid), in)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !out.Allowed {
			t.Fatal("expected Allowed=true")
		}
		if !strings.Contains(out.Explanation, cedar.ActionGetObject) || !strings.Contains(out.Explanation, in.ResourceName) {
			t.Fatalf("explanation missing action/resource: %q", out.Explanation)
		}
		// Call 0 is the InspectPolicy gate; call 1 is the simulated action.
		if len(fe.calls) != 2 {
			t.Fatalf("expected 2 authz calls (gate + simulate), got %d", len(fe.calls))
		}
		sim := fe.calls[1]
		if sim.action != cedar.ActionGetObject {
			t.Fatalf("simulated action: got %q want %q", sim.action, cedar.ActionGetObject)
		}
		if sim.resource.TenantID != objTenant || sim.resource.Collection != "logs" {
			t.Fatalf("resource from name: got tenant=%v key=%q want %v/logs", sim.resource.TenantID, sim.resource.Collection, objTenant)
		}
		if sim.princ.Subject != "svc-1" {
			t.Fatalf("principal subject: got %q want svc-1", sim.princ.Subject)
		}
		if len(sim.princ.Roles) != 1 || sim.princ.Roles[0] != "tenant.admin" {
			t.Fatalf("principal roles not forwarded: %+v", sim.princ.Roles)
		}
		if sim.princ.TenantID != objTenant {
			t.Fatalf("principal tenant should be the parsed resource tenant: got %v want %v", sim.princ.TenantID, objTenant)
		}
	})

	t.Run("denied decision → Allowed=false, no error", func(t *testing.T) {
		fe := &fakeEngine{authzFn: func(action string, _ *cedar.Resource, _ *cedar.Principal) (cedar.Decision, error) {
			if action == cedar.ActionInspectPolicy {
				return cedar.DecisionAllow, nil // gate passes
			}
			return cedar.DecisionDeny, nil // simulated action denied
		}}
		h := NewHandler(fe, &fakeStore{})
		out, err := h.SimulateAuthz(authedCtx(tid), SimulateAuthzInput{
			Action:       cedar.ActionDeleteObject,
			ResourceName: "tenants/" + tid.String(),
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if out.Allowed {
			t.Fatal("expected Allowed=false for a denied decision")
		}
	})
}

func TestGetEffectivePolicy(t *testing.T) {
	tid := uuid.New()

	t.Run("invalid resource name → parse error", func(t *testing.T) {
		h := NewHandler(allowEngine(), &fakeStore{})
		_, err := h.GetEffectivePolicy(authedCtx(tid), "tenants/bad-uuid", tid)
		if err == nil || !strings.Contains(err.Error(), "invalid tenant id") {
			t.Fatalf("expected parse error, got %v", err)
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(allowEngine(), &fakeStore{})
		_, err := h.GetEffectivePolicy(context.Background(), "tenants/"+tid.String(), tid)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("authz denied → permission denied", func(t *testing.T) {
		h := NewHandler(denyEngine(), &fakeStore{})
		_, err := h.GetEffectivePolicy(authedCtx(tid), "tenants/"+tid.String(), tid)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("store fetch error propagates", func(t *testing.T) {
		fs := &fakeStore{fetchFn: func(context.Context, uuid.UUID, string) (cedar.Layers, []byte, string, error) {
			return cedar.Layers{}, nil, "", errors.New("db down")
		}}
		h := NewHandler(allowEngine(), fs)
		_, err := h.GetEffectivePolicy(authedCtx(tid), "tenants/"+tid.String(), tid)
		if err == nil || !strings.Contains(err.Error(), "db down") {
			t.Fatalf("expected store error to propagate, got %v", err)
		}
	})

	t.Run("tenant-only resource → single tenant layer", func(t *testing.T) {
		fs := &fakeStore{fetchFn: func(_ context.Context, _ uuid.UUID, key string) (cedar.Layers, []byte, string, error) {
			if key != "" {
				t.Fatalf("tenant-only name must fetch empty collection, got %q", key)
			}
			return cedar.Layers{Tenant: "permit(principal, action, resource);"}, nil, "", nil
		}}
		h := NewHandler(allowEngine(), fs)
		out, err := h.GetEffectivePolicy(authedCtx(tid), "tenants/"+tid.String(), tid)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if out.MergedCedarPolicy != "permit(principal, action, resource);" {
			t.Fatalf("merged text: %q", out.MergedCedarPolicy)
		}
		if len(out.Layers) != 1 {
			t.Fatalf("expected 1 layer for a tenant-only name, got %d", len(out.Layers))
		}
		if !strings.Contains(out.Layers[0].Source, tid.String()) {
			t.Fatalf("tenant layer source: %q", out.Layers[0].Source)
		}
	})

	// Attribution comes off the store's own layers now. It used to be
	// recovered by splitting a joined string on a comment marker, which a
	// tenant policy containing that text would have defeated.
	t.Run("collection resource → tenant + collection layers, attributed", func(t *testing.T) {
		objTenant := uuid.New()
		var gotTenant uuid.UUID
		var gotKey string
		fs := &fakeStore{fetchFn: func(_ context.Context, tenant uuid.UUID, key string) (cedar.Layers, []byte, string, error) {
			gotTenant, gotKey = tenant, key
			return cedar.Layers{Tenant: "tenant-rule", Collection: "obj-rule"}, nil, "", nil
		}}
		h := NewHandler(allowEngine(), fs)
		out, err := h.GetEffectivePolicy(authedCtx(tid), "tenants/"+objTenant.String()+"/collections/logs", tid)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if gotTenant != objTenant || gotKey != "logs" {
			t.Fatalf("store fetched (%v,%q), want %v/logs", gotTenant, gotKey, objTenant)
		}
		if len(out.Layers) != 2 {
			t.Fatalf("expected 2 layers, got %d", len(out.Layers))
		}
		if out.Layers[0].CedarPolicy != "tenant-rule" {
			t.Fatalf("tenant layer text: %q", out.Layers[0].CedarPolicy)
		}
		if out.Layers[1].CedarPolicy != "obj-rule" {
			t.Fatalf("collection layer text: %q", out.Layers[1].CedarPolicy)
		}
		if !strings.Contains(out.Layers[1].Source, "logs") {
			t.Fatalf("collection layer source: %q", out.Layers[1].Source)
		}
	})

	// The bucket layer sits between them, named by the bucket, and the merged
	// text carries it in the same order the engine compiles it.
	t.Run("collection in a bucket with a policy → tenant, bucket, collection", func(t *testing.T) {
		const bucket = "storageBackends/primary/buckets/shared"
		fs := &fakeStore{fetchFn: func(context.Context, uuid.UUID, string) (cedar.Layers, []byte, string, error) {
			return cedar.Layers{Tenant: "tenant-rule", Bucket: "bucket-rule", BucketName: bucket, Collection: "obj-rule"}, nil, "", nil
		}}
		h := NewHandler(allowEngine(), fs)
		out, err := h.GetEffectivePolicy(authedCtx(tid), "tenants/"+tid.String()+"/collections/logs", tid)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(out.Layers) != 3 || out.Layers[1].Source != bucket || out.Layers[1].CedarPolicy != "bucket-rule" {
			t.Fatalf("layers = %+v, want the bucket's in the middle", out.Layers)
		}
		b, c := strings.Index(out.MergedCedarPolicy, "bucket-rule"), strings.Index(out.MergedCedarPolicy, "obj-rule")
		if b < 0 || c < b {
			t.Fatalf("merged text out of order:\n%s", out.MergedCedarPolicy)
		}
	})
}

func TestParseSimulateResource(t *testing.T) {
	fallback := uuid.New()
	tenant := uuid.New()

	t.Run("tenant + collection name", func(t *testing.T) {
		gotT, gotK, err := parseSimulateResource("tenants/"+tenant.String()+"/collections/logs", fallback)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if gotT != tenant || gotK != "logs" {
			t.Fatalf("got (%v,%q), want %v/logs", gotT, gotK, tenant)
		}
	})

	t.Run("tenant-only name", func(t *testing.T) {
		gotT, gotK, err := parseSimulateResource("tenants/"+tenant.String(), fallback)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if gotT != tenant || gotK != "" {
			t.Fatalf("got (%v,%q), want %v/empty", gotT, gotK, tenant)
		}
	})

	t.Run("invalid tenant uuid in 4-part name errors", func(t *testing.T) {
		if _, _, err := parseSimulateResource("tenants/nope/collections/k", fallback); err == nil {
			t.Fatal("expected error for invalid tenant uuid")
		}
	})

	t.Run("invalid tenant uuid in 2-part name errors", func(t *testing.T) {
		if _, _, err := parseSimulateResource("tenants/nope", fallback); err == nil {
			t.Fatal("expected error for invalid tenant uuid")
		}
	})

	t.Run("backend/bucket name falls back to principal tenant", func(t *testing.T) {
		gotT, gotK, err := parseSimulateResource("storageBackends/s3-eu", fallback)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if gotT != fallback || gotK != "" {
			t.Fatalf("got (%v,%q), want fallback %v/empty", gotT, gotK, fallback)
		}
	})
}

// The simulated principal must carry the credential KIND, or a simulation
// cannot reproduce a decision from a policy that reads `principal.kind` — the
// built-in permit letting a machine delete its own objects is one — and would
// answer "denied" for a call that succeeds in production.
func TestSimulateAuthz_CarriesThePrincipalKind(t *testing.T) {
	tid := uuid.New()
	fe := allowEngine()
	h := NewHandler(fe, &fakeStore{})

	_, err := h.SimulateAuthz(authedCtx(tid), SimulateAuthzInput{
		PrincipalSubject: "acme",
		PrincipalKind:    "capability",
		Action:           "DeleteObject",
		ResourceName:     "tenants/" + tid.String() + "/collections/k",
	})
	if err != nil {
		t.Fatalf("SimulateAuthz: %v", err)
	}
	// Two calls: the InspectPolicy gate, then the simulation itself.
	if len(fe.calls) == 0 {
		t.Fatal("the engine was never asked")
	}
	last := fe.calls[len(fe.calls)-1]
	if last.princ.Kind != "capability" {
		t.Fatalf("simulated principal kind = %q, want %q", last.princ.Kind, "capability")
	}
}
