package app

import (
	"context"
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3adapter"
)

func TestParseDynamicCredsRef(t *testing.T) {
	for _, c := range []struct {
		ref, ns, name string
		ok            bool
	}{
		{"primary-creds", "", "primary-creds", true},
		{"paladin/primary-creds", "paladin", "primary-creds", true},
		{"", "", "", false},
		{"vault://kv/paladin", "", "", false},
		{"paladin/", "", "", false}, // trailing slash → no name
	} {
		ns, name, ok := parseDynamicCredsRef(c.ref)
		if ok != c.ok {
			t.Errorf("%q → ok=%v, want %v", c.ref, ok, c.ok)
			continue
		}
		if ok && (ns != c.ns || name != c.name) { // ns/name only meaningful on success
			t.Errorf("%q → (%q,%q), want (%q,%q)", c.ref, ns, name, c.ns, c.name)
		}
	}
}

type fakeResolver struct {
	val map[string]string
	err error
}

func (f fakeResolver) ResolveSecret(_ context.Context, ref *config.SecretRef) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.val[ref.Key], nil
}

// Exercises the dynamic-probe error paths that don't require a network dial.
func TestProbeDynamic_Errors(t *testing.T) {
	row := admindomain.StorageBackend{BackendID: "dyn", Kind: "s3-compatible", CredentialsSecretRef: "creds"}

	if err := (&s3BackendProber{clients: map[string]*s3adapter.Client{}}).
		probeDynamic(context.Background(), row); err == nil {
		t.Error("nil resolver should error")
	}

	bad := row
	bad.CredentialsSecretRef = "vault://x"
	if err := (&s3BackendProber{resolver: fakeResolver{}}).
		probeDynamic(context.Background(), bad); err == nil {
		t.Error("unsupported ref should error")
	}

	if err := (&s3BackendProber{resolver: fakeResolver{err: errors.New("k8s 403")}}).
		probeDynamic(context.Background(), row); err == nil {
		t.Error("resolver error should propagate")
	}
}
