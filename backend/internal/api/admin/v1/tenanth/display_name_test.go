package tenanth

import (
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
)

// A display name longer than the column CHECK allows is refused before a row
// is written, as the caller's mistake: the insert failed the CHECK and the
// caller read Internal. The limit counts characters, as char_length does.
func TestDisplayNameLimit(t *testing.T) {
	atLimit := strings.Repeat("é", MaxDisplayNameChars)
	over := atLimit + "x"
	tid := uuid.New()

	repo := &fakeRepo{}
	if _, err := NewHandler(repo, allow()).CreateTenant(adminCtx(tid), CreateTenantArgs{Slug: "acme", DisplayName: atLimit}); err != nil {
		t.Errorf("create at the limit: %v", err)
	}
	repo = &fakeRepo{}
	_, err := NewHandler(repo, allow()).CreateTenant(adminCtx(tid), CreateTenantArgs{Slug: "acme", DisplayName: over})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("create over the limit: code = %v (%v), want InvalidArgument", connect.CodeOf(err), err)
	}
	if repo.lastCreateTx.Slug != "" {
		t.Error("a row was written for a refused display name")
	}

	_, err = NewHandler(&fakeRepo{}, allow()).UpdateTenant(adminCtx(tid), UpdateTenantArgs{TenantID: tid, DisplayName: &over})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("update over the limit: code = %v (%v), want InvalidArgument", connect.CodeOf(err), err)
	}
}
