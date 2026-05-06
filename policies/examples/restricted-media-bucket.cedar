// Example: media bucket that accepts only images and videos up to 50 MiB,
// with a time-window restriction and a tag requirement.
//
// Apply by binding to a specific objectKey (or bucket) — replace
// "placeholder" with the tenant UUID at load time.

// Writes constrained by size, content type, and a required `category` tag.
permit (
    principal in Tenant::"placeholder",
    action in [Action::"PutObject", Action::"PresignPut"],
    resource
) when {
    context.size_bytes <= 52428800 &&
    (context.content_type like "image/*" || context.content_type like "video/*") &&
    resource.tags.contains("category")
};

// Read available anytime for tenant members.
permit (
    principal in Tenant::"placeholder",
    action in [Action::"GetObject", Action::"PresignGet", Action::"HeadObject"],
    resource
);

// Block .svg outright (XSS vector).
forbid (
    principal,
    action in [Action::"PutObject", Action::"PresignPut"],
    resource
) when {
    resource.key like "*.svg" || context.content_type == "image/svg+xml"
};
