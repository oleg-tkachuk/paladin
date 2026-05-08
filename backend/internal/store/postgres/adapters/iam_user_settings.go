package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/usersettingsh"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// UserSettingsRepo is the postgres adapter for usersettingsh.Repository.
type UserSettingsRepo struct {
	q *sqlc.Queries
}

func NewUserSettingsRepo(q *sqlc.Queries) *UserSettingsRepo {
	return &UserSettingsRepo{q: q}
}

var _ usersettingsh.Repository = (*UserSettingsRepo)(nil)

func (r *UserSettingsRepo) Get(ctx context.Context, userID uuid.UUID) (usersettingsh.Settings, error) {
	row, err := r.q.GetUserSettings(ctx, pgUUID(userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return usersettingsh.Settings{}, usersettingsh.ErrNotFound
		}
		return usersettingsh.Settings{}, fmt.Errorf("get user_settings: %w", err)
	}
	return settingsFromSQLC(row), nil
}

func (r *UserSettingsRepo) Upsert(ctx context.Context, s usersettingsh.Settings) (usersettingsh.Settings, error) {
	prefs := s.Preferences
	if len(prefs) == 0 {
		// Postgres NOT NULL DEFAULT '{}'::jsonb. Bind explicitly so a nil
		// slice doesn't become SQL NULL and trip the constraint.
		prefs = []byte("{}")
	}
	row, err := r.q.UpsertUserSettings(ctx,
		pgUUID(s.UserID),
		pgUUID(s.TenantID),
		s.Timezone,
		s.Locale,
		s.Theme,
		prefs,
	)
	if err != nil {
		return usersettingsh.Settings{}, fmt.Errorf("upsert user_settings: %w", err)
	}
	return settingsFromSQLC(row), nil
}

func (r *UserSettingsRepo) ListByTenant(ctx context.Context, tenantID uuid.UUID, pageSize int32) ([]usersettingsh.Settings, error) {
	rows, err := r.q.ListUserSettingsByTenant(ctx, pgUUID(tenantID), pageSize)
	if err != nil {
		return nil, fmt.Errorf("list user_settings: %w", err)
	}
	out := make([]usersettingsh.Settings, 0, len(rows))
	for _, row := range rows {
		out = append(out, settingsFromSQLC(row))
	}
	return out, nil
}

func (r *UserSettingsRepo) Delete(ctx context.Context, userID uuid.UUID) error {
	rows, err := r.q.DeleteUserSettings(ctx, pgUUID(userID))
	if err != nil {
		return fmt.Errorf("delete user_settings: %w", err)
	}
	if rows == 0 {
		// Idempotent — deleting "no row" is success from the caller's POV
		// (post-condition: no settings row exists for this user).
		return nil
	}
	return nil
}

func settingsFromSQLC(s sqlc.UserSetting) usersettingsh.Settings {
	return usersettingsh.Settings{
		UserID:          uuidFrom(s.UserID),
		TenantID:        uuidFrom(s.TenantID),
		Timezone:        s.Timezone,
		Locale:          s.Locale,
		Theme:           s.Theme,
		Preferences:     s.Preferences,
		ResourceVersion: s.ResourceVersion,
		CreatedAt:       timeFrom(s.CreatedAt),
		UpdatedAt:       timeFrom(s.UpdatedAt),
	}
}
