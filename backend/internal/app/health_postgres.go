package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/migrations"
)

// Detail names the postgres components report. The console shows them as
// they are; they are a contract with nothing but a reader.
const (
	detailConnections   = "connections"
	detailIdle          = "idle"
	detailNoIdle        = "acquired with no idle connection"
	detailSchemaApplied = "schema applied"
	detailSchemaWanted  = "schema this build needs"
)

// postgresComponent pings the primary, and describes the role's pool: how
// many of its connections are in use, and how many acquires found no idle
// connection (pgx's EmptyAcquireCount). That count includes the connections
// opened as the pool warms up after start, so it is not a measure of load on
// its own — its growth on a warm pool is. The pool is read in memory;
// describing it touches no database.
func postgresComponent(db *postgres.DB) health.Check {
	return health.Check{
		Name:     "postgres",
		Category: health.CategoryDatabase,
		Critical: true,
		Func:     func(ctx context.Context) error { return db.Ping(ctx) },
		Describe: func(context.Context) []health.Detail {
			if db == nil || db.Pool == nil {
				return nil
			}
			return poolDetails(db.Pool.Stat())
		},
	}
}

// poolStat is what poolDetails reads of a pgxpool.Stat.
type poolStat interface {
	AcquiredConns() int32
	MaxConns() int32
	IdleConns() int32
	EmptyAcquireCount() int64
}

func poolDetails(st poolStat) []health.Detail {
	return []health.Detail{
		{Name: detailConnections, Value: fmt.Sprintf("%d of %d in use", st.AcquiredConns(), st.MaxConns())},
		{Name: detailIdle, Value: strconv.Itoa(int(st.IdleConns()))},
		{Name: detailNoIdle, Value: fmt.Sprintf("%d times since start", st.EmptyAcquireCount())},
	}
}

// schemaVersions is what the schema component reads of goose: the version
// the database has applied, and the migrations this build carries.
// *goose.Provider implements it.
type schemaVersions interface {
	GetDBVersion(ctx context.Context) (int64, error)
	ListSources() []*goose.Source
}

// schemaComponent checks the database schema is at least the one this build
// was written against. Behind, a query this build makes can name a column
// that is not there yet — the migrate Job did not run — so it is critical:
// the pod must not take traffic. Ahead is normal mid-rollout, when the Job
// has migrated for the new build and the old pods are still serving.
func schemaComponent(db *postgres.DB) health.Check {
	versions, err := gooseVersions(db)
	if err != nil {
		return health.Check{
			Name:     "postgres-schema",
			Category: health.CategoryDatabase,
			Critical: true,
			Func:     func(context.Context) error { return err },
		}
	}
	return schemaCheck(versions)
}

// gooseVersions reads the versions through goose itself, over the role's
// own pool, so the version table is read the way goose writes it.
func gooseVersions(db *postgres.DB) (schemaVersions, error) {
	if db == nil {
		return nil, errors.New("no database")
	}
	pool, ok := db.Pool.(*pgxpool.Pool)
	if !ok {
		return nil, fmt.Errorf("pool %T cannot be read through goose", db.Pool)
	}
	return goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrations.FS)
}

func schemaCheck(v schemaVersions) health.Check {
	var wanted int64
	for _, s := range v.ListSources() {
		wanted = max(wanted, s.Version)
	}
	// The applied version the last check read, for Describe: snapshots run
	// concurrently, so it is shared under a lock rather than read twice.
	var (
		mu      sync.Mutex
		applied *int64
	)
	return health.Check{
		Name:     "postgres-schema",
		Category: health.CategoryDatabase,
		Critical: true,
		Func: func(ctx context.Context) error {
			got, err := v.GetDBVersion(ctx)
			mu.Lock()
			applied = nil
			if err == nil {
				applied = &got
			}
			mu.Unlock()
			if err != nil {
				return fmt.Errorf("read the applied schema version: %w", err)
			}
			if got < wanted {
				return fmt.Errorf("schema at %d, this build needs %d: migrations have not run", got, wanted)
			}
			return nil
		},
		Describe: func(context.Context) []health.Detail {
			var d []health.Detail
			mu.Lock()
			if applied != nil {
				d = append(d, health.Detail{Name: detailSchemaApplied, Value: strconv.FormatInt(*applied, 10)})
			}
			mu.Unlock()
			return append(d, health.Detail{Name: detailSchemaWanted, Value: strconv.FormatInt(wanted, 10)})
		},
	}
}
