package admin

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1"
)

// requireCompilablePolicy has its own test. What had none is whether the
// servers actually CALL it.
//
// Mutation testing found that: inverting the check at any of its eleven call
// sites — which accepts an uncompilable policy and rejects a valid one —
// survived the package suite. The function was held; its application was not,
// and a guard nobody applies is decoration.
//
// The stake is authorisation. Cedar text that does not compile, stored on a
// tenant or a bucket, is a policy that cannot be evaluated: whatever the engine
// does with it at request time, it is not what the operator wrote.
//
// The handler is nil in every case here, deliberately. The guard runs before
// anything touches it, so a nil handler is exactly the assertion — if the
// guard stops applying, the test panics instead of quietly passing.
const brokenPolicy = "permit(principal, action, resource) when { this is not cedar"

func TestUncompilableCedarIsRefusedByEveryRPCThatTakesIt(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		call func() error
	}{
		{"CreateBucket", func() error {
			s := &BucketServer{}
			_, err := s.CreateBucket(ctx, connect.NewRequest(&pb.CreateBucketRequest{
				Parent: "storageBackends/primary", BucketId: "b",
				Bucket: &pb.Bucket{CedarPolicy: brokenPolicy},
			}))
			return err
		}},
		{"UpdateBucket", func() error {
			s := &BucketServer{}
			_, err := s.UpdateBucket(ctx, connect.NewRequest(&pb.UpdateBucketRequest{
				Bucket: &pb.Bucket{
					Name:        "storageBackends/primary/buckets/b",
					CedarPolicy: brokenPolicy,
				},
			}))
			return err
		}},
		{"SetBucketPolicy", func() error {
			s := &BucketServer{}
			_, err := s.SetBucketPolicy(ctx, connect.NewRequest(&pb.SetBucketPolicyRequest{
				Name: "storageBackends/primary/buckets/b", CedarPolicy: brokenPolicy,
			}))
			return err
		}},
		{"CreateTenant", func() error {
			s := &TenantServer{}
			_, err := s.CreateTenant(ctx, connect.NewRequest(&pb.CreateTenantRequest{
				TenantId: "t", Tenant: &pb.Tenant{InheritedCedarPolicy: brokenPolicy},
			}))
			return err
		}},
		{"UpdateTenant", func() error {
			s := &TenantServer{}
			_, err := s.UpdateTenant(ctx, connect.NewRequest(&pb.UpdateTenantRequest{
				Tenant: &pb.Tenant{Name: "tenants/t", InheritedCedarPolicy: brokenPolicy},
			}))
			return err
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call()
			if err == nil {
				t.Fatal("an uncompilable Cedar policy was accepted — it will be " +
					"stored and evaluated as something other than what was written")
			}
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Errorf("code = %v, want invalid_argument", got)
			}
			if !strings.Contains(err.Error(), "cedar policy does not compile") {
				t.Errorf("error %q does not say the policy is the problem — an "+
					"operator gets a rejection with no way to know which field", err)
			}
		})
	}
}

// The other direction: an EMPTY policy is legitimate — it means "inherit" —
// and must not be turned into an error by tightening the guard. Without this
// the test above is satisfied by a server that refuses every request.
func TestAnEmptyCedarPolicyIsNotAnError(t *testing.T) {
	if err := requireCompilablePolicy(""); err != nil {
		t.Errorf("an empty policy was rejected: %v — empty means inherit, and "+
			"most buckets carry no policy of their own", err)
	}
}
