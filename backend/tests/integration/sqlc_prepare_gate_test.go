//go:build integration

// A schema gate for the SQL sqlc DOES generate.
//
// The sibling gate (sql_prepare_gate_test.go) deliberately skips the sqlc
// package on the reasoning that sqlc already checks those queries against the
// schema. It does — for names and shapes. It does not ask Postgres to deduce
// the parameter types, and that is a separate way to be wrong:
//
//	SET state = sqlc.arg('state'),                        -- operation_state
//	done_at = CASE WHEN sqlc.arg('state') IN ('SUCCEEDED') -- text
//
// sqlc generated that happily. Postgres refuses it with 42P08, "inconsistent
// types deduced for parameter $2" — but only at execution, and only on the
// path that runs it. The path was UpdateOperationState, so every long-running
// operation logged "operation succeeded" and then "failed to mark SUCCEEDED",
// staying RUNNING forever with its response unwritten. Nothing else noticed:
// the RPC had already returned, and the runner treats the state write as
// best-effort.
//
// PREPARE is exactly the missing step. It forces the deduction without
// executing anything.
package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// generatedQuery is one `const name = ` + "`" + `SQL` + "`" + “ in the sqlc output.
type generatedQuery struct {
	file string
	name string
	sql  string
}

var sqlcConstRe = regexp.MustCompile("(?s)const (\\w+) = `([^`]+)`")

func TestGeneratedSQLPrepares(t *testing.T) {
	ctx := context.Background()
	pool := pgharness.Setup(t).PoolMigrate

	queries := generatedQueries(t, "../../internal/store/postgres/sqlc")
	if len(queries) < 100 {
		t.Fatalf("found only %d generated queries — the extractor is broken, not the code", len(queries))
	}
	t.Logf("checking %d generated queries", len(queries))

	var checked, skipped int
	var failures []string
	for i, q := range queries {
		stmt := fmt.Sprintf("gen_q%d", i)
		_, err := pool.Exec(ctx, fmt.Sprintf("PREPARE %s AS %s", stmt, q.sql))
		if err == nil {
			checked++
			_, _ = pool.Exec(ctx, "DEALLOCATE "+stmt)
			continue
		}
		if reason, ok := tolerated(err); ok {
			skipped++
			if testing.Verbose() {
				t.Logf("%s %s skipped (%s)", q.file, q.name, reason)
			}
			continue
		}
		failures = append(failures, fmt.Sprintf("%s %s:\n  %v", q.file, q.name, err))
	}

	sort.Strings(failures)
	for _, f := range failures {
		t.Errorf("generated query does not prepare against the schema:\n%s", f)
	}
	t.Logf("prepared %d, skipped %d, failed %d", checked, skipped, len(failures))
}

// generatedQueries pulls every query constant out of the sqlc output. It reads
// the generated Go rather than queries/*.sql on purpose: the .sql sources carry
// sqlc.arg/narg/embed directives Postgres cannot parse, while the generated
// constants are the real statements, numbered placeholders and all — the exact
// text the driver sends.
func generatedQueries(t *testing.T, dir string) []generatedQuery {
	t.Helper()
	var out []generatedQuery
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read sqlc dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 — repo-local
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range sqlcConstRe.FindAllStringSubmatch(string(src), -1) {
			out = append(out, generatedQuery{file: e.Name(), name: m[1], sql: m[2]})
		}
	}
	return out
}
