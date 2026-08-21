//go:build integration

// PREPARE proves a query's names and types resolve against the schema. It
// says nothing about what the caller then does with the result, and that gap
// shipped a real defect: api_token's ListByTenant selected thirteen columns
// into fourteen Scan destinations, so every call errored at runtime and the
// console's API-token list was permanently empty. The query itself was
// perfectly valid SQL.
//
// Postgres knows exactly how many columns a statement returns — Prepare hands
// back the field descriptions. The Go AST knows exactly how many destinations
// each Scan passes. Comparing the two turns a runtime error into a build-time
// one for every hand-written query in the tree.
//
// Coverage is deliberately partial. A Scan whose row variable cannot be traced
// back to a Query in the same function is skipped rather than guessed at, and
// the count of skips is logged so the blind spot stays visible.
package integration

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/tests/integration/pgharness"
)

// scanSite is one Scan call paired with the query whose row it consumes.
type scanSite struct {
	file  string
	line  int
	sql   string
	dests int    // number of arguments passed to Scan
	via   string // "QueryRow" or "Query" — for the failure message
}

func extractScans(t *testing.T, root string) (sites []scanSite, untraced int) {
	t.Helper()
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
			return nil
		}
		fileConsts := constStrings(f)

		// Walk one function at a time: both a row variable and a `const q`
		// mean something only inside the function that declares them, and
		// `q` in particular is reused across functions in nearly every
		// adapter file.
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue // imports, types, vars — no Scan calls to trace
			}
			s, u := scansInFunc(fn, fset, path, constsInFunc(fn, fileConsts))
			sites = append(sites, s...)
			untraced += u
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return sites, untraced
}

func scansInFunc(fn *ast.FuncDecl, fset *token.FileSet, path string, consts map[string]string) (sites []scanSite, untraced int) {
	// rowVars maps a variable holding a pgx.Rows to the SQL that produced it.
	rowVars := map[string]string{}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		// `rows, err := pool.Query(ctx, sql, …)` — remember the binding.
		if as, ok := n.(*ast.AssignStmt); ok && len(as.Rhs) == 1 {
			if call, ok := as.Rhs[0].(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok &&
					sel.Sel.Name == "Query" && len(call.Args) >= 2 {
					if sql, ok := resolveString(call.Args[1], consts); ok && looksLikeSQL(sql) {
						if len(as.Lhs) > 0 {
							if id, ok := as.Lhs[0].(*ast.Ident); ok {
								rowVars[id.Name] = sql
							}
						}
					}
				}
			}
		}

		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Scan" {
			return true
		}

		// Case 1: `pool.QueryRow(ctx, sql, …).Scan(a, b, c)` — the query is
		// the receiver of this very Scan.
		if inner, ok := sel.X.(*ast.CallExpr); ok {
			if isel, ok := inner.Fun.(*ast.SelectorExpr); ok &&
				isel.Sel.Name == "QueryRow" && len(inner.Args) >= 2 {
				if sql, ok := resolveString(inner.Args[1], consts); ok && looksLikeSQL(sql) {
					sites = append(sites, scanSite{
						file: path, line: fset.Position(call.Pos()).Line,
						sql: sql, dests: len(call.Args), via: "QueryRow",
					})
					return true
				}
			}
			untraced++
			return true
		}

		// Case 2: `rows.Scan(a, b, c)` where rows came from a Query above.
		if id, ok := sel.X.(*ast.Ident); ok {
			if sql, known := rowVars[id.Name]; known {
				sites = append(sites, scanSite{
					file: path, line: fset.Position(call.Pos()).Line,
					sql: sql, dests: len(call.Args), via: "Query",
				})
				return true
			}
		}
		untraced++
		return true
	})
	return sites, untraced
}

// TestScanArityMatchesResultColumns asks Postgres how many columns each
// hand-written query returns and compares it with how many destinations the
// caller scans into. A mismatch is a guaranteed runtime failure on the first
// call — the kind that a fake-backed handler test cannot see, because a fake
// cannot disagree with a schema.
func TestScanArityMatchesResultColumns(t *testing.T) {
	ctx := context.Background()
	pool := pgharness.Setup(t).PoolMigrate

	sites, untraced := extractScans(t, "../../internal")
	if len(sites) < 20 {
		t.Fatalf("traced only %d Scan sites — the extractor is broken, not the code", len(sites))
	}
	t.Logf("checking %d Scan sites (%d skipped: row variable not traceable to a query)",
		len(sites), untraced)

	var checked, unpreparable int
	for i, s := range sites {
		cols, ok := resultColumns(ctx, t, pool, fmt.Sprintf("scan_arity_%d", i), s.sql)
		if !ok {
			unpreparable++
			continue
		}
		// A statement with no result columns (INSERT without RETURNING, say)
		// is not something Scan should be reading; the PREPARE gate owns that
		// case, and pairing here would only produce noise.
		if cols == 0 {
			continue
		}
		checked++
		if cols != s.dests {
			t.Errorf("%s:%d scans %d destinations from a %s returning %d columns:\n  %s",
				s.file, s.line, s.dests, s.via, cols, firstLine(s.sql))
		}
	}
	t.Logf("compared %d queries, %d could not be prepared in isolation", checked, unpreparable)
}

// resultColumns prepares the statement and reports how many columns it
// returns. Statements PREPARE cannot check in isolation (see the tolerated
// list in the PREPARE gate) report ok=false rather than failing here — that
// gate already owns those.
func resultColumns(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name, sql string) (int, bool) {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()

	sd, err := conn.Conn().Prepare(ctx, name, sql)
	if err != nil {
		return 0, false
	}
	defer func() { _ = conn.Conn().Deallocate(ctx, name) }()
	return len(sd.Fields), true
}
