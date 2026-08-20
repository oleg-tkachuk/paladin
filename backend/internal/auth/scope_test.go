package auth

import "testing"

func TestParseScope(t *testing.T) {
	cases := []struct {
		in     string
		want   Scope
		errish bool
	}{
		{"*", Scope{Type: ScopeWildcard}, false},
		{"tenant:abc", Scope{Type: ScopeTenant, Value: "abc"}, false},
		{"backend:primary", Scope{Type: ScopeBackend, Value: "primary"}, false},
		{"bucket:paladin-archive", Scope{Type: ScopeBucket, Value: "paladin-archive"}, false},
		{"collection:b/k", Scope{Type: ScopeCollection, Value: "b/k"}, false},
		{"", Scope{}, true},
		{"unknown:value", Scope{}, true},
		{"bucket:", Scope{}, true},
		{"badformat", Scope{}, true},
	}
	for _, tc := range cases {
		got, err := ParseScope(tc.in)
		if tc.errish {
			if err == nil {
				t.Errorf("ParseScope(%q): expected error, got %+v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseScope(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseScope(%q): got %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestScopeMatch(t *testing.T) {
	wildcard := []Scope{{Type: ScopeWildcard}}
	tenantOnly := []Scope{{Type: ScopeTenant, Value: "t1"}}
	bucketOnly := []Scope{{Type: ScopeBucket, Value: "paladin-archive"}}
	collection := []Scope{{Type: ScopeCollection, Value: "paladin-archive/photos"}}

	cases := []struct {
		name   string
		scopes []Scope
		claim  ResourceClaim
		want   bool
	}{
		{"wildcard admits anything", wildcard, ResourceClaim{TenantID: "anyone"}, true},
		{"tenant scope hits", tenantOnly, ResourceClaim{TenantID: "t1"}, true},
		{"tenant scope misses", tenantOnly, ResourceClaim{TenantID: "t2"}, false},
		{"bucket scope hits", bucketOnly, ResourceClaim{BucketId: "paladin-archive"}, true},
		{"bucket scope misses on wrong bucket", bucketOnly, ResourceClaim{BucketId: "paladin-uploads"}, false},
		{"collection scope requires both bucket and key", collection, ResourceClaim{BucketId: "paladin-archive", Collection: "photos"}, true},
		{"collection scope misses without bucket", collection, ResourceClaim{Collection: "photos"}, false},
		{"empty scopes deny", nil, ResourceClaim{TenantID: "t1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchScope(tc.scopes, tc.claim)
			if got != tc.want {
				t.Fatalf("MatchScope: got %v want %v", got, tc.want)
			}
		})
	}
}

func TestScopeRoundTrip(t *testing.T) {
	in := []Scope{
		{Type: ScopeWildcard},
		{Type: ScopeTenant, Value: "t1"},
		{Type: ScopeBucket, Value: "b"},
		{Type: ScopeCollection, Value: "b/k"},
	}
	wire := make([]string, 0, len(in))
	for _, s := range in {
		wire = append(wire, s.String())
	}
	got, err := ParseScopes(wire)
	if err != nil {
		t.Fatalf("ParseScopes: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("len: got %d want %d", len(got), len(in))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("[%d]: got %+v want %+v", i, got[i], in[i])
		}
	}
}
