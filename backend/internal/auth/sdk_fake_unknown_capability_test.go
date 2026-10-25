package auth_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// unknownTable is the table the server and both SDKs' fakes are held to
// (sdk/testdata/unknown_capabilities.json).
const unknownTable = "../../../sdk/testdata/unknown_capabilities.json"

// The table's placeholders for a capability another issuer minted.
const (
	placeholderForeignToken   = "{foreign_token}"
	placeholderForeignBiscuit = "{foreign_biscuit}"
)

// A consumer's unit tests that present a capability from somewhere else — a
// stale fixture, another environment's token — must see it refused as the
// server refuses it. Each token of the shared table goes through the data
// plane's interceptor chain and through paladintest, and both must give the
// table's code and reason, and its message where it names one. The Python
// SDK's tests hold its fake to the code and the reason.
func TestTheSDKFakeRefusesAnUnknownCapabilityAsTheServerDoes(t *testing.T) {
	raw, err := os.ReadFile(unknownTable)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Cases []struct {
			Name    string `json:"name"`
			Token   string `json:"token"`
			Code    string `json:"code"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Cases) == 0 {
		t.Fatal("the table has no cases")
	}
	foreign := captest.NewMinter(t)
	vc, foreignToken := foreign.Mint(t, limes.Capability{})
	sealed, err := foreign.Issuer.Biscuit(context.Background(), limes.BiscuitRequest{Capability: vc})
	if err != nil {
		t.Fatal(err)
	}
	fill := strings.NewReplacer(placeholderForeignToken, foreignToken, placeholderForeignBiscuit, sealed.Token).Replace

	m := captest.NewMinter(t)
	usage, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: m.Store})
	if err != nil {
		t.Fatal(err)
	}
	server := paladindatav1connect.NewObjectServiceClient(unarytest.Client(func(s *connect.Server) {
		paladindatav1connect.RegisterObjectServiceHandler(s, caveatHandlers{tenant: "t"})
	}, middleware.ScrubInternal(), auth.CapabilityEstablishingInterceptor(m.Verifier, auth.TokenPlaneData, usage, 0, nil)))
	fake := paladintest.New(t, paladintest.WithStrictAuth())
	obj := fake.Put(fake.Collection(), fidelityKey, "text/plain", []byte("x"))

	for _, tc := range table.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			token := fill(tc.Token)
			var code connect.Code
			if err := code.UnmarshalText([]byte(tc.Code)); err != nil {
				t.Fatal(err)
			}
			ctx := unarytest.WithHeader(context.Background(), paladin.HeaderCapability, token)
			_, serverErr := server.GetObject(ctx, &datav1.GetObjectRequest{Name: "x"})
			_, fakeErr := fake.Connect(paladin.WithCapability(token)).Data.Object.GetObject(context.Background(), &datav1.GetObjectRequest{Name: obj.GetName()})
			for side, err := range map[string]error{"the server": serverErr, "the fake": fakeErr} {
				got := answerOf(err)
				if got.code != code || got.reason != tc.Reason {
					t.Errorf("%s answers %+v, the table %s %s", side, got, tc.Code, tc.Reason)
				}
				if tc.Message != "" && got.message != tc.Message {
					t.Errorf("%s says %q, the table %q", side, got.message, tc.Message)
				}
			}
		})
	}
}
