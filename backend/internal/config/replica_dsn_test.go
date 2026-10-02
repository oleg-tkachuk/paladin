package config

import (
	"strings"
	"testing"
	"time"
)

func TestReplicaDSN(t *testing.T) {
	cases := map[string]struct {
		primary, replica string
		want             string
		wantErr          bool
	}{
		"explicit wins": {
			primary: "postgres://u@db-rw:5432/p", replica: "postgres://u@elsewhere/p",
			want: "postgres://u@elsewhere/p",
		},
		"cnpg service fqdn, tls params kept": {
			primary: "postgres://paladin_app@paladin-postgresql-rw.database.svc.cluster.local:5432/paladin?sslmode=verify-full&sslrootcert=/etc/paladin/pg-ca/ca.crt",
			want:    "postgres://paladin_app@paladin-postgresql-ro.database.svc.cluster.local:5432/paladin?sslmode=verify-full&sslrootcert=/etc/paladin/pg-ca/ca.crt",
		},
		"short name, no port": {
			primary: "postgresql://u:pw@db-rw/p", want: "postgresql://u:pw@db-ro/p",
		},
		"key=value": {
			primary: "host=db-rw.ns port=5432 user=u dbname=p sslmode=require",
			want:    "host=db-ro.ns port=5432 user=u dbname=p sslmode=require",
		},
		// Only the first label is the service; a -rw elsewhere is not it.
		"rw in the domain only": {primary: "postgres://u@db.ns-rw.svc/p", wantErr: true},
		"not cnpg":              {primary: "postgres://u@postgres:5432/p", wantErr: true},
		"bare suffix":           {primary: "postgres://u@-rw/p", wantErr: true},
		"kv without host":       {primary: "user=u dbname=p", wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := Postgres{DSN: tc.primary, Replica: PostgresReplica{DSN: tc.replica}}
			got, err := p.ReplicaDSN()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "replica.dsn") {
					t.Fatalf("got %q, %v; want an error naming replica.dsn", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestValidate_Replica(t *testing.T) {
	enabled := func(dsn, primary string) Config {
		c := minimalValidConfig()
		if primary != "" {
			c.Datastores.Postgres.DSN = primary
		}
		c.Datastores.Postgres.Replica = PostgresReplica{
			Enabled: true, DSN: dsn, MaxLag: 2 * time.Second, LagCheckPeriod: 5 * time.Second,
		}
		return c
	}

	// Off is off: nothing about the block is checked, so a half-filled one
	// left behind never stops a deploy.
	off := minimalValidConfig()
	off.Datastores.Postgres.Replica = PostgresReplica{DSN: "", LagCheckPeriod: 0}
	if err := off.Validate(); err != nil {
		t.Fatalf("disabled replica rejected: %v", err)
	}

	if c := enabled("", "postgres://u@db-rw:5432/p"); c.Validate() != nil {
		t.Fatalf("CNPG primary with no replica dsn rejected: %v", c.Validate())
	}
	mustReject(t, enabled("", "postgres://u@postgres:5432/p"), "replica.dsn")

	c := enabled("postgres://u@r/p", "")
	c.Datastores.Postgres.Replica.LagCheckPeriod = 0
	mustReject(t, c, "lag_check_period")

	c = enabled("postgres://u@r/p", "")
	c.Datastores.Postgres.Replica.Password = "inline"
	c.Datastores.Postgres.Replica.PasswordSecret = &SecretRef{Name: "x", Key: "k"}
	mustReject(t, c, "replica.password and replica.password_secret")
}
