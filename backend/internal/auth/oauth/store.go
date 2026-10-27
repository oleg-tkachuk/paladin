package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	// SelfRegistered is a client that registered itself (RFC 7591), not one
	// seeded from the configuration. Only these are reaped.
	SelfRegistered bool
	CreatedAt      time.Time
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

// MaxListedClients bounds ListClients: an operator's view of the registered
// clients, not an export. Past it the oldest are shown.
const MaxListedClients = 500

// ClientLister lists the registered clients, oldest first.
type ClientLister interface {
	ListClients(ctx context.Context) ([]Client, error)
}

// ClientDeleter removes a registered client. Its authorization codes and its
// grants — the refresh tokens issued to it — go with it, by cascade.
type ClientDeleter interface {
	// DeleteClient returns ErrClientNotFound when no client has the id.
	DeleteClient(ctx context.Context, clientID string) error
}

// ClientReaper removes self-registered clients nobody uses.
type ClientReaper interface {
	// ReapIdleClients deletes up to limit self-registered clients created
	// before createdBefore that hold neither a grant live at now nor an
	// authorization code still redeemable at now, and returns how many.
	ReapIdleClients(ctx context.Context, createdBefore, now time.Time, limit int) (int64, error)
	// PurgeExpiredCodes deletes the authorization codes that expired before
	// olderThan, and returns how many.
	PurgeExpiredCodes(ctx context.Context, olderThan time.Time) (int64, error)
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

// DB is what PgxStore runs its statements on: a pool, or a transaction —
// which is how a test runs them as a given role.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ DB = (*pgxpool.Pool)(nil)
	_ DB = (pgx.Tx)(nil)
)

// PgxStore implements Store over a DB. uuid columns are passed as
// strings with ::uuid casts and scanned back as strings — pgx v5's default
// type map doesn't register a UUID type, and this keeps the store free of
// pgtype plumbing.
type PgxStore struct {
	pool DB
}

func NewPgxStore(pool DB) *PgxStore { return &PgxStore{pool: pool} }

var (
	_ Store         = (*PgxStore)(nil)
	_ ClientLister  = (*PgxStore)(nil)
	_ ClientDeleter = (*PgxStore)(nil)
	_ ClientReaper  = (*PgxStore)(nil)
)

func (s *PgxStore) UpsertClient(ctx context.Context, c Client) error {
	const q = `
INSERT INTO oauth_clients
    (client_id, client_name, redirect_uris, allowed_scopes, allowed_audiences, secret_hash, is_public, self_registered)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (client_id) DO UPDATE SET
    client_name       = EXCLUDED.client_name,
    redirect_uris     = EXCLUDED.redirect_uris,
    allowed_scopes    = EXCLUDED.allowed_scopes,
    allowed_audiences = EXCLUDED.allowed_audiences,
    secret_hash       = EXCLUDED.secret_hash,
    is_public         = EXCLUDED.is_public,
    self_registered   = EXCLUDED.self_registered`
	_, err := s.pool.Exec(ctx, q,
		c.ClientID, c.ClientName, nonNil(c.RedirectURIs), nonNil(c.AllowedScopes), nonNil(c.AllowedAudiences), c.SecretHash, c.Public, c.SelfRegistered)
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
SELECT client_id, client_name, redirect_uris, allowed_scopes, allowed_audiences, secret_hash, is_public, self_registered, created_at
FROM oauth_clients WHERE client_id = $1`
	var c Client
	err := s.pool.QueryRow(ctx, q, clientID).Scan(
		&c.ClientID, &c.ClientName, &c.RedirectURIs, &c.AllowedScopes, &c.AllowedAudiences,
		&c.SecretHash, &c.Public, &c.SelfRegistered, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Client{}, ErrClientNotFound
		}
		return Client{}, err
	}
	return c, nil
}

// ListClients returns up to MaxListedClients registered clients, oldest
// first, without their secret hashes.
func (s *PgxStore) ListClients(ctx context.Context) ([]Client, error) {
	const q = `
SELECT client_id, client_name, redirect_uris, allowed_scopes, allowed_audiences, is_public, self_registered, created_at
FROM oauth_clients ORDER BY created_at, client_id LIMIT $1`
	rows, err := s.pool.Query(ctx, q, MaxListedClients)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Client
	for rows.Next() {
		var c Client
		if err := rows.Scan(&c.ClientID, &c.ClientName, &c.RedirectURIs, &c.AllowedScopes,
			&c.AllowedAudiences, &c.Public, &c.SelfRegistered, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PgxStore) DeleteClient(ctx context.Context, clientID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM oauth_clients WHERE client_id = $1`, clientID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrClientNotFound
	}
	return nil
}

// ReapIdleClients reads refresh_tokens, whose row-level security hides every
// tenant but the connection's: run on a pool that bypasses it, or every
// client looks as if it held no grant and is deleted with its users' grants.
func (s *PgxStore) ReapIdleClients(ctx context.Context, createdBefore, now time.Time, limit int) (int64, error) {
	const q = `
DELETE FROM oauth_clients
WHERE id IN (
    SELECT c.id FROM oauth_clients c
    WHERE c.self_registered AND c.created_at < $1
      AND NOT EXISTS (
          SELECT 1 FROM refresh_tokens rt
          WHERE rt.oauth_client_id = c.client_id AND rt.revoked = FALSE AND rt.expires_at > $2)
      AND NOT EXISTS (
          SELECT 1 FROM oauth_authorization_codes ac
          WHERE ac.oauth_client_id = c.id AND ac.consumed_at IS NULL AND ac.expires_at > $2)
    ORDER BY c.created_at
    LIMIT $3)`
	tag, err := s.pool.Exec(ctx, q, createdBefore, now, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *PgxStore) CreateCode(ctx context.Context, c AuthCode) error {
	const q = `
INSERT INTO oauth_authorization_codes
    (code_hash, oauth_client_id, user_id, tenant_id, redirect_uri, code_challenge, code_challenge_method, scopes, audience, expires_at)
SELECT $1, c.id, $3::uuid, $4::uuid, $5, $6, $7, $8, $9, $10
FROM oauth_clients c WHERE c.client_id = $2`
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
RETURNING (SELECT c.client_id FROM oauth_clients c WHERE c.id = oauth_client_id), user_id::text, tenant_id::text, redirect_uri,
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
