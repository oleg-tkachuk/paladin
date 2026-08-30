//go:build integration

package integration

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// TestBucketReferenceListMatchesSchema is the guard on a list that has already
// gone stale once.
//
// DeleteBucket refuses while anything holds the bucket under ON DELETE
// RESTRICT, and it learns what holds it from CountBucketReferences — a query
// with the relations written into it by hand. The first version of that query
// knew about collections and nothing else, so a bucket with no collections but
// a tenant default binding walked straight past the check and produced the raw
// `violates foreign key constraint "tenant_default_bindings_bucket_id_fkey"`
// the check exists to prevent. Six relations, one counted.
//
// A hand-written list cannot be trusted to keep up with the schema, so it is
// not trusted: this reads the RESTRICT set out of pg_constraint and requires
// the query to cover exactly it. Add a seventh RESTRICT reference to buckets
// and this fails, naming it, instead of the gap surfacing later as a 500.
//
// CASCADE dependents (replication_state, quotas today) are excluded on both
// sides — they never block a delete, so listing them would send an operator
// after rows that clean themselves up.
func TestBucketReferenceListMatchesSchema(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	q := sqlc.New(pool)

	rows, err := pool.Query(ctx, `
		SELECT DISTINCT c.conrelid::regclass::text
		FROM pg_constraint c
		WHERE c.confrelid = 'buckets'::regclass
		  AND c.contype = 'f'
		  AND c.confdeltype = 'r'`)
	if err != nil {
		t.Fatalf("read pg_constraint: %v", err)
	}
	defer rows.Close()
	var schema []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		schema = append(schema, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(schema) == 0 {
		t.Fatal("no RESTRICT references to buckets found — either the schema " +
			"changed fundamentally or this query stopped matching; do not " +
			"read this as 'nothing to check'")
	}

	// The query answers for a bucket that does not exist, which is fine: the
	// relations it reports are fixed by the SQL, and the counts are all zero.
	counted, err := q.CountBucketReferences(ctx, "no-such-backend", "no-such-bucket")
	if err != nil {
		t.Fatalf("CountBucketReferences: %v", err)
	}
	var listed []string
	for _, r := range counted {
		listed = append(listed, r.Relation)
	}

	sort.Strings(schema)
	sort.Strings(listed)
	if len(schema) != len(listed) {
		t.Fatalf("the query covers %v; the schema restricts %v", listed, schema)
	}
	for i := range schema {
		if schema[i] != listed[i] {
			t.Fatalf("the query covers %v; the schema restricts %v", listed, schema)
		}
	}
}

// TestDeleteBucketHeldByANonCollectionIsAConflict is the runtime half: the
// adapter's delete must map the database's refusal to ErrConflict rather than
// letting a raw SQLSTATE out, whichever relation does the refusing. The
// handler's pre-count normally answers first; this covers the race, and any
// relation the count has not learned about yet.
func TestDeleteBucketHeldByANonCollectionIsAConflict(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	q := sqlc.New(pool)
	repo := adapters.NewBucketRepoV2(q, pool)

	tenant, _ := mkTenant(t, ctx, pool, "shared")
	backendID := "be-" + uuid.NewString()[:8]
	bucketName := "bk-" + uuid.NewString()[:8]
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, provider, endpoint, region)
		 VALUES ($1, 's3-compatible', 'garage', 'http://x.invalid:3900', 'us-east-1')`,
		backendID)
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (backend_id, name, owner_tenant_id)
		 SELECT id, $2, $3 FROM storage_backends WHERE name = $1`,
		backendID, bucketName, tenant)
	// A default binding, deliberately: no collection is involved, which is
	// precisely the shape that defeated the first version of the guard.
	mustExec(t, ctx, pool,
		`INSERT INTO tenant_default_bindings (tenant_id, bucket_id, set_by)
		 SELECT $1, b.id, 'integration-test' FROM buckets b
		 JOIN storage_backends sb ON sb.id = b.backend_id
		 WHERE sb.name = $2 AND b.name = $3`,
		tenant, backendID, bucketName)

	err := repo.Delete(ctx, backendID, bucketName, 0)
	if !errors.Is(err, admindomain.ErrConflict) {
		t.Fatalf("Delete = %v, want ErrConflict", err)
	}
	if got := err.Error(); !strings.Contains(got, "tenant_default_bindings") {
		t.Errorf("the error must name what is holding the bucket.\ngot: %v", got)
	}
}

// TestPurgeTenantNamesTheRelationHoldingIt covers the message an operator
// actually acts on. It used to be a fixed string — "tenant still has object
// keys or objects" — where "object keys" is the internal name for collections
// and "objects" was the other half of a guess. The blocker is just as often
// `users`, which every tenant has, so a purge refused by users sent the
// operator looking through collections.
//
// The relation now comes from the field Postgres fills on the violation, so
// it is right for every referencing table without a list to maintain — the
// thing that made the first bucket guard incomplete.
func TestPurgeTenantNamesTheRelationHoldingIt(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	q := sqlc.New(pool)
	repo := adapters.NewTenantRepo(q, pool)

	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	mustExec(t, ctx, pool,
		`INSERT INTO users (tenant_id, subject, password_hash, roles)
		 VALUES ($1, 'someone', 'x', '{}')`, tenantID)

	err := repo.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return repo.HardDeleteTx(ctx, tx, tenantID, 0)
	})
	if err == nil {
		t.Fatal("HardDelete on a tenant with a user: want a refusal")
	}
	if !strings.Contains(err.Error(), "users") {
		t.Errorf("the message must name what is holding the tenant.\ngot: %v", err)
	}
	if strings.Contains(err.Error(), "object keys") {
		t.Errorf("the old guess is back.\ngot: %v", err)
	}
}
