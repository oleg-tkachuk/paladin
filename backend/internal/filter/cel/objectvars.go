package cel

import "time"

// ObjectRow is what a filter over ObjectSchema can see of one object.
//
// The data plane (ListObjects, CountObjects) and the lifecycle worker both
// evaluate ObjectSchema filters, and each used to build its own variable map.
// They drifted: the data plane never projected the three timestamps, and the
// lifecycle worker never projected `key`, so `key.startsWith("logs/")` — the
// most common lifecycle rule there is — failed to evaluate on every object and
// expired nothing. One projection, shared, is how they stay in step; the test
// beside it holds it to the schema.
type ObjectRow struct {
	Key         string
	State       string
	ContentType string
	SizeBytes   int64
	Tags        map[string]string
	Metadata    map[string]string
	ExternalRef string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// CommittedAt is nil until the upload completes.
	CommittedAt *time.Time
}

// ObjectVars projects row into the variables ObjectSchema declares.
//
// Maps are never nil: `tags["k"]` must mean the same on an object with no
// tags as on one with other tags. A nil CommittedAt leaves `committed_at`
// out rather than inventing a time, so a filter on it does not match an
// object that was never committed; the evaluation reports the missing
// attribute, which callers treat as "does not match".
func ObjectVars(row ObjectRow) map[string]any {
	vars := map[string]any{
		"key":          row.Key,
		"state":        row.State,
		"content_type": row.ContentType,
		"size_bytes":   row.SizeBytes,
		"tags":         nonNil(row.Tags),
		"metadata":     nonNil(row.Metadata),
		"external_ref": row.ExternalRef,
		"created_at":   row.CreatedAt,
		"updated_at":   row.UpdatedAt,
	}
	if row.CommittedAt != nil {
		vars["committed_at"] = *row.CommittedAt
	}
	return vars
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
