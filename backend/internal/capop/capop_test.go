package capop_test

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/capop"
)

const (
	opGet = "get"
	opPut = "put"
)

func TestAdmitted_EmptyWithoutHolder(t *testing.T) {
	ctx := context.Background()
	capop.Stamp(ctx, opGet)
	if got := capop.Admitted(ctx); got != "" {
		t.Fatalf("Admitted without a holder = %q, want empty", got)
	}
}

func TestAdmitted_EmptyUntilStamped(t *testing.T) {
	if got := capop.Admitted(capop.WithHolder(context.Background())); got != "" {
		t.Fatalf("Admitted before Stamp = %q, want empty", got)
	}
}

func TestStamp_LastWinsWithoutRebinding(t *testing.T) {
	ctx := capop.WithHolder(context.Background())
	capop.Stamp(ctx, opGet)
	capop.Stamp(ctx, opPut)
	if got := capop.Admitted(ctx); got != opPut {
		t.Fatalf("Admitted = %q, want %q", got, opPut)
	}
}

func TestWithHolder_IsPerRequest(t *testing.T) {
	first := capop.WithHolder(context.Background())
	capop.Stamp(first, opGet)
	if got := capop.Admitted(capop.WithHolder(first)); got != "" {
		t.Fatalf("a new holder inherited %q, want empty", got)
	}
}
