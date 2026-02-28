package postgres

import (
	"encoding/json"
	"errors"
	"fmt"

	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// MapPgError converts pgx errors to domain-appropriate errors
func MapPgError(err error) error {
	if err == nil {
		return nil
	}

	// No rows found - return nil for Get operations (caller decides if this is an error)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}

	// Check for postgres-specific errors
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// Specific granular matches for common constraints
		switch pgErr.Code {
		case pgerrcode.UniqueViolation:
			return apperrors.Conflict("duplicate key violation", err)
		case pgerrcode.ForeignKeyViolation:
			return apperrors.ValidationFailed("foreign key violation", err)
		case pgerrcode.NotNullViolation:
			return apperrors.ValidationFailed("required field missing", err)
		case pgerrcode.CheckViolation:
			return apperrors.ValidationFailed("check constraint violation", err)
		case pgerrcode.ExclusionViolation:
			return apperrors.Conflict("exclusion constraint violation", err)
		case pgerrcode.DeadlockDetected:
			return apperrors.Conflict("database deadlock detected", err)
		}

		// Broad class-based matches for all other Postgres error codes
		if len(pgErr.Code) >= 2 {
			switch pgErr.Code[:2] {
			case "08": // Connection Exception
				return apperrors.Internal("database connection error", err)
			case "22": // Data Exception
				return apperrors.ValidationFailed("invalid data representation", err)
			case "23": // Integrity Constraint Violation
				return apperrors.ValidationFailed("integrity constraint violation", err)
			case "25": // Invalid Transaction State
				return apperrors.Internal("invalid transaction state", err)
			case "28": // Invalid Authorization Specification
				return apperrors.Internal("database authorization error", err)
			case "3D", "3F": // Invalid Catalog Name / Schema Name
				return apperrors.Internal("database configuration error", err)
			case "40": // Transaction Rollback
				return apperrors.Conflict("transaction rollback", err)
			case "42": // Syntax Error or Access Rule Violation
				return apperrors.Internal("database syntax or access error", err)
			case "53": // Insufficient Resources
				return apperrors.Internal("database insufficient resources", err)
			case "54": // Program Limit Exceeded
				return apperrors.Internal("database program limit exceeded", err)
			case "55": // Object Not In Prerequisite State
				return apperrors.Internal("database object state error", err)
			case "57": // Operator Intervention
				return apperrors.Internal("database operator intervention", err)
			case "58": // System Error (errors external to PostgreSQL itself)
				return apperrors.Internal("database system error", err)
			case "F0": // Configuration File Error
				return apperrors.Internal("database configuration file error", err)
			case "HV", "HW": // Foreign Data Wrapper Error
				return apperrors.Internal("database fdw error", err)
			case "P0": // PL/pgSQL Error
				return apperrors.Internal("database plpgsql error", err)
			case "XX": // Internal Error
				return apperrors.Internal("database internal error", err)
			}
		}
	}

	return err
}

// unmarshalJSONB unmarshals JSONB bytes into a map
func unmarshalJSONB(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return make(map[string]any), nil
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal jsonb: %w", err)
	}
	return result, nil
}

// marshalJSONB marshals a map into JSONB bytes
func marshalJSONB(data map[string]any) ([]byte, error) {
	if data == nil {
		return []byte("{}"), nil
	}
	result, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal jsonb: %w", err)
	}
	return result, nil
}

// unmarshalStringMap unmarshals JSONB bytes into a map[string]string
func unmarshalStringMap(data []byte) (map[string]string, error) {
	if len(data) == 0 {
		return make(map[string]string), nil
	}
	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal string map: %w", err)
	}
	return result, nil
}

// marshalStringMap marshals a map[string]string into JSONB bytes
func marshalStringMap(data map[string]string) ([]byte, error) {
	if data == nil {
		return []byte("{}"), nil
	}
	result, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal string map: %w", err)
	}
	return result, nil
}
