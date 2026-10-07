package capabilityh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/capability"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// failingInsertStore is fakeStore whose Insert fails as the Postgres store
// does: with a sentinel for a tenant it cannot issue for, or with the
// driver's own error.
type failingInsertStore struct {
	fakeStore
	err error
}

func (s *failingInsertStore) Insert(context.Context, capability.Capability, capability.Principal) error {
	return s.err
}

// errDriver is what an outage looks like on the way up: the store's
// wrapping around text from the database that a caller must not act on.
var errDriver = errors.New(`capability/postgres: insert: ERROR: insert or update on table "capability_records" violates foreign key constraint (SQLSTATE 23503)`)

func issueRequest(tenant uuid.UUID) *connect.Request[adminv1.CapabilityServiceIssueRequest] {
	return connect.NewRequest(&adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_SERVICE, TenantId: tenant.String(), Subject: "svc",
		},
		Audience:   []string{capability.AudiencePlaneData},
		TtlSeconds: 300,
		Caveats:    &adminv1.CapabilityCaveats{Ops: []string{string(capability.OpGet)}},
	})
}

// issuanceCases is every kind an issuance can fail by, with the answer the
// caller must get: before, every one of them was InvalidArgument — an outage
// told the caller not to retry, and a tenant still being provisioned looked
// like a malformed request.
var issuanceCases = []struct {
	name   string
	err    error
	code   connect.Code
	reason commonv1.ErrorReason
}{
	{"an unknown tenant", fmt.Errorf("%w: %s", capability.ErrUnknownTenant, uuid.NewString()), connect.CodeNotFound, commonv1.ErrorReason_ERROR_REASON_TENANT_NOT_FOUND},
	{"a deleted tenant", fmt.Errorf("%w: %s", capability.ErrTenantDeleted, uuid.NewString()), connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_TENANT_ALREADY_DELETED},
	{"a store failure", errDriver, connect.CodeInternal, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED},
}

func TestIssueAnswersAFailureByItsKind(t *testing.T) {
	for _, tc := range issuanceCases {
		t.Run(tc.name, func(t *testing.T) {
			store := &failingInsertStore{err: tc.err}
			h := NewHandler(mkIssuer(t, store), store, nil, &allowAuthorizer{})
			_, err := h.Issue(adminCtx(), issueRequest(uuid.New()))
			assertKind(t, err, tc.code, tc.reason)
		})
	}
}

func TestDelegateAnswersAFailureByItsKind(t *testing.T) {
	for _, tc := range issuanceCases {
		t.Run(tc.name, func(t *testing.T) {
			parent := mkParent(uuid.New(), capability.OpGet, capability.OpShare)
			store := &failingInsertStore{fakeStore: fakeStore{cap: &parent}, err: tc.err}
			h := NewHandler(mkIssuer(t, store), store, nil, &denyAuthorizer{})
			_, err := h.Delegate(auth.WithCapability(context.Background(), &parent), connect.NewRequest(&adminv1.CapabilityServiceDelegateRequest{
				ParentId: parent.ID.String(), TtlSeconds: 60,
			}))
			assertKind(t, err, tc.code, tc.reason)
		})
	}
}

// A malformed request is still InvalidArgument: the change maps failures that
// are not the request's away from it, not the request's own.
func TestIssueStillRefusesAMalformedRequest(t *testing.T) {
	store := &fakeStore{}
	h := NewHandler(mkIssuer(t, store), store, nil, &allowAuthorizer{})
	req := issueRequest(uuid.New())
	req.Msg.Audience = []string{""}
	_, err := h.Issue(adminCtx(), req)
	assertKind(t, err, connect.CodeInvalidArgument, commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
}

func assertKind(t *testing.T, err error, code connect.Code, reason commonv1.ErrorReason) {
	t.Helper()
	if got := reasonOf(t, err); connect.CodeOf(err) != code || got != reason {
		t.Fatalf("err = %v (reason %v), want %v with %v", err, got, code, reason)
	}
	if code == connect.CodeInternal {
		return // the message of an Internal error is the server-wide scrubber's to clear
	}
	for _, leak := range []string{"capability_records", "SQLSTATE", "fkey"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("message %q carries %q", err.Error(), leak)
		}
	}
}

// reasonOf is the ErrorInfo reason a server error carries in Paladin's
// domain, as a client's typed errors read it.
func reasonOf(t *testing.T, err error) commonv1.ErrorReason {
	t.Helper()
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED
	}
	for _, d := range cerr.Details() {
		v, verr := d.Value()
		if verr != nil {
			t.Fatalf("detail does not decode: %v", verr)
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok && info.GetDomain() == paladin.ErrorDomain {
			return commonv1.ErrorReason(commonv1.ErrorReason_value[info.GetReason()])
		}
	}
	return commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED
}

// failingGetStore fails the parent lookup as a store outage does.
type failingGetStore struct{ fakeStore }

func (*failingGetStore) Get(context.Context, uuid.UUID) (*capability.Capability, error) {
	return nil, errDriver
}

// The admin path answered NotFound for any failure to read the parent, an
// outage included, so a caller dropped a parent that was there.
func TestDelegateTellsAMissingParentFromAStoreFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		store capability.Store
		code  connect.Code
	}{
		"a missing parent": {&fakeStore{}, connect.CodeNotFound},
		"a store failure":  {&failingGetStore{}, connect.CodeInternal},
	} {
		t.Run(name, func(t *testing.T) {
			h := NewHandler(mkIssuer(t, tc.store), tc.store, nil, &allowAuthorizer{})
			_, err := h.Delegate(adminCtx(), connect.NewRequest(&adminv1.CapabilityServiceDelegateRequest{
				ParentId: uuid.NewString(), TtlSeconds: 60,
			}))
			if connect.CodeOf(err) != tc.code {
				t.Errorf("err = %v, want %v", err, tc.code)
			}
		})
	}
}
