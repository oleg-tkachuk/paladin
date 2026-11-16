package main

import (
	"context"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"gopkg.in/yaml.v3"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

const (
	testTenantID   = "01a0f4a9-7600-77a9-862e-11ea31d523ca"
	testTenantSlug = "platform"
	testCollection = "fixture-seed"
)

// fakeTenants answers GetTenant for one tenant, by slug or id, and counts calls.
type fakeTenants struct{ calls int }

func (f *fakeTenants) GetTenant(_ context.Context, req *adminv1.GetTenantRequest) (*adminv1.Tenant, error) {
	f.calls++
	switch req.GetName() {
	case tenantPrefix + testTenantSlug, tenantPrefix + testTenantID:
		return &adminv1.Tenant{TenantId: testTenantID, Slug: testTenantSlug}, nil
	}
	return nil, connect.NewError(connect.CodeNotFound, "no rows in result set")
}

// fakeCollections holds the collections that exist, by resource name.
type fakeCollections map[string]bool

func (f fakeCollections) GetCollection(_ context.Context, req *adminv1.GetCollectionRequest) (*adminv1.Collection, error) {
	if f[req.GetName()] {
		return &adminv1.Collection{Name: req.GetName()}, nil
	}
	return nil, connect.NewError(connect.CodeNotFound, "no rows in result set")
}

// The API names a tenant by its id; the flag takes the slug an operator
// knows, or the id. A slug used to go into the resource name as it was,
// which the API refuses.
func TestResolveTenantID(t *testing.T) {
	ctx := context.Background()

	t.Run("an id is used as it is", func(t *testing.T) {
		f := &fakeTenants{}
		got, err := resolveTenantID(ctx, f, testTenantID)
		if err != nil || got != testTenantID {
			t.Fatalf("got %q, %v", got, err)
		}
		if f.calls != 0 {
			t.Errorf("looked an id up %d times", f.calls)
		}
	})

	t.Run("a slug is looked up", func(t *testing.T) {
		got, err := resolveTenantID(ctx, &fakeTenants{}, testTenantSlug)
		if err != nil || got != testTenantID {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("an unknown slug names itself", func(t *testing.T) {
		_, err := resolveTenantID(ctx, &fakeTenants{}, "nowhere")
		if err == nil || !strings.Contains(err.Error(), `"nowhere"`) {
			t.Fatalf("err = %v, want one naming the tenant", err)
		}
	})
}

// load/stress seed under a collection that must already exist and be bound
// to a bucket; the default named one no cluster has, and the run failed deep
// in ListObjects. It now says what is missing before anything is written.
func TestCollectionParent(t *testing.T) {
	ctx := context.Background()
	want := tenantPrefix + testTenantID + collectionsSegment + testCollection

	t.Run("an existing collection under a slug", func(t *testing.T) {
		got, err := collectionParent(ctx, &fakeTenants{}, fakeCollections{want: true}, testTenantSlug, testCollection)
		if err != nil || got != want {
			t.Fatalf("got %q, %v; want %q", got, err, want)
		}
	})

	t.Run("a missing collection says what to do", func(t *testing.T) {
		_, err := collectionParent(ctx, &fakeTenants{}, fakeCollections{}, testTenantSlug, testCollection)
		if err == nil {
			t.Fatal("no error for a collection that does not exist")
		}
		for _, part := range []string{testCollection, testTenantSlug, "--object-key"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("error %q does not name %q", err, part)
			}
		}
	})
}

// The flags' defaults are the planes as `task backend:seed:up` forwards them,
// not the console's old Node routes, and they pass the dev-target guard.
func TestDefaultPlaneURLs(t *testing.T) {
	for _, u := range []string{defaultAdminURL, defaultIAMURL, defaultDataURL} {
		if strings.Contains(u, "/api/rpc/") {
			t.Errorf("%s is a route of the console's old server", u)
		}
		if err := assertDevTarget(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

// taskfile is the component Taskfile `task backend:seed:up` runs, relative to
// this package.
const taskfile = "../../Taskfile.yaml"

// taskDefault reads the default out of a Task variable that takes an
// override: '{{.NAME | default "18090"}}'.
var taskDefault = regexp.MustCompile(`\| default "(\d+)"`)

// The flags' default ports are the ones `_seed` forwards the planes to: a run
// outside the task reaches the same forwards, and one edited without the
// other would dial a port nothing listens on.
func TestDefaultPortsAreTheSeedTasks(t *testing.T) {
	raw, err := os.ReadFile(taskfile)
	if err != nil {
		t.Fatal(err)
	}
	var tf struct {
		Tasks map[string]struct {
			Vars map[string]string `yaml:"vars"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(raw, &tf); err != nil {
		t.Fatal(err)
	}
	vars := tf.Tasks["_seed"].Vars
	for name, def := range map[string]string{
		"SEED_ADMIN_PORT": defaultAdminURL,
		"SEED_IAM_PORT":   defaultIAMURL,
		"SEED_DATA_PORT":  defaultDataURL,
	} {
		u, err := url.Parse(def)
		if err != nil {
			t.Fatal(err)
		}
		m := taskDefault.FindStringSubmatch(vars[name])
		if m == nil || m[1] != u.Port() {
			t.Errorf("%s is %q in %s, the flag defaults to port %q", name, vars[name], taskfile, u.Port())
		}
	}
}

// Teardown deletes each fixture object at the version it was listed at:
// DeleteObject refuses a call without one, which left every object behind.
func TestFixtureDeleteRequest(t *testing.T) {
	const (
		name    = "tenants/t/collections/c/objects/o"
		version = "7"
	)
	got := fixtureDeleteRequest(&datav1.Object{Name: name, ResourceVersion: version})
	if got.GetName() != name || got.GetResourceVersion() != version || !got.GetPermanent() {
		t.Errorf("request = %+v, want %s at version %s, permanently", got, name, version)
	}
}

// Demo teardown deletes the subscriptions seedDemo wrote, at their versions,
// and leaves every other subscription in the tenant alone.
func TestFixtureSubscriptionDeletes(t *testing.T) {
	fixture := &adminv1.EventSubscription{
		Name: "tenants/t/eventSubscriptions/a", ResourceVersion: "3",
		Sink: &adminv1.EventSink{Target: &adminv1.EventSink_Http{Http: &adminv1.HttpSink{Url: "https://example.test/webhook/baseline"}}},
	}
	other := &adminv1.EventSubscription{
		Name: "tenants/t/eventSubscriptions/b", ResourceVersion: "1",
		Sink: &adminv1.EventSink{Target: &adminv1.EventSink_Http{Http: &adminv1.HttpSink{Url: "https://hooks.example.com/real"}}},
	}
	got := fixtureSubscriptionDeletes([]*adminv1.EventSubscription{fixture, other})
	if len(got) != 1 || got[0].GetName() != fixture.GetName() || got[0].GetResourceVersion() != "3" {
		t.Errorf("deletes = %+v, want only %s at version 3", got, fixture.GetName())
	}
}
