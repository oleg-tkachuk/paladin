package objecth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
)

// objectResourcesTable is the object resource space both SDKs are held to.
const objectResourcesTable = "../../../../../../sdk/testdata/object_resources.json"

// tenantPlaceholder stands for the table's tenant in its URIs.
const tenantPlaceholder = "{tenant}"

// upperTenantPlaceholder and otherTenantPlaceholder stand, in the reach rows,
// for the table's tenant in upper case and for another tenant.
const (
	upperTenantPlaceholder = "{TENANT}"
	otherTenantPlaceholder = "{other_tenant}"
)

// reachNothing is the reach of a row whose prefix covers no object.
const reachNothing = "nothing"

// resourceRef is an object, or a key prefix, by collection and key.
type resourceRef struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
}

// objectResources is sdk/testdata/object_resources.json.
type objectResources struct {
	Tenant    string `json:"tenant"`
	Resources []struct {
		resourceRef
		URI string `json:"uri"`
	} `json:"resources"`
	Covers []struct {
		Name   string      `json:"name"`
		Grant  resourceRef `json:"grant"`
		Object resourceRef `json:"object"`
		Match  bool        `json:"match"`
	} `json:"covers"`
	OtherTenant string     `json:"other_tenant"`
	Reach       []reachRow `json:"reach"`
}

// reachObject is an object of a reach row, in the table's tenant unless it
// names another.
type reachObject struct {
	resourceRef
	Tenant string `json:"tenant"`
}

// reachRow is a stored resource prefix and the objects it does and does not
// cover.
type reachRow struct {
	Name    string        `json:"name"`
	Prefix  string        `json:"prefix"`
	Reach   string        `json:"reach"`
	Matches []reachObject `json:"matches"`
	Misses  []reachObject `json:"misses"`
}

func readObjectResources(t *testing.T) objectResources {
	t.Helper()
	raw, err := os.ReadFile(objectResourcesTable)
	if err != nil {
		t.Fatal(err)
	}
	var table objectResources
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Resources) == 0 || len(table.Covers) == 0 {
		t.Fatalf("the table has %d resources and %d covers", len(table.Resources), len(table.Covers))
	}
	return table
}

// The server names an object as the SDKs do, and parses back what it names.
func TestCapabilityObjectURIMatchesTheSharedTable(t *testing.T) {
	table := readObjectResources(t)
	tenant, covers := uuid.MustParse(table.Tenant), table.Covers
	for _, r := range table.Resources {
		got := CapabilityObjectURI(tenant, r.Collection, r.Key)
		if want := strings.ReplaceAll(r.URI, tenantPlaceholder, tenant.String()); got != want {
			t.Errorf("CapabilityObjectURI(%q, %q) = %q, want %q", r.Collection, r.Key, got, want)
		}
		if r.Key == "" {
			continue // a collection's prefix names no object
		}
		gotTenant, collection, key, ok := parseCapabilityObjectURI(got)
		if !ok || gotTenant != tenant || collection != r.Collection || key != r.Key {
			t.Errorf("parse(%q) = %v %q %q %v", got, gotTenant, collection, key, ok)
		}
	}
	for _, c := range covers {
		grant := CapabilityObjectURI(tenant, c.Grant.Collection, c.Grant.Key)
		object := CapabilityObjectURI(tenant, c.Object.Collection, c.Object.Key)
		if got := limes.MatchResource(grant, object); got != c.Match {
			t.Errorf("%s: limes.MatchResource(%q, %q) = %v, want %v", c.Name, grant, object, got, c.Match)
		}
	}
}

// A capability confined to collection "a" is refused an object of the nested
// collection "a/b", where the two used to share one URI prefix.
func TestACollectionCaveatDoesNotAdmitANestedCollection(t *testing.T) {
	tenant := uuid.New()
	ctx := auth.WithCapability(context.Background(), captest.Verified(t, limes.Capability{
		Subject: limes.Principal{TenantID: tenant},
		Caveats: limes.Caveats{
			Ops:              []limes.Op{limes.OpGet},
			ResourcePrefixes: []string{capabilityCollectionURI(tenant, "a")},
		},
	}))
	if err := auth.AssertCapabilityOp(ctx, limes.OpGet, CapabilityObjectURI(tenant, "a", "b/c")); err != nil {
		t.Errorf("an object of collection a: %v, want admitted", err)
	}
	if err := auth.AssertCapabilityOp(ctx, limes.OpGet, CapabilityObjectURI(tenant, "a/b", "c")); !errors.Is(err, limes.ErrResourceNotAllowed) {
		t.Errorf("an object of collection a/b: %v, want ErrResourceNotAllowed", err)
	}
}

// legacyObjectURI is how a URI was spelled before the collection became one
// segment: its "/" kept, so nested collections shared their parent's prefix.
func legacyObjectURI(tenant uuid.UUID, collection, key string) string {
	return capabilityObjectURIPrefix + tenant.String() + storagepath.Separator + collection + storagepath.Separator + key
}

// A resource issued in the old spelling only narrows when read in the new
// one: whatever object it now reaches, it reached before. A grant on a nested
// collection now reaches its parent's keys of the same path text instead,
// which it reached before too, and none of the nested collection's objects.
func TestALegacyResourceNeverWidens(t *testing.T) {
	tenant := uuid.New()
	refs := []resourceRef{
		{"a", ""}, {"a", "b/"}, {"a", "b/c"}, {"a", "x"}, {"a/b", ""}, {"a/b", "c"},
		{"a/b/c", "d"}, {"ab", "c"}, {"a-b", "c"}, {"docs", "2026/q1/r.pdf"},
	}
	for _, grant := range refs {
		legacy := legacyObjectURI(tenant, grant.Collection, grant.Key)
		for _, obj := range refs {
			if obj.Key == "" {
				continue
			}
			now := limes.MatchResource(legacy, CapabilityObjectURI(tenant, obj.Collection, obj.Key))
			before := limes.MatchResource(legacy, legacyObjectURI(tenant, obj.Collection, obj.Key))
			if now && !before {
				t.Errorf("legacy grant %q now reaches (%q, %q), which it did not before", legacy, obj.Collection, obj.Key)
			}
		}
	}
}

// The server's match reaches what the console says a stored prefix reaches,
// partial and refused prefixes included.
func TestAStoredPrefixReachesWhatTheSharedTableSays(t *testing.T) {
	table := readObjectResources(t)
	if len(table.Reach) == 0 {
		t.Fatal("the table has no reach rows")
	}
	tenant := uuid.MustParse(table.Tenant)
	other := uuid.MustParse(table.OtherTenant)
	expand := strings.NewReplacer(
		tenantPlaceholder, tenant.String(),
		upperTenantPlaceholder, strings.ToUpper(tenant.String()),
		otherTenantPlaceholder, other.String(),
	)
	uriOf := func(o reachObject) string {
		id := tenant
		if o.Tenant != "" {
			id = uuid.MustParse(expand.Replace(o.Tenant))
		}
		return CapabilityObjectURI(id, o.Collection, o.Key)
	}
	for _, r := range table.Reach {
		prefix := expand.Replace(r.Prefix)
		if r.Reach == reachNothing && len(r.Matches) > 0 {
			t.Errorf("%s: reaches nothing but lists matches", r.Name)
		}
		for _, o := range r.Matches {
			if uri := uriOf(o); !limes.MatchResource(prefix, uri) {
				t.Errorf("%s: limes.MatchResource(%q, %q) = false, want true", r.Name, prefix, uri)
			}
		}
		for _, o := range r.Misses {
			if uri := uriOf(o); limes.MatchResource(prefix, uri) {
				t.Errorf("%s: limes.MatchResource(%q, %q) = true, want false", r.Name, prefix, uri)
			}
		}
	}
}
