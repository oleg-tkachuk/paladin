package paladintest_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// copyRequests is a request ceiling small enough to reach in a test.
const copyRequests = 2

// sealed issues a capability with ops for the data and admin planes and
// returns its Biscuit, which the fake seals as the server does.
func sealed(t *testing.T, srv *paladintest.Server, mutate func(*adminv1.CapabilityServiceIssueRequest), ops ...paladin.Op) *adminv1.CapabilityServiceIssueResponse {
	t.Helper()
	admin := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability
	req := issueRequest(srv, ops...)
	if mutate != nil {
		mutate(req)
	}
	resp := issue(t, admin, req)
	if _, err := paladin.BiscuitOf(resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// attenuated is token narrowed by a, which paladin.Attenuate must accept.
func attenuated(t *testing.T, token string, a paladin.Attenuation) string {
	t.Helper()
	out, err := paladin.Attenuate(token, a)
	if err != nil {
		t.Fatalf("Attenuate: %v", err)
	}
	return out
}

func ensureStorage(p *paladin.Paladin) error {
	_, err := p.Data.StorageBootstrap.EnsureTenantStorage(context.Background(), &datav1.EnsureTenantStorageRequest{BackendId: "b", Bucket: "bucket"})
	return err
}

// A consumer narrows the Biscuit it was issued and hands the copy on; the
// fake holds the copy to what it was narrowed to, as the server does, and
// the Biscuit it came from keeps what it had.
func TestACopyOfTheBiscuitIsHeldToItsNarrowing(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	other := srv.Put(srv.Collection("other"), "k", "text/plain", []byte("y"))
	resp := sealed(t, srv, nil, paladin.OpGet, paladin.OpManage)
	full := srv.Connect(paladin.WithCapability(resp.GetBiscuit()))

	cases := map[string]struct {
		narrow paladin.Attenuation
		call   func(*paladin.Paladin) error
		reason commonv1.ErrorReason
		// fullServes is whether the Biscuit the copy came from makes the
		// call: it does what the narrowing took away, but not what the
		// call itself cannot do.
		fullServes bool
	}{
		"an operation it dropped": {
			narrow:     paladin.Attenuation{Ops: []paladin.Op{paladin.OpGet}},
			call:       ensureStorage,
			reason:     commonv1.ErrorReason_ERROR_REASON_CAPABILITY_OP_NOT_ALLOWED,
			fullServes: true,
		},
		"a resource outside its prefix": {
			narrow:     paladin.Attenuation{ResourcePrefixes: []string{paladin.ObjectResource(srv.Tenant(), paladintest.DefaultCollection, "")}},
			call:       func(p *paladin.Paladin) error { return getObject(p, other) },
			reason:     commonv1.ErrorReason_ERROR_REASON_CAPABILITY_RESOURCE_NOT_ALLOWED,
			fullServes: true,
		},
		"a plane it dropped": {
			narrow:     paladin.Attenuation{Planes: []paladin.Plane{paladin.PlaneAdmin}},
			call:       func(p *paladin.Paladin) error { return getObject(p, obj) },
			reason:     commonv1.ErrorReason_ERROR_REASON_CAPABILITY_AUDIENCE_MISMATCH,
			fullServes: true,
		},
		"an expiry it passed": {
			narrow:     paladin.Attenuation{ExpiresAt: time.Now().Add(-time.Minute)},
			call:       func(p *paladin.Paladin) error { return getObject(p, obj) },
			reason:     commonv1.ErrorReason_ERROR_REASON_CAPABILITY_EXPIRED,
			fullServes: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			narrowed := srv.Connect(paladin.WithCapability(attenuated(t, resp.GetBiscuit(), tc.narrow)))
			err := tc.call(narrowed)
			if connect.CodeOf(err) != connect.CodePermissionDenied || paladin.Reason(err) != tc.reason {
				t.Errorf("the copy: err = %v (reason %s), want PermissionDenied %s", err, paladin.Reason(err), tc.reason)
			}
			if err := tc.call(full); tc.fullServes && err != nil {
				t.Errorf("the Biscuit it came from: %v, want the call served", err)
			}
		})
	}
	// What the narrowing keeps, the copy is served.
	narrowed := srv.Connect(paladin.WithCapability(attenuated(t, resp.GetBiscuit(), paladin.Attenuation{Ops: []paladin.Op{paladin.OpGet}})))
	if err := getObject(narrowed, obj); err != nil {
		t.Errorf("a get the copy keeps: %v", err)
	}
}

// A block that widens the Biscuit is refused as the server refuses it: the
// whole token, not just the widening.
func TestACopyThatWidensIsRefused(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
	resp := sealed(t, srv, nil, paladin.OpGet)
	wider := srv.Connect(paladin.WithCapability(attenuated(t, resp.GetBiscuit(), paladin.Attenuation{Ops: []paladin.Op{paladin.OpGet, paladin.OpDelete}})))
	err := getObject(wider, obj)
	if connect.CodeOf(err) != connect.CodePermissionDenied || paladin.Reason(err) != commonv1.ErrorReason_ERROR_REASON_CAPABILITY_BISCUIT_ATTENUATION {
		t.Errorf("err = %v (reason %s), want PermissionDenied BISCUIT_ATTENUATION", err, paladin.Reason(err))
	}
}

// A copy stops with the capability it came from, and spends that
// capability's requests.
func TestACopyFollowsItsCapability(t *testing.T) {
	t.Run("revoked", func(t *testing.T) {
		srv := paladintest.New(t, paladintest.WithStrictAuth())
		obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
		resp := sealed(t, srv, nil, paladin.OpGet)
		narrowed := srv.Connect(paladin.WithCapability(attenuated(t, resp.GetBiscuit(), paladin.Attenuation{Ops: []paladin.Op{paladin.OpGet}})))
		admin := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability
		if _, err := admin.Revoke(context.Background(), &adminv1.CapabilityServiceRevokeRequest{Id: resp.GetCapability().GetId()}); err != nil {
			t.Fatal(err)
		}
		wantRefused(t, getObject(narrowed, obj), capabilityRevoked)
	})
	t.Run("its request ceiling", func(t *testing.T) {
		srv := paladintest.New(t, paladintest.WithStrictAuth())
		obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("x"))
		resp := sealed(t, srv, func(r *adminv1.CapabilityServiceIssueRequest) {
			r.Caveats.RequestCeiling = &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: copyRequests}
		}, paladin.OpGet)
		full := srv.Connect(paladin.WithCapability(resp.GetBiscuit()))
		narrowed := srv.Connect(paladin.WithCapability(attenuated(t, resp.GetBiscuit(), paladin.Attenuation{Ops: []paladin.Op{paladin.OpGet}})))
		for _, p := range []*paladin.Paladin{full, narrowed} {
			if err := getObject(p, obj); err != nil {
				t.Fatal(err)
			}
		}
		err := getObject(narrowed, obj)
		if paladin.Reason(err) != commonv1.ErrorReason_ERROR_REASON_CAPABILITY_REQUEST_LIMIT_EXCEEDED {
			t.Errorf("request %d: err = %v, want REQUEST_LIMIT_EXCEEDED", copyRequests+1, err)
		}
	})
}

// A copy cannot delegate, as the server refuses it: its holder attenuates
// it instead.
func TestACopyCannotDelegate(t *testing.T) {
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	resp := sealed(t, srv, nil, paladin.OpGet, paladin.OpShare)
	copied := attenuated(t, resp.GetBiscuit(), paladin.Attenuation{Ops: []paladin.Op{paladin.OpGet, paladin.OpShare}})
	_, err := srv.Connect(paladin.WithCapability(copied)).Admin.Capability.Delegate(context.Background(),
		&adminv1.CapabilityServiceDelegateRequest{ParentId: resp.GetCapability().GetId()})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("err = %v, want FailedPrecondition", err)
	}
}
