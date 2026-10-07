package bucketh

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"connectrpc.com/connect"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

func init() {
	apiutil.RegisterError(features.ErrUnsupported, connect.CodeFailedPrecondition,
		commonv1.ErrorReason_ERROR_REASON_BACKEND_FEATURE_UNSUPPORTED)
}

// BackendReader reads a storage backend with its probed features. A public
// bucket is created only where the probe found anonymous reads enforced.
type BackendReader interface {
	Get(ctx context.Context, backendID string) (admindomain.StorageBackend, error)
}

// SetBackends wires the backend reader. Without one, no public bucket can be
// created: nothing shows the backend can serve it.
func (h *Handler) SetBackends(r BackendReader) { h.backends = r }

// publicBaseURLSchemes are the schemes a CDN address may use.
var publicBaseURLSchemes = map[string]bool{"https": true, "http": true}

// checkPublicBucket admits a bucket's public settings (ADR-0027). A private
// bucket may carry none of them; a public one must be provisioned by Paladin,
// which sets its policy, list only passive content types, be allowed by the
// ConfigurePublicRead action, and sit on a backend whose probe found
// anonymous reads enforced and which does not encrypt with SSE-KMS.
func (h *Handler) checkPublicBucket(ctx context.Context, in CreateBucketInput) error {
	b := in.Bucket
	if !b.PublicRead {
		if b.PublicBaseURL != "" {
			return apiutil.MapError(publicread.Rulef("public_base_url is for a public bucket"))
		}
		return nil
	}
	if !in.ProvisionOnBackend {
		return apiutil.MapError(publicread.Rulef(
			"a public bucket must be created with provision_on_backend: Paladin sets the policy that makes it public"))
	}
	if err := publicread.CheckAllowedTypes(b.Constraints.AllowedContentTypes); err != nil {
		return apiutil.MapError(err)
	}
	if err := checkPublicBaseURL(b.PublicBaseURL); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.authorize(ctx, cedar.ActionConfigurePublicRead, b.BackendID, b.BucketName, b.OwnerTenantID); err != nil {
		return err
	}
	if h.backends == nil {
		return apiutil.MapError(fmt.Errorf("%w: the backend's features cannot be read here", features.ErrUnsupported))
	}
	backend, err := h.backends.Get(ctx, b.BackendID)
	switch {
	case errors.Is(err, admindomain.ErrNotFound):
		return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("backend %q does not exist", b.BackendID))
	case err != nil:
		return apiutil.MapError(err)
	}
	if got := features.SupportOf(backend.Features, features.AnonymousReadPolicy); got != features.Supported {
		return apiutil.MapError(fmt.Errorf(
			"%w: backend %q: %s is %s; a public bucket needs it supported — run TestBackend to probe it",
			features.ErrUnsupported, b.BackendID, features.AnonymousReadPolicy, got))
	}
	// An unsigned GET cannot read an SSE-KMS object: the store needs the
	// caller's kms:Decrypt.
	if backend.SSE.Type == admindomain.SSETypeKMS {
		return apiutil.MapError(fmt.Errorf(
			"%w: backend %q encrypts with SSE-KMS, and an unsigned GET cannot decrypt",
			features.ErrUnsupported, b.BackendID))
	}
	return nil
}

// checkPublicBaseURL admits an empty base URL (the backend's public endpoint
// serves the bucket) or an absolute http(s) URL with a host and nothing a
// prefix of every object's URL cannot carry.
func checkPublicBaseURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("public_base_url: %w", err)
	}
	if !publicBaseURLSchemes[u.Scheme] || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(raw, "/") {
		return fmt.Errorf("public_base_url %q must be an http(s) URL with a host, no credentials, query, fragment or trailing slash", raw)
	}
	return nil
}

// refuseLifecycleOnPublic: the lifecycle worker moves objects to the trash,
// which leaves a public object's bytes served (ADR-0027).
func (h *Handler) refuseLifecycleOnPublic(ctx context.Context, backendID, bucketName string, rules []admindomain.LifecycleRule) error {
	if len(rules) == 0 {
		return nil
	}
	b, err := h.repo.Get(ctx, backendID, bucketName)
	if err != nil {
		return apiutil.MapError(err)
	}
	if b.PublicRead {
		return apiutil.MapError(publicread.Rulef(
			"a public bucket takes no lifecycle rules: they move objects to the trash, where their bytes stay public"))
	}
	return nil
}
