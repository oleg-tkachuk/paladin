package contract

import (
	"testing"

	"buf.build/go/protovalidate"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// A bucket name S3 refuses must be refused at the API. The backend's
// CreateBucket rejects it only after the control plane has written the bucket
// row, which then sits in the console as "failed".
func TestCreateBucketRejectsNamesS3Refuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		valid bool
	}{
		{"paladin-primary", true},
		{"logs.2026", true},
		{"abc", true},
		{"bad_name", false},
		{"Bad-Name", false},
		{"-leading", false},
		{"trailing-", false},
		{".leading", false},
		{"ab", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := protovalidate.Validate(&adminv1.CreateBucketRequest{
				Parent:   "storageBackends/primary",
				BucketId: tc.name,
				Bucket:   &adminv1.Bucket{},
			})
			if tc.valid && err != nil {
				t.Errorf("rejected a valid name: %v", err)
			}
			if !tc.valid && err == nil {
				t.Error("accepted a name S3 refuses")
			}
		})
	}
}
