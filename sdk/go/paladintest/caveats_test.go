package paladintest_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// refused checks that err is the server's refusal of a capability: code,
// reason and message, word for word.
func refused(t *testing.T, err error, code connect.Code, reason commonv1.ErrorReason, message string) {
	t.Helper()
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want %v %q", err, code, message)
	}
	if ce.Code() != code || paladin.Reason(err) != reason || ce.Message() != message {
		t.Errorf("got %v %v %q, want %v %v %q", ce.Code(), paladin.Reason(err), ce.Message(), code, reason, message)
	}
}

// strictCapability issues, through the fake's CapabilityService, a capability
// for the data plane that req's caveats narrow, and connects with it.
func strictCapability(t *testing.T, srv *paladintest.Server, mutate func(*adminv1.CapabilityServiceIssueRequest)) (*paladin.Paladin, *adminv1.Capability) {
	t.Helper()
	admin := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability
	req := issueRequest(srv, paladin.OpGet, paladin.OpList, paladin.OpPut, paladin.OpDelete, paladin.OpManage, paladin.OpObjectPresign)
	if mutate != nil {
		mutate(req)
	}
	resp := issue(t, admin, req)
	return srv.Connect(paladin.WithCapability(resp.GetToken())), resp.GetCapability()
}

// A consumer that issued a capability for the bearer audience, or for
// another plane, saw its unit tests pass and the cluster refuse it.
func TestStrictAuthHoldsACapabilityToItsPlanes(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	for name, tc := range map[string]struct {
		audience []string
		message  string
	}{
		"the bearer audience": {[]string{paladin.AudienceData}, `audience mismatch: token audience [paladin-data] lacks "data"`},
		"another plane":       {paladin.PlaneNames(paladin.PlaneAdmin), `audience mismatch: token audience [admin] lacks "data"`},
	} {
		t.Run(name, func(t *testing.T) {
			p, _ := strictCapability(t, srv, func(r *adminv1.CapabilityServiceIssueRequest) { r.Audience = tc.audience })
			refused(t, getObject(p, obj), connect.CodePermissionDenied, commonv1.ErrorReason_ERROR_REASON_CAPABILITY_AUDIENCE_MISMATCH, tc.message)
		})
	}
}

// An operation the caveats do not name, a resource outside them, and an
// operation bound to no resource under a resource-restricted capability are
// refused as limes refuses them, without the library's name.
func TestStrictAuthHoldsACallToTheCaveats(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	uri := paladin.ObjectResource(srv.Tenant(), paladintest.DefaultCollection, "k")
	elsewhere := paladin.ObjectResource(srv.Tenant(), "elsewhere", "")
	ctx := context.Background()

	listOnly, _ := strictCapability(t, srv, func(r *adminv1.CapabilityServiceIssueRequest) {
		r.Caveats.Ops = paladin.OpNames(paladin.OpList)
	})
	refused(t, getObject(listOnly, obj), connect.CodePermissionDenied, commonv1.ErrorReason_ERROR_REASON_CAPABILITY_OP_NOT_ALLOWED,
		`operation not allowed: "get" not in [list]`)

	restricted, _ := strictCapability(t, srv, func(r *adminv1.CapabilityServiceIssueRequest) {
		r.Caveats.ResourcePrefixes = []string{elsewhere}
	})
	refused(t, getObject(restricted, obj), connect.CodePermissionDenied, commonv1.ErrorReason_ERROR_REASON_CAPABILITY_RESOURCE_NOT_ALLOWED,
		`resource not allowed: "`+uri+`"`)
	_, err := restricted.Data.StorageBootstrap.EnsureTenantStorage(ctx, &datav1.EnsureTenantStorageRequest{BackendId: "b", Bucket: "bucket"})
	refused(t, err, connect.CodePermissionDenied, commonv1.ErrorReason_ERROR_REASON_CAPABILITY_RESOURCE_NOT_ALLOWED,
		`resource not allowed: operation "manage" is not bound to a resource, and the capability is resource-restricted`)

	// A prefix that holds the object lets it through, and a listing is asked
	// for by the collection's own prefix.
	scoped, _ := strictCapability(t, srv, func(r *adminv1.CapabilityServiceIssueRequest) {
		r.Caveats.ResourcePrefixes = []string{paladin.ObjectResource(srv.Tenant(), paladintest.DefaultCollection, "")}
	})
	if err := getObject(scoped, obj); err != nil {
		t.Errorf("an object under the prefix: %v", err)
	}
	if _, err := scoped.Data.Object.ListObjects(ctx, &datav1.ListObjectsRequest{Parent: srv.Collection().String()}); err != nil {
		t.Errorf("the collection's listing: %v", err)
	}

	// What the RPC looks up comes first: an object that is not there is
	// NotFound, whatever the capability allows.
	missing := &datav1.Object{Name: srv.Collection().String() + "/objects/missing"}
	if err := getObject(listOnly, missing); !errors.Is(err, paladin.ErrNotFound) {
		t.Errorf("a missing object: %v, want NotFound", err)
	}
}

// The request ceiling is counted on every request, the capability's own
// first and then each ancestor's, and the request past it is refused.
func TestStrictAuthCountsTheRequestCeiling(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	const limit = 2
	p, _ := strictCapability(t, srv, func(r *adminv1.CapabilityServiceIssueRequest) {
		r.Caveats.RequestCeiling = &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: limit}
	})
	for range limit {
		if err := getObject(p, obj); err != nil {
			t.Fatalf("within the ceiling: %v", err)
		}
	}
	refused(t, getObject(p, obj), connect.CodeResourceExhausted, commonv1.ErrorReason_ERROR_REASON_CAPABILITY_REQUEST_LIMIT_EXCEEDED,
		"request limit exceeded")

	// A parent with room for its Delegate and one request of its child.
	parent, parentCap := strictCapability(t, srv, func(r *adminv1.CapabilityServiceIssueRequest) {
		r.Caveats.Ops = append(r.Caveats.Ops, string(paladin.OpShare))
		r.Caveats.RequestCeiling = &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: limit}
	})
	child, err := parent.Admin.Capability.Delegate(context.Background(), &adminv1.CapabilityServiceDelegateRequest{
		ParentId: parentCap.GetId(),
		Caveats:  &adminv1.CapabilityCaveats{Ops: paladin.OpNames(paladin.OpGet)},
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	asChild := srv.Connect(paladin.WithCapability(child.GetToken()))
	if err := getObject(asChild, obj); err != nil {
		t.Fatalf("the child's first request: %v", err)
	}
	refused(t, getObject(asChild, obj), connect.CodeResourceExhausted, commonv1.ErrorReason_ERROR_REASON_CAPABILITY_REQUEST_LIMIT_EXCEEDED,
		"request limit exceeded: ancestor "+parentCap.GetId())
}

// Without WithStrictAuth, the fake serves every call, as before.
func TestCaveatsAreCheckedOnlyUnderStrictAuth(t *testing.T) {
	srv := paladintest.New(t)
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	p, _ := strictCapability(t, srv, func(r *adminv1.CapabilityServiceIssueRequest) {
		r.Audience = paladin.PlaneNames(paladin.PlaneAdmin)
		r.Caveats.Ops = paladin.OpNames(paladin.OpList)
	})
	if err := getObject(p, obj); err != nil {
		t.Errorf("err = %v, want the object", err)
	}
}
