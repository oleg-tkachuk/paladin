package postgres

import (
	"encoding/json"
	"errors"
	"fmt"

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
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("duplicate key: %w", err)
		case "23503": // foreign_key_violation
			return fmt.Errorf("foreign key violation: %w", err)
		case "23502": // not_null_violation
			return fmt.Errorf("required field missing: %w", err)
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
