//go:build sdkconformance

// Package conformance runs the shared SDK scenarios (sdk/testdata/scenarios.json)
// against a live server — the compose stack backend/scripts/verify-stack.sh
// boots, phase sdk. The Python SDK runs the same scenarios, so the two cannot
// disagree with the server and both pass.
//
//	PALADIN_SDK_DATA_URL=http://localhost:18080 PALADIN_SDK_TOKEN=… \
//	PALADIN_SDK_COLLECTION=tenants/…/collections/default \
//	go test -tags=sdkconformance ./conformance/...
package conformance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"testing"

	"connectrpc.com/connect"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// The environment the stack gate sets.
const (
	envDataURL    = "PALADIN_SDK_DATA_URL"
	envToken      = "PALADIN_SDK_TOKEN"
	envCollection = "PALADIN_SDK_COLLECTION"
	scenariosFile = "../../testdata/scenarios.json"
	// multipartSize is over the threshold the scenario sets, so the upload
	// goes in parts.
	multipartThreshold = 5 << 20
	multipartSize      = 2*multipartThreshold + 7
	smallSize          = 4 << 10
)

type env struct {
	data       *paladin.DataPlane
	collection paladin.CollectionName
}

func setup(t *testing.T) env {
	t.Helper()
	url, token, collection := os.Getenv(envDataURL), os.Getenv(envToken), os.Getenv(envCollection)
	if url == "" || token == "" || collection == "" {
		t.Fatalf("set %s, %s and %s: under the sdkconformance tag a missing server is a failure, not a skip",
			envDataURL, envToken, envCollection)
	}
	name, err := paladin.ParseCollectionName(collection)
	if err != nil {
		t.Fatal(err)
	}
	p, err := paladin.Connect(paladin.Endpoints{Data: url}, paladin.WithTokens(paladin.StaticToken(token)))
	if err != nil {
		t.Fatal(err)
	}
	return env{data: p.Data, collection: name}
}

func random(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func key(t *testing.T) string { return "sdk-conformance/" + t.Name() + "/" + rand.Text() }

func put(t *testing.T, e env, k string, body []byte, threshold int64) *datav1.Object {
	t.Helper()
	obj, err := paladin.Upload(context.Background(), e.data, paladin.UploadInput{
		Parent: e.collection.String(), Key: k, ContentType: "application/octet-stream",
		Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{MultipartThreshold: threshold})
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

func get(t *testing.T, e env, name string, opts paladin.DownloadOptions) []byte {
	t.Helper()
	r, err := paladin.Download(context.Background(), e.data, name, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var scenarios = map[string]func(*testing.T, env){
	"upload_small": func(t *testing.T, e env) {
		body := random(t, smallSize)
		obj := put(t, e, key(t), body, 0)
		if !bytes.Equal(get(t, e, obj.GetName(), paladin.DownloadOptions{}), body) {
			t.Error("the content read back differs")
		}
	},
	"upload_multipart": func(t *testing.T, e env) {
		body := random(t, multipartSize)
		obj := put(t, e, key(t), body, multipartThreshold)
		if !bytes.Equal(get(t, e, obj.GetName(), paladin.DownloadOptions{}), body) {
			t.Error("the content read back differs")
		}
	},
	"range_read": func(t *testing.T, e env) {
		body := random(t, smallSize)
		obj := put(t, e, key(t), body, 0)
		if got := get(t, e, obj.GetName(), paladin.DownloadOptions{Offset: 100, Length: 50}); !bytes.Equal(got, body[100:150]) {
			t.Error("the range read back differs")
		}
	},
	"lookup_by_uri": func(t *testing.T, e env) {
		k := key(t)
		body := random(t, smallSize)
		put(t, e, k, body, 0)
		r, err := paladin.DownloadURI(context.Background(), e.data,
			paladin.ObjectURI{Collection: e.collection, Key: k}.String(), paladin.DownloadOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Close() }()
		if got, _ := io.ReadAll(r); !bytes.Equal(got, body) {
			t.Error("the object found by URI differs")
		}
	},
	"list_pages": func(t *testing.T, e env) {
		for range 3 {
			put(t, e, key(t), random(t, 1), 0)
		}
		// One per page, so following the pages is what finds them all.
		const pageSize = 1
		seen := 0
		for _, err := range paladin.Pages(context.Background(), e.data.Object.ListObjects,
			&datav1.ListObjectsRequest{Parent: e.collection.String(), Page: &commonv1.PageRequest{PageSize: pageSize}},
			(*datav1.ListObjectsResponse).GetObjects) {
			if err != nil {
				t.Fatal(err)
			}
			seen++
		}
		if seen < 3 {
			t.Errorf("listed %d objects, want at least the 3 just uploaded (page size %d)", seen, pageSize)
		}
	},
	"not_found_is_typed": func(t *testing.T, e env) {
		name := e.collection.String() + "/objects/00000000-0000-4000-8000-000000000000"
		_, err := e.data.Object.GetObject(context.Background(), connect.NewRequest(&datav1.GetObjectRequest{Name: name}))
		if !errors.Is(err, paladin.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	},
	"idempotent_replay": func(t *testing.T, e env) {
		ctx := paladin.WithIdempotencyKey(context.Background(), rand.Text())
		req := &datav1.UploadObjectRequest{Parent: e.collection.String(), Key: key(t), ContentType: "text/plain", SizeHintBytes: 1}
		first, err := e.data.Object.UploadObject(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}
		again, err := e.data.Object.UploadObject(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}
		if again.Msg.GetObject().GetName() != first.Msg.GetObject().GetName() {
			t.Errorf("a repeat with the same key made %s, want the first response's %s",
				again.Msg.GetObject().GetName(), first.Msg.GetObject().GetName())
		}
	},
	"error_names_the_release": func(t *testing.T, e env) {
		name := e.collection.String() + "/objects/00000000-0000-4000-8000-000000000000"
		_, err := e.data.Object.GetObject(context.Background(), connect.NewRequest(&datav1.GetObjectRequest{Name: name}))
		var pe *paladin.Error
		if !errors.As(err, &pe) || pe.ServerVersion == "" {
			t.Errorf("err = %v; want a *paladin.Error naming the server's release", err)
		}
	},
}

func TestEveryScenarioIsImplemented(t *testing.T) {
	raw, err := os.ReadFile(scenariosFile)
	if err != nil {
		t.Fatal(err)
	}
	var listed struct{ Scenarios []string }
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	var have []string
	for name := range scenarios {
		have = append(have, name)
	}
	sort.Strings(have)
	sort.Strings(listed.Scenarios)
	if fmt.Sprint(have) != fmt.Sprint(listed.Scenarios) {
		t.Fatalf("this runner implements %v, the shared list is %v", have, listed.Scenarios)
	}
}

func TestScenarios(t *testing.T) {
	e := setup(t)
	for name, run := range scenarios {
		t.Run(name, func(t *testing.T) { run(t, e) })
	}
}
