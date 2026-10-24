package auth_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// attenuationTable is the table the server and both SDKs' fakes are held to
// (sdk/testdata/biscuit_attenuations.json).
const attenuationTable = "../../../sdk/testdata/biscuit_attenuations.json"

// placeholderCollection is the table's prefix of the fake's default
// collection.
const placeholderCollection = "{collection}"

// attenuationCase is one capability of the table, the blocks its Biscuit is
// narrowed by, and the calls made with the copy.
type attenuationCase struct {
	Name         string         `json:"name"`
	Audience     []string       `json:"audience"`
	Ops          []limes.Op     `json:"ops"`
	Prefixes     []string       `json:"prefixes"`
	Attenuations []attenuation  `json:"attenuations"`
	Calls        []fidelityCall `json:"calls"`
}

// attenuation is one block, as paladin.Attenuate takes it.
type attenuation struct {
	Ops              []paladin.Op    `json:"ops"`
	ResourcePrefixes []string        `json:"resource_prefixes"`
	Planes           []paladin.Plane `json:"planes"`
	ExpiresInSeconds int64           `json:"expires_in_seconds"`
}

// narrow is token narrowed by each block of blocks in turn, prefixes filled.
func narrow(t *testing.T, token string, blocks []attenuation, fill func(string) string) string {
	t.Helper()
	for _, b := range blocks {
		a := paladin.Attenuation{Ops: b.Ops, Planes: b.Planes}
		for _, p := range b.ResourcePrefixes {
			a.ResourcePrefixes = append(a.ResourcePrefixes, fill(p))
		}
		if b.ExpiresInSeconds != 0 {
			a.ExpiresAt = time.Now().Add(time.Duration(b.ExpiresInSeconds) * time.Second)
		}
		var err error
		if token, err = paladin.Attenuate(token, a); err != nil {
			t.Fatalf("Attenuate: %v", err)
		}
	}
	return token
}

// A consumer's unit tests narrow the Biscuit the fake seals and hand the copy
// on, so a copy the server refuses must be refused there too, in the same
// words. Each case of the shared table runs through the data plane's
// interceptor chain, with a Biscuit the server's library seals, and through
// paladintest, and both must give the table's answer. The Python SDK's tests
// hold its fake to the same table.
func TestTheSDKFakeHoldsABiscuitCopyAsTheServerDoes(t *testing.T) {
	raw, err := os.ReadFile(attenuationTable)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Key   string            `json:"key"`
		Cases []attenuationCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if table.Key != fidelityKey || len(table.Cases) == 0 {
		t.Fatalf("the table's key %q and %d cases; want %q and some", table.Key, len(table.Cases), fidelityKey)
	}
	fake := paladintest.New(t, paladintest.WithStrictAuth())
	obj := fake.Put(fake.Collection(), fidelityKey, "text/plain", []byte("x"))
	tenant := fake.Tenant()
	fill := strings.NewReplacer(
		placeholderObject, paladin.ObjectResource(tenant, paladintest.DefaultCollection, fidelityKey),
		placeholderElsewhere, paladin.ObjectResource(tenant, elsewhereCollection, ""),
		placeholderCollection, paladin.ObjectResource(tenant, paladintest.DefaultCollection, ""),
	).Replace

	for _, tc := range table.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Audience == nil {
				tc.Audience = paladin.PlaneNames(paladin.PlaneData)
			}
			if tc.Ops == nil {
				tc.Ops = []limes.Op{limes.OpGet, limes.OpManage}
			}
			for i, p := range tc.Prefixes {
				tc.Prefixes[i] = fill(p)
			}
			serverCalls := serverCopySide(t, tenant, tc, fill)
			fakeCalls := fakeCopySide(t, fake, obj.GetName(), tc, fill)
			for i, call := range tc.Calls {
				want := answer{reason: call.Reason, message: fill(call.Message)}
				if call.Code != "" {
					if err := want.code.UnmarshalText([]byte(call.Code)); err != nil {
						t.Fatalf("call %d: code %q: %v", i, call.Code, err)
					}
				}
				if got := answerOf(serverCalls[i]); got != want {
					t.Errorf("call %d (%s): the server answers %+v, the table %+v", i, call.Call, got, want)
				}
				if got := answerOf(fakeCalls[i]); got != want {
					t.Errorf("call %d (%s): the fake answers %+v, the table %+v", i, call.Call, got, want)
				}
			}
		})
	}
}

// serverCopySide makes tc's calls through the data plane's chain with a copy
// of a Biscuit the server's library sealed, and returns their errors.
func serverCopySide(t *testing.T, tenant string, tc attenuationCase, fill func(string) string) []error {
	t.Helper()
	m := captest.NewMinter(t)
	usage, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: m.Store})
	if err != nil {
		t.Fatal(err)
	}
	vc, _ := m.Mint(t, limes.Capability{
		Subject:  limes.Principal{TenantID: uuid.MustParse(tenant)},
		Audience: tc.Audience,
		Caveats:  limes.Caveats{Ops: tc.Ops, ResourcePrefixes: tc.Prefixes},
	})
	sealed, err := m.Issuer.Biscuit(context.Background(), limes.BiscuitRequest{Capability: vc})
	if err != nil {
		t.Fatal(err)
	}
	token := narrow(t, sealed.Token, tc.Attenuations, fill)
	h := caveatHandlers{tenant: tenant}
	client := unarytest.Client(func(s *connect.Server) {
		paladindatav1connect.RegisterObjectServiceHandler(s, h)
		paladindatav1connect.RegisterStorageBootstrapServiceHandler(s, h)
	}, middleware.ScrubInternal(), auth.CapabilityEstablishingInterceptor(m.Verifier, auth.TokenPlaneData, usage, 0, nil))
	objects := paladindatav1connect.NewObjectServiceClient(client)
	storage := paladindatav1connect.NewStorageBootstrapServiceClient(client)
	var errs []error
	for _, call := range tc.Calls {
		ctx := unarytest.WithHeader(context.Background(), paladin.HeaderCapability, token)
		switch call.Call {
		case callGet:
			_, err = objects.GetObject(ctx, &datav1.GetObjectRequest{Name: paladin.ObjectName{CollectionName: paladin.CollectionName{Tenant: tenant, Collection: paladintest.DefaultCollection}, Object: fidelityKey}.String()})
		case callManage:
			_, err = storage.EnsureTenantStorage(ctx, &datav1.EnsureTenantStorageRequest{BackendId: "b", Bucket: "bucket"})
		}
		errs = append(errs, err)
	}
	return errs
}

// fakeCopySide makes tc's calls against fake with a copy of the Biscuit its
// CapabilityService sealed, and returns their errors.
func fakeCopySide(t *testing.T, fake *paladintest.Server, object string, tc attenuationCase, fill func(string) string) []error {
	t.Helper()
	admin := fake.Connect(paladin.WithBearerToken(fake.IssueBearerToken(fake.Tenant()))).Admin.Capability
	ops := make([]paladin.Op, 0, len(tc.Ops))
	for _, op := range tc.Ops {
		ops = append(ops, paladin.Op(op))
	}
	resp, err := admin.Issue(context.Background(), &adminv1.CapabilityServiceIssueRequest{
		Subject:  &adminv1.CapabilityPrincipal{Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: fake.Tenant(), Subject: captest.Subject},
		Audience: tc.Audience,
		Caveats: &adminv1.CapabilityCaveats{
			Ops:              paladin.OpNames(ops...),
			ResourcePrefixes: tc.Prefixes,
			RequestCeiling:   &adminv1.CapabilityCaveats_UnlimitedRequests{UnlimitedRequests: true},
			BudgetCeiling:    &adminv1.CapabilityCaveats_UnlimitedBudget{UnlimitedBudget: &adminv1.UnlimitedBudget{}},
		},
	})
	if err != nil {
		t.Fatalf("the fake's Issue: %v", err)
	}
	sealed, err := paladin.BiscuitOf(resp)
	if err != nil {
		t.Fatal(err)
	}
	p := fake.Connect(paladin.WithCapability(narrow(t, sealed, tc.Attenuations, fill)))
	var errs []error
	for _, call := range tc.Calls {
		switch call.Call {
		case callGet:
			_, err = p.Data.Object.GetObject(context.Background(), &datav1.GetObjectRequest{Name: object})
		case callManage:
			_, err = p.Data.StorageBootstrap.EnsureTenantStorage(context.Background(), &datav1.EnsureTenantStorageRequest{BackendId: "b", Bucket: "bucket"})
		}
		errs = append(errs, err)
	}
	return errs
}
