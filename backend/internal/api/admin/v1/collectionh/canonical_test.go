package collectionh

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Sample UUID — pinned literal so a round-trip test can string-compare.
var tid = uuid.MustParse("0a8c0000-0000-7000-8000-000000000f12")

func TestCanonicalName_RoundTrip(t *testing.T) {
	cases := []struct {
		name       string
		backend    string
		bucket     string
		tenant     uuid.UUID
		collection string
	}{
		{"single-segment", "aws-eu", "paladin-prod-eu", tid, "assets-prod"},
		{"multi-segment", "r2-global", "paladin-cold", tid, "invoices/2026/q1"},
		{"deep-multi", "minio-dev", "dev", tid, "a/b/c/d/e/f"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CanonicalName(tc.backend, tc.bucket, tc.tenant, tc.collection)
			ref, err := ParseCanonical(got)
			if err != nil {
				t.Fatalf("ParseCanonical(%q): %v", got, err)
			}
			if ref.BackendID != tc.backend ||
				ref.BucketName != tc.bucket ||
				ref.TenantID != tc.tenant ||
				ref.Collection != tc.collection {
				t.Errorf("round-trip mismatch: got %+v, want b=%s bk=%s tid=%s ok=%s",
					ref, tc.backend, tc.bucket, tc.tenant, tc.collection)
			}
			if !IsCanonical(got) {
				t.Errorf("IsCanonical(%q) = false, want true", got)
			}
		})
	}
}

func TestCanonicalName_PanicsOnEmpty(t *testing.T) {
	cases := []struct {
		name       string
		backend    string
		bucket     string
		tenant     uuid.UUID
		collection string
	}{
		{"empty-backend", "", "b", tid, "ok"},
		{"empty-bucket", "be", "", tid, "ok"},
		{"nil-tenant", "be", "b", uuid.Nil, "ok"},
		{"empty-objectkey", "be", "b", tid, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("expected panic, got none")
				}
			}()
			_ = CanonicalName(tc.backend, tc.bucket, tc.tenant, tc.collection)
		})
	}
}

func TestParseCanonical_Rejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // substring expected in error
	}{
		{"tenant-first-not-canonical",
			"tenants/" + tid.String() + "/collections/x",
			"missing \"storageBackends/\""},
		{"no-bucket-sep",
			"storageBackends/aws-eu/tenants/" + tid.String() + "/collections/x",
			"missing \"/buckets/\""},
		{"no-tenant-sep",
			"storageBackends/aws-eu/buckets/b/collections/x",
			"missing \"/tenants/\""},
		{"no-objectkey-sep",
			"storageBackends/aws-eu/buckets/b/tenants/" + tid.String() + "/x",
			"missing \"/collections/\""},
		{"bad-tenant-uuid",
			"storageBackends/aws-eu/buckets/b/tenants/not-a-uuid/collections/x",
			"tenant_id"},
		{"empty-body",
			"storageBackends/aws-eu/buckets/b/tenants/" + tid.String() + "/collections/",
			"empty collection body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseCanonical(tc.in)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestTenantPathName(t *testing.T) {
	got := TenantPathName(tid, "invoices/2026/q1")
	want := "tenants/" + tid.String() + "/collections/invoices/2026/q1"
	if got != want {
		t.Errorf("TenantPathName: got %q, want %q", got, want)
	}
}

func TestIsCanonical(t *testing.T) {
	canonical := CanonicalName("aws-eu", "b", tid, "ok")
	tenantPath := TenantPathName(tid, "ok")
	if !IsCanonical(canonical) {
		t.Errorf("IsCanonical(canonical) = false")
	}
	if IsCanonical(tenantPath) {
		t.Errorf("IsCanonical(tenantPath) = true — should be false")
	}
}
