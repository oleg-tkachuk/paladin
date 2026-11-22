package paladintest_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// objectKeysTable is the server's key rule, which the server's tests read too.
const objectKeysTable = "../../testdata/object_keys.json"

// The fake stores an object under the keys the server does and refuses the
// rest, on an upload and on a multipart initiate alike.
func TestTheFakeHoldsKeysToTheServersRule(t *testing.T) {
	raw, err := os.ReadFile(objectKeysTable)
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Canonical []string `json:"canonical"`
		Refused   []string `json:"refused"`
		Lengths   []struct {
			Unit      string `json:"unit"`
			Segments  []int  `json:"segments"`
			Canonical bool   `json:"canonical"`
		} `json:"lengths"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Lengths) == 0 {
		t.Fatal("the table lists no length cases")
	}
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	parent := srv.Collection().String()
	upload := func(key string) error {
		_, err := p.Data.Object.UploadObject(ctx, &datav1.UploadObjectRequest{
			Parent: parent, Key: key, ContentType: testContentType, ChecksumValue: emptySHA256,
		})
		return err
	}
	initiate := func(key string) error {
		_, err := p.Data.MultipartUpload.InitiateMultipartUpload(ctx, &datav1.InitiateMultipartUploadRequest{
			Parent: parent, Key: key, ContentType: testContentType, SizeBytes: 1,
			ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
		})
		return err
	}
	for _, key := range table.Canonical {
		if err := upload(key); err != nil {
			t.Errorf("upload %q: %v, want it stored", key, err)
		}
		if err := initiate("multipart/" + key); err != nil {
			t.Errorf("initiate %q: %v, want it stored", key, err)
		}
	}
	for _, key := range table.Refused {
		if err := upload(key); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("upload %q: %v, want InvalidArgument", key, err)
		}
		if err := initiate(key); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("initiate %q: %v, want InvalidArgument", key, err)
		}
	}
	// A key at the length limit has no room for a prefix that would keep the
	// multipart upload off the plain one's key, so it initiates on a fake of
	// its own.
	other := paladintest.New(t)
	otherP, otherParent := other.Connect(), other.Collection().String()
	initiateElsewhere := func(key string) error {
		_, err := otherP.Data.MultipartUpload.InitiateMultipartUpload(ctx, &datav1.InitiateMultipartUploadRequest{
			Parent: otherParent, Key: key, ContentType: testContentType, SizeBytes: 1,
			ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
		})
		return err
	}
	for _, c := range table.Lengths {
		segments := make([]string, 0, len(c.Segments))
		for _, n := range c.Segments {
			if n%len(c.Unit) != 0 {
				t.Fatalf("length case %q: %d bytes is not a whole number of units", c.Unit, n)
			}
			segments = append(segments, strings.Repeat(c.Unit, n/len(c.Unit)))
		}
		key := strings.Join(segments, "/")
		for name, err := range map[string]error{"upload": upload(key), "initiate": initiateElsewhere(key)} {
			if got := err == nil; got != c.Canonical {
				t.Errorf("%s %q in segments of %v bytes: %v, want stored %v", name, c.Unit, c.Segments, err, c.Canonical)
			}
		}
	}
}
