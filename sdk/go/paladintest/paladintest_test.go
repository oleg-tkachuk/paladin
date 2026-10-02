package paladintest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"connectrpc.com/connect"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

func read(t *testing.T, r *paladin.ObjectReader, err error) []byte {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestUploadAndDownloadThroughTheFake(t *testing.T) {
	cases := []struct {
		name      string
		size      int
		threshold int64
	}{
		{"one PUT", 1 << 10, 0},
		{"multipart", 2*paladintest.PartSize + 3, paladintest.PartSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := paladintest.New(t)
			p := srv.Connect()
			body := bytes.Repeat([]byte("paladin "), tc.size/len("paladin ")+1)[:tc.size]
			obj, err := paladin.Upload(context.Background(), p.Data, paladin.UploadInput{
				Parent: srv.Collection().String(), Key: "docs/a.pdf", ContentType: "application/pdf",
				Size: int64(len(body)), Body: bytes.NewReader(body),
			}, paladin.UploadOptions{MultipartThreshold: tc.threshold})
			if err != nil {
				t.Fatal(err)
			}
			if stored, ok := srv.Content(obj.GetName()); !ok || !bytes.Equal(stored, body) {
				t.Fatal("the fake does not hold what was uploaded")
			}
			r, err := paladin.Download(context.Background(), p.Data, obj.GetName(), paladin.DownloadOptions{})
			if got := read(t, r, err); !bytes.Equal(got, body) {
				t.Errorf("downloaded %d bytes, want %d", len(got), len(body))
			}
			uri := paladin.ObjectURI{Collection: srv.Collection(), Key: "docs/a.pdf"}.String()
			r, err = paladin.DownloadURI(context.Background(), p.Data, uri, paladin.DownloadOptions{Offset: 1, Length: 3})
			if got := read(t, r, err); !bytes.Equal(got, body[1:4]) {
				t.Errorf("range by URI = %q, want %q", got, body[1:4])
			}
		})
	}
}

func TestTheFakeRecordsTheChecksumDownloadVerifies(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	body := []byte("verified")
	obj, err := paladin.Upload(context.Background(), p.Data, paladin.UploadInput{
		Parent: srv.Collection().String(), Key: "k", Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if obj.GetChecksum().GetAlgorithm() != paladin.ChecksumSHA256 || obj.GetChecksum().GetValue() == "" {
		t.Errorf("checksum = %v, want the SHA-256 the upload completed with", obj.GetChecksum())
	}
}

func TestPutListAndDelete(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	for _, key := range []string{"c", "a", "b"} {
		srv.Put(srv.Collection(), key, "text/plain", []byte(key))
	}
	srv.Put(srv.Collection("other"), "z", "text/plain", []byte("z"))
	var keys []string
	for o, err := range paladin.Pages(context.Background(), p.Data.Object.ListObjects,
		&datav1.ListObjectsRequest{Parent: srv.Collection().String()}, (*datav1.ListObjectsResponse).GetObjects) {
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, o.GetKey())
	}
	if len(keys) != 3 || keys[0] != "a" || keys[2] != "c" {
		t.Errorf("listed %v, want [a b c] from the one collection", keys)
	}
	obj, err := paladin.LookupObject(context.Background(), p.Data, paladin.ObjectURI{Collection: srv.Collection(), Key: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Data.Object.DeleteObject(context.Background(), connect.NewRequest(&datav1.DeleteObjectRequest{Name: obj.GetName()})); err != nil {
		t.Fatal(err)
	}
	_, err = p.Data.Object.GetObject(context.Background(), connect.NewRequest(&datav1.GetObjectRequest{Name: obj.GetName()}))
	if !errors.Is(err, paladin.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound after the delete", err)
	}
}

func TestEverythingElseIsUnimplemented(t *testing.T) {
	p := paladintest.New(t).Connect()
	_, err := p.Admin.Tenant.ListTenants(context.Background(), connect.NewRequest(&adminv1.ListTenantsRequest{}))
	if !errors.Is(err, paladin.ErrContractSkew) {
		t.Errorf("err = %v, want Unimplemented, which the SDK reads as a contract skew", err)
	}
}

func TestTheFakeRefusesWhatTheServerRefuses(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	// A collection under the tenant's slug, not its id.
	_, err := p.Data.Object.UploadObject(context.Background(), connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: "tenants/acme/collections/c", Key: "k",
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("err = %v, want InvalidArgument", err)
	}
	// A completion whose ETag is not the content's.
	up, err := p.Data.Object.UploadObject(context.Background(), connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: srv.Collection().String(), Key: "k",
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Data.Object.CompleteObject(context.Background(), connect.NewRequest(&datav1.CompleteObjectRequest{Name: up.Msg.GetObject().GetName(), Etag: "x"}))
	if !errors.Is(err, paladin.ErrFailedPrecondition) {
		t.Errorf("err = %v, want FailedPrecondition", err)
	}
}
