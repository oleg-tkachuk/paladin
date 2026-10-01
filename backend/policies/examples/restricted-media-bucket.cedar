// Example: media bucket that accepts only images and videos up to 50 MiB,
// with a time-window restriction and a tag requirement.
//
// Apply by binding to a specific objectKey (or bucket) — replace
// "placeholder" with the tenant UUID at load time.

// Writes constrained by size, content type, and a required `category` tag.
// An upload whose key the server assigns is checked against the Collection,
// which has neither tags nor key; `has` keeps those reads from erroring.
permit (
    principal in Tenant::"placeholder",
    action in [Action::"PutObject", Action::"PresignPut"],
    resource
) when {
    context.size_bytes <= 52428800 &&
    (context.content_type like "image/*" || context.content_type like "video/*") &&
    resource has tags && resource.tags.contains("category")
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
    (resource has key && resource.key like "*.svg") ||
    context.content_type == "image/svg+xml"
};
