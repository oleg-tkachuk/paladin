package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// UserRepo satisfies authstore.UserRepository.
type UserRepo struct {
	q *sqlc.Queries
}

func NewUserRepo(q *sqlc.Queries) *UserRepo { return &UserRepo{q: q} }

var _ authstore.UserRepository = (*UserRepo)(nil)

func (r *UserRepo) Create(ctx context.Context, u authstore.User) (authstore.User, error) {
	if u.UserID == uuid.Nil {
		u.UserID = uuid.Must(uuid.NewV7())
	}
	roles, _ := json.Marshal(u.Roles)
	scopes, _ := json.Marshal(scopesToWire(u.Scopes))
	if err := r.q.CreateUser(ctx,
		pgUUID(u.UserID),
		pgUUID(u.TenantID),
		u.Subject,
		strPtr(u.DisplayName),
		u.PasswordHash,
		roles,
		scopes,
		u.Disabled,
	); err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return authstore.User{}, authstore.ErrSubjectTaken
		}
		return authstore.User{}, fmt.Errorf("create user: %w", err)
	}
	return r.GetByID(ctx, u.UserID)
}

func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (authstore.User, error) {
	row, err := r.q.GetUserByID(ctx, pgUUID(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authstore.User{}, authstore.ErrNotFound
		}
		return authstore.User{}, err
	}
	return userFromSQLC(row), nil
}

func (r *UserRepo) FindBySubjectGlobal(ctx context.Context, subject string) ([]authstore.User, error) {
	rows, err := r.q.FindUsersBySubjectGlobal(ctx, subject)
	if err != nil {
		return nil, err
	}
	out := make([]authstore.User, 0, len(rows))
	for _, row := range rows {
		out = append(out, userFromSQLC(row))
	}
	return out, nil
}

func (r *UserRepo) GetBySubject(ctx context.Context, tenantID uuid.UUID, subject string) (authstore.User, error) {
	row, err := r.q.GetUserBySubject(ctx, pgUUID(tenantID), subject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authstore.User{}, authstore.ErrNotFound
		}
		return authstore.User{}, err
	}
	return userFromSQLC(row), nil
}

func (r *UserRepo) Update(ctx context.Context, u authstore.User, expectedVersion int64) (authstore.User, error) {
	roles, _ := json.Marshal(u.Roles)
	scopes, _ := json.Marshal(scopesToWire(u.Scopes))
	displayName := strPtr(u.DisplayName)
	disabled := u.Disabled
	rows, err := r.q.UpdateUser(ctx, displayName, &disabled, roles, scopes, pgUUID(u.UserID), expectedVersion)
	if err != nil {
		return authstore.User{}, fmt.Errorf("update user: %w", err)
	}
	if rows == 0 {
		return authstore.User{}, authstore.ErrVersionMismatch
	}
	return r.GetByID(ctx, u.UserID)
}

func (r *UserRepo) Delete(ctx context.Context, id uuid.UUID, expectedVersion int64) error {
	rows, err := r.q.DeleteUser(ctx, pgUUID(id), expectedVersion)
	if err != nil {
		return err
	}
	if rows == 0 {
		return authstore.ErrVersionMismatch
	}
	return nil
}

func (r *UserRepo) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash []byte) error {
	return r.q.UpdateUserPasswordHash(ctx, pgUUID(id), hash)
}

func (r *UserRepo) TouchLogin(ctx context.Context, id uuid.UUID, at time.Time) error {
	return r.q.TouchUserLogin(ctx, pgUUID(id), pgTS(at))
}

func (r *UserRepo) List(ctx context.Context, args authstore.ListUsersArgs) ([]authstore.User, string, error) {
	limit := int32(50)
	if args.PageSize > 0 && args.PageSize <= 1000 {
		limit = args.PageSize
	}
	var afterID uuid.UUID
	if args.PageToken != "" {
		id, err := uuid.Parse(args.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("invalid page_token: %w", err)
		}
		afterID = id
	}
	var rows []sqlc.User
	if args.TenantID == uuid.Nil {
		got, err := r.q.ListUsersAll(ctx, pgUUID(afterID), limit)
		if err != nil {
			return nil, "", err
		}
		rows = got
	} else {
		got, err := r.q.ListUsersByTenant(ctx, pgUUID(args.TenantID), pgUUID(afterID), limit)
		if err != nil {
			return nil, "", err
		}
		rows = got
	}
	out := make([]authstore.User, 0, len(rows))
	for _, row := range rows {
		out = append(out, userFromSQLC(row))
	}
	var next string
	if int32(len(out)) == limit && len(out) > 0 {
		next = out[len(out)-1].UserID.String()
	}
	return out, next, nil
}

func userFromSQLC(u sqlc.User) authstore.User {
	var roles []string
	_ = json.Unmarshal(u.Roles, &roles)
	var scopeStrs []string
	_ = json.Unmarshal(u.Scopes, &scopeStrs)
	scopes, _ := auth.ParseScopes(scopeStrs)
	return authstore.User{
		UserID:          uuidFrom(u.ID),
		TenantID:        uuidFrom(u.TenantID),
		Subject:         u.Subject,
		DisplayName:     derefStr(u.DisplayName),
		PasswordHash:    u.PasswordHash,
		Roles:           roles,
		Scopes:          scopes,
		Disabled:        u.Disabled,
		ResourceVersion: u.ResourceVersion,
		CreatedAt:       timeFrom(u.CreatedAt),
		UpdatedAt:       timeFrom(u.UpdatedAt),
		LastLoginAt:     timePtr(u.LastLoginAt),
	}
}

func scopesToWire(scopes []auth.Scope) []string {
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, s.String())
	}
	return out
}
