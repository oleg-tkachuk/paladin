package iam

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	authh "github.com/oleg-tkachuk/paladin/internal/api/iam/v1/authh"
	commonpb "github.com/oleg-tkachuk/paladin/internal/api/pb/common/v1"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/health"
)

// The iam shim is a pure translation layer: resource-name parsing and
// domain↔proto projection. Everything here runs without any handler wiring.

var (
	tenantID = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	userID   = uuid.MustParse("22222222-2222-2222-2222-222222222222")
)

// ─── timestamp / page helpers ──────────────────────────────────────────────

func TestTsHelpers(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	if tsProto(time.Time{}) != nil {
		t.Error("a zero time must project as nil, not epoch 0")
	}
	if got := tsProto(now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("tsProto = %v", got)
	}
	if tsPtrProto(nil) != nil {
		t.Error("nil pointer must project as nil")
	}
	zero := time.Time{}
	if tsPtrProto(&zero) != nil {
		t.Error("pointer to a zero time must project as nil")
	}
	if got := tsPtrProto(&now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("tsPtrProto = %v", got)
	}
}

func TestPageResponseProto(t *testing.T) {
	if pageResponseProto("") != nil {
		t.Error("no next token must omit the page response entirely")
	}
	if got := pageResponseProto("tok"); got == nil || got.NextPageToken != "tok" {
		t.Errorf("pageResponseProto = %v", got)
	}
}

// ─── scopes ────────────────────────────────────────────────────────────────

// The scope type mapping is used in both directions on the auth path, so a
// round trip must be lossless for every defined type.
func TestScopeTypeRoundTrip(t *testing.T) {
	for _, st := range []auth.ScopeType{
		auth.ScopeTenant, auth.ScopeBackend, auth.ScopeBucket, auth.ScopeCollection,
	} {
		if got := scopeTypeFromProto(scopeTypeProto(st)); got != st {
			t.Errorf("round trip of %q gave %q", st, got)
		}
	}
}

func TestScopeTypeProtoUnknown(t *testing.T) {
	if got := scopeTypeProto(auth.ScopeType("nonsense")); got != commonpb.ScopeType_SCOPE_TYPE_UNSPECIFIED {
		t.Errorf("unknown scope type = %v, want UNSPECIFIED", got)
	}
}

func TestScopeTypeFromProtoUnknown(t *testing.T) {
	if got := scopeTypeFromProto(commonpb.ScopeType_SCOPE_TYPE_UNSPECIFIED); got != auth.ScopeType("") {
		t.Errorf("UNSPECIFIED = %q, want the empty type", got)
	}
	if got := scopeTypeFromProto(commonpb.ScopeType(99)); got != auth.ScopeType("") {
		t.Errorf("out-of-range = %q, want the empty type", got)
	}
}

func TestScopesRoundTrip(t *testing.T) {
	in := []auth.Scope{
		{Type: auth.ScopeTenant, Value: "acme"},
		{Type: auth.ScopeBucket, Value: "logs"},
	}

	got := scopesFromProto(scopesToProto(in))
	if len(got) != len(in) {
		t.Fatalf("got %d scopes, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("scope[%d] = %+v, want %+v", i, got[i], in[i])
		}
	}
}

// Empty must stay empty rather than becoming nil, so the field serialises
// consistently.
func TestScopesToProtoEmpty(t *testing.T) {
	if got := scopesToProto(nil); got == nil || len(got) != 0 {
		t.Errorf("scopesToProto(nil) = %v, want an empty slice", got)
	}
}

// A nil entry in the wire slice must be skipped, not panic or produce a
// zero-value scope.
func TestScopesFromProtoSkipsNil(t *testing.T) {
	got := scopesFromProto([]*commonpb.Scope{
		nil,
		{Type: commonpb.ScopeType_SCOPE_TYPE_TENANT, Value: "a"},
		nil,
	})
	if len(got) != 1 || got[0].Value != "a" {
		t.Errorf("got %+v, want one tenant scope", got)
	}
}

// ─── userToProto ───────────────────────────────────────────────────────────

func TestUserToProtoNil(t *testing.T) {
	if userToProto(nil) != nil {
		t.Error("nil in, nil out")
	}
}

func TestUserToProto(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	login := now.Add(-time.Hour)
	u := &authstore.User{
		UserID: userID, TenantID: tenantID, Subject: "alice@example.com",
		DisplayName: "Alice", Roles: []string{"admin"},
		Scopes:   []auth.Scope{{Type: auth.ScopeTenant, Value: "acme"}},
		Disabled: true, ResourceVersion: 9,
		CreatedAt: now, UpdatedAt: now, LastLoginAt: &login,
		PasswordHash: []byte("super-secret-bcrypt-hash"),
	}

	got := userToProto(u)

	if want := "tenants/" + tenantID.String() + "/users/" + userID.String(); got.Name != want {
		t.Errorf("Name = %q, want %q", got.Name, want)
	}
	if got.Subject != "alice@example.com" || got.DisplayName != "Alice" || !got.Disabled {
		t.Errorf("scalars = %+v", got)
	}
	if got.ResourceVersion != "9" {
		t.Errorf("ResourceVersion = %q, want 9", got.ResourceVersion)
	}
	if len(got.Scopes) != 1 || got.Scopes[0].Value != "acme" {
		t.Errorf("Scopes = %+v", got.Scopes)
	}
	if got.LastLoginAt == nil || !got.LastLoginAt.AsTime().Equal(login) {
		t.Errorf("LastLoginAt = %v", got.LastLoginAt)
	}
	// The password hash has no wire field; this pins that the projection is an
	// explicit allow-list rather than a struct copy.
	if got.String() != "" && contains(got.String(), "bcrypt") {
		t.Error("password hash leaked into the wire message")
	}
}

func TestUserToProtoOmitsUnsetLastLogin(t *testing.T) {
	got := userToProto(&authstore.User{UserID: userID, TenantID: tenantID})
	if got.LastLoginAt != nil {
		t.Error("a user who never logged in must have no LastLoginAt")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

// ─── resource-name parsers ─────────────────────────────────────────────────

func TestUserIDFromName(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		got, err := userIDFromName("tenants/" + tenantID.String() + "/users/" + userID.String())
		if err != nil || got != userID {
			t.Errorf("got %v, %v", got, err)
		}
	})
	for label, n := range map[string]string{
		"empty":            "",
		"wrong collection": "tenants/" + tenantID.String() + "/apiKeys/" + userID.String(),
		"bad uuid":         "tenants/" + tenantID.String() + "/users/nope",
		"extra segments":   "tenants/a/users/b/c",
	} {
		t.Run(label, func(t *testing.T) {
			if _, err := userIDFromName(n); err == nil {
				t.Errorf("want an error for %q", n)
			}
		})
	}
}

// An empty parent means "no tenant filter", which is distinct from an invalid
// one — collapsing them would turn a typo into a silent list-everything.
func TestTenantFromParent(t *testing.T) {
	t.Run("empty means unfiltered", func(t *testing.T) {
		got, err := tenantFromParent("")
		if err != nil || got != uuid.Nil {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("happy path", func(t *testing.T) {
		got, err := tenantFromParent("tenants/" + tenantID.String())
		if err != nil || got != tenantID {
			t.Errorf("got %v, %v", got, err)
		}
	})
	for label, p := range map[string]string{
		"wrong prefix": "orgs/" + tenantID.String(),
		"bad uuid":     "tenants/not-a-uuid",
		"prefix only":  "tenants/",
	} {
		t.Run(label, func(t *testing.T) {
			if _, err := tenantFromParent(p); err == nil {
				t.Errorf("want an error for %q", p)
			}
		})
	}
}

func TestParseRV(t *testing.T) {
	t.Run("empty is zero", func(t *testing.T) {
		got, err := parseRV("")
		if err != nil || got != 0 {
			t.Errorf("got %d, %v", got, err)
		}
	})
	t.Run("digits", func(t *testing.T) {
		got, err := parseRV("1234")
		if err != nil || got != 1234 {
			t.Errorf("got %d, %v", got, err)
		}
	})
	// The hand-rolled digit loop accepts only ASCII digits — no signs, spaces,
	// or unicode numerals.
	for label, s := range map[string]string{
		"negative":      "-1",
		"plus":          "+1",
		"letters":       "12a",
		"space":         "1 2",
		"unicode digit": "１２",
	} {
		t.Run(label, func(t *testing.T) {
			if _, err := parseRV(s); err == nil {
				t.Errorf("want an error for %q", s)
			}
		})
	}
}

// Get and Delete pass different shapes of the same name, so both must parse.
func TestParseUserResourceName(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		got, err := parseUserResourceName("users/" + userID.String())
		if err != nil || got != userID {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("with /settings suffix", func(t *testing.T) {
		got, err := parseUserResourceName("users/" + userID.String() + "/settings")
		if err != nil || got != userID {
			t.Errorf("got %v, %v", got, err)
		}
	})
	for label, n := range map[string]string{
		"empty":        "",
		"prefix only":  "users/",
		"wrong prefix": "people/" + userID.String(),
		"bad uuid":     "users/nope",
	} {
		t.Run(label, func(t *testing.T) {
			if _, err := parseUserResourceName(n); err == nil {
				t.Errorf("want an error for %q", n)
			}
		})
	}
}

// Slug support is still pending, so a non-UUID parent must fail with the
// explicit "use UUID" message rather than being silently accepted.
func TestParseTenantParent(t *testing.T) {
	t.Run("uuid", func(t *testing.T) {
		got, err := parseTenantParent("tenants/" + tenantID.String())
		if err != nil || got != tenantID {
			t.Errorf("got %v, %v", got, err)
		}
	})
	t.Run("slug is rejected for now", func(t *testing.T) {
		_, err := parseTenantParent("tenants/acme")
		if err == nil {
			t.Fatal("want an error for a slug parent")
		}
		if !contains(err.Error(), "UUID") {
			t.Errorf("error should point at the UUID requirement, got %v", err)
		}
	})
	for label, p := range map[string]string{
		"empty":        "",
		"prefix only":  "tenants/",
		"wrong prefix": "orgs/x",
	} {
		t.Run(label, func(t *testing.T) {
			if _, err := parseTenantParent(p); err == nil {
				t.Errorf("want an error for %q", p)
			}
		})
	}
}

func TestIndexOf(t *testing.T) {
	cases := []struct {
		s, sub string
		want   int
	}{
		{"abcdef", "cd", 2},
		{"abcdef", "abc", 0},
		{"abcdef", "ef", 4},
		{"abcdef", "zz", -1},
		{"abc", "abcd", -1},
		{"", "a", -1},
		{"abc", "", 0},
	}
	for _, tc := range cases {
		if got := indexOf(tc.s, tc.sub); got != tc.want {
			t.Errorf("indexOf(%q, %q) = %d, want %d", tc.s, tc.sub, got, tc.want)
		}
	}
}

// ─── health projection ─────────────────────────────────────────────────────

func TestStatusToProto(t *testing.T) {
	cases := map[health.ComponentStatus]pb.ComponentStatus{
		health.StatusHealthy:   pb.ComponentStatus_COMPONENT_STATUS_HEALTHY,
		health.StatusDegraded:  pb.ComponentStatus_COMPONENT_STATUS_DEGRADED,
		health.StatusUnhealthy: pb.ComponentStatus_COMPONENT_STATUS_UNHEALTHY,
		// Anything unrecognised must be UNSPECIFIED rather than defaulting to
		// healthy — a probe bug must never read as "all good".
		health.ComponentStatus("weird"): pb.ComponentStatus_COMPONENT_STATUS_UNSPECIFIED,
		health.ComponentStatus(""):      pb.ComponentStatus_COMPONENT_STATUS_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := statusToProto(in); got != want {
			t.Errorf("statusToProto(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSnapshotToProto(t *testing.T) {
	s := health.Snapshot{
		Role:   "api",
		Status: health.StatusDegraded,
		Components: []health.Component{
			{Name: "postgres", Status: health.StatusHealthy, LatencyMs: 3, Category: "db", Critical: true},
			{Name: "s3", Status: health.StatusUnhealthy, Message: "timeout", Category: "storage"},
		},
	}

	got := snapshotToProto(s)

	if got.Role != "api" {
		t.Errorf("Role = %q", got.Role)
	}
	if got.Status != statusToProto(health.StatusDegraded) {
		t.Errorf("Status = %v", got.Status)
	}
	if len(got.Components) != 2 {
		t.Fatalf("got %d components, want 2", len(got.Components))
	}
	if got.Components[0].Name != "postgres" || got.Components[0].LatencyMs != 3 ||
		!got.Components[0].Critical || got.Components[0].Category != "db" {
		t.Errorf("component[0] = %+v", got.Components[0])
	}
	if got.Components[1].Message != "timeout" {
		t.Errorf("component[1] message = %q", got.Components[1].Message)
	}
}

func TestSnapshotToProtoNoComponents(t *testing.T) {
	got := snapshotToProto(health.Snapshot{Role: "worker", Status: health.StatusHealthy})
	if got.Components == nil {
		t.Error("Components must be an empty slice, not nil")
	}
	if len(got.Components) != 0 {
		t.Errorf("want no components, got %d", len(got.Components))
	}
}

// ─── object-key routes ─────────────────────────────────────────────────────

// nil/empty in → nil out so the field is omitted rather than sent as [].
func TestCollectionRoutesToProtoEmpty(t *testing.T) {
	if collectionRoutesToProto(nil) != nil {
		t.Error("nil routes must project as nil")
	}
	if collectionRoutesToProto([]authh.CollectionRoute{}) != nil {
		t.Error("empty routes must project as nil")
	}
}

func TestCollectionRoutesToProto(t *testing.T) {
	got := collectionRoutesToProto([]authh.CollectionRoute{
		{Canonical: "c1", TenantPath: "t1", BareAlias: "b1", Backend: "primary", Bucket: "bkt"},
		{Canonical: "c2"},
	})

	if len(got) != 2 {
		t.Fatalf("got %d routes, want 2", len(got))
	}
	if got[0].Canonical != "c1" || got[0].TenantPath != "t1" || got[0].BareAlias != "b1" ||
		got[0].Backend != "primary" || got[0].Bucket != "bkt" {
		t.Errorf("route[0] = %+v", got[0])
	}
	// Order must be preserved — the UI renders these as a routing table.
	if got[1].Canonical != "c2" {
		t.Errorf("route[1] = %+v", got[1])
	}
}

// ─── settings JSON round trip ──────────────────────────────────────────────

// nil vs {} is a meaningful distinction: "no preferences set" must not become
// "preferences set to empty".
func TestBytesToStructNilAndEmpty(t *testing.T) {
	for label, b := range map[string][]byte{"nil": nil, "empty": {}} {
		t.Run(label, func(t *testing.T) {
			got, err := bytesToStruct(b)
			if err != nil {
				t.Fatalf("bytesToStruct: %v", err)
			}
			if got != nil {
				t.Errorf("want nil, got %v", got)
			}
		})
	}
}

func TestBytesToStructRoundTrip(t *testing.T) {
	in := []byte(`{"theme":"dark","density":3}`)

	st, err := bytesToStruct(in)
	if err != nil {
		t.Fatalf("bytesToStruct: %v", err)
	}
	if st.Fields["theme"].GetStringValue() != "dark" {
		t.Errorf("theme = %v", st.Fields["theme"])
	}
	if st.Fields["density"].GetNumberValue() != 3 {
		t.Errorf("density = %v", st.Fields["density"])
	}

	out, err := structToBytes(st)
	if err != nil {
		t.Fatalf("structToBytes: %v", err)
	}
	again, err := bytesToStruct(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if again.Fields["theme"].GetStringValue() != "dark" {
		t.Errorf("round trip lost the theme: %v", again)
	}
}

func TestBytesToStructRejectsInvalidJSON(t *testing.T) {
	for label, b := range map[string][]byte{
		"not json":   []byte("nonsense"),
		"json array": []byte(`[1,2]`),
		"truncated":  []byte(`{"a":`),
	} {
		t.Run(label, func(t *testing.T) {
			if _, err := bytesToStruct(b); err == nil {
				t.Errorf("want an error for %q", b)
			}
		})
	}
}

func TestStructToBytesNil(t *testing.T) {
	got, err := structToBytes(nil)
	if err != nil {
		t.Fatalf("structToBytes(nil): %v", err)
	}
	if got != nil {
		t.Errorf("nil must stay nil so callers can tell it apart from {}, got %q", got)
	}
}

func TestStructToBytesEmptyStruct(t *testing.T) {
	got, err := structToBytes(&structpb.Struct{})
	if err != nil {
		t.Fatalf("structToBytes: %v", err)
	}
	if string(got) != "{}" {
		t.Errorf("an empty struct must serialise as {}, got %q", got)
	}
}

// ─── field mask ────────────────────────────────────────────────────────────

func TestPaths(t *testing.T) {
	if paths(nil) != nil {
		t.Error("a nil mask must yield nil, meaning update-everything")
	}
	got := paths(&fieldmaskpb.FieldMask{Paths: []string{"theme", "locale"}})
	if len(got) != 2 || got[0] != "theme" || got[1] != "locale" {
		t.Errorf("paths = %v", got)
	}
	if p := paths(&fieldmaskpb.FieldMask{}); len(p) != 0 {
		t.Errorf("an empty mask must yield no paths, got %v", p)
	}
}
