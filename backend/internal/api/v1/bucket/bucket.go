// Package bucket holds the provisioner seam: the CreateBucket/DeleteBucket
// calls a storage backend has to answer, declared here so handler tests can
// fake them and s3adapter can implement them.
//
// It is all that survives of what was once this domain's whole v1 layer. The
// handler went first — nothing constructed it, every BucketService RPC is
// served by api/admin/v1/bucketh, and its unreachable DeleteBucket carried a
// referential guard that the live handler did not, which cost a debugging
// session that ended in Postgres refusing the delete on a foreign key.
//
// The row type, the repository interface, the Create/Update/List argument
// structs and the version-mismatch sentinel followed, along with the
// adapters.BucketRepo that existed only to satisfy them and the
// wire.Repos.Bucket field that was assigned and never read. Removing the
// handler had removed their only consumer; what remained referred solely to
// itself.
package bucket

import "context"

// Provisioner abstracts the AWS-side CreateBucket call so handler tests can
// fake it. Implementations live in s3adapter.
type Provisioner interface {
	// CreateBucket provisions a real S3 bucket on the backend. Returns nil
	// if the bucket already exists (idempotent).
	CreateBucket(ctx context.Context, backendID, bucketName, region string) error
	// DeleteBucket removes the real S3 bucket. Optional — BucketService
	// only calls this when delete_remote=true.
	DeleteBucket(ctx context.Context, backendID, bucketName string) error
}
