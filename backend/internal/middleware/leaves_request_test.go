package middleware

import (
	"context"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// Every unary interceptor shares the request message with the layers around
// it, and must leave it as it came. Each test below runs a call that takes
// the interceptor down the path where it reads the request, behind
// unarytest.LeavesRequest, which fails the test if the request changed.

const (
	// leavesTimezone is a field the request carries, so a layer that clears
	// or rewrites the request shows in the comparison.
	leavesTimezone = "Europe/Kyiv"
	// leavesSubject is the login a rate-limited attempt names.
	leavesSubject = "alice"
	// leavesSizeHint is an upload's declared size, well under leavesByteCap.
	leavesSizeHint = int64(1 << 10)
	// leavesByteCap is a cap the upload is checked against and fits under.
	leavesByteCap = int64(1 << 20)
	// leavesBackend and leavesBucket are where the upload's collection is
	// bound.
	leavesBackend = "primary"
	leavesBucket  = "docs"
	// leavesPlatformSubject is the platform admin that names other tenants.
	leavesPlatformSubject = "ops"
)

// platformAdminCtx is a context for a platform admin of tenant own.
func platformAdminCtx(own uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: own, Subject: leavesPlatformSubject, Roles: []string{apiutil.RolePlatformAdmin},
	})
}

// batchHandler answers BatchUpdateTags and keeps the context it ran under.
type batchHandler struct {
	paladindatav1connect.UnimplementedBatchServiceHandler
	ctx context.Context
}

func (h *batchHandler) BatchUpdateTags(ctx context.Context, _ *datav1.BatchUpdateTagsRequest) (*datav1.Operation, error) {
	h.ctx = ctx
	return &datav1.Operation{}, nil
}

// callBatchUpdateTags calls h through the interceptors with a request naming
// parent.
func callBatchUpdateTags(ctx context.Context, h *batchHandler, parent string, interceptors ...connect.ServerInterceptor) error {
	client := paladindatav1connect.NewBatchServiceClient(unarytest.Client(func(s *connect.Server) {
		paladindatav1connect.RegisterBatchServiceHandler(s, h)
	}, interceptors...))
	_, err := client.BatchUpdateTags(ctx, &datav1.BatchUpdateTagsRequest{
		Parent: parent,
		Tags:   map[string]string{"team": "ops"},
	})
	return err
}

// The audit log reads the request after the handler returns, to record it.
func TestAuditLeavesTheRequest(t *testing.T) {
	w := &recordingAuditWriter{}
	svc := &stubSettings{}
	client := paladiniamv1connect.NewUserSettingsServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterUserSettingsServiceHandler(s, svc)
	}, unarytest.LeavesRequest(t), AuditWithMirror(w, auth.AudienceData, false, nil)))
	// A mutation, so the interceptor writes a row from the request.
	if _, err := client.UpdateMine(principalCtx(uuid.New()), &iamv1.UpdateMineRequest{Timezone: leavesTimezone}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if svc.updateCalls != 1 || len(w.rows) != 1 {
		t.Fatalf("handler ran %d times and %d rows were written, want 1 and 1", svc.updateCalls, len(w.rows))
	}
}

// uploadHandler answers UploadObject and counts the calls that reach it.
type uploadHandler struct {
	paladindatav1connect.UnimplementedObjectServiceHandler
	calls int
}

func (h *uploadHandler) UploadObject(context.Context, *datav1.UploadObjectRequest) (*datav1.UploadObjectResponse, error) {
	h.calls++
	return &datav1.UploadObjectResponse{}, nil
}

// The quota check reads the upload's size and collection, against both the
// tenant's cap and the bucket's.
func TestQuotaLeavesTheRequest(t *testing.T) {
	tenant := uuid.New()
	reader := &scopedReader{
		tenant: admindomain.Quota{MaxTotalBytes: new(leavesByteCap)},
		bucket: admindomain.Quota{MaxTotalBytes: new(leavesByteCap)},
	}
	bindings := &fakeBindings{backendID: leavesBackend, bucket: leavesBucket}
	h := &uploadHandler{}
	client := paladindatav1connect.NewObjectServiceClient(unarytest.Client(func(s *connect.Server) {
		paladindatav1connect.RegisterObjectServiceHandler(s, h)
	}, unarytest.LeavesRequest(t), NewQuotaSoftCheck(reader).WithBucketScope(bindings).Interceptor()))
	if _, err := client.UploadObject(principalCtx(tenant), &datav1.UploadObjectRequest{
		Parent:        collectionIn(tenant),
		SizeHintBytes: leavesSizeHint,
	}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if h.calls != 1 || reader.askedBcket != leavesBucket {
		t.Fatalf("handler ran %d times, bucket quota read for %q; want 1 and %q", h.calls, reader.askedBcket, leavesBucket)
	}
}

// The idempotency store reads the request's key and fingerprints the request.
func TestIdempotencyLeavesTheRequest(t *testing.T) {
	store := newMemStore()
	svc := &stubSystem{}
	client := paladiniamv1connect.NewUserSettingsServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterUserSettingsServiceHandler(s, svc)
	}, unarytest.LeavesRequest(t), NewIdempotencyInterceptor(store, IdempotencyConfig{TTL: time.Minute})))
	ctx := withIdempotencyKey(principalCtx(uuid.New()), uuid.New().String())
	if _, err := client.UpdateMine(ctx, &iamv1.UpdateMineRequest{Timezone: leavesTimezone}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if svc.calls != 1 || len(store.data) != 1 {
		t.Fatalf("handler ran %d times and %d responses were stored, want 1 and 1", svc.calls, len(store.data))
	}
}

// The freeze walks the request for the tenants it names.
func TestTenantFreezeLeavesTheRequest(t *testing.T) {
	own, live := uuid.New(), uuid.New()
	states := &fakeStates{states: map[uuid.UUID]auth.TenantState{live: auth.TenantLive}}
	h := &batchHandler{}
	if err := callBatchUpdateTags(platformAdminCtx(own), h, collectionIn(live),
		unarytest.LeavesRequest(t), TenantFreeze(states, fakeSlugs{})); err != nil {
		t.Fatalf("call: %v", err)
	}
	if h.ctx == nil || states.reads != 1 {
		t.Fatalf("handler reached: %v, tenant states read: %d; want true and 1", h.ctx != nil, states.reads)
	}
}

// The acting-tenant step walks the request for the tenant a platform admin
// names.
func TestActOnNamedTenantLeavesTheRequest(t *testing.T) {
	own, target := uuid.New(), uuid.New()
	h := &batchHandler{}
	if err := callBatchUpdateTags(platformAdminCtx(own), h, collectionIn(target),
		unarytest.LeavesRequest(t), ActOnNamedTenant()); err != nil {
		t.Fatalf("call: %v", err)
	}
	if h.ctx == nil {
		t.Fatal("the handler was not reached")
	}
	if got, ok := auth.ActingTenant(h.ctx); !ok || got != target {
		t.Fatalf("acting tenant = %s (set=%v), want %s", got, ok, target)
	}
}

// loginHandler answers Login and counts the attempts that reach it.
type loginHandler struct {
	paladiniamv1connect.UnimplementedAuthServiceHandler
	calls int
}

func (h *loginHandler) Login(context.Context, *iamv1.LoginRequest) (*iamv1.LoginResponse, error) {
	h.calls++
	return &iamv1.LoginResponse{}, nil
}

// The login limiter reads the subject an attempt names, to key its bucket.
func TestLoginRateLimitLeavesTheRequest(t *testing.T) {
	l := NewLoginRateLimiter(0, 0)
	h := &loginHandler{}
	client := paladiniamv1connect.NewAuthServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterAuthServiceHandler(s, h)
	}, unarytest.LeavesRequest(t), l.Interceptor()))
	if _, err := client.Login(context.Background(), &iamv1.LoginRequest{Subject: leavesSubject}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if h.calls != 1 || len(l.buckets) == 0 {
		t.Fatalf("handler ran %d times and %d buckets were kept, want 1 and some", h.calls, len(l.buckets))
	}
}
