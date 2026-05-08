package apiutil

import (
	"testing"

	"github.com/google/uuid"
)

func TestValidateTenantSlug(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"acme", false},
		{"acme-prod", false},
		{"a1b", false},
		{"acme-1", false},
		{"a-very-long-but-still-acceptable-tenant-slug-12345", false},

		// Bad: too short.
		{"", true},
		{"a", true},
		{"ab", true},
		// Bad: starts with digit.
		{"1acme", true},
		// Bad: trailing dash.
		{"acme-", true},
		// Bad: uppercase.
		{"Acme", true},
		// Bad: underscore.
		{"acme_prod", true},
		// Bad: too long (>63).
		{"abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcdefghij" + "abcde", true},
	}
	for _, tc := range cases {
		err := ValidateTenantSlug(tc.in)
		if tc.wantErr && err == nil {
			t.Errorf("ValidateTenantSlug(%q): want error, got nil", tc.in)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("ValidateTenantSlug(%q): unexpected error: %v", tc.in, err)
		}
	}
}

func TestParseTenantNameRef(t *testing.T) {
	t.Parallel()
	id := uuid.New()

	cases := []struct {
		in       string
		wantID   uuid.UUID
		wantSlug string
		wantErr  bool
	}{
		{"tenants/" + id.String(), id, "", false},
		{id.String(), id, "", false},
		{"tenants/acme", uuid.Nil, "acme", false},
		{"acme", uuid.Nil, "acme", false},
		{"tenants/", uuid.Nil, "", true},
		{"tenants/Bad_Slug", uuid.Nil, "", true},
		{"", uuid.Nil, "", true},
	}
	for _, tc := range cases {
		ref, err := ParseTenantNameRef(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseTenantNameRef(%q): want error, got %+v", tc.in, ref)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTenantNameRef(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if ref.ID != tc.wantID {
			t.Errorf("ParseTenantNameRef(%q): id=%v want %v", tc.in, ref.ID, tc.wantID)
		}
		if ref.Slug != tc.wantSlug {
			t.Errorf("ParseTenantNameRef(%q): slug=%q want %q", tc.in, ref.Slug, tc.wantSlug)
		}
	}
}
