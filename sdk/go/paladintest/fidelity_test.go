package paladintest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect"

	validatepb "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// The server's answer to a request its contract's rules refuse begins so.
const validationFailed = "validation error"

// oneByteSHA256 is the base64 SHA-256 of "x", what an upload of it is
// registered with.
const oneByteSHA256 = "LXEWQrcmsEQBYnyp+6wy9chTD7GQPMTbAiWHF5IaSIE="

// nextVersion is the resource_version after one more change to the row.
func nextVersion(t *testing.T, version string) string {
	t.Helper()
	n, err := strconv.ParseInt(version, 10, 64)
	if err != nil {
		t.Fatalf("resource_version %q: %v", version, err)
	}
	return strconv.FormatInt(n+1, 10)
}

// A request the server's protovalidate interceptor refuses reached the
// fake's handlers, so a test passed on a call the server would answer
// InvalidArgument.
func TestTheFakeValidatesEveryRequest(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"GetObject with no name": func() error {
			_, err := p.Data.Object.GetObject(ctx, connect.NewRequest(&datav1.GetObjectRequest{}))
			return err
		},
		"UploadObject with no content type": func() error {
			_, err := p.Data.Object.UploadObject(ctx, connect.NewRequest(&datav1.UploadObjectRequest{
				Parent: srv.Collection().String(), Key: "k", SizeHintBytes: 1,
			}))
			return err
		},
		"DeleteObject with no resource_version": func() error {
			obj := srv.Put(srv.Collection(), "k", testContentType, []byte("x"))
			_, err := p.Data.Object.DeleteObject(ctx, connect.NewRequest(&datav1.DeleteObjectRequest{Name: obj.GetName()}))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			if !errors.Is(err, paladin.ErrInvalidArgument) || !strings.Contains(err.Error(), validationFailed) {
				t.Errorf("err = %v, want the server's validation failure", err)
			}
			if !hasViolations(err) {
				t.Errorf("err = %v, want the buf.validate.Violations detail the server attaches", err)
			}
		})
	}
}

// ListObjects ignored a filter and an ordering, so a test of a filtered
// listing passed on every object in the collection.
func TestListObjectsRefusesWhatItDoesNotApply(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	parent := srv.Collection().String()
	for name, req := range map[string]*datav1.ListObjectsRequest{
		"filter":     {Parent: parent, Filter: `key = "a"`},
		"order_by":   {Parent: parent, OrderBy: "key"},
		"sort_order": {Parent: parent, SortOrder: commonv1.SortOrder_SORT_ORDER_DESC},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := p.Data.Object.ListObjects(context.Background(), connect.NewRequest(req))
			if connect.CodeOf(err) != connect.CodeUnimplemented || !strings.Contains(err.Error(), name) {
				t.Errorf("err = %v, want Unimplemented naming %s", err, name)
			}
		})
	}
	if _, err := p.Data.Object.ListObjects(context.Background(), connect.NewRequest(&datav1.ListObjectsRequest{Parent: parent})); err != nil {
		t.Errorf("a plain listing: %v", err)
	}
}

// DeleteObject ignored resource_version, so a delete racing a change passed
// where the server answers a version conflict.
func TestDeleteObjectChecksTheVersion(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			version func(current string) string
			want    error
			reason  commonv1.ErrorReason
		}{
			{"the current version", func(v string) string { return v }, nil, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED},
			{"a stale version", func(v string) string { return nextVersion(t, v) }, paladin.ErrVersionConflict, commonv1.ErrorReason_ERROR_REASON_VERSION_CONFLICT},
			{"not a version", func(string) string { return "v1" }, paladin.ErrInvalidArgument, commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT},
		} {
			t.Run(tc.name+" permanent="+strconv.FormatBool(permanent), func(t *testing.T) {
				srv := paladintest.New(t)
				obj := srv.Put(srv.Collection(), "k", testContentType, []byte("x"))
				_, err := srv.Connect().Data.Object.DeleteObject(context.Background(), connect.NewRequest(&datav1.DeleteObjectRequest{
					Name: obj.GetName(), ResourceVersion: tc.version(obj.GetResourceVersion()), Permanent: permanent,
				}))
				if tc.want == nil {
					if err != nil {
						t.Fatalf("delete at the current version: %v", err)
					}
					return
				}
				if !errors.Is(err, tc.want) || paladin.Reason(err) != tc.reason {
					t.Errorf("err = %v (reason %v), want %v with %v", err, paladin.Reason(err), tc.want, tc.reason)
				}
				if _, ok := srv.Content(obj.GetName()); !ok {
					t.Error("a refused delete removed the object")
				}
			})
		}
	}
}

// Every object stayed at the version it was registered at, so a client
// holding a version from before a change deleted the object anyway.
func TestAChangeAdvancesTheVersion(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	registered, err := p.Data.Object.UploadObject(ctx, connect.NewRequest(&datav1.UploadObjectRequest{
		Parent: srv.Collection().String(), Key: "k", ContentType: testContentType, SizeHintBytes: 1,
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256, ChecksumValue: oneByteSHA256,
	}))
	if err != nil {
		t.Fatal(err)
	}
	name := registered.Msg.GetObject().GetName()
	srv.MarkFailed(name)
	got, err := p.Data.Object.GetObject(ctx, connect.NewRequest(&datav1.GetObjectRequest{Name: name}))
	if err != nil {
		t.Fatal(err)
	}
	if want := nextVersion(t, registered.Msg.GetObject().GetResourceVersion()); got.Msg.GetResourceVersion() != want {
		t.Errorf("version after a change = %q, want %q", got.Msg.GetResourceVersion(), want)
	}
}

// A multipart object was completed with no checksum, so a download of it was
// checked for its size alone. The fake records the composite the server
// does, over the part size, and Download recomputes it from the bytes.
func TestAMultipartDownloadIsVerified(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	body := bytes.Repeat([]byte("p"), 2*paladintest.PartSize+1) // three parts
	obj, err := paladin.Upload(ctx, p.Data, paladin.UploadInput{
		Parent: srv.Collection().String(), Key: "big", ContentType: testContentType,
		Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{MultipartThreshold: paladintest.PartSize})
	if err != nil {
		t.Fatal(err)
	}
	sum := obj.GetChecksum()
	if sum.GetPartSizeBytes() != paladintest.PartSize || !strings.HasSuffix(sum.GetValue(), "-3") {
		t.Fatalf("checksum = %v, want a composite over three %d-byte parts", sum, paladintest.PartSize)
	}
	read := func() error {
		r, err := paladin.Download(ctx, p.Data, obj.GetName(), paladin.DownloadOptions{})
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		_, err = io.Copy(io.Discard, r)
		return err
	}
	if err := read(); err != nil {
		t.Fatalf("a whole read of the stored bytes: %v", err)
	}
	// Storage answers with other bytes of the same length: only the
	// composite tells them apart. http.Error appends a newline.
	corrupt := strings.Repeat("q", len(body)-1)
	srv.FailStorage(func(r *http.Request) (int, string) {
		if r.Method == http.MethodGet {
			return http.StatusOK, corrupt
		}
		return 0, ""
	})
	var ie *paladin.IntegrityError
	if err := read(); !errors.As(err, &ie) || ie.What != paladin.ChecksumSHA256 {
		t.Fatalf("err = %v, want an IntegrityError on the composite", err)
	}
}

// hasViolations reports whether err carries a buf.validate.Violations detail.
func hasViolations(err error) bool {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return false
	}
	for _, d := range cerr.Details() {
		if msg, derr := d.Value(); derr == nil {
			if _, ok := msg.(*validatepb.Violations); ok {
				return true
			}
		}
	}
	return false
}
