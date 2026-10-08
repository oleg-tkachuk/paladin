package bucketh

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// denyOne allows every action but one.
type denyOne struct{ action cedar.Action }

func (d denyOne) IsAuthorized(_ context.Context, _ *cedar.Principal, a cedar.Action, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	if a == d.action {
		return cedar.DecisionDeny, nil
	}
	return cedar.DecisionAllow, nil
}

type fakeBackends struct {
	backend admindomain.StorageBackend
	err     error
}

func (f fakeBackends) Get(context.Context, string) (admindomain.StorageBackend, error) {
	return f.backend, f.err
}

// servesPublicly is a backend whose probe found anonymous reads enforced.
func servesPublicly() admindomain.StorageBackend {
	return admindomain.StorageBackend{
		BackendID: "primary",
		Features:  []features.Result{{Feature: features.AnonymousReadPolicy, Support: features.Supported}},
	}
}

func publicBucket() admindomain.Bucket {
	b := validBucket()
	b.PublicRead = true
	b.Constraints.AllowedContentTypes = []string{"image/jpeg", "image/webp"}
	return b
}

func reasonOf(t *testing.T, err error) commonv1.ErrorReason {
	t.Helper()
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("err = %v, want a connect error", err)
	}
	for _, d := range cerr.Details() {
		v, derr := connectproto.UnmarshalErrorDetail(d)
		if derr != nil {
			continue
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok {
			return commonv1.ErrorReason(commonv1.ErrorReason_value[info.GetReason()])
		}
	}
	return commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED
}

func TestCreatePublicBucket(t *testing.T) {
	rule := commonv1.ErrorReason_ERROR_REASON_PUBLIC_COLLECTION_RULE
	unsupported := commonv1.ErrorReason_ERROR_REASON_BACKEND_FEATURE_UNSUPPORTED
	withSupport := func(s features.Support) fakeBackends {
		b := servesPublicly()
		b.Features[0].Support = s
		return fakeBackends{backend: b}
	}
	kms := servesPublicly()
	// As the domain stores it, from either "aws:kms" in the configuration or
	// SSE_TYPE_KMS on the API.
	kms.SSE.Type = admindomain.SSETypeKMS

	for name, tc := range map[string]struct {
		bucket    func() admindomain.Bucket
		provision bool
		authz     cedar.Authorizer
		backends  BackendReader
		code      connect.Code
		reason    commonv1.ErrorReason
	}{
		"a private bucket with a CDN address": {
			func() admindomain.Bucket { b := validBucket(); b.PublicBaseURL = "https://cdn.example"; return b },
			false, allowAuthorizer{}, fakeBackends{backend: servesPublicly()}, connect.CodeFailedPrecondition, rule,
		},
		"not provisioned by Paladin": {
			publicBucket, false, allowAuthorizer{}, fakeBackends{backend: servesPublicly()}, connect.CodeFailedPrecondition, rule,
		},
		"no allowed content types": {
			func() admindomain.Bucket { b := publicBucket(); b.Constraints.AllowedContentTypes = nil; return b },
			true, allowAuthorizer{}, fakeBackends{backend: servesPublicly()}, connect.CodeFailedPrecondition, rule,
		},
		"an active content type": {
			func() admindomain.Bucket {
				b := publicBucket()
				b.Constraints.AllowedContentTypes = []string{"image/svg+xml"}
				return b
			},
			true, allowAuthorizer{}, fakeBackends{backend: servesPublicly()}, connect.CodeFailedPrecondition, rule,
		},
		"a CDN address with a query": {
			func() admindomain.Bucket { b := publicBucket(); b.PublicBaseURL = "https://cdn.example?x=1"; return b },
			true, allowAuthorizer{}, fakeBackends{backend: servesPublicly()}, connect.CodeInvalidArgument, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED,
		},
		"without ConfigurePublicRead": {
			publicBucket, true, denyOne{cedar.ActionConfigurePublicRead}, fakeBackends{backend: servesPublicly()}, connect.CodePermissionDenied, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED,
		},
		"no backend reader": {
			publicBucket, true, allowAuthorizer{}, nil, connect.CodeFailedPrecondition, unsupported,
		},
		"never probed": {
			publicBucket, true, allowAuthorizer{}, fakeBackends{backend: admindomain.StorageBackend{BackendID: "primary"}}, connect.CodeFailedPrecondition, unsupported,
		},
		"probed unsupported": {
			publicBucket, true, allowAuthorizer{}, withSupport(features.Unsupported), connect.CodeFailedPrecondition, unsupported,
		},
		"SSE-KMS": {
			publicBucket, true, allowAuthorizer{}, fakeBackends{backend: kms}, connect.CodeFailedPrecondition, unsupported,
		},
		"an unknown backend": {
			publicBucket, true, allowAuthorizer{}, fakeBackends{err: admindomain.ErrNotFound}, connect.CodeFailedPrecondition, commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED,
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := NewHandler(&fakeRepo{backendEnabled: true, getTxBucket: tc.bucket()}, okProvisioner{}, tc.authz)
			if tc.backends != nil {
				h.SetBackends(tc.backends)
			}
			_, err := h.CreateBucket(ctxAs(apiutil.RolePlatformAdmin),
				CreateBucketInput{Bucket: tc.bucket(), ProvisionOnBackend: tc.provision})
			if code(err) != tc.code {
				t.Fatalf("code = %v (%v), want %v", code(err), err, tc.code)
			}
			if tc.reason != commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
				if got := reasonOf(t, err); got != tc.reason {
					t.Errorf("reason = %v, want %v", got, tc.reason)
				}
			}
		})
	}
}

func TestCreatePublicBucketWhereTheBackendServesIt(t *testing.T) {
	b := publicBucket()
	b.PublicBaseURL = "https://cdn.example"
	repo := &fakeRepo{backendEnabled: true, getTxBucket: b}
	h := NewHandler(repo, okProvisioner{}, allowAuthorizer{})
	h.SetBackends(fakeBackends{backend: servesPublicly()})
	if _, err := h.CreateBucket(ctxAs(apiutil.RoleTenantProvisioner),
		CreateBucketInput{Bucket: b, ProvisionOnBackend: true}); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if !repo.createTxBucket.PublicRead || repo.createTxBucket.PublicBaseURL != b.PublicBaseURL ||
		repo.createTxBucket.ProvisionState != admindomain.BucketProvisionStatePending {
		t.Errorf("stored %+v, want a pending public bucket with its CDN address", repo.createTxBucket)
	}
}

func TestEnsureBucketNeverPublishes(t *testing.T) {
	h := NewHandler(&fakeRepo{backendEnabled: true}, okProvisioner{}, allowAuthorizer{})
	h.SetBackends(fakeBackends{backend: servesPublicly()})
	_, _, err := h.EnsureBucket(ctxAs(apiutil.RolePlatformAdmin), CreateBucketInput{Bucket: publicBucket(), ProvisionOnBackend: true})
	if reasonOf(t, err) != commonv1.ErrorReason_ERROR_REASON_PUBLIC_COLLECTION_RULE {
		t.Errorf("err = %v, want a public collection rule", err)
	}
}

func TestLifecycleRulesOnAPublicBucket(t *testing.T) {
	rules := []admindomain.LifecycleRule{{ID: "expire"}}
	for name, tc := range map[string]struct {
		public bool
		rules  []admindomain.LifecycleRule
		ok     bool
	}{
		"rules on a public bucket":   {true, rules, false},
		"clearing a public bucket's": {true, nil, true},
		"rules on a private bucket":  {false, rules, true},
	} {
		t.Run(name, func(t *testing.T) {
			b := validBucket()
			b.PublicRead = tc.public
			h := NewHandler(&fakeRepo{getBucket: b}, okProvisioner{}, allowAuthorizer{})
			_, err := h.SetLifecycleRules(ctxAs(apiutil.RolePlatformAdmin), b.BackendID, b.BucketName, tc.rules, 0)
			if tc.ok != (err == nil) {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if !tc.ok && reasonOf(t, err) != commonv1.ErrorReason_ERROR_REASON_PUBLIC_COLLECTION_RULE {
				t.Errorf("reason = %v, want a public collection rule", reasonOf(t, err))
			}
		})
	}
}
