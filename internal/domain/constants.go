package domain

// Sorting constants
const (
	SortOrderAsc  = "asc"
	SortOrderDesc = "desc"
)

// Operation status constants
const (
	StatusSuccess     = "success"
	StatusError       = "error"
	StatusConflict    = "conflict"
	StatusUnavailable = "unavailable"
	StatusDown        = "down"
	StatusNotFound    = "not_found"
)

// Security constants
const (
	Redacted = "[REDACTED]"
)

// Action represents a privileged operation that requires authorization.
type Action string

const (
	ActionCreate Action = "create"
	ActionRead   Action = "read"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
	ActionPatch  Action = "patch"
	ActionSign   Action = "sign"
)

// Metric names or other stable technical contracts can go here if needed
// but for now let's focus on those identified by linter.
