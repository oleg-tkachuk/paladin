package resolve

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/internal/auth"
)

func TestResolveObjectKeyName(t *testing.T) {
	tid := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u", TenantID: tid})

	t.Run("tenant (C) shape", func(t *testing.T) {
		ref, err := ResolveObjectKeyName(ctx, "tenants/"+tid.String()+"/objectKeys/inv/2026")
		if err != nil {
			t.Fatal(err)
		}
		if ref.TenantID != tid || ref.ObjectKey != "inv/2026" || ref.Shape != ShapeTenant {
			t.Errorf("got %+v", ref)
		}
	})

	t.Run("canonical (A) shape carries backend+bucket", func(t *testing.T) {
		ref, err := ResolveObjectKeyName(ctx,
			"storageBackends/primary/buckets/b1/tenants/"+tid.String()+"/objectKeys/inv/2026")
		if err != nil {
			t.Fatal(err)
		}
		if ref.BackendID != "primary" || ref.BucketName != "b1" ||
			ref.TenantID != tid || ref.ObjectKey != "inv/2026" || ref.Shape != ShapeCanonical {
			t.Errorf("got %+v", ref)
		}
	})

	t.Run("bare (B) shape takes tenant from ctx", func(t *testing.T) {
		ref, err := ResolveObjectKeyName(ctx, "just-a-key")
		if err != nil {
			t.Fatal(err)
		}
		if ref.TenantID != tid || ref.ObjectKey != "just-a-key" || ref.Shape != ShapeBare {
			t.Errorf("got %+v", ref)
		}
	})

	t.Run("bare shape without a caller tenant errors", func(t *testing.T) {
		if _, err := ResolveObjectKeyName(context.Background(), "bare"); err == nil {
			t.Error("want error for bare name with no ctx tenant")
		}
	})

	t.Run("invalid tenant uuid errors", func(t *testing.T) {
		if _, err := ResolveObjectKeyName(ctx, "tenants/not-a-uuid/objectKeys/x"); err == nil {
			t.Error("want error for non-uuid tenant")
		}
	})

	t.Run("empty name errors", func(t *testing.T) {
		if _, err := ResolveObjectKeyName(ctx, ""); err == nil {
			t.Error("want error for empty name")
		}
	})
}

func TestResolveTenantParent(t *testing.T) {
	tid := uuid.New()
	got, err := ResolveTenantParent("tenants/" + tid.String())
	if err != nil || got != tid {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, _ := ResolveTenantParent(""); got != uuid.Nil {
		t.Errorf("empty parent should be Nil, got %v", got)
	}
	if _, err := ResolveTenantParent("tenants/bad"); err == nil {
		t.Error("want error for bad tenant parent")
	}
}
