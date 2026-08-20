package auth

import (
	"fmt"
	"strings"
)

// ScopeType is the discriminator on Scope. Wire-encoded as the string before
// the colon in the JWT `scopes` claim values.
type ScopeType string

const (
	ScopeWildcard   ScopeType = "*"
	ScopeTenant     ScopeType = "tenant"
	ScopeBackend    ScopeType = "backend"
	ScopeBucket     ScopeType = "bucket"
	ScopeCollection ScopeType = "collection"
)

// Scope is one entry on a Principal.Scopes list. JWT-wire form is "type:value"
// (or the literal "*" for full-wildcard).
type Scope struct {
	Type  ScopeType
	Value string
}

// String returns the wire form.
func (s Scope) String() string {
	if s.Type == ScopeWildcard {
		return "*"
	}
	return string(s.Type) + ":" + s.Value
}

// ParseScope decodes one wire-form entry. The literal "*" produces a
// ScopeWildcard with empty Value; everything else must be "type:value".
func ParseScope(raw string) (Scope, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Scope{}, fmt.Errorf("auth: empty scope")
	}
	if raw == "*" {
		return Scope{Type: ScopeWildcard}, nil
	}
	idx := strings.IndexByte(raw, ':')
	if idx < 0 || idx == len(raw)-1 {
		return Scope{}, fmt.Errorf("auth: malformed scope %q (want type:value)", raw)
	}
	t := ScopeType(raw[:idx])
	switch t {
	case ScopeTenant, ScopeBackend, ScopeBucket, ScopeCollection:
		// ok
	default:
		return Scope{}, fmt.Errorf("auth: unknown scope type %q", t)
	}
	return Scope{Type: t, Value: raw[idx+1:]}, nil
}

// ParseScopes decodes a JWT claim — either a comma-separated string or an
// already-parsed []string from the JSON decoder.
func ParseScopes(raw []string) ([]Scope, error) {
	out := make([]Scope, 0, len(raw))
	for _, s := range raw {
		sc, err := ParseScope(s)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}

// ResourceClaim is the minimal projection a handler hands to ScopeMatch when
// it needs to ask "is this principal allowed to touch this resource?". Every
// field is optional — the matcher uses whatever is populated.
type ResourceClaim struct {
	TenantID   string
	BackendID  string
	BucketName string
	Collection string // Collection name within bucket
}

// MatchScope reports whether at least one Scope on the principal admits the
// resource. Wildcard ("*") admits anything. Otherwise, any single matching
// scope is sufficient (Cedar still has the final say).
//
// The match is intentionally PERMISSIVE — Cedar's `forbid (...)` rules are
// authoritative for fine-grained policy. This function is a fast pre-check
// that lets handlers reject obviously out-of-scope requests before paying
// the Cedar evaluation cost.
func MatchScope(scopes []Scope, r ResourceClaim) bool {
	for _, s := range scopes {
		switch s.Type {
		case ScopeWildcard:
			return true
		case ScopeTenant:
			if r.TenantID != "" && s.Value == r.TenantID {
				return true
			}
		case ScopeBackend:
			if r.BackendID != "" && s.Value == r.BackendID {
				return true
			}
		case ScopeBucket:
			if r.BucketName != "" && s.Value == r.BucketName {
				return true
			}
		case ScopeCollection:
			if r.Collection != "" && r.BucketName != "" {
				want := r.BucketName + "/" + r.Collection
				if s.Value == want {
					return true
				}
			}
		}
	}
	return false
}
