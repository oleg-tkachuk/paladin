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
