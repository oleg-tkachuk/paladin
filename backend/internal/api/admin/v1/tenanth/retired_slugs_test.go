package tenanth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

const (
	testSlug       = "acme"
	testRetiredRef = RetiredSlugNamePrefix + testSlug
)

// A refused slug says what holds it, so a client can point at the trash or
// at the retired slugs instead of parsing the message; errors.Is still
// matches each refusal, wrapped as the handler wraps it.
func TestSlugTakenSaysTheHolder(t *testing.T) {
	for sentinel, want := range map[error]SlugHolder{
		ErrSlugConflict:    SlugHolderLive,
		ErrSlugHeldInTrash: SlugHolderTrashed,
		ErrSlugRetired:     SlugHolderRetired,
	} {
		t.Run(string(want), func(t *testing.T) {
			wrapped := fmt.Errorf("create tenant: %w", sentinel)
			if !errors.Is(wrapped, sentinel) {
				t.Fatal("errors.Is does not see the sentinel through a wrap")
			}
			var ce *connect.Error
			if !errors.As(apiutil.MapError(wrapped), &ce) {
				t.Fatal("MapError did not return a connect error")
			}
			if ce.Code() != connect.CodeAlreadyExists {
				t.Errorf("code = %v, want AlreadyExists", ce.Code())
			}
			info := errorInfo(t, ce)
			if got := info.GetMetadata()[SlugHolderMetadataKey]; got != string(want) {
				t.Errorf("holder = %q, want %q", got, want)
			}
		})
	}
}

func errorInfo(t *testing.T, ce *connect.Error) *errdetails.ErrorInfo {
	t.Helper()
	for _, d := range ce.Details() {
		v, err := connectproto.UnmarshalErrorDetail(d)
		if err != nil {
			t.Fatal(err)
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	t.Fatal("no ErrorInfo on the error")
	return nil
}

func TestListRetiredTenantSlugs(t *testing.T) {
	tid := uuid.New()
	former := uuid.New()
	retired := []RetiredSlug{{Slug: testSlug, FormerTenantID: former, RetiredAt: time.Unix(1, 0)}}

	t.Run("not a platform admin", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		_, err := h.ListRetiredTenantSlugs(principalCtx(tid), 0, "")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, deny())
		_, err := h.ListRetiredTenantSlugs(adminCtx(tid), 0, "")
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("a page, and whether it is enforced", func(t *testing.T) {
		for _, allowReuse := range []bool{false, true} {
			var gotAfter string
			var gotSize int32
			repo := &fakeRepo{listRetiredFn: func(_ context.Context, after string, size int32) ([]RetiredSlug, string, error) {
				gotAfter, gotSize = after, size
				return retired, testSlug, nil
			}}
			authz := allow()
			h := NewHandler(repo, authz)
			h.SetSlugReuse(allowReuse)
			page, err := h.ListRetiredTenantSlugs(adminCtx(tid), 7, "abc")
			if err != nil {
				t.Fatal(err)
			}
			if gotAfter != "abc" || gotSize != 7 {
				t.Errorf("repo asked after %q size %d, want abc 7", gotAfter, gotSize)
			}
			if len(page.Slugs) != 1 || page.Slugs[0] != retired[0] || page.NextToken != testSlug {
				t.Errorf("page = %+v", page)
			}
			if page.Enforced == allowReuse {
				t.Errorf("Enforced = %v with reuse allowed = %v", page.Enforced, allowReuse)
			}
			if authz.lastAction != cedar.ActionReadTenant {
				t.Errorf("authorized %v, want ReadTenant", authz.lastAction)
			}
		}
	})

	t.Run("a store failure is logged and internal", func(t *testing.T) {
		repo := &fakeRepo{listRetiredFn: func(context.Context, string, int32) ([]RetiredSlug, string, error) {
			return nil, "", errors.New("boom")
		}}
		core, logs := observer.New(zapcore.ErrorLevel)
		h := NewHandler(repo, allow())
		h.SetLogger(zap.New(core))
		_, err := h.ListRetiredTenantSlugs(adminCtx(tid), 0, "")
		wantCode(t, err, connect.CodeInternal)
		if logs.Len() != 1 {
			t.Errorf("logged %d errors, want 1", logs.Len())
		}
	})
}

func TestReleaseTenantSlug(t *testing.T) {
	tid := uuid.New()
	former := uuid.New()

	t.Run("not a platform admin", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		wantCode(t, h.ReleaseTenantSlug(principalCtx(tid), testRetiredRef), connect.CodePermissionDenied)
	})

	t.Run("policy denies", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, deny())
		wantCode(t, h.ReleaseTenantSlug(adminCtx(tid), testRetiredRef), connect.CodePermissionDenied)
	})

	for name, ref := range map[string]string{
		"another resource": "tenants/" + testSlug,
		"no slug":          RetiredSlugNamePrefix,
		"not a slug":       RetiredSlugNamePrefix + "Not A Slug",
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			repo := &fakeRepo{releaseSlugFn: func(context.Context, string) (uuid.UUID, error) {
				called = true
				return former, nil
			}}
			h := NewHandler(repo, allow())
			wantCode(t, h.ReleaseTenantSlug(adminCtx(tid), ref), connect.CodeInvalidArgument)
			if called {
				t.Error("an invalid name reached the store")
			}
		})
	}

	t.Run("released, and logged with whose it was", func(t *testing.T) {
		var gotSlug string
		repo := &fakeRepo{releaseSlugFn: func(_ context.Context, slug string) (uuid.UUID, error) {
			gotSlug = slug
			return former, nil
		}}
		core, logs := observer.New(zapcore.InfoLevel)
		authz := allow()
		h := NewHandler(repo, authz)
		h.SetLogger(zap.New(core))
		if err := h.ReleaseTenantSlug(adminCtx(tid), testRetiredRef); err != nil {
			t.Fatal(err)
		}
		if gotSlug != testSlug {
			t.Errorf("released %q, want %q", gotSlug, testSlug)
		}
		if authz.lastAction != cedar.ActionManageTenant {
			t.Errorf("authorized %v, want ManageTenant", authz.lastAction)
		}
		if logs.FilterMessage("tenant slug released").
			FilterField(zap.String("former_tenant_id", former.String())).Len() != 1 {
			t.Errorf("no release line naming the former tenant: %v", logs.All())
		}
	})

	t.Run("a slug not retired is success, and says so", func(t *testing.T) {
		core, logs := observer.New(zapcore.InfoLevel)
		h := NewHandler(&fakeRepo{}, allow())
		h.SetLogger(zap.New(core))
		if err := h.ReleaseTenantSlug(adminCtx(tid), testRetiredRef); err != nil {
			t.Fatal(err)
		}
		if logs.FilterField(zap.String("slug", testSlug)).Len() != 1 {
			t.Errorf("no line for the no-op release: %v", logs.All())
		}
	})

	t.Run("a store failure is logged and internal", func(t *testing.T) {
		repo := &fakeRepo{releaseSlugFn: func(context.Context, string) (uuid.UUID, error) {
			return uuid.Nil(), errors.New("boom")
		}}
		core, logs := observer.New(zapcore.ErrorLevel)
		h := NewHandler(repo, allow())
		h.SetLogger(zap.New(core))
		wantCode(t, h.ReleaseTenantSlug(adminCtx(tid), testRetiredRef), connect.CodeInternal)
		if logs.FilterField(zap.String("slug", testSlug)).Len() != 1 {
			t.Errorf("no error line naming the slug: %v", logs.All())
		}
	})
}
