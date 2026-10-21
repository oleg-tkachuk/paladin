package auth

// The prefixes of a principal's subject when it is not a person, before the
// API token's or the capability's id. The audit log records the subject, and
// the console reads it back to name the actor; sdk/testdata/actor_subjects.json
// holds both to these spellings.
const (
	SubjectPrefixAPIToken   = "apikey:"
	SubjectPrefixCapability = "capability:"
	SubjectPrefixSystem     = "system:"
)
