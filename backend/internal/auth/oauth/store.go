package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrClientNotFound — no oauth_clients row for the given client_id.
	ErrClientNotFound = errors.New("oauth: client not found")
	// ErrCodeInvalid — the authorization code is unknown, expired, or already
	// consumed. Deliberately one error so the /token endpoint never leaks
	// WHICH of those it was (an attacker probing codes learns nothing).
	ErrCodeInvalid = errors.New("oauth: authorization code is invalid, expired, or already used")
)

// Client is a registered OAuth client (RFC 6749 §2). SecretHash is nil for
// public (PKCE-only) clients; confidential clients store a bcrypt hash.
type Client struct {
	ClientID         string
	ClientName       string
	RedirectURIs     []string
	AllowedScopes    []string
	AllowedAudiences []string
	SecretHash       []byte
	Public           bool
	CreatedAt        time.Time
}

// AuthCode is a short-lived, single-use authorization code bound to a PKCE
// challenge. Code holds the plaintext only in-memory (at create/consume); the
// row persists sha256(Code) so a DB leak can't be redeemed.
type AuthCode struct {
	Code            string
	ClientID        string
	UserID          uuid.UUID
	TenantID        uuid.UUID
	RedirectURI     string
	CodeChallenge   string
	ChallengeMethod string
	Scopes          []string
	Audience        string
	ExpiresAt       time.Time
}

// Store is the OAuth AS persistence surface.
type Store interface {
	UpsertClient(ctx context.Context, c Client) error
	GetClient(ctx context.Context, clientID string) (Client, error)
	CreateCode(ctx context.Context, c AuthCode) error
	// ConsumeCode atomically marks the code used and returns it, exactly once.
	// Expired or already-consumed codes return ErrCodeInvalid.
	ConsumeCode(ctx context.Context, code string) (AuthCode, error)
	PurgeExpiredCodes(ctx context.Context, olderThan time.Time) (int64, error)
}

// hashCode is the at-rest transform for authorization codes.
func hashCode(code string) []byte {
	sum := sha256.Sum256([]byte(code))
	return sum[:]
}

// GenerateCode returns a cryptographically-random, URL-safe authorization
// code (256 bits of entropy).
func GenerateCode() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// PgxStore implements Store over a pgxpool.Pool. uuid columns are passed as
// strings with ::uuid casts and scanned back as strings — pgx v5's default
// type map doesn't register google/uuid, and this keeps the store free of
// pgtype plumbing.
type PgxStore struct {
	pool *pgxpool.Pool
}

func NewPgxStore(pool *pgxpool.Pool) *PgxStore { return &PgxStore{pool: pool} }

var _ Store = (*PgxStore)(nil)

func (s *PgxStore) UpsertClient(ctx context.Context, c Client) error {
	const q = `
INSERT INTO oauth_clients
    (client_id, client_name, redirect_uris, allowed_scopes, allowed_audiences, secret_hash, is_public)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (client_id) DO UPDATE SET
    client_name       = EXCLUDED.client_name,
    redirect_uris     = EXCLUDED.redirect_uris,
    allowed_scopes    = EXCLUDED.allowed_scopes,
    allowed_audiences = EXCLUDED.allowed_audiences,
    secret_hash       = EXCLUDED.secret_hash,
    is_public         = EXCLUDED.is_public`
	_, err := s.pool.Exec(ctx, q,
		c.ClientID, c.ClientName, nonNil(c.RedirectURIs), nonNil(c.AllowedScopes), nonNil(c.AllowedAudiences), c.SecretHash, c.Public)
	return err
}

// nonNil coalesces a nil slice to an empty one so pgx encodes a Postgres
// empty array ('{}') rather than NULL — the array columns are NOT NULL.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *PgxStore) GetClient(ctx context.Context, clientID string) (Client, error) {
	const q = `
SELECT client_id, client_name, redirect_uris, allowed_scopes, allowed_audiences, secret_hash, is_public, created_at
FROM oauth_clients WHERE client_id = $1`
	var c Client
	err := s.pool.QueryRow(ctx, q, clientID).Scan(
		&c.ClientID, &c.ClientName, &c.RedirectURIs, &c.AllowedScopes, &c.AllowedAudiences,
		&c.SecretHash, &c.Public, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Client{}, ErrClientNotFound
		}
		return Client{}, err
	}
	return c, nil
}

func (s *PgxStore) CreateCode(ctx context.Context, c AuthCode) error {
	const q = `
INSERT INTO oauth_authorization_codes
    (code_hash, client_id, user_id, tenant_id, redirect_uri, code_challenge, code_challenge_method, scopes, audience, expires_at)
VALUES ($1, $2, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10)`
	_, err := s.pool.Exec(ctx, q,
		hashCode(c.Code), c.ClientID, c.UserID.String(), c.TenantID.String(),
		c.RedirectURI, c.CodeChallenge, c.ChallengeMethod, nonNil(c.Scopes), c.Audience, c.ExpiresAt)
	return err
}

func (s *PgxStore) ConsumeCode(ctx context.Context, code string) (AuthCode, error) {
	// Atomic single-use: the WHERE clause gates on not-yet-consumed AND
	// not-expired, and the UPDATE stamps consumed_at in the same statement —
	// two concurrent redeems can't both win, and a replay finds no row.
	const q = `
UPDATE oauth_authorization_codes
SET consumed_at = now()
WHERE code_hash = $1 AND consumed_at IS NULL AND expires_at > now()
RETURNING client_id, user_id::text, tenant_id::text, redirect_uri,
          code_challenge, code_challenge_method, scopes, audience, expires_at`
	var (
		out              AuthCode
		userID, tenantID string
	)
	err := s.pool.QueryRow(ctx, q, hashCode(code)).Scan(
		&out.ClientID, &userID, &tenantID, &out.RedirectURI,
		&out.CodeChallenge, &out.ChallengeMethod, &out.Scopes, &out.Audience, &out.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthCode{}, ErrCodeInvalid
		}
		return AuthCode{}, err
	}
	if out.UserID, err = uuid.Parse(userID); err != nil {
		return AuthCode{}, err
	}
	if out.TenantID, err = uuid.Parse(tenantID); err != nil {
		return AuthCode{}, err
	}
	out.Code = code
	return out, nil
}

func (s *PgxStore) PurgeExpiredCodes(ctx context.Context, olderThan time.Time) (int64, error) {
	const q = `DELETE FROM oauth_authorization_codes WHERE expires_at < $1`
	tag, err := s.pool.Exec(ctx, q, olderThan)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
