package paladintest_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"

	"connectrpc.com/connect"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// operationKey is a caller's key for one logical operation, carried on ctx.
const operationKey = "nightly-export-2026-10-06"

// The key a caller puts on ctx went on every call made with it, and the
// server answered a repeated key with its first response: DownloadMany under
// a key handed back the first object's URL — and so its bytes — for every
// name. Each name must read its own object.
func TestDownloadManyUnderAContextKeyReadsEachObject(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	want := map[string]string{}
	var names []string
	for i := range 4 {
		body := fmt.Sprintf("body %d", i)
		o := srv.Put(srv.Collection(), fmt.Sprintf("k%d", i), "text/plain", []byte(body))
		names = append(names, o.GetName())
		want[o.GetName()] = body
	}
	var (
		mu  sync.Mutex
		got = map[string]string{}
	)
	ctx := paladin.WithIdempotencyKey(context.Background(), operationKey)
	failures := paladin.DownloadMany(ctx, p.Data, names, 2, func(name string, r *paladin.ObjectReader) error {
		b, err := io.ReadAll(r)
		mu.Lock()
		defer mu.Unlock()
		got[name] = string(b)
		return err
	})
	if failures != nil {
		t.Fatalf("failures: %v", failures)
	}
	for name, body := range want {
		if got[name] != body {
			t.Errorf("%s read %q, want %q", name, got[name], body)
		}
	}
}

// UploadMany under one key used it for every input's UploadObject: the
// server answered all but the first with the first input's object.
func TestUploadManyUnderAContextKeyCreatesEachObject(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	var inputs []paladin.UploadInput
	for i := range 3 {
		body := []byte(fmt.Sprintf("payload %d", i))
		inputs = append(inputs, paladin.UploadInput{
			Parent: srv.Collection().String(), Key: fmt.Sprintf("u%d", i),
			ContentType: "text/plain", Size: int64(len(body)), Body: bytes.NewReader(body),
		})
	}
	ctx := paladin.WithIdempotencyKey(context.Background(), operationKey)
	objects, failures := paladin.UploadMany(ctx, p.Data, inputs, 2, paladin.UploadOptions{})
	if failures != nil {
		t.Fatalf("failures: %v", failures)
	}
	seen := map[string]bool{}
	for i, o := range objects {
		if seen[o.GetName()] {
			t.Errorf("input %d came back as %s, already returned for another input", i, o.GetName())
		}
		seen[o.GetName()] = true
	}
}

// A multipart upload under a key presigned every part with the first part's
// URL, which storage refuses for any other part.
func TestMultipartUploadUnderAContextKeyPresignsEachPart(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	// Three parts at the fake's part size, so PresignPart runs three times.
	body := bytes.Repeat([]byte("p"), 2*paladintest.PartSize+3)
	ctx := paladin.WithIdempotencyKey(context.Background(), operationKey)
	obj, err := paladin.Upload(ctx, p.Data, paladin.UploadInput{
		Parent: srv.Collection().String(), Key: "big.bin", ContentType: "application/octet-stream",
		Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{MultipartThreshold: paladintest.PartSize})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	r, err := paladin.Download(context.Background(), p.Data, obj.GetName(), paladin.DownloadOptions{})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer func() { _ = r.Close() }()
	if got, _ := io.ReadAll(r); !bytes.Equal(got, body) {
		t.Errorf("stored %q, want %q", got, body)
	}
}

// The fake answers a key reused for a different request as the server does.
func TestTheFakeRefusesAKeyReusedForAnotherRequest(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	a := srv.Put(srv.Collection(), "a", "text/plain", []byte("a"))
	b := srv.Put(srv.Collection(), "b", "text/plain", []byte("b"))
	// The raw client, so nothing between the caller and the fake narrows
	// the key the way the SDK's helpers now do.
	ctx := paladin.WithIdempotencyKey(context.Background(), operationKey)
	call := func(name string) error {
		_, err := p.Data.Object.DownloadObject(ctx, connect.NewRequest(&datav1.DownloadObjectRequest{Name: name}))
		return err
	}
	if err := call(a.GetName()); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := call(a.GetName()); err != nil {
		t.Fatalf("same request again must replay: %v", err)
	}
	if err := call(b.GetName()); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument for the key reused on another object", connect.CodeOf(err))
	}
}
