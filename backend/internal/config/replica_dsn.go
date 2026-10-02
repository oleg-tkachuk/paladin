package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ReplicaDSN returns the DSN the read replica connects with: replica.dsn when
// set, otherwise one derived from the primary's.
//
// The derivation is the CloudNativePG service convention, which is what every
// Paladin environment runs (ADR-0005): a cluster `<name>` is served read-write
// at `<name>-rw` and read-only, across its streaming standbys, at `<name>-ro`.
// CNPG creates those standbys, seeds them and keeps them replaying WAL, and the
// schema arrives with the WAL — so turning the replica on is the one flag and
// nothing else. User, database and every parameter (sslmode, sslrootcert) are
// kept; CNPG's server certificate carries the -ro name, so verify-full holds.
//
// A primary host without the -rw suffix cannot be mapped, and is an error
// rather than a guess: the operator sets replica.dsn explicitly then.
func (p Postgres) ReplicaDSN() (string, error) {
	if p.Replica.DSN != "" {
		return p.Replica.DSN, nil
	}
	return deriveReplicaDSN(p.DSN)
}

// kvHost matches the host keyword of a key=value DSN.
var kvHost = regexp.MustCompile(`(^|\s)host=([^\s]+)`)

func deriveReplicaDSN(primary string) (string, error) {
	if strings.HasPrefix(primary, "postgres://") || strings.HasPrefix(primary, "postgresql://") {
		u, err := url.Parse(primary)
		if err != nil {
			return "", fmt.Errorf("parse primary dsn: %w", err)
		}
		host, ok := roHost(u.Hostname())
		if !ok {
			return "", errNoROHost(u.Hostname())
		}
		if port := u.Port(); port != "" {
			host += ":" + port
		}
		u.Host = host
		return u.String(), nil
	}
	m := kvHost.FindStringSubmatchIndex(primary)
	if m == nil {
		return "", errNoROHost("")
	}
	h := primary[m[4]:m[5]]
	ro, ok := roHost(h)
	if !ok {
		return "", errNoROHost(h)
	}
	return primary[:m[4]] + ro + primary[m[5]:], nil
}

// roHost maps the first DNS label `<name>-rw` to `<name>-ro`.
func roHost(h string) (string, bool) {
	label, rest, _ := strings.Cut(h, ".")
	name, ok := strings.CutSuffix(label, "-rw")
	if !ok || name == "" {
		return "", false
	}
	if rest == "" {
		return name + "-ro", true
	}
	return name + "-ro." + rest, true
}

func errNoROHost(h string) error {
	return fmt.Errorf("replica.dsn is empty and the primary host %q is not a CloudNativePG "+
		"`<cluster>-rw` service, so no `-ro` replica host can be derived; set replica.dsn", h)
}
