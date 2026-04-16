package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/oleg-tkachuk/paladin/internal/domain"
)

// pgUoWFactory implements domain.UoWFactory using a PostgreSQL connection pool.
type pgUoWFactory struct {
	db *DB
}

// NewUoWFactory creates a new Unit of Work factory.
func NewUoWFactory(db *DB) domain.UoWFactory {
	return &pgUoWFactory{db: db}
}

// Begin starts a new transaction and returns a UnitOfWork bound to it.
func (f *pgUoWFactory) Begin(ctx context.Context) (domain.UnitOfWork, error) {
	if f.db == nil || f.db.Pool == nil {
		return nil, fmt.Errorf("database pool not initialized")
	}

	tx, err := f.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}

	// Create a queries instance bound to the transaction
	qtx := f.db.Queries.WithTx(tx)

	// Create a DB wrapper that uses the transaction for queries
	// (so repos that take *DB will use the tx instead)
	// We need a specific struct that implements the tx
	return &pgUoW{
		tx:   tx,
		dbTx: &DB{Pool: f.db.Pool, Queries: qtx, log: f.db.log},
	}, nil
}

// pgUoW implements domain.UnitOfWork for PostgreSQL.
type pgUoW struct {
	tx   pgx.Tx
	dbTx *DB
}

func (u *pgUoW) Objects() domain.ObjectsRepository {
	return NewObjectsRepo(u.dbTx)
}

func (u *pgUoW) Multipart() domain.MultipartRepository {
	return NewMultipartRepo(u.dbTx)
}

func (u *pgUoW) Idempotency() domain.IdempotencyRepository {
	return NewIdempotencyRepo(u.dbTx)
}

func (u *pgUoW) AuditLogs() domain.AuditLogRepository {
	return NewAuditLogRepo(u.dbTx)
}

func (u *pgUoW) Categories() domain.CategoryRepository {
	return NewCategoryRepo(u.dbTx)
}

func (u *pgUoW) UploadIntents() domain.UploadIntentsRepository {
	return NewUploadIntentsRepo(u.dbTx)
}

func (u *pgUoW) Commit(ctx context.Context) error {
	if err := u.tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (u *pgUoW) Rollback(ctx context.Context) error {
	if err := u.tx.Rollback(ctx); err != nil {
		return fmt.Errorf("rollback transaction: %w", err)
	}

	return nil
}
