package capabilityh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
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

func (s *failingInsertStore) Insert(context.Context, limes.InsertRequest) error {
	return s.err
}

// errDriver is what an outage looks like on the way up: the store's
// wrapping around text from the database that a caller must not act on.
var errDriver = errors.New(`capability/postgres: insert: ERROR: insert or update on table "capability_records" violates foreign key constraint (SQLSTATE 23503)`)

func issueRequest(tenant uuid.UUID) *adminv1.CapabilityServiceIssueRequest {
	return &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_SERVICE, TenantId: tenant.String(), Subject: "svc",
		},
		Audience:   []string{auth.TokenPlaneData},
		TtlSeconds: 300,
		Caveats:    openCaveats(limes.OpGet),
	}
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
	{"an unknown tenant", fmt.Errorf("%w: %s", limes.ErrUnknownTenant, uuid.New().String()), connect.CodeNotFound, commonv1.ErrorReason_ERROR_REASON_TENANT_NOT_FOUND},
	{"a deleted tenant", fmt.Errorf("%w: %s", limes.ErrTenantDeleted, uuid.New().String()), connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_TENANT_ALREADY_DELETED},
	{"a store failure", errDriver, connect.CodeUnavailable, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED},
}

func TestIssueAnswersAFailureByItsKind(t *testing.T) {
	for _, tc := range issuanceCases {
		t.Run(tc.name, func(t *testing.T) {
			store := &failingInsertStore{err: tc.err}
			h := mkHandler(t, store, nil, &allowAuthorizer{})
			_, err := h.Issue(adminCtx(), issueRequest(uuid.New()))
			assertKind(t, err, tc.code, tc.reason)
		})
	}
}

func TestDelegateAnswersAFailureByItsKind(t *testing.T) {
	for _, tc := range issuanceCases {
		t.Run(tc.name, func(t *testing.T) {
			store := &failingInsertStore{}
			m := newMinter(t, store)
			// The parent is recorded; only the child's insert fails.
			parent := mintParent(t, m, limes.OpGet, limes.OpShare)
			store.err = tc.err
			h := NewHandler(m.Issuer, m.Verifier, store, nil, &denyAuthorizer{})
			_, err := h.Delegate(auth.WithCapability(context.Background(), parent), delegateReq(parent))
			assertKind(t, err, tc.code, tc.reason)
		})
	}
}

// A malformed request is still InvalidArgument: the change maps failures that
// are not the request's away from it, not the request's own.
func TestIssueStillRefusesAMalformedRequest(t *testing.T) {
	store := &fakeStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})
	req := issueRequest(uuid.New())
	req.Audience = []string{""}
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
		v, verr := connectproto.UnmarshalErrorDetail(d)
		if verr != nil {
			t.Fatalf("detail does not decode: %v", verr)
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok && info.GetDomain() == paladin.ErrorDomain {
			return commonv1.ErrorReason(commonv1.ErrorReason_value[info.GetReason()])
		}
	}
	return commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED
}

// A top-level capability has no parent whose ceilings an unset one could
// keep; issuing it frozen, or unlimited, unasked would be the old 0 again.
func TestIssueRefusesAnUnsetCeiling(t *testing.T) {
	for name, clear := range map[string]func(*adminv1.CapabilityCaveats){
		"requests": func(c *adminv1.CapabilityCaveats) { c.RequestCeiling = nil },
		"budget":   func(c *adminv1.CapabilityCaveats) { c.BudgetCeiling = nil },
	} {
		t.Run(name, func(t *testing.T) {
			store := &failingInsertStore{err: errDriver}
			h := mkHandler(t, store, nil, &allowAuthorizer{})
			req := issueRequest(uuid.New())
			clear(req.Caveats)
			_, err := h.Issue(adminCtx(), req)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("err = %v, want InvalidArgument before the store is reached", err)
			}
		})
	}
}

// The lineage refusals only a delegation meets: a chain past
// MaxDelegationDepth is a parent that can no longer delegate, and a child
// wider than its parent — another tenant among them — is a denial.
func TestDelegateAnswersALineageRefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code connect.Code
	}{
		{"a chain too deep", fmt.Errorf("%w: %s", limes.ErrDelegationTooDeep, uuid.New().String()), connect.CodeFailedPrecondition},
		{"a child in another tenant", fmt.Errorf("%w: tenant %s", limes.ErrDelegationTooWide, uuid.New().String()), connect.CodePermissionDenied},
		{"a revoked parent", fmt.Errorf("%w: parent", limes.ErrRevoked), connect.CodeFailedPrecondition},
		{"an expired parent", fmt.Errorf("%w: parent", limes.ErrExpired), connect.CodeFailedPrecondition},
		{"a unit other than the parent's", fmt.Errorf("%w: EUR", limes.ErrUnitCodeMismatch), connect.CodePermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &failingInsertStore{}
			m := newMinter(t, store)
			parent := mintParent(t, m, limes.OpGet, limes.OpShare)
			store.err = tc.err
			h := NewHandler(m.Issuer, m.Verifier, store, nil, &denyAuthorizer{})
			_, err := h.Delegate(auth.WithCapability(context.Background(), parent), delegateReq(parent))
			if got := connect.CodeOf(err); got != tc.code {
				t.Fatalf("err = %v (%v), want %v", err, got, tc.code)
			}
		})
	}
}

// Every operation's rows name codes limes defines: a code a release renames
// would otherwise leave its row dead and the table answering instead.
func TestExceptionsNameLimesCodes(t *testing.T) {
	for name, e := range map[string]auth.Exceptions{
		"issuance": issuanceExceptions, "delegation": delegationExceptions,
		"record": recordExceptions, "list": listExceptions,
	} {
		if undefined := e.Undefined(); len(undefined) > 0 {
			t.Errorf("%s exceptions name %v, which limes does not define", name, undefined)
		}
	}
}
