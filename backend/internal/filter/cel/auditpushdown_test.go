package cel

import (
	"testing"
	"time"
)

func TestExtractAuditPushdown(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		expr string
		want AuditPushdown
	}{
		{
			name: "empty",
			expr: "",
			want: AuditPushdown{},
		},
		{
			name: "action equality",
			expr: `action == "/paladin.admin.v1.TenantService/CreateTenant"`,
			want: AuditPushdown{
				ActionEq: "/paladin.admin.v1.TenantService/CreateTenant",
			},
		},
		{
			name: "action prefix",
			expr: `action.startsWith("/paladin.admin.v1.")`,
			want: AuditPushdown{
				ActionPrefix: "/paladin.admin.v1.",
			},
		},
		{
			name: "actor subject equality",
			expr: `actor_subject == "alice@example.com"`,
			want: AuditPushdown{
				ActorSubjectEq: "alice@example.com",
			},
		},
		{
			name: "time range gte and lte",
			expr: `at >= timestamp("2026-05-01T12:00:00Z") && at <= timestamp("2026-05-02T12:00:00Z")`,
			want: AuditPushdown{
				AtGTE: t0,
				AtLTE: t0.Add(24 * time.Hour),
			},
		},
		{
			name: "compound conjunction",
			expr: `action.startsWith("/paladin.admin.v1.") && actor_subject == "ops" && at >= timestamp("2026-05-01T12:00:00Z")`,
			want: AuditPushdown{
				ActionPrefix:   "/paladin.admin.v1.",
				ActorSubjectEq: "ops",
				AtGTE:          t0,
			},
		},
		{
			name: "unrecognised leaves stay residual",
			// resource_name is not on the recognised list — should be
			// silently skipped (in-memory CEL still enforces it).
			expr: `action == "x" && resource_name == "y"`,
			want: AuditPushdown{
				ActionEq: "x",
			},
		},
		{
			name: "disjunction is not a top-level conjunct",
			// The OR is opaque to the extractor — neither side becomes
			// a SQL predicate. In-memory CEL handles it unchanged.
			expr: `action == "x" || action == "y"`,
			want: AuditPushdown{},
		},
		{
			name: "strict bounds widen safely",
			// `at > X` is treated as `at >= X` at the SQL layer; the
			// in-memory CEL eval drops the boundary row.
			expr: `at > timestamp("2026-05-01T12:00:00Z")`,
			want: AuditPushdown{
				AtGTE: t0,
			},
		},
		{
			name: "second equality on same field discarded",
			expr: `action == "x" && action == "y"`,
			want: AuditPushdown{
				ActionEq: "x",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractAuditPushdown(tc.expr)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if got.ActionEq != tc.want.ActionEq ||
				got.ActionPrefix != tc.want.ActionPrefix ||
				got.ActorSubjectEq != tc.want.ActorSubjectEq ||
				!got.AtGTE.Equal(tc.want.AtGTE) ||
				!got.AtLTE.Equal(tc.want.AtLTE) {
				t.Errorf("pushdown mismatch:\n got: %+v\nwant: %+v", got, tc.want)
			}
		})
	}
}

// TestExtractAuditPushdown_BadExprIsBenign confirms a parse failure
// produces an error but never panics — the audit handler swallows the
// error and lets in-memory CEL re-surface it.
func TestExtractAuditPushdown_BadExprIsBenign(t *testing.T) {
	if _, err := ExtractAuditPushdown("(("); err == nil {
		t.Fatal("expected parse error, got nil")
	}
}
