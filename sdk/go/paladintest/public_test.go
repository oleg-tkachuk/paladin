package paladintest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"testing"

	"connectrpc.com/connect"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// publicKeyShape is a key the server names a public object with.
var publicKeyShape = regexp.MustCompile(`^[a-z2-7]{26}$`)

func get(t *testing.T, url string) (int, []byte, http.Header) {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx,gosec // a stranger's plain GET of the fake's own URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body, resp.Header
}

// An object uploaded into a public collection is named by the fake and served
// unsigned at its public_url with the collection's Cache-Control, as the
// server and its store do; deleted, the URL answers 404.
func TestAPublicObjectIsServedAtItsURL(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	body := []byte("a published picture")
	obj, err := paladin.Upload(ctx, p.Data, paladin.UploadInput{
		Parent: srv.PublicCollection().String(), ContentType: "image/jpeg",
		Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !publicKeyShape.MatchString(obj.GetKey()) || obj.GetPublicUrl() == "" {
		t.Fatalf("object %v, want a server-named key and a public URL", obj)
	}
	status, got, header := get(t, obj.GetPublicUrl())
	if status != http.StatusOK || !bytes.Equal(got, body) || header.Get("Cache-Control") != paladintest.PublicCacheControl {
		t.Fatalf("GET = %d %q %v, want the bytes with the collection's Cache-Control", status, got, header)
	}
	if _, err := p.Data.Object.DeleteObject(ctx, connect.NewRequest(&datav1.DeleteObjectRequest{
		Name: obj.GetName(), ResourceVersion: obj.GetResourceVersion(), Permanent: true,
	})); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := get(t, obj.GetPublicUrl()); status != http.StatusNotFound {
		t.Errorf("after delete GET = %d, want 404", status)
	}
}

func TestAPublicCollectionRefusesWhatTheServerDoes(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	_, err := p.Data.Object.UploadObject(ctx, connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: srv.PublicCollection().String(), Key: "chosen.jpg", ContentType: "image/jpeg", SizeHintBytes: 1,
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256, ChecksumValue: oneByteSHA256,
	}))
	if paladin.Reason(err) != commonv1.ErrorReason_ERROR_REASON_PUBLIC_COLLECTION_RULE {
		t.Errorf("a client's key: err = %v, want the public collection rule", err)
	}

	obj := srv.Put(srv.PublicCollection(), "", "image/jpeg", []byte("x"))
	_, err = p.Data.Object.DeleteObject(ctx, connect.NewRequest(&datav1.DeleteObjectRequest{
		Name: obj.GetName(), ResourceVersion: obj.GetResourceVersion(),
	}))
	if !errors.Is(err, paladin.ErrFailedPrecondition) || paladin.Reason(err) != commonv1.ErrorReason_ERROR_REASON_PUBLIC_COLLECTION_RULE {
		t.Errorf("a delete to the trash: err = %v, want the public collection rule", err)
	}
	if status, _, _ := get(t, obj.GetPublicUrl()); status != http.StatusOK {
		t.Error("a refused delete stopped the object being served")
	}
}

// The upload URL is bound to the Cache-Control, so a PUT without it is
// refused, as the store refuses an altered signed header.
func TestAPublicUploadIsBoundToItsCacheControl(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	resp, err := p.Data.Object.UploadObject(context.Background(), connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: srv.PublicCollection().String(), ContentType: "image/jpeg", SizeHintBytes: 1,
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256, ChecksumValue: oneByteSHA256,
	}))
	if err != nil {
		t.Fatal(err)
	}
	headers := resp.Msg.GetUploadUrl().GetRequiredHeaders()
	if headers["Cache-Control"] != paladintest.PublicCacheControl {
		t.Fatalf("required headers %v, want the Cache-Control", headers)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, resp.Msg.GetUploadUrl().GetUrl(), bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		if k != "Cache-Control" {
			req.Header.Set(k, v)
		}
	}
	put, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = put.Body.Close()
	if put.StatusCode/100 == 2 {
		t.Error("a PUT without the signed Cache-Control was stored")
	}
}

// A private collection is as it was: the client's key stands, no URL.
func TestAPrivateObjectHasNoPublicURL(t *testing.T) {
	srv := paladintest.New(t)
	if obj := srv.Put(srv.Collection(), "mine", "image/jpeg", []byte("x")); obj.GetKey() != "mine" || obj.GetPublicUrl() != "" {
		t.Errorf("object %v, want the private one untouched", obj)
	}
}

// The store serves what a PUT stored before CompleteObject: it knows nothing
// of Paladin's states.
func TestAPublicObjectIsServedAsSoonAsItsBytesLand(t *testing.T) {
	srv := paladintest.New(t)
	resp, err := srv.Connect().Data.Object.UploadObject(context.Background(), connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: srv.PublicCollection().String(), ContentType: "image/jpeg", SizeHintBytes: 1,
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256, ChecksumValue: oneByteSHA256,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := get(t, resp.Msg.GetObject().GetPublicUrl()); status != http.StatusNotFound {
		t.Fatalf("before the PUT GET = %d, want 404", status)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, resp.Msg.GetUploadUrl().GetUrl(), bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range resp.Msg.GetUploadUrl().GetRequiredHeaders() {
		req.Header.Set(k, v)
	}
	put, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = put.Body.Close()
	if status, got, _ := get(t, resp.Msg.GetObject().GetPublicUrl()); status != http.StatusOK || string(got) != "x" {
		t.Errorf("after the PUT, before Complete, GET = %d %q, want the bytes", status, got)
	}
}
