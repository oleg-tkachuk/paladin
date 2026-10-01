// Package pgerr classifies Postgres integrity errors.
//
// Every adapter used to do this inline, and they did not agree. The important
// disagreement was RESTRICT: Postgres raises 23503 foreign_key_violation for a
// plain FK, but 23001 restrict_violation when the constraint is declared
// ON DELETE RESTRICT — and most call sites checked only the first. The
// consequence is not cosmetic. A tenant with children returned Internal
// instead of "this tenant still owns data", so the operator saw a 500 where
// the system was working exactly as designed.
//
// One classifier, one place to add a code, and the RESTRICT pair is folded in
// once so no future adapter can get it half right.
package pgerr

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Kind is the classification the domain actually distinguishes. Codes that
// mean the same thing to a caller collapse into one Kind.
type Kind int

const (
	// Unknown is anything this package does not classify — including a
	// non-Postgres error. Callers must treat it as an unexpected failure
	// rather than a business outcome.
	Unknown Kind = iota

	// ForeignKeyViolation covers 23503 (foreign_key_violation) and 23001
	// (restrict_violation). They are the same fact to a caller — a row is
	// referenced by another — and Postgres splits them purely on how the
	// constraint was declared.
	ForeignKeyViolation

	// UniqueViolation is 23505: a unique index or primary key rejected the
	// row. Usually "this name is taken".
	UniqueViolation

	// CheckViolation is 23514: a CHECK constraint or a trigger that raises
	// it refused the row. Paladin uses this for rules enforced in the
	// database, such as object-lock retention.
	CheckViolation

	// NotNullViolation is 23502. Almost always a bug in the caller rather
	// than a business outcome, but adapters occasionally need to tell it
	// apart from an unknown failure.
	NotNullViolation

	// SerializationFailure covers 40001 (serialization_failure) and 40P01
	// (deadlock_detected). Both mean "retry the transaction"; neither says
	// anything was wrong with the request.
	SerializationFailure
)

// SQLSTATE codes, named so call sites read as intent rather than digits.
const (
	codeRestrictViolation   = "23001"
	codeNotNullViolation    = "23502"
	codeForeignKeyViolation = "23503"
	codeUniqueViolation     = "23505"
	codeCheckViolation      = "23514"
	codeSerializationFail   = "40001"
	codeDeadlockDetected    = "40P01"
)

// Classify returns the Kind of err, unwrapping as needed. Unknown for
// anything that is not a recognised Postgres error.
func Classify(err error) Kind {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return Unknown
	}
	switch pgErr.Code {
	case codeForeignKeyViolation, codeRestrictViolation:
		return ForeignKeyViolation
	case codeUniqueViolation:
		return UniqueViolation
	case codeCheckViolation:
		return CheckViolation
	case codeNotNullViolation:
		return NotNullViolation
	case codeSerializationFail, codeDeadlockDetected:
		return SerializationFailure
	default:
		return Unknown
	}
}

// Is reports whether err classifies as k. Never true for Unknown: "I could not
// classify this" is not a condition to branch on.
func Is(err error, k Kind) bool {
	if k == Unknown {
		return false
	}
	return Classify(err) == k
}

// Constraint returns the name of the constraint Postgres reported, or "" when
// the error is not a Postgres error or carries no constraint.
//
// For adapters that must tell two violations of the same Kind apart — a
// tenant's slug and its display name are separate unique indexes, and the
// message a user should see differs.
func Constraint(err error) string {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return ""
	}
	return pgErr.ConstraintName
}

// Table returns the relation Postgres named in the error, or "" when the error
// is not a Postgres error or carries no table.
//
// On a foreign-key violation this is the REFERENCING table — the one holding
// the row that blocks the delete — which is precisely what an operator needs
// told and the one thing a constraint name cannot be parsed into. Postgres
// reports both:
//
//	ERROR:  update or delete on table "tenants" violates RESTRICT setting of
//	        foreign key constraint "users_tenant_id_fkey" on table "users"
//	TABLE NAME:  users
//	CONSTRAINT NAME:  users_tenant_id_fkey
//
// Deriving "users" from "users_tenant_id_fkey" by stripping suffixes looks
// easy and is not: the shortest trailing `_..._id` turns
// buckets_owner_tenant_id_fkey into "buckets_owner", and the longest turns
// tenant_default_bindings_bucket_id_fkey into "tenant". No purely syntactic
// rule gets both, because the boundary between table and column is not in the
// string. Asking for the field Postgres already filled in does.
func Table(err error) string {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return ""
	}
	return pgErr.TableName
}

// Column returns the column a Postgres error names — set for NOT NULL
// violations, where there is no constraint name to go by — or "" when err is
// not a Postgres error or names none.
func Column(err error) string {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return ""
	}
	return pgErr.ColumnName
}

// ConstraintIs reports whether err is a Postgres error raised by the named
// constraint. Convenience for the common `Is(err, k) && Constraint(err) == n`.
func ConstraintIs(err error, name string) bool {
	return Constraint(err) == name
}
