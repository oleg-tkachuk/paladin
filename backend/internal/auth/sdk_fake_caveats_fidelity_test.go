package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

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

// The data plane's check of a capability, as the handlers make it: GetObject
// asserts get on the object, EnsureTenantStorage manage on no resource.
type caveatHandlers struct {
	paladindatav1connect.UnimplementedObjectServiceHandler
	paladindatav1connect.UnimplementedStorageBootstrapServiceHandler
	tenant string
}

// fidelityKey is the object both sides hold.
const fidelityKey = "k"

func (h caveatHandlers) GetObject(ctx context.Context, _ *datav1.GetObjectRequest) (*datav1.Object, error) {
	if err := auth.AssertCapabilityOp(ctx, limes.OpGet, paladin.ObjectResource(h.tenant, paladintest.DefaultCollection, fidelityKey)); err != nil {
		return nil, err
	}
	return &datav1.Object{}, nil
}

func (h caveatHandlers) EnsureTenantStorage(ctx context.Context, _ *datav1.EnsureTenantStorageRequest) (*datav1.EnsureTenantStorageResponse, error) {
	if err := auth.AssertCapabilityOp(ctx, limes.OpManage, ""); err != nil {
		return nil, err
	}
	return &datav1.EnsureTenantStorageResponse{}, nil
}

// answer is what a refusal says to the caller, with the ids of the two sides'
// capabilities, which differ, named alike.
type answer struct {
	code    connect.Code
	reason  string
	message string
}

func answerOf(err error, ids ...string) answer {
	if err == nil {
		return answer{}
	}
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return answer{message: err.Error()}
	}
	msg := ce.Message()
	for _, id := range ids {
		msg = strings.ReplaceAll(msg, id, "<id>")
	}
	return answer{code: ce.Code(), reason: reasonOf(ce), message: msg}
}

// reasonOf is the reason the ErrorInfo of e names, "" when it carries none: read
// from the wire's details, as a client without the SDK's interceptors gets
// them.
func reasonOf(e *connect.Error) string {
	for _, d := range e.Details() {
		v, err := connectproto.UnmarshalErrorDetail(d)
		if err != nil {
			continue
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok {
			return info.GetReason()
		}
	}
	return ""
}

// refusalTable is the table the server and both SDKs' fakes are held to
// (sdk/testdata/capability_refusals.json).
const refusalTable = "../../../sdk/testdata/capability_refusals.json"

// fidelityCase is one capability of the table, with the calls made with it.
type fidelityCase struct {
	Name     string         `json:"name"`
	Audience []string       `json:"audience"`
	Ops      []limes.Op     `json:"ops"`
	Prefixes []string       `json:"prefixes"`
	Requests int64          `json:"max_requests"`
	Calls    []fidelityCall `json:"calls"`
}

// fidelityCall is one call and the answer it gets; none when it succeeds.
type fidelityCall struct {
	Call    string `json:"call"`
	Code    string `json:"code"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// The calls a case makes, as the table names them.
const (
	callGet    = "get_object"
	callManage = "ensure_tenant_storage"
)

// The table's placeholders for what depends on the fake's tenant.
const (
	placeholderObject    = "{object}"
	placeholderElsewhere = "{elsewhere}"
)

// elsewhereCollection is the collection {elsewhere} is the prefix of.
const elsewhereCollection = "elsewhere"

// A consumer's unit tests run against the SDK's fake under WithStrictAuth, so
// a capability the server refuses must be refused there too, in the same
// words: one issued for the bearer audience rather than the plane passed the
// fake and failed on the cluster. Each case of the shared table runs through
// the data plane's interceptor chain — the library prefix scrubbed, the
// capability verified and counted — into a handler that asserts as the real
// ones do, and through paladintest, and both must give the table's answer.
// The Python SDK's tests hold its fake to the same table.
func TestTheSDKFakeRefusesACapabilityAsTheServerDoes(t *testing.T) {
	raw, err := os.ReadFile(refusalTable)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Key   string         `json:"key"`
		Cases []fidelityCase `json:"cases"`
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
			serverCalls, _ := serverSide(t, tenant, tc)
			fakeCalls, _ := fakeSide(t, fake, obj.GetName(), tc)
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

// serverSide makes tc's calls through the data plane's chain, and returns
// their errors and the capability's id.
func serverSide(t *testing.T, tenant string, tc fidelityCase) ([]error, string) {
	t.Helper()
	m := captest.NewMinter(t)
	usage, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: m.Store})
	if err != nil {
		t.Fatal(err)
	}
	caveats := limes.Caveats{Ops: tc.Ops, ResourcePrefixes: tc.Prefixes}
	if tc.Requests > 0 {
		caveats.Requests = limes.RequestCeiling{Max: tc.Requests}
	}
	cap, token := m.Mint(t, limes.Capability{
		Subject:  limes.Principal{TenantID: uuid.MustParse(tenant)},
		Audience: tc.Audience,
		Caveats:  caveats,
	})
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
	return errs, cap.Capability().ID.String()
}

// fakeSide makes tc's calls against fake with a capability its
// CapabilityService issued, and returns their errors and its id.
func fakeSide(t *testing.T, fake *paladintest.Server, object string, tc fidelityCase) ([]error, string) {
	t.Helper()
	admin := fake.Connect(paladin.WithBearerToken(fake.IssueBearerToken(fake.Tenant()))).Admin.Capability
	ops := make([]paladin.Op, 0, len(tc.Ops))
	for _, op := range tc.Ops {
		ops = append(ops, paladin.Op(op))
	}
	caveats := &adminv1.CapabilityCaveats{
		Ops:              paladin.OpNames(ops...),
		ResourcePrefixes: tc.Prefixes,
		RequestCeiling:   &adminv1.CapabilityCaveats_UnlimitedRequests{UnlimitedRequests: true},
		BudgetCeiling:    &adminv1.CapabilityCaveats_UnlimitedBudget{UnlimitedBudget: &adminv1.UnlimitedBudget{}},
	}
	if tc.Requests > 0 {
		caveats.RequestCeiling = &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: tc.Requests}
	}
	resp, err := admin.Issue(context.Background(), &adminv1.CapabilityServiceIssueRequest{
		Subject:  &adminv1.CapabilityPrincipal{Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: fake.Tenant(), Subject: captest.Subject},
		Audience: tc.Audience,
		Caveats:  caveats,
	})
	if err != nil {
		t.Fatalf("the fake's Issue: %v", err)
	}
	p := fake.Connect(paladin.WithCapability(resp.GetToken()))
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
	return errs, resp.GetCapability().GetId()
}
