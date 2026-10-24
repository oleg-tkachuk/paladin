package auth_test

import (
	"context"
	"errors"
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

// fidelityCase is one capability, with the calls made with it.
type fidelityCase struct {
	audience []string
	ops      []limes.Op
	prefixes []string
	// requests is its request ceiling; 0 is none.
	requests int64
	// calls are what the capability is presented to, in order.
	calls []string
}

// The calls a case makes.
const (
	callGet    = "get"
	callManage = "manage"
)

// A consumer's unit tests run against the SDK's fake under WithStrictAuth, so
// a capability the server refuses must be refused there too, in the same
// words: one issued for the bearer audience rather than the plane passed the
// fake and failed on the cluster. Each case runs through the data plane's
// interceptor chain — the library prefix scrubbed, the capability verified
// and counted — into a handler that asserts as the real ones do, and through
// paladintest alike.
func TestTheSDKFakeRefusesACapabilityAsTheServerDoes(t *testing.T) {
	fake := paladintest.New(t, paladintest.WithStrictAuth())
	obj := fake.Put(fake.Collection(), fidelityKey, "text/plain", []byte("x"))
	tenant := fake.Tenant()
	elsewhere := paladin.ObjectResource(tenant, "elsewhere", "")

	cases := map[string]fidelityCase{
		"the bearer audience": {audience: []string{paladin.AudienceData}, calls: []string{callGet}},
		"another plane":       {audience: paladin.PlaneNames(paladin.PlaneAdmin), calls: []string{callGet}},
		"an op it lacks":      {ops: []limes.Op{limes.OpList}, calls: []string{callGet}},
		"a resource outside":  {prefixes: []string{elsewhere}, calls: []string{callGet}},
		"no resource under a restricted one": {
			ops: []limes.Op{limes.OpManage}, prefixes: []string{elsewhere}, calls: []string{callManage},
		},
		"past its request ceiling": {requests: 1, calls: []string{callGet, callGet}},
		"allowed":                  {calls: []string{callGet, callManage}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.audience == nil {
				tc.audience = paladin.PlaneNames(paladin.PlaneData)
			}
			if tc.ops == nil {
				tc.ops = []limes.Op{limes.OpGet, limes.OpManage}
			}
			serverCalls, serverID := serverSide(t, tenant, tc)
			fakeCalls, fakeID := fakeSide(t, fake, obj.GetName(), tc)
			for i, call := range tc.calls {
				s, f := answerOf(serverCalls[i], serverID), answerOf(fakeCalls[i], fakeID)
				if s != f {
					t.Errorf("call %d (%s): the server answers %+v, the fake %+v", i, call, s, f)
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
	caveats := limes.Caveats{Ops: tc.ops, ResourcePrefixes: tc.prefixes}
	if tc.requests > 0 {
		caveats.Requests = limes.RequestCeiling{Max: tc.requests}
	}
	cap, token := m.Mint(t, limes.Capability{
		Subject:  limes.Principal{TenantID: uuid.MustParse(tenant)},
		Audience: tc.audience,
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
	for _, call := range tc.calls {
		ctx := unarytest.WithHeader(context.Background(), paladin.HeaderCapability, token)
		switch call {
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
	ops := make([]paladin.Op, 0, len(tc.ops))
	for _, op := range tc.ops {
		ops = append(ops, paladin.Op(op))
	}
	caveats := &adminv1.CapabilityCaveats{
		Ops:              paladin.OpNames(ops...),
		ResourcePrefixes: tc.prefixes,
		RequestCeiling:   &adminv1.CapabilityCaveats_UnlimitedRequests{UnlimitedRequests: true},
		BudgetCeiling:    &adminv1.CapabilityCaveats_UnlimitedBudget{UnlimitedBudget: &adminv1.UnlimitedBudget{}},
	}
	if tc.requests > 0 {
		caveats.RequestCeiling = &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: tc.requests}
	}
	resp, err := admin.Issue(context.Background(), &adminv1.CapabilityServiceIssueRequest{
		Subject:  &adminv1.CapabilityPrincipal{Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: fake.Tenant(), Subject: captest.Subject},
		Audience: tc.audience,
		Caveats:  caveats,
	})
	if err != nil {
		t.Fatalf("the fake's Issue: %v", err)
	}
	p := fake.Connect(paladin.WithCapability(resp.GetToken()))
	var errs []error
	for _, call := range tc.calls {
		switch call {
		case callGet:
			_, err = p.Data.Object.GetObject(context.Background(), &datav1.GetObjectRequest{Name: object})
		case callManage:
			_, err = p.Data.StorageBootstrap.EnsureTenantStorage(context.Background(), &datav1.EnsureTenantStorageRequest{BackendId: "b", Bucket: "bucket"})
		}
		errs = append(errs, err)
	}
	return errs, resp.GetCapability().GetId()
}
