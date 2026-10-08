package config

// The configuration keys that switch a component the /health page reports
// on. A disabled component names its key, so an operator reading the page
// knows what turns it on.
const (
	KeyReplicaEnabled    = "datastores.postgres.replica.enabled"
	KeyCapabilityEnabled = "capability.enabled"
	KeyAPITokenEnabled   = "api_token.enabled"
)
