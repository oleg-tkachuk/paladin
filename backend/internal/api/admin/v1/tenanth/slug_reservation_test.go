package tenanth

import (
	"context"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// A slug a trashed or purged tenant held names that tenant in every policy,
// token and client, so a new tenant may take it only when the deployment
// allows reuse (admin.tenants.reserve_slugs false). The handler tells the
// repository which, and the zero value of a Handler reserves.
func TestCreateTenantPassesSlugReuse(t *testing.T) {
	tid := uuid.New()
	for name, tc := range map[string]struct {
		set   bool
		allow bool
		want  bool
	}{
		"a handler never told reserves": {want: false},
		"reuse allowed":                 {set: true, allow: true, want: true},
		"reuse refused":                 {set: true, allow: false, want: false},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepo{}
			h := NewHandler(repo, allow())
			if tc.set {
				h.SetSlugReuse(tc.allow)
			}
			if _, err := h.CreateTenant(adminCtx(tid), CreateTenantArgs{Slug: "acme"}); err != nil {
				t.Fatal(err)
			}
			if repo.lastCreateTx.AllowSlugReuse != tc.want {
				t.Errorf("AllowSlugReuse = %v, want %v", repo.lastCreateTx.AllowSlugReuse, tc.want)
			}
		})
	}
}

// A reserved slug is refused as AlreadyExists, saying which kind of holder
// keeps it, and the refusal is logged with the slug.
func TestCreateTenantRefusesAReservedSlug(t *testing.T) {
	tid := uuid.New()
	for _, sentinel := range []error{ErrSlugHeldInTrash, ErrSlugRetired} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			repo := &fakeRepo{createTxFn: func(context.Context, pgx.Tx, CreateTenantArgs) error { return sentinel }}
			core, logs := observer.New(zapcore.WarnLevel)
			h := NewHandler(repo, allow())
			h.SetLogger(zap.New(core))
			_, err := h.CreateTenant(adminCtx(tid), CreateTenantArgs{Slug: "acme"})
			wantCode(t, err, connect.CodeAlreadyExists)
			if got := err.Error(); !strings.Contains(got, sentinel.Error()) {
				t.Errorf("message %q does not say %q", got, sentinel.Error())
			}
			entries := logs.FilterField(zap.String("slug", "acme")).All()
			if len(entries) != 1 {
				t.Fatalf("logged %d refusals naming the slug, want 1", len(entries))
			}
		})
	}
}
