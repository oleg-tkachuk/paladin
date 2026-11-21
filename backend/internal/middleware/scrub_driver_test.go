package middleware

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
)

// errCollectionMissing stands in for a store's not-found sentinel.
var errCollectionMissing = errors.New("collection not found")

// uniqueViolation is what Postgres answers a duplicate insert with: the
// constraint, the table's key and the SQLSTATE, all of it the server's own.
func uniqueViolation() *pgconn.PgError {
	return &pgconn.PgError{
		Severity:       "ERROR",
		Code:           "23505",
		Message:        `duplicate key value violates unique constraint "tenants_slug_key"`,
		ConstraintName: "tenants_slug_key",
		TableName:      "tenants",
	}
}

// A refusal keeps its code and what it says in product terms, and loses the
// text of any driver error it wraps — wherever in the message the wrap put it.
func TestScrubDropsDriverTextFromARefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		code connect.Code
		err  error
		want string
	}{
		{
			"no rows after the sentinel", connect.CodeNotFound,
			fmt.Errorf("%w: %q: %w", errCollectionMissing, "docs", pgx.ErrNoRows),
			`collection not found: "docs"`,
		},
		{
			"constraint in the middle", connect.CodeAlreadyExists,
			fmt.Errorf("create tenant %q: %w: %w", "acme", uniqueViolation(), apiutil.ErrAlreadyExists),
			`create tenant "acme": already exists`,
		},
		{
			"nothing but the driver", connect.CodeAlreadyExists,
			uniqueViolation(),
			"already exists",
		},
		{
			"too many rows", connect.CodeFailedPrecondition,
			fmt.Errorf("lookup: %w", pgx.ErrTooManyRows),
			"lookup",
		},
		{
			"no driver error", connect.CodeNotFound,
			fmt.Errorf("%w: %q", errCollectionMissing, "docs"),
			`collection not found: "docs"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := connect.NewError(tc.code, tc.err.Error()).WithCause(tc.err)
			var out *connect.Error
			if !errors.As(scrub(in, "req-1"), &out) {
				t.Fatal("not a connect error")
			}
			if out.Code() != tc.code || out.Message() != tc.want {
				t.Errorf("scrubbed to %v %q, want %v %q", out.Code(), out.Message(), tc.code, tc.want)
			}
			if !errors.Is(out, tc.err) {
				t.Error("the cause was dropped: logs and errors.Is lose the original")
			}
		})
	}
}

// A message that flattened pgx.ErrNoRows with %v kept no chain to find it by,
// and still loses its text.
func TestScrubDropsFlattenedNoRows(t *testing.T) {
	in := connect.NewError(connect.CodeNotFound, fmt.Sprintf("user %q: %v", "ann", pgx.ErrNoRows))
	if got := connect.CodeOf(scrub(in, "req-1")); got != connect.CodeNotFound {
		t.Fatalf("code = %v", got)
	}
	var out *connect.Error
	if !errors.As(scrub(in, "req-1"), &out) || out.Message() != `user "ann"` {
		t.Errorf("scrubbed to %q", out.Message())
	}
}

// The guard covers MapError's refusals: a store that wrapped the driver's
// error under a sentinel reaches the caller without it.
func TestScrubDropsDriverTextFromMapError(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("get bucket %q: %w: %w", "b1", apiutil.ErrNotFound, pgx.ErrNoRows),
		fmt.Errorf("create bucket %q: %w: %w", "b1", apiutil.ErrAlreadyExists, uniqueViolation()),
	} {
		out := scrub(apiutil.MapError(err), "req-1")
		if msg := out.Error(); strings.Contains(msg, pgx.ErrNoRows.Error()) || strings.Contains(msg, "SQLSTATE") || strings.Contains(msg, "tenants_slug_key") {
			t.Errorf("caller reads %q", msg)
		}
		if !strings.Contains(out.Error(), `"b1"`) {
			t.Errorf("caller reads %q, which no longer names the bucket", out.Error())
		}
	}
}

// Stripping a driver's text is a defect in the wrap that put it there, and
// the server says so, naming the RPC and the request.
func TestScrubInternalLogsTheDriverTextItDrops(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	cause := fmt.Errorf("%w: %w", errCollectionMissing, pgx.ErrNoRows)
	probe := &unarytest.Probe{OnCall: func(context.Context) error {
		return connect.NewError(connect.CodeNotFound, cause.Error()).WithCause(cause)
	}}
	_, err := unarytest.CallProbe(context.Background(), probe, []connect.ServerInterceptor{ScrubInternal(zap.New(core))}, HeaderRequestID, "req-7")
	if strings.Contains(err.Error(), pgx.ErrNoRows.Error()) {
		t.Fatalf("caller reads %v", err)
	}
	entries := logs.FilterMessage(msgDriverTextDropped).All()
	if len(entries) != 1 {
		t.Fatalf("%d %q lines, want 1", len(entries), msgDriverTextDropped)
	}
	if fields := entries[0].ContextMap(); fields["request_id"] != "req-7" || fields["code"] != connect.CodeNotFound.String() {
		t.Errorf("fields %v", fields)
	}

	logs.TakeAll()
	clean := &unarytest.Probe{OnCall: func(context.Context) error {
		return connect.NewError(connect.CodeNotFound, errCollectionMissing.Error()).WithCause(errCollectionMissing)
	}}
	if _, err := unarytest.CallProbe(context.Background(), clean, []connect.ServerInterceptor{ScrubInternal(zap.New(core))}); err == nil {
		t.Fatal("want the refusal")
	}
	if n := logs.Len(); n != 0 {
		t.Errorf("%d lines for a refusal with no driver text", n)
	}
}
