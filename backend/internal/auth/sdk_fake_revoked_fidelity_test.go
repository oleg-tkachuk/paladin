package auth_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// A capability revoked after it was issued is refused by the data plane's
// verifier in its own words — the library's title, the revoked reason and
// code — and paladintest under WithStrictAuth answers it the same: it used to
// say "capability: revoked", which the server never sent.
func TestTheSDKFakeRefusesARevokedCapabilityAsTheServerDoes(t *testing.T) {
	m := captest.NewMinter(t)
	usage, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: m.Store})
	if err != nil {
		t.Fatal(err)
	}
	vc, token := m.Mint(t, limes.Capability{})
	if _, err := m.Store.Revoke(context.Background(), limes.RevokeRequest{CapabilityID: vc.Capability().ID}); err != nil {
		t.Fatal(err)
	}
	server := paladindatav1connect.NewObjectServiceClient(unarytest.Client(func(s *connect.Server) {
		paladindatav1connect.RegisterObjectServiceHandler(s, caveatHandlers{tenant: "t"})
	}, middleware.ScrubInternal(zap.NewNop()), auth.CapabilityEstablishingInterceptor(m.Verifier, auth.TokenPlaneData, usage, 0, nil)))
	ctx := unarytest.WithHeader(context.Background(), paladin.HeaderCapability, token)
	_, serverErr := server.GetObject(ctx, &datav1.GetObjectRequest{Name: "x"})

	fake := paladintest.New(t, paladintest.WithStrictAuth())
	obj := fake.Put(fake.Collection(), fidelityKey, "text/plain", []byte("x"))
	fakeToken := fake.IssueCapability(fake.Tenant())
	fake.Revoke(fakeToken)
	_, fakeErr := fake.Connect(paladin.WithCapability(fakeToken)).Data.Object.GetObject(context.Background(), &datav1.GetObjectRequest{Name: obj.GetName()})

	serverAnswer, fakeAnswer := answerOf(serverErr), answerOf(fakeErr)
	if serverAnswer.code != connect.CodeUnauthenticated || serverAnswer.reason != commonv1.ErrorReason_ERROR_REASON_CAPABILITY_REVOKED.String() {
		t.Fatalf("the server answers %+v, want a revoked refusal", serverAnswer)
	}
	if fakeAnswer != serverAnswer {
		t.Errorf("the fake answers %+v, the server %+v", fakeAnswer, serverAnswer)
	}
}
