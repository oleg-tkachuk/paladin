package userh

import (
	"context"
	"errors"
	"slices"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/limes"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// fakeLister records the listing asked of it and the tenant acted for.
type fakeLister struct {
	page   limes.Page[limes.RecordListing]
	err    error
	asked  limes.ListRecordsRequest
	acting uuid.UUID
}

func (f *fakeLister) ListRecords(ctx context.Context, req limes.ListRecordsRequest) (limes.Page[limes.RecordListing], error) {
	f.asked = req
	f.acting, _ = auth.ActingTenant(ctx)
	return f.page, f.err
}

func capabilityFixture(t *testing.T) (authstore.User, *fakeLister, *recordingAuthorizer, *Handler) {
	t.Helper()
	u := authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "alice"}
	l := &fakeLister{page: limes.Page[limes.RecordListing]{
		Items: []limes.RecordListing{{Disposition: limes.RecordDispositionActive}},
		Next:  "next",
	}}
	az := &recordingAuthorizer{}
	return u, l, az, NewHandler(&fakeUserRepo{user: u}, az).WithCapabilities(l)
}

func TestListUserCapabilities(t *testing.T) {
	t.Run("the user's, active, newest first", func(t *testing.T) {
		u, l, az, h := capabilityFixture(t)
		got, err := h.ListUserCapabilities(ctxAs(u.TenantID), ref(u), UserCapabilitiesPage{Size: 7, Token: "cursor"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 1 || got.Next != "next" {
			t.Errorf("page = %d items, next %q; want the lister's", len(got.Items), got.Next)
		}
		want := limes.ListRecordsRequest{
			TenantID: u.TenantID, OnBehalfOf: u.UserID.String(),
			Dispositions: []limes.RecordDisposition{limes.RecordDispositionActive},
			Order:        limes.ListOrderDescending, Cursor: "cursor", Limit: 7,
		}
		if l.asked.TenantID != want.TenantID || l.asked.OnBehalfOf != want.OnBehalfOf ||
			!slices.Equal(l.asked.Dispositions, want.Dispositions) || l.asked.Order != want.Order ||
			l.asked.Cursor != want.Cursor || l.asked.Limit != want.Limit {
			t.Errorf("asked %+v, want %+v", l.asked, want)
		}
		if l.acting != u.TenantID {
			t.Errorf("acted for tenant %s, want the user's %s", l.acting, u.TenantID)
		}
		if az.action != cedar.ActionReadUserCapabilities {
			t.Errorf("authorized %s, want %s", az.action, cedar.ActionReadUserCapabilities)
		}
	})

	t.Run("the dispositions named", func(t *testing.T) {
		u, l, _, h := capabilityFixture(t)
		named := []limes.RecordDisposition{limes.RecordDispositionExpired, limes.RecordDispositionRevoked}
		if _, err := h.ListUserCapabilities(ctxAs(u.TenantID), ref(u), UserCapabilitiesPage{Dispositions: named}); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(l.asked.Dispositions, named) {
			t.Errorf("dispositions = %v, want %v", l.asked.Dispositions, named)
		}
	})

	t.Run("another tenant's user, for a platform admin", func(t *testing.T) {
		u, l, _, h := capabilityFixture(t)
		if _, err := h.ListUserCapabilities(ctxAs(uuid.New(), "platform.admin"), ref(u), UserCapabilitiesPage{}); err != nil {
			t.Fatal(err)
		}
		if l.asked.TenantID != u.TenantID || l.acting != u.TenantID {
			t.Errorf("listed tenant %s acting %s, want the user's %s", l.asked.TenantID, l.acting, u.TenantID)
		}
	})
}

func TestListUserCapabilitiesRefusals(t *testing.T) {
	u, l, _, h := capabilityFixture(t)

	if _, err := h.ListUserCapabilities(ctxAs(uuid.New(), "tenant.admin"), ref(u), UserCapabilitiesPage{}); code(err) != connect.CodeNotFound {
		t.Errorf("another tenant's user: code = %v, want NotFound", code(err))
	}
	denied := NewHandler(&fakeUserRepo{user: u}, denyAuthorizer{}).WithCapabilities(l)
	if _, err := denied.ListUserCapabilities(ctxAs(u.TenantID), ref(u), UserCapabilitiesPage{}); code(err) != connect.CodePermissionDenied {
		t.Errorf("denied by policy: code = %v, want PermissionDenied", code(err))
	}
	bare := NewHandler(&fakeUserRepo{user: u}, allowAuthorizer{})
	if _, err := bare.ListUserCapabilities(ctxAs(u.TenantID), ref(u), UserCapabilitiesPage{}); code(err) != connect.CodeUnimplemented {
		t.Errorf("no lister: code = %v, want Unimplemented", code(err))
	}
	if _, err := h.ListUserCapabilities(context.Background(), ref(u), UserCapabilitiesPage{}); code(err) != connect.CodeUnauthenticated {
		t.Errorf("anonymous: code = %v, want Unauthenticated", code(err))
	}
}

func TestListUserCapabilitiesStoreErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		want   connect.Code
		reason commonv1.ErrorReason
	}{
		{"a page token of another listing", limes.ErrCursorMismatch, connect.CodeInvalidArgument, commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT},
		{"a malformed request", limes.ErrInvalidRequest, connect.CodeInvalidArgument, commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT},
		{"the store failing", errors.New("connection reset"), connect.CodeUnavailable, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, l, _, h := capabilityFixture(t)
			l.err = tc.err
			_, err := h.ListUserCapabilities(ctxAs(u.TenantID), ref(u), UserCapabilitiesPage{})
			if code(err) != tc.want || reasonOf(t, err) != tc.reason {
				t.Errorf("code = %v reason %v, want %v reason %v", code(err), reasonOf(t, err), tc.want, tc.reason)
			}
		})
	}
}

// reasonOf is the ErrorInfo reason err carries; UNSPECIFIED for none.
func reasonOf(t *testing.T, err error) commonv1.ErrorReason {
	t.Helper()
	cerr, ok := errors.AsType[*connect.Error](err)
	if !ok {
		t.Fatalf("err = %v, want a connect error", err)
	}
	for _, d := range cerr.Details() {
		if v, derr := connectproto.UnmarshalErrorDetail(d); derr == nil {
			if info, ok := v.(*errdetails.ErrorInfo); ok {
				return commonv1.ErrorReason(commonv1.ErrorReason_value[info.GetReason()])
			}
		}
	}
	return commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED
}
