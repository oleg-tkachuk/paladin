package paladin_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"testing"

	"connectrpc.com/connect"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/internal/facadegen"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// generatedFacade is the file go generate writes in this package.
const generatedFacade = "facade_gen.go"

func TestFacadeIsGenerated(t *testing.T) {
	want, err := facadegen.Render()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(generatedFacade)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale: run go generate ./paladin", generatedFacade)
	}
}

// audienceRecorder answers every call with 404 and records the bearer token
// each path was called with.
type audienceRecorder struct {
	mu    sync.Mutex
	auths map[string]string
}

func (a *audienceRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.auths[r.URL.Path] = r.Header.Get(paladin.HeaderAuthorization)
	a.mu.Unlock()
	http.NotFound(w, r)
}

// perAudience hands out "tok-<audience>".
type perAudience struct{}

func (perAudience) Token(_ context.Context, audience string) (string, error) {
	return "tok-" + audience, nil
}

func TestConnectBuildsEveryServiceOfEveryPlane(t *testing.T) {
	const url = "https://paladin.example"
	p, err := paladin.Connect(paladin.Endpoints{Data: url, Admin: url, IAM: url})
	if err != nil {
		t.Fatal(err)
	}
	planes := map[string]any{"Data": p.Data, "Admin": p.Admin, "IAM": p.IAM}
	for _, plane := range facadegen.Planes {
		v := reflect.ValueOf(planes[plane.Name]).Elem()
		services := facadegen.Services(plane.Package)
		if v.NumField() != len(services) {
			t.Errorf("%s plane has %d clients, the contract %d services", plane.Name, v.NumField(), len(services))
		}
		for _, s := range services {
			f := v.FieldByName(facadegen.Field(s))
			if !f.IsValid() || f.IsNil() {
				t.Errorf("%s plane has no client for %s", plane.Name, s)
			}
		}
	}
}

func TestConnectLeavesOutPlanesWithoutAnEndpoint(t *testing.T) {
	p, err := paladin.Connect(paladin.Endpoints{Data: "https://data.example"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Data == nil || p.Admin != nil || p.IAM != nil {
		t.Errorf("planes = data %v, admin %v, iam %v; want only data", p.Data != nil, p.Admin != nil, p.IAM != nil)
	}
	if _, err := paladin.Connect(paladin.Endpoints{}); !errors.Is(err, paladin.ErrNoEndpoints) {
		t.Errorf("no endpoints: err = %v, want ErrNoEndpoints", err)
	}
	if _, err := paladin.Connect(paladin.Endpoints{Data: "not a url"}); !errors.Is(err, paladin.ErrInvalidBaseURL) {
		t.Errorf("bad endpoint: err = %v, want ErrInvalidBaseURL", err)
	}
}

func TestWithTokensNeedsAPlane(t *testing.T) {
	if _, err := paladin.New("https://a.example", paladin.WithTokens(perAudience{})); !errors.Is(err, paladin.ErrNoAudience) {
		t.Fatalf("err = %v, want ErrNoAudience", err)
	}
}

// Each plane refuses a token issued for another, so each gets its own.
func TestConnectSendsEachPlaneItsOwnAudience(t *testing.T) {
	rec := &audienceRecorder{auths: map[string]string{}}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	p, err := paladin.Connect(paladin.Endpoints{Data: srv.URL, IAM: srv.URL}, paladin.WithTokens(perAudience{}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _ = p.IAM.Health.GetVersion(ctx, connect.NewRequest(&iamv1.GetVersionRequest{}))
	_, _ = p.Data.Object.GetObject(ctx, connect.NewRequest(&datav1.GetObjectRequest{}))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	want := map[string]string{
		"/paladin.iam.v1.HealthService/GetVersion": "Bearer tok-" + paladin.AudienceIAM,
		"/paladin.data.v1.ObjectService/GetObject": "Bearer tok-" + paladin.AudienceData,
	}
	for path, auth := range want {
		if got := rec.auths[path]; got != auth {
			t.Errorf("%s sent %q, want %q", path, got, auth)
		}
	}
}
