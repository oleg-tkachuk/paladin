package capabilityh

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// leaseCeilingTable is the shared table of how a lease is held against the
// request ceilings of its chain.
const leaseCeilingTable = "../../../../../../sdk/testdata/capability_lease_ceilings.json"

// The roles the ceiling table seeds, and its data-plane call.
const (
	roleParent  = "parent"
	callRequest = "request"
	// ceilingKey is the object a request reads.
	ceilingKey = "k"
)

type leaseCeilingCase struct {
	Name              string      `json:"name"`
	ParentMaxRequests int64       `json:"parent_max_requests"`
	ChildMaxRequests  int64       `json:"child_max_requests"`
	Steps             []leaseStep `json:"steps"`
}

func readLeaseCeilingTable(t *testing.T) []leaseCeilingCase {
	t.Helper()
	raw, err := os.ReadFile(leaseCeilingTable)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Cases []leaseCeilingCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Cases) == 0 {
		t.Fatal("the lease ceiling table has no cases")
	}
	return table.Cases
}

// ceilingObjects answers every read: what the table holds is the counting
// in front of it.
type ceilingObjects struct {
	paladindatav1connect.UnimplementedObjectServiceHandler
}

func (ceilingObjects) GetObject(context.Context, *datav1.GetObjectRequest) (*datav1.Object, error) {
	return &datav1.Object{}, nil
}

// ceilingSide runs a case's steps on one side: lease calls as an operator,
// requests through the data plane presenting a role's capability.
type ceilingSide struct {
	leases   leaseCalls
	operator context.Context
	ids      map[string]string
	request  func(role string) error
}

func (s ceilingSide) run(t *testing.T, who string, steps []leaseStep) {
	t.Helper()
	for i, step := range steps {
		var got leaseAnswer
		if step.Call == callRequest {
			if err := s.request(step.Of); err != nil {
				got = answerOfLease(nil, err)
			}
		} else {
			got, _ = runLeaseStep(s.operator, s.leases, s.leases, s.operator, step, s.ids[step.Of])
		}
		if got != step.want() {
			t.Errorf("%s step %d (%s of %s) answers %+v, the table %+v", who, i, step.Call, step.Of, got, step.want())
		}
	}
}

// serverCeilingSide seeds a case's chain on limes's in-memory stores, and
// counts its requests through the data plane's capability interceptor on
// the store its leases are held in.
func serverCeilingSide(t *testing.T, tc leaseCeilingCase) ceilingSide {
	t.Helper()
	m := captest.NewMinter(t)
	usage, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: m.Store})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(m.Issuer, m.Verifier, m.Store, usage, &allowAuthorizer{}).WithLeases(usage).WithLineage(m.Store)
	parent, parentToken := m.Mint(t, limes.Capability{
		Subject:  limes.Principal{TenantID: uuid.New()},
		Audience: paladin.PlaneNames(paladin.PlaneData, paladin.PlaneAdmin),
		Caveats: limes.Caveats{
			Ops:      []limes.Op{limes.OpGet, limes.OpShare},
			Requests: limes.RequestCeiling{Max: tc.ParentMaxRequests},
		},
	})
	caveats := narrowed(parent, limes.OpGet, limes.OpShare)
	caveats.Requests = limes.RequestCeiling{Max: tc.ChildMaxRequests}
	child, childToken := m.Delegate(t, parent, caveats)
	client := unarytest.Client(func(s *connect.Server) {
		paladindatav1connect.RegisterObjectServiceHandler(s, ceilingObjects{})
	}, auth.CapabilityEstablishingInterceptor(m.Verifier, auth.TokenPlaneData, usage, 0, nil))
	objects := paladindatav1connect.NewObjectServiceClient(client)
	tokens := map[string]string{roleParent: parentToken, roleChild: childToken}
	name := paladin.ObjectName{
		CollectionName: paladin.CollectionName{Tenant: parent.Capability().Subject.TenantID.String(), Collection: paladintest.DefaultCollection},
		Object:         ceilingKey,
	}.String()
	return ceilingSide{
		leases:   h,
		operator: adminCtx(),
		ids:      map[string]string{roleParent: parent.Capability().ID.String(), roleChild: child.Capability().ID.String()},
		request: func(role string) error {
			ctx := unarytest.WithHeader(context.Background(), paladin.HeaderCapability, tokens[role])
			_, err := objects.GetObject(ctx, &datav1.GetObjectRequest{Name: name})
			return err
		},
	}
}

// fakeCeilingSide seeds a case's chain on paladintest, through its own
// CapabilityService.
func fakeCeilingSide(t *testing.T, tc leaseCeilingCase) ceilingSide {
	t.Helper()
	ctx := context.Background()
	srv := paladintest.New(t, paladintest.WithStrictAuth())
	obj := srv.Put(srv.Collection(), ceilingKey, "text/plain", []byte("x"))
	operator := srv.Connect(paladin.WithBearerToken(srv.IssueBearerToken(srv.Tenant()))).Admin.Capability
	ceiling := func(max int64) *adminv1.CapabilityCaveats {
		c := openCaveats(limes.OpGet, limes.OpShare)
		c.RequestCeiling = &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: max}
		return c
	}
	parent, err := operator.Issue(ctx, &adminv1.CapabilityServiceIssueRequest{
		Subject:  &adminv1.CapabilityPrincipal{Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: srv.Tenant(), Subject: captest.Subject},
		Audience: paladin.PlaneNames(paladin.PlaneData, paladin.PlaneAdmin),
		Caveats:  ceiling(tc.ParentMaxRequests),
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := srv.Connect(paladin.WithCapability(parent.GetToken())).Admin.Capability.Delegate(ctx, &adminv1.CapabilityServiceDelegateRequest{
		ParentId: parent.GetCapability().GetId(), TtlSeconds: delegateTTLSeconds, Caveats: ceiling(tc.ChildMaxRequests),
	})
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{roleParent: parent.GetToken(), roleChild: child.GetToken()}
	return ceilingSide{
		leases:   operator,
		operator: ctx,
		ids:      map[string]string{roleParent: parent.GetCapability().GetId(), roleChild: child.GetCapability().GetId()},
		request: func(role string) error {
			_, err := srv.Connect(paladin.WithCapability(tokens[role])).Data.Object.GetObject(ctx, &datav1.GetObjectRequest{Name: obj.GetName()})
			return err
		},
	}
}

// A lease holds its requests against its own ceiling and every ancestor's,
// and the requests made under it reach the ancestors when it settles: the
// server and paladintest give the table's answer at each step, and the
// Python SDK's tests hold its fake to the same table.
func TestTheSDKFakeHoldsALeaseAgainstTheChainsCeilingsAsTheServerDoes(t *testing.T) {
	for _, tc := range readLeaseCeilingTable(t) {
		t.Run(tc.Name, func(t *testing.T) {
			serverCeilingSide(t, tc).run(t, "server", tc.Steps)
			fakeCeilingSide(t, tc).run(t, "fake", tc.Steps)
		})
	}
}
