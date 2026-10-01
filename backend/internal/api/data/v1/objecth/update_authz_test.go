package objecth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// updateRepo serves one existing object to UpdateObject.
type updateRepo struct {
	fakeObjectRepo
	obj Object
}

func (r *updateRepo) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	return r.obj, nil
}

func (r *updateRepo) LookupBucket(context.Context, uuid.UUID, string, bool) (string, string, error) {
	return "backend-1", "bucket-1", nil
}

func (r *updateRepo) RunInTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return fn(ctx, nil)
}

func (r *updateRepo) UpdateMetadataTx(context.Context, pgx.Tx, UpdateMetadataArgs) (Object, error) {
	return r.obj, nil
}

// UpdateObject names one object, so Cedar must see that object. Checked
// against the Collection, a policy on resource.key or resource.tags could not
// apply to an update at all.
func TestUpdateObjectAuthorizesTheObject(t *testing.T) {
	tenantID := uuid.New()
	obj := Object{
		ObjectID: uuid.New(), TenantID: tenantID, Collection: "docs", Key: "reports/q1.pdf",
		ContentType: "application/pdf", SizeBytes: 42, Tags: map[string]string{"class": "pii"},
	}
	authz := &recordingAuthorizer{}
	h := &Handler{repo: &updateRepo{obj: obj}, policy: authz}
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})

	if _, err := h.UpdateObject(ctx, UpdateObjectInput{
		Collection: "docs", ObjectID: obj.ObjectID.String(), UpdatedFields: []string{"tags"},
	}); err != nil {
		t.Fatalf("UpdateObject: %v", err)
	}
	r := authz.lastResource
	if authz.lastAction != cedar.ActionUpdateObject || r == nil {
		t.Fatalf("authorized %q with %+v, want UpdateObject", authz.lastAction, r)
	}
	if r.Key != obj.Key || r.Tags["class"] != "pii" || r.ContentType != obj.ContentType {
		t.Errorf("resource = key %q tags %v type %q, want the object's own", r.Key, r.Tags, r.ContentType)
	}
	if r.BucketName != "bucket-1" {
		t.Errorf("bucket = %q, want the collection's binding for scoped tokens", r.BucketName)
	}
}
