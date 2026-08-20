package apiutil

import (
	"testing"

	"github.com/google/uuid"
)

var rnTID = uuid.MustParse("0a8c0000-0000-7000-8000-000000000f12")

func TestParseCollectionName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"valid", "collections/assets-prod", "assets-prod", false},
		{"valid-with-slash-in-key", "collections/a/b/c", "a/b/c", false},
		{"missing-prefix", "assets-prod", "", true},
		{"wrong-prefix", "collections/x", "", true},
		{"prefix-only", "collections/", "", true},
		{"empty", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCollectionName(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCollectionName(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCollectionName(%q): unexpected error %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseCollectionName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseTenantName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    uuid.UUID
		wantErr bool
	}{
		{"prefixed-uuid", "tenants/" + rnTID.String(), rnTID, false},
		{"bare-uuid", rnTID.String(), rnTID, false},
		{"prefixed-non-uuid", "tenants/acme-corp", uuid.Nil, true},
		{"garbage", "not-a-uuid", uuid.Nil, true},
		{"empty", "", uuid.Nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTenantName(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseTenantName(%q) = %s, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTenantName(%q): unexpected error %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseTenantName(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// ParseTenantNameRef is covered by slug_test.go (UUID / slug / error cases);
// HasID is covered by TestTenantRefHasID below.

func TestParseOperationName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    uuid.UUID
		wantErr bool
	}{
		{"prefixed", "operations/" + rnTID.String(), rnTID, false},
		{"bare", rnTID.String(), rnTID, false},
		{"invalid", "operations/nope", uuid.Nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOperationName(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseOperationName(%q) = %s, want error", tc.in, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("ParseOperationName(%q) = %s err=%v, want %s", tc.in, got, err, tc.want)
			}
		})
	}
}

func TestTenantRefHasID(t *testing.T) {
	if (TenantRef{ID: rnTID}).HasID() != true {
		t.Error("HasID() should be true when ID is set")
	}
	if (TenantRef{Slug: "acme"}).HasID() != false {
		t.Error("HasID() should be false for a slug-only ref")
	}
	if (TenantRef{}).HasID() != false {
		t.Error("HasID() should be false for the zero ref")
	}
}
