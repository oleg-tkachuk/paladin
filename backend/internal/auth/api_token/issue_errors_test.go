package api_token

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"
)

// failingStore refuses every insert, as a database that is down does.
type failingStore struct{ *memStore }

func (failingStore) Insert(context.Context, Token, []byte) error {
	return errors.New("connection reset")
}

// A request Issue refuses is ErrInvalidIssue, which the caller can fix; a
// store that fails is not, so the admin RPC can tell a bad request from an
// outage.
func TestIssueTellsARefusalFromAFailure(t *testing.T) {
	t.Parallel()
	ok := IssueRequest{TenantID: uuid.New(), Name: "svc", Audience: []string{"data"}, TTL: time.Hour}
	refusals := map[string]func(IssueRequest) IssueRequest{
		"no tenant":   func(r IssueRequest) IssueRequest { r.TenantID = uuid.Nil(); return r },
		"no name":     func(r IssueRequest) IssueRequest { r.Name = ""; return r },
		"no audience": func(r IssueRequest) IssueRequest { r.Audience = nil; return r },
		"ttl too long": func(r IssueRequest) IssueRequest {
			r.TTL = 48 * time.Hour
			return r
		},
		"negative rate limit": func(r IssueRequest) IssueRequest {
			n := -1
			r.RateLimitRPM = &n
			return r
		},
	}
	issuer, _, _ := buildIssuerVerifier(t)
	for name, change := range refusals {
		t.Run(name, func(t *testing.T) {
			if _, err := issuer.Issue(context.Background(), change(ok)); !errors.Is(err, ErrInvalidIssue) {
				t.Fatalf("err = %v, want ErrInvalidIssue", err)
			}
		})
	}

	failing, err := NewIssuer(IssuerConfig{Store: failingStore{newMemStore()}, Hasher: mustHasher(t), MaxTTL: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	_, err = failing.Issue(context.Background(), ok)
	if err == nil || errors.Is(err, ErrInvalidIssue) {
		t.Fatalf("store failure = %v, want an error that is not ErrInvalidIssue", err)
	}
}
