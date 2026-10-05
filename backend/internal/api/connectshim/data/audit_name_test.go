package data

import (
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

// The audit row is filed under the first resource a request names. Several
// requests carry it in a field the audit interceptor does not read
// (object_name, source_name), and without a name the row is in no tenant's
// trail. A batch names its collection before its objects; a copy its source
// before its destination.
func TestNameParsersNameTheFirstResourceForAudit(t *testing.T) {
	tenant := uuid.New()
	collection := "tenants/" + tenant.String() + "/collections/docs"
	object := collection + "/objects/" + uuid.NewString()

	ctx := apiutil.WithResourceSlot(ctxTenant(tenant))
	if _, _, err := collectionNameParts(ctx, collection); err != nil {
		t.Fatalf("collection: %v", err)
	}
	if _, _, _, err := objectNameParts(ctx, object); err != nil {
		t.Fatalf("object: %v", err)
	}
	if got := apiutil.ResourceFromContext(ctx); got != collection {
		t.Errorf("resource = %q, want the first name, %q", got, collection)
	}

	// An object name alone — CompleteMultipartUpload's object_name, a copy's
	// source_name — is what the row has to go on.
	ctx = apiutil.WithResourceSlot(ctxTenant(tenant))
	if _, _, _, err := objectNameParts(ctx, object); err != nil {
		t.Fatalf("object: %v", err)
	}
	if got := apiutil.ResourceFromContext(ctx); got != object {
		t.Errorf("resource = %q, want %q", got, object)
	}

	ctx = apiutil.WithResourceSlot(ctxTenant(tenant))
	if _, _, err := versionParent(ctx, object+"/versions/"+uuid.NewString()); err != nil {
		t.Fatalf("version: %v", err)
	}
	if got, ok := apiutil.TenantInResourceName(apiutil.ResourceFromContext(ctx)); !ok || got != tenant {
		t.Errorf("a version's name files the row under %s (%v), want %s", got, ok, tenant)
	}

	// A name refused for its tenant names nothing.
	ctx = apiutil.WithResourceSlot(ctxTenant(uuid.New()))
	if _, _, err := collectionNameParts(ctx, collection); err == nil {
		t.Fatal("another tenant's collection was admitted")
	}
	if got := apiutil.ResourceFromContext(ctx); got != "" {
		t.Errorf("resource = %q for a refused name, want none", got)
	}
}
