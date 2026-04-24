package tenant

import (
	"strings"

	"github.com/google/uuid"
)

// defaultPolicyTemplate is applied to a freshly created tenant when the
// caller does not supply inherited_cedar_policy. The literal "placeholder"
// is substituted with the tenant's UUID at creation time.
//
// Mirrors policies/examples/default.cedar — update both together.
const defaultPolicyTemplate = `// Default deny-by-default policy applied on tenant creation.
// Tenant members may read/write their own objects; admins may manage them.

permit (
    principal in Tenant::"placeholder",
    action in [Action::"GetObject", Action::"PresignGet", Action::"HeadObject"],
    resource
);

permit (
    principal in Tenant::"placeholder",
    action in [Action::"PutObject", Action::"PresignPut"],
    resource
) when {
    context.size_bytes <= 5368709120 &&
    !(resource.key like "*.exe" ||
      resource.key like "*.dll" ||
      resource.key like "*.bat" ||
      resource.key like "*.sh")
};

permit (
    principal in Tenant::"placeholder",
    action in [
        Action::"DeleteObject",
        Action::"RestoreObject",
        Action::"UpdateObject",
        Action::"CopyObject"
    ],
    resource
) when {
    principal.roles.contains("bucket:admin") ||
    principal.roles.contains("platform-admin")
};

permit (
    principal in Tenant::"placeholder",
    action == Action::"AdminBucket",
    resource
) when {
    principal.roles.contains("platform-admin")
};
`

// renderDefaultPolicy returns the default Cedar policy with the placeholder
// Tenant UID substituted for the concrete tenant UUID.
func renderDefaultPolicy(tenantID uuid.UUID) string {
	return strings.ReplaceAll(defaultPolicyTemplate, "placeholder", tenantID.String())
}
