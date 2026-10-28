package apiutil

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// recordingPolicy answers one decision and keeps the request it decided.
type recordingPolicy struct {
	decision cedar.Decision
	err      error
	called   bool
	resource *cedar.Resource
	tenant   uuid.UUID
	request  cedar.RequestContext
}

func (r *recordingPolicy) IsAuthorized(_ context.Context, p *cedar.Principal, _ cedar.Action, res *cedar.Resource, rc cedar.RequestContext) (cedar.Decision, error) {
	r.called, r.resource, r.tenant, r.request = true, res, p.TenantID, rc
	return r.decision, r.err
}

func TestAuthorize(t *testing.T) {
	caller := &auth.Principal{Subject: "alice", TenantID: uuid.New(), TenantSlug: "acme"}
	asCaller := auth.WithPrincipal(context.Background(), caller)
	resource := &cedar.Resource{TenantID: uuid.New(), Collection: "docs"}
	engineDown := errors.New("policy store down")

	for _, tc := range []struct {
		name     string
		ctx      context.Context
		policy   *recordingPolicy
		want     connect.Code
		asksWith bool
	}{
		{"an allowed caller", asCaller, &recordingPolicy{decision: cedar.DecisionAllow}, 0, true},
		{"a denied caller", asCaller, &recordingPolicy{decision: cedar.DecisionDeny}, connect.CodePermissionDenied, true},
		{"an engine that fails", asCaller, &recordingPolicy{err: engineDown}, connect.CodeInternal, true},
		{"no caller", context.Background(), &recordingPolicy{decision: cedar.DecisionAllow}, connect.CodeUnauthenticated, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Authorize(tc.ctx, tc.policy, cedar.ActionInspectPolicy, resource)
			if got := connect.CodeOf(err); err != nil && got != tc.want || err == nil && tc.want != 0 {
				t.Fatalf("err = %v, want code %v", err, tc.want)
			}
			if tc.want == 0 && p != caller {
				t.Errorf("caller = %+v, want the principal on the context", p)
			}
			if tc.policy.called != tc.asksWith {
				t.Errorf("asked the policy: %v, want %v", tc.policy.called, tc.asksWith)
			}
			if tc.asksWith && tc.policy.resource != resource {
				t.Errorf("decided on %+v, want %+v", tc.policy.resource, resource)
			}
			if tc.want == connect.CodeInternal && !errors.Is(err, engineDown) {
				t.Errorf("err = %v, want the engine's failure as its cause", err)
			}
		})
	}
}

// A gate on the caller's own tenant decides on that tenant, by id and slug.
func TestAuthorizeOwnTenant(t *testing.T) {
	caller := &auth.Principal{Subject: "alice", TenantID: uuid.New(), TenantSlug: "acme"}
	policy := &recordingPolicy{decision: cedar.DecisionAllow}
	p, err := AuthorizeOwnTenant(auth.WithPrincipal(context.Background(), caller), policy, cedar.ActionIssueCapability)
	if err != nil || p != caller {
		t.Fatalf("AuthorizeOwnTenant = %+v, %v; want the caller", p, err)
	}
	if r := policy.resource; r.TenantID != caller.TenantID || r.TenantSlug != caller.TenantSlug {
		t.Errorf("decided on tenant %s %q, want the caller's %s %q", r.TenantID, r.TenantSlug, caller.TenantID, caller.TenantSlug)
	}
}

// Deciding as a given principal asks with it, not with the context's, and
// with the request the caller describes: an upload's size and type.
func TestDecide(t *testing.T) {
	const size, contentType = 42, "text/plain"
	pinned := uuid.New()
	policy := &recordingPolicy{decision: cedar.DecisionDeny}
	err := Decide(context.Background(), policy, &cedar.Principal{TenantID: pinned}, cedar.ActionInspectPolicy, &cedar.Resource{},
		cedar.RequestContext{SizeBytes: size, ContentType: contentType})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("err = %v, want permission denied", err)
	}
	if policy.tenant != pinned {
		t.Errorf("asked as tenant %s, want %s", policy.tenant, pinned)
	}
	if policy.request.SizeBytes != size || policy.request.ContentType != contentType {
		t.Errorf("asked with %+v, want size %d and type %q", policy.request, size, contentType)
	}
}

// errBrokenPolicy stands for an engine failure a domain registers a code for,
// as collectionh registers cedar.ErrPolicyUnparseable.
var errBrokenPolicy = errors.New("tenant policy does not parse")

func init() {
	RegisterError(errBrokenPolicy, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_POLICY_UNPARSEABLE)
}

// An engine failure the domain gave a code is answered with that code, not as
// the server's own fault.
func TestTheGateMapsARegisteredEngineFailure(t *testing.T) {
	asCaller := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "alice", TenantID: uuid.New()})
	_, err := Authorize(asCaller, &recordingPolicy{err: errBrokenPolicy}, cedar.ActionSetObjectTaint, &cedar.Resource{})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !errors.Is(err, errBrokenPolicy) {
		t.Errorf("err = %v, want FailedPrecondition caused by the engine's failure", err)
	}
}
