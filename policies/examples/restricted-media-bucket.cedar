// Example: media bucket that accepts only images and videos up to 50 MiB,
// with a time-window restriction and a tag requirement.

permit (
    principal in Tenant::"placeholder",
    action in [Action::"PutObject", Action::"PresignPut"],
    resource
) when {
    context.size_bytes <= 52428800 &&
    (context.content_type like "image/*" || context.content_type like "video/*") &&
    // Require a `category` tag to classify uploads.
    resource.tags.contains("category")
};

// Read available anytime for members.
permit (
    principal in Tenant::"placeholder",
    action in [Action::"GetObject", Action::"PresignGet"],
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
