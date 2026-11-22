package paladintest_test

import (
	"context"
	"encoding/json"
	"os"
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
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
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
}
