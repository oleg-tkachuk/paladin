package api_token

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// memStore is an in-memory Store stub for tests. Keyed by the HMAC digest,
// mirroring the production unique-index lookup.
type memStore struct {
	rows     map[uuid.UUID]Token
	byDigest map[string]uuid.UUID
}

func newMemStore() *memStore {
	return &memStore{rows: map[uuid.UUID]Token{}, byDigest: map[string]uuid.UUID{}}
}

func (m *memStore) Insert(_ context.Context, t Token, digest []byte) error {
	t.Plaintext = "" // mimic prod: never persist plaintext
	m.rows[t.ID] = t
	m.byDigest[string(digest)] = t.ID
	return nil
}

func (m *memStore) FindByDigest(_ context.Context, digest []byte) (Token, error) {
	id, ok := m.byDigest[string(digest)]
	if !ok {
		return Token{}, ErrTokenNotFound
	}
	return m.rows[id], nil
}

func (m *memStore) Get(_ context.Context, id uuid.UUID) (Token, error) {
	t, ok := m.rows[id]
	if !ok {
		return Token{}, ErrTokenNotFound
	}
	return t, nil
}

func (m *memStore) Revoke(_ context.Context, id uuid.UUID) error {
	if t, ok := m.rows[id]; ok && t.RevokedAt == nil {
		now := time.Now().UTC()
		t.RevokedAt = &now
		m.rows[id] = t
	}
	return nil
}

func (m *memStore) TouchLastUsed(_ context.Context, id, _ uuid.UUID, at time.Time) error {
	if t, ok := m.rows[id]; ok {
		t.LastUsedAt = &at
		m.rows[id] = t
	}
	return nil
}

func (m *memStore) ListByTenant(context.Context, ListByTenantArgs) ([]Token, string, error) {
	return nil, "", nil
}

func (m *memStore) PurgeExpired(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

// buildIssuerVerifier wires an in-memory Store, Issuer, and Verifier sharing
// one Hasher (as production does).
func buildIssuerVerifier(t *testing.T) (*Issuer, *Verifier, *memStore) {
	t.Helper()
	store := newMemStore()
	hasher := mustHasher(t)
	issuer, err := NewIssuer(IssuerConfig{
		Store:  store,
		Hasher: hasher,
		MaxTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	verifier, err := NewVerifier(VerifierConfig{
		Store:         store,
		Hasher:        hasher,
		TouchLastUsed: false,
	})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	return issuer, verifier, store
}

// TestIssue_VerifyHappyPath runs Issue → Verify against the same store
// and confirms the round trip works for a normal token.
func TestIssue_VerifyHappyPath(t *testing.T) {
	t.Parallel()
	issuer, verifier, _ := buildIssuerVerifier(t)
	ctx := context.Background()

	tok, err := issuer.Issue(ctx, IssueRequest{
		TenantID: uuid.New(),
		Name:     "ingest-svc",
		Audience: []string{"data"},
		Scopes:   []string{"api:write"},
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if tok.Plaintext == "" {
		t.Fatal("plaintext not populated on Issue")
	}

	got, err := verifier.Verify(ctx, tok.Plaintext, "data")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.ID != tok.ID {
		t.Errorf("verified ID mismatch")
	}
}

// TestIssue_TTLCap rejects requests that exceed MaxTTL — defends
// against admin tooling that omits client-side validation.
func TestIssue_TTLCap(t *testing.T) {
	t.Parallel()
	issuer, _, _ := buildIssuerVerifier(t)
	_, err := issuer.Issue(context.Background(), IssueRequest{
		TenantID: uuid.New(),
		Name:     "x",
		Audience: []string{"data"},
		TTL:      48 * time.Hour, // > issuer.MaxTTL = 24h
	})
	if err == nil {
		t.Fatal("expected TTL cap error, got nil")
	}
}

// TestVerify_AudienceMismatch rejects tokens that don't authorise the
// calling plane.
func TestVerify_AudienceMismatch(t *testing.T) {
	t.Parallel()
	issuer, verifier, _ := buildIssuerVerifier(t)
	ctx := context.Background()
	tok, _ := issuer.Issue(ctx, IssueRequest{
		TenantID: uuid.New(),
		Name:     "x",
		Audience: []string{"data"},
		TTL:      time.Hour,
	})
	_, err := verifier.Verify(ctx, tok.Plaintext, "admin")
	if !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("expected ErrAudienceMismatch, got %v", err)
	}
}

// TestVerify_Expired returns ErrTokenExpired for tokens past their TTL.
func TestVerify_Expired(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	hasher := mustHasher(t)
	clock := &mutableClock{now: time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)}
	issuer, _ := NewIssuer(IssuerConfig{Store: store, Hasher: hasher, MaxTTL: time.Hour, Now: clock.Now})
	verifier, _ := NewVerifier(VerifierConfig{Store: store, Hasher: hasher, Now: clock.Now, Leeway: time.Second})

	tok, _ := issuer.Issue(context.Background(), IssueRequest{
		TenantID: uuid.New(),
		Name:     "x",
		Audience: []string{"data"},
		TTL:      time.Minute,
	})
	clock.now = clock.now.Add(time.Hour)
	_, err := verifier.Verify(context.Background(), tok.Plaintext, "data")
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

// TestVerify_Revoked rejects tokens after Revoke flips revoked_at.
func TestVerify_Revoked(t *testing.T) {
	t.Parallel()
	issuer, verifier, store := buildIssuerVerifier(t)
	ctx := context.Background()
	tok, _ := issuer.Issue(ctx, IssueRequest{
		TenantID: uuid.New(),
		Name:     "x",
		Audience: []string{"data"},
		TTL:      time.Hour,
	})
	if err := store.Revoke(ctx, tok.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := verifier.Verify(ctx, tok.Plaintext, "data")
	if !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("expected ErrTokenRevoked, got %v", err)
	}
}

// TestVerify_TamperedPlaintext rejects forged tokens. We craft a
// plaintext with the same prefix but a different body and confirm the
// digest miss surfaces as ErrTokenNotFound (not "valid prefix, wrong
// hash" — that distinction would leak prefix existence).
func TestVerify_TamperedPlaintext(t *testing.T) {
	t.Parallel()
	issuer, verifier, _ := buildIssuerVerifier(t)
	ctx := context.Background()
	tok, _ := issuer.Issue(ctx, IssueRequest{
		TenantID: uuid.New(),
		Name:     "x",
		Audience: []string{"data"},
		TTL:      time.Hour,
	})
	// Forge: keep prefix, swap last byte.
	body := []byte(tok.Plaintext)
	body[len(body)-1] = 'A'
	if string(body) == tok.Plaintext {
		body[len(body)-1] = 'B'
	}
	_, err := verifier.Verify(ctx, string(body), "data")
	if !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("expected ErrTokenNotFound for tampered token, got %v", err)
	}
}

// mutableClock is a clock the tests advance to exercise TTL gates.
type mutableClock struct {
	now time.Time
}

func (m *mutableClock) Now() time.Time { return m.now }
