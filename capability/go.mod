// Capability — a budgeted, delegable, individually revocable authorisation
// primitive for agentic workloads.
//
// This module deliberately depends on NOTHING that ties it to object storage
// or to a database. The dependency list below is the enforcement point for
// FR-002 / FR-003: if a storage SDK or a database driver ever appears here,
// the extraction has regressed and the standalone CI job fails.
//
// Persistence is the consumer's concern — the module publishes Store,
// UsageStore[TX] and KeyResolver contracts and nothing more. See
// specs/003-capability-module-extraction/contracts/module-api.md.
module github.com/oleg-tkachuk/paladin/capability

go 1.26.0

require (
	github.com/google/uuid v1.6.0
	go.opentelemetry.io/otel v1.44.0
	go.opentelemetry.io/otel/metric v1.44.0
)
