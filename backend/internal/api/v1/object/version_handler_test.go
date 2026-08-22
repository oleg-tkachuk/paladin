package object

import (
	"testing"

	"github.com/google/uuid"
)

func TestParseVersionName(t *testing.T) {
	objectID := uuid.Must(uuid.NewV7())
	tenantID := uuid.Must(uuid.NewV7())
	versionID := uuid.Must(uuid.NewV7())

	cases := []struct {
		name        string
		input       string
		wantBucket  string
		wantObject  uuid.UUID
		wantVersion uuid.UUID
		wantErr     bool
	}{
		{
			name:        "AIP-122 form",
			input:       "tenants/" + tenantID.String() + "/collections/photos/objects/" + objectID.String() + "/versions/" + versionID.String(),
			wantBucket:  "photos",
			wantObject:  objectID,
			wantVersion: versionID,
		},
		{
			name:    "missing /versions/",
			input:   "tenants/" + tenantID.String() + "/collections/photos/objects/" + objectID.String(),
			wantErr: true,
		},
		{
			name:    "bad version_id",
			input:   "tenants/" + tenantID.String() + "/collections/photos/objects/" + objectID.String() + "/versions/not-a-uuid",
			wantErr: true,
		},
		// The regression this parser was rewritten for: a collection body with
		// a slash in it is an ordinary name (every e2e fixture uses one), but
		// the old segment-counting parser rejected it — making the version
		// RPCs unreachable for those collections.
		{
			name:        "collection with a slash",
			input:       "tenants/" + tenantID.String() + "/collections/e2e/7f3fec78/objects/" + objectID.String() + "/versions/" + versionID.String(),
			wantBucket:  "e2e/7f3fec78",
			wantObject:  objectID,
			wantVersion: versionID,
		},
		{
			name:        "deeply nested collection",
			input:       "tenants/" + tenantID.String() + "/collections/a/b/c/d/objects/" + objectID.String() + "/versions/" + versionID.String(),
			wantBucket:  "a/b/c/d",
			wantObject:  objectID,
			wantVersion: versionID,
		},
		// A collection may legitimately contain the separator tokens. The last
		// occurrence is the real suffix, so the leading ones stay in the body.
		{
			name:        "collection containing /objects/",
			input:       "tenants/" + tenantID.String() + "/collections/x/objects/y/objects/" + objectID.String() + "/versions/" + versionID.String(),
			wantBucket:  "x/objects/y",
			wantObject:  objectID,
			wantVersion: versionID,
		},
		{
			name:        "collection containing /versions/",
			input:       "tenants/" + tenantID.String() + "/collections/x/versions/y/objects/" + objectID.String() + "/versions/" + versionID.String(),
			wantBucket:  "x/versions/y",
			wantObject:  objectID,
			wantVersion: versionID,
		},
		{
			name:    "missing tenants/ prefix",
			input:   "collections/photos/objects/" + objectID.String() + "/versions/" + versionID.String(),
			wantErr: true,
		},
		{
			name:    "tenant id is not a uuid",
			input:   "tenants/not-a-uuid/collections/photos/objects/" + objectID.String() + "/versions/" + versionID.String(),
			wantErr: true,
		},
		{
			name:    "missing /collections/",
			input:   "tenants/" + tenantID.String() + "/objects/" + objectID.String() + "/versions/" + versionID.String(),
			wantErr: true,
		},
		{
			name:    "missing /objects/",
			input:   "tenants/" + tenantID.String() + "/collections/photos/versions/" + versionID.String(),
			wantErr: true,
		},
		{
			name:    "empty collection body",
			input:   "tenants/" + tenantID.String() + "/collections//objects/" + objectID.String() + "/versions/" + versionID.String(),
			wantErr: true,
		},
		{
			name:    "bad object_id",
			input:   "tenants/" + tenantID.String() + "/collections/photos/objects/not-a-uuid/versions/" + versionID.String(),
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVersionName(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.collection != tc.wantBucket {
				t.Errorf("collection: got %q want %q", got.collection, tc.wantBucket)
			}
			if got.objectID != tc.wantObject {
				t.Errorf("objectID: got %v want %v", got.objectID, tc.wantObject)
			}
			if got.versionID != tc.wantVersion {
				t.Errorf("versionID: got %v want %v", got.versionID, tc.wantVersion)
			}
		})
	}
}
