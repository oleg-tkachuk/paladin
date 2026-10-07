// Package adapters wires the v2 handler-facing Repository interfaces onto the
// sqlc-generated Queries layer. Each file here maps one handler package:
// tenant, collection, object, presign, multipart, operation. The adapters live in
// their own package so the handler packages stay free of pgx/sqlc imports.
package adapters

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
)

// ─── uuid ↔ pgtype.UUID ─────────────────────────────────────────────────────

func pgUUID(u uuid.UUID) pgtype.UUID {
	if u == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

func uuidFrom(p pgtype.UUID) uuid.UUID {
	if !p.Valid {
		return uuid.Nil
	}
	return uuid.UUID(p.Bytes)
}

// ─── time ↔ pgtype.Timestamptz ──────────────────────────────────────────────

func pgTS(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func timeFrom(p pgtype.Timestamptz) time.Time {
	if !p.Valid {
		return time.Time{}
	}
	return p.Time
}

func timePtr(p pgtype.Timestamptz) *time.Time {
	if !p.Valid {
		return nil
	}
	t := p.Time
	return &t
}

// ─── *string helpers ────────────────────────────────────────────────────────

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ─── *int64 helpers ─────────────────────────────────────────────────────────

// positivePtr is n, or nil — SQL NULL — when it is not positive.
func positivePtr(n int64) *int64 {
	if n <= 0 {
		return nil
	}
	return &n
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// ─── []byte JSONB ↔ map[string]string ──────────────────────────────────────

func encodeMap(m map[string]string) []byte {
	if len(m) == 0 {
		return []byte(`{}`)
	}
	b, _ := json.Marshal(m)
	return b
}

func decodeMap(b []byte) map[string]string {
	if len(b) == 0 {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// ─── Checksum algorithm int16 ↔ proto enum name ────────────────────────────

func checksumAlgoInt(name string) int16 {
	switch name {
	case checksum.CRC32C:
		return 1
	case checksum.SHA256:
		return 2
	case checksum.MD5:
		return 3
	default:
		return 0
	}
}

func checksumAlgoName(v int16) string {
	switch v {
	case 1:
		return checksum.CRC32C
	case 2:
		return checksum.SHA256
	case 3:
		return checksum.MD5
	default:
		return ""
	}
}

// ─── pgx error helpers ──────────────────────────────────────────────────────

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
