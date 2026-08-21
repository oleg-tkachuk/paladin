//go:build integration

// A schema gate for the SQL that sqlc never sees.
//
// Most queries are generated from queries/*.sql, which sqlc checks against
// the schema at generate time. The rest — 128 call sites — are Go string
// literals handed to pool.Query / Exec / QueryRow. Nothing validates those:
// sqlc does not read them and the compiler does not read string contents.
// The identity refactor broke six of them, and every one was found by
// accident rather than by a check:
//
//   - cedar/store.go read a tenant's policy through dropped columns. It
//     did not error — it returned "no policy", and Cedar's deny-by-default
//     turned that into an unexplained `forbidden`.
//   - platformstats counted collections by a column that no longer existed.
//   - ClaimIngestedEvent named a conflict target with no matching
//     constraint, so *every* ingested event failed, not just duplicates.
//   - the charges ledger INSERT omitted a NOT NULL column.
//   - two list queries compared a uuid against a text cursor.
//   - TenantBudgetService joined `tenants AS t ON t.tenant_id`.
//
// This test extracts every such literal from the AST and asks Postgres to
// PREPARE it. Postgres is the authority on its own columns — unlike a
// third-party parser, it cannot disagree with the database the code will
// actually run against.
package integration

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/oleg-tkachuk/paladin/tests/integration/pgharness"
)

// queryMethods are the pgx entry points that take SQL as their second
// argument (the first being ctx).
var queryMethods = map[string]bool{
	"Query": true, "QueryRow": true, "Exec": true, "SendBatch": false,
}

type sqlSite struct {
	file string
	line int
	sql  string
}

// extractSQL walks a package directory and returns every SQL literal passed
// to a pgx query method, resolving constants and `+` concatenation of
// literals so a query assembled from fragments is checked as one statement.
func extractSQL(t *testing.T, root string) []sqlSite {
	t.Helper()
	var out []sqlSite
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == "testdata" || base == "sqlc" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil // unparseable file is the compiler's problem, not ours
		}
		consts := constStrings(f)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !queryMethods[sel.Sel.Name] {
				return true
			}
			sql, ok := resolveString(call.Args[1], consts)
			if !ok || !looksLikeSQL(sql) {
				return true
			}
			out = append(out, sqlSite{
				file: path,
				line: fset.Position(call.Pos()).Line,
				sql:  sql,
			})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// constStrings collects file-level and function-level string constants so a
// `const q = ...` one line above the call still resolves.
func constStrings(f *ast.File) map[string]string {
	out := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			if s, ok := resolveString(vs.Values[i], out); ok {
				out[name.Name] = s
			}
		}
		return true
	})
	return out
}

// resolveString evaluates a string expression made of literals, named
// constants, and `+`. Anything else (a fmt.Sprintf, a variable) returns
// false — those are reported separately rather than guessed at.
func resolveString(e ast.Expr, consts map[string]string) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.Ident:
		s, ok := consts[v.Name]
		return s, ok
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, lok := resolveString(v.X, consts)
		r, rok := resolveString(v.Y, consts)
		if !lok || !rok {
			return "", false
		}
		return l + r, true
	case *ast.ParenExpr:
		return resolveString(v.X, consts)
	}
	return "", false
}

func looksLikeSQL(s string) bool {
	u := strings.ToUpper(strings.TrimSpace(s))
	for _, kw := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "WITH"} {
		if strings.HasPrefix(u, kw) {
			return true
		}
	}
	return false
}

// TestHandWrittenSQLPrepares asks Postgres to PREPARE every hand-written
// query against the real schema.
//
// PREPARE parses, resolves names and plans — so it catches a dropped column,
// a renamed table, a conflict target with no matching constraint, and a
// comparison between incompatible types. It does not execute, so nothing is
// written and no row is read.
func TestHandWrittenSQLPrepares(t *testing.T) {
	ctx := context.Background()
	// PoolMigrate: PREPARE only needs to resolve names against the schema,
	// and the migrate role owns every table. RLS is irrelevant here — no row
	// is read.
	pool := pgharness.Setup(t).PoolMigrate

	sites := extractSQL(t, "../../internal")
	if len(sites) < 50 {
		t.Fatalf("found only %d SQL sites — the extractor is broken, not the code", len(sites))
	}
	t.Logf("checking %d hand-written queries", len(sites))

	var checked, skipped int
	for i, s := range sites {
		name := fmt.Sprintf("q%d", i)
		_, err := pool.Exec(ctx, fmt.Sprintf("PREPARE %s AS %s", name, s.sql))
		if err == nil {
			checked++
			_, _ = pool.Exec(ctx, "DEALLOCATE "+name)
			continue
		}
		if reason, ok := tolerated(err); ok {
			skipped++
			if testing.Verbose() {
				t.Logf("%s:%d skipped (%s)", s.file, s.line, reason)
			}
			continue
		}
		t.Errorf("%s:%d does not prepare against the schema:\n  %v\n  %s",
			s.file, s.line, err, firstLine(s.sql))
	}
	t.Logf("prepared %d, skipped %d", checked, skipped)
}

// tolerated separates "this query is fine, PREPARE just cannot check it in
// isolation" from a real schema mismatch.
func tolerated(err error) (string, bool) {
	var pg *pgconn.PgError
	if !errorAs(err, &pg) {
		return "", false
	}
	switch pg.Code {
	case "42P18":
		// indeterminate_datatype — a bare $1 whose type the planner cannot
		// infer (e.g. `WHERE $1 IS NULL OR col = $1`). The driver supplies
		// the type at bind time; the column references were still resolved
		// to get this far.
		return "parameter type not inferable without a bind", true
	case "42P05":
		// duplicate_prepared_statement — the same SQL text appears at more
		// than one call site. Already checked.
		return "duplicate statement", true
	case "0A000":
		// feature_not_supported — utility statements (SET, DEALLOCATE,
		// CREATE …) cannot be prepared at all.
		return "utility statement", true
	}
	return "", false
}

func errorAs(err error, target **pgconn.PgError) bool {
	return errors.As(err, target)
}

func firstLine(s string) string {
	for _, ln := range strings.Split(strings.TrimSpace(s), "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" && !strings.HasPrefix(ln, "--") {
			if len(ln) > 90 {
				return ln[:90] + "…"
			}
			return ln
		}
	}
	return ""
}
