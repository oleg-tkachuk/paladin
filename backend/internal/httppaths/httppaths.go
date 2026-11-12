// Package httppaths holds the plain-HTTP paths two components must spell
// alike, with no imports, so a light component can name one without linking
// the package that serves it.
package httppaths

// AuditStream is where the admin plane serves the audit stream, and where
// the console's session role forwards it from.
const AuditStream = "/audit/stream"
