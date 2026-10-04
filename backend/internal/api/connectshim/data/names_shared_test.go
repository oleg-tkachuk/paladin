package data

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

// sharedNames is the table both SDKs' tests read (sdk/testdata/names.json):
// the server parses the same names the same way.
var sharedNames = filepath.Join("..", "..", "..", "..", "..", "sdk", "testdata", "names.json")

// Kinds of the table the server does not parse, and why.
var notServerParsed = map[string]string{
	// Checked against apiutil.ParseTenantNameRef, which parses them.
	"tenant": "tenant names are parsed by apiutil",
	// paladin:// object URIs are an SDK form; no RPC takes one.
	"uri": "no RPC takes a paladin:// object URI",
}

type sharedName struct {
	In         string `json:"in"`
	OK         bool   `json:"ok"`
	Out        string `json:"out"`
	Tenant     string `json:"tenant"`
	Collection string `json:"collection"`
	Object     string `json:"object"`
	Version    string `json:"version"`
}

// parsedName is what the server made of a name.
type parsedName struct {
	collection, object, out string
}

var serverParsers = map[string]func(context.Context, string) (parsedName, error){
	"collection": func(ctx context.Context, s string) (parsedName, error) {
		c, err := collectionNameParts(ctx, s)
		return parsedName{collection: c}, err
	},
	"object": func(ctx context.Context, s string) (parsedName, error) {
		n, err := parseObjectName(ctx, s)
		return parsedName{collection: n.Collection, object: n.Object, out: n.String()}, err
	},
	"version": func(_ context.Context, s string) (parsedName, error) {
		n, err := versionParent(s)
		return parsedName{collection: n.Collection, object: n.Object}, err
	},
}

func TestServerParsesTheSharedNames(t *testing.T) {
	raw, err := os.ReadFile(sharedNames)
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	for kind, msg := range all {
		if kind[0] == '_' {
			continue
		}
		if _, skipped := notServerParsed[kind]; skipped {
			continue
		}
		parse, ok := serverParsers[kind]
		if !ok {
			t.Errorf("the table has kind %q, which this test neither parses nor names in notServerParsed", kind)
			continue
		}
		var cases []sharedName
		if err := json.Unmarshal(msg, &cases); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		for _, c := range cases {
			t.Run(kind+"/"+c.In, func(t *testing.T) {
				if !c.OK {
					// As a platform admin, so a refusal is the parser's, not the
					// tenant check's.
					if _, err := parse(ctxTenant(uuid.Nil, apiutil.RolePlatformAdmin), c.In); err == nil {
						t.Fatal("accepted a name the SDKs refuse")
					}
					return
				}
				// As a caller of the name's own tenant, so the tenant the parser
				// returns must match the caller's however the name spelled it.
				got, err := parse(ctxTenant(uuid.MustParse(c.Tenant)), c.In)
				if err != nil {
					t.Fatalf("refused a name the SDKs accept: %v", err)
				}
				if got.collection != c.Collection || got.object != c.Object {
					t.Errorf("got collection %q object %q, want %q %q", got.collection, got.object, c.Collection, c.Object)
				}
				want := c.Out
				if want == "" {
					want = c.In
				}
				if got.out != "" && got.out != want {
					t.Errorf("prints %q, want %q", got.out, want)
				}
			})
		}
	}
}
