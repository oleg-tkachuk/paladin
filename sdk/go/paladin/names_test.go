package paladin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// sharedCases is sdk/testdata/names.json, which the Python SDK's tests read
// too: both SDKs must accept, refuse and print the same names.
const sharedCases = "../../testdata/names.json"

type nameCase struct {
	In         string `json:"in"`
	OK         bool   `json:"ok"`
	Out        string `json:"out"`
	Tenant     string `json:"tenant"`
	Collection string `json:"collection"`
	Object     string `json:"object"`
	Version    string `json:"version"`
	Key        string `json:"key"`
}

func loadNameCases(t *testing.T) map[string][]nameCase {
	t.Helper()
	raw, err := os.ReadFile(sharedCases)
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	out := map[string][]nameCase{}
	for kind, msg := range all {
		if kind[0] == '_' {
			continue
		}
		var cases []nameCase
		if err := json.Unmarshal(msg, &cases); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		out[kind] = cases
	}
	return out
}

// parsed is what a parser made of a case: the fields, and the string it prints.
type parsed struct {
	tenant, collection, object, version, key, out string
}

var parsers = map[string]func(string) (parsed, error){
	"tenant": func(s string) (parsed, error) {
		n, err := paladin.ParseTenantName(s)
		return parsed{tenant: n.Tenant, out: n.String()}, err
	},
	"collection": func(s string) (parsed, error) {
		n, err := paladin.ParseCollectionName(s)
		return parsed{tenant: n.Tenant, collection: n.Collection, out: n.String()}, err
	},
	"object": func(s string) (parsed, error) {
		n, err := paladin.ParseObjectName(s)
		return parsed{tenant: n.Tenant, collection: n.Collection, object: n.Object, out: n.String()}, err
	},
	"version": func(s string) (parsed, error) {
		n, err := paladin.ParseObjectVersionName(s)
		return parsed{tenant: n.Tenant, collection: n.Collection, object: n.Object, version: n.Version, out: n.String()}, err
	},
	"uri": func(s string) (parsed, error) {
		u, err := paladin.ParseObjectURI(s)
		return parsed{tenant: u.Collection.Tenant, collection: u.Collection.Collection, key: u.Key, out: u.String()}, err
	},
}

func TestNamesAgainstTheSharedCases(t *testing.T) {
	all := loadNameCases(t)
	if len(all) != len(parsers) {
		t.Fatalf("the shared cases cover %d kinds, the parsers %d", len(all), len(parsers))
	}
	for kind, cases := range all {
		parse := parsers[kind]
		for _, tc := range cases {
			got, err := parse(tc.In)
			if !tc.OK {
				if !errors.Is(err, paladin.ErrInvalidName) {
					t.Errorf("%s %q: err = %v, want ErrInvalidName", kind, tc.In, err)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s %q: %v", kind, tc.In, err)
				continue
			}
			want := parsed{tc.Tenant, tc.Collection, tc.Object, tc.Version, tc.Key, tc.In}
			if tc.Out != "" {
				want.out = tc.Out
			}
			if got != want {
				t.Errorf("%s %q:\n got %+v\nwant %+v", kind, tc.In, got, want)
			}
			// What it prints parses back to the same.
			again, err := parse(got.out)
			if err != nil || again != got {
				t.Errorf("%s %q does not round-trip: %+v, %v", kind, got.out, again, err)
			}
		}
	}
}

func TestDownloadURILooksTheObjectUpByKey(t *testing.T) {
	data, dp, _ := newTransfer(t, 0)
	body := []byte("by key")
	upload(t, data, "k", body)
	const tenant = "0b6f7c1e-4f6a-4a39-9d55-3a8c2b7e1f00"
	uri := paladin.ObjectURI{Collection: paladin.CollectionName{Tenant: tenant, Collection: "c"}, Key: "k"}.String()
	r, err := paladin.DownloadURI(context.Background(), data, uri, paladin.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, body) {
		t.Errorf("downloaded %q, want %q", got, body)
	}
	if want := "tenants/" + tenant + "/collections/c|k"; len(dp.lookedUp) != 1 || dp.lookedUp[0] != want {
		t.Errorf("LookupObject got %v, want [%s]", dp.lookedUp, want)
	}
	if _, err := paladin.DownloadURI(context.Background(), data, "paladin://docs/k", paladin.DownloadOptions{}); !errors.Is(err, paladin.ErrInvalidName) {
		t.Errorf("err = %v, want ErrInvalidName", err)
	}
}
