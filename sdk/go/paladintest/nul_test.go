package paladintest_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// withNUL is request text holding NUL (U+0000) between printable characters.
const withNUL = "a\x00b"

// The server refuses a request string holding NUL as InvalidArgument naming
// the field, before any handler and before validation. The fake ran
// protovalidate alone, so a test passed on a call the server refuses.
func TestTheFakeRefusesRequestTextHoldingNUL(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		req  *datav1.UploadObjectRequest
		want string
	}{
		{"a key", &datav1.UploadObjectRequest{
			Parent: srv.Collection().String(), Key: withNUL, ContentType: testContentType,
			SizeHintBytes: 1, ChecksumValue: oneByteSHA256,
		}, "key: holds a NUL character"},
		{"a metadata value", &datav1.UploadObjectRequest{
			Parent: srv.Collection().String(), Key: "k", ContentType: testContentType,
			SizeHintBytes: 1, ChecksumValue: oneByteSHA256, Metadata: map[string]string{"m": withNUL},
		}, "metadata.value: holds a NUL character"},
		// Ahead of validation, as on the server: the missing content type is
		// not what the caller is told.
		{"a request validation also refuses", &datav1.UploadObjectRequest{
			Parent: srv.Collection().String(), Key: withNUL,
		}, "key: holds a NUL character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Data.Object.UploadObject(ctx, tc.req)
			if !errors.Is(err, paladin.ErrInvalidArgument) {
				t.Fatalf("err = %v, want InvalidArgument", err)
			}
			cerr, ok := errors.AsType[*connect.Error](err)
			if !ok || cerr.Message() != tc.want {
				t.Errorf("err = %v, want the message %q", err, tc.want)
			}
		})
	}
	list, err := p.Data.Object.ListObjects(ctx, &datav1.ListObjectsRequest{Parent: srv.Collection().String()})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(list.GetObjects()); n != 0 {
		t.Errorf("the fake created %d objects for refused requests", n)
	}
}
