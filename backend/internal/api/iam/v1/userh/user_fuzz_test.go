package userh

import (
	"slices"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
)

// createCodes is what CreateUser answers a malformed user with: no subject,
// no password, or one too long to hash.
var createCodes = []connect.Code{connect.CodeInvalidArgument}

// updateCodes is what UpdateUser could answer client input with; over a store
// that takes anything it answers none of them.
var updateCodes = []connect.Code{connect.CodeInvalidArgument}

// maskSeparator splits the fuzzed update mask into its paths.
const maskSeparator = ","

// Whatever subject, display name, password and role a create names,
// CreateUser either stores that user — under the subject and display name
// sent, with a hash the password verifies against — or refuses it as the
// caller's mistake, in words that name nothing of the server.
func FuzzCreateUser(f *testing.F) {
	for _, c := range [][4]string{
		{"alice@example.com", "Alice", "s3cret", apiutil.RoleTenantAdmin},
		{"", "Alice", "s3cret", ""},
		{"alice", "", "", ""},
		{"a\x00b", "x\x00", "pw\x00", "\t"},
		{"bob", "Bob \u202e", strings.Repeat("p", maxBcryptPasswordBytes+1), "role"},
		{"\xff\xfe", "\xc3", "\xff", "%!v"},
	} {
		f.Add(c[0], c[1], c[2], c[3])
	}
	tenant := uuid.New()
	ctx := ctxAs(tenant, apiutil.RolePlatformAdmin)
	f.Fuzz(func(t *testing.T, subject, displayName, password, role string) {
		repo := &fakeUserRepo{}
		h := NewHandler(repo, allowAuthorizer{}).WithPasswordHasher(testHasher)
		_, err := h.CreateUser(ctx, CreateUserInput{
			TenantID: tenant, Subject: subject, DisplayName: displayName,
			InitialPassword: password, Roles: []string{role},
		})
		fuzzassert.Refusal(t, err, createCodes, subject, displayName, password, role)
		if err != nil {
			return
		}
		got := repo.created
		if got.Subject != subject || got.DisplayName != displayName || got.TenantID != tenant {
			t.Fatalf("stored %q/%q in %v, sent %q/%q in %v", got.Subject, got.DisplayName, got.TenantID, subject, displayName, tenant)
		}
		if verr := auth.CheckPassword(got.PasswordHash, password); verr != nil {
			t.Fatalf("stored hash does not verify password %q: %v", password, verr)
		}
	})
}

// maxBcryptPasswordBytes is the longest password bcrypt hashes; one longer is
// refused with bcrypt.ErrPasswordTooLong.
const maxBcryptPasswordBytes = 72

// Whatever display name, role, disabled flag and mask an update names,
// UpdateUser either writes exactly the fields its mask names — the subject,
// tenant and password hash never — or refuses it as the caller's mistake.
func FuzzUpdateUser(f *testing.F) {
	for _, c := range []struct {
		mask, displayName, role string
		disabled                bool
	}{
		{pathDisplayName, "Alice", "", false},
		{pathDisabled, "", "", true},
		{pathRoles, "", apiutil.RoleTenantAdmin, false},
		{pathDisplayName + maskSeparator + pathRoles, "x\x00", "\t", true},
		{"subject,password_hash", "Mallory", "role", false},
		{"", "\xff", "%!v", false},
	} {
		f.Add(c.mask, c.displayName, c.role, c.disabled)
	}
	tenant := uuid.New()
	ctx := ctxAs(tenant, apiutil.RolePlatformAdmin)
	f.Fuzz(func(t *testing.T, mask, displayName, role string, disabled bool) {
		seeded := authstore.User{
			UserID: uuid.New(), TenantID: tenant, Subject: "alice", DisplayName: "Alice",
			PasswordHash: []byte("hash"), Roles: []string{apiutil.RoleTenantAdmin},
		}
		repo := &fakeUserRepo{user: seeded}
		h := NewHandler(repo, allowAuthorizer{}).WithPasswordHasher(testHasher)
		paths := strings.Split(mask, maskSeparator)
		_, err := h.UpdateUser(ctx, UpdateUserInput{
			User: ref(seeded), UpdateMask: paths,
			DisplayName: displayName, Disabled: disabled, Roles: []string{role},
		})
		fuzzassert.Refusal(t, err, updateCodes, mask, displayName, role)
		if err != nil {
			return
		}
		got := repo.updated
		if got.Subject != seeded.Subject || got.TenantID != seeded.TenantID || string(got.PasswordHash) != string(seeded.PasswordHash) {
			t.Fatalf("update with mask %q changed what no mask path names: %+v", mask, got)
		}
		wantName := seeded.DisplayName
		if slices.Contains(paths, pathDisplayName) {
			wantName = displayName
		}
		if got.DisplayName != wantName {
			t.Fatalf("display name %q stored, want %q (mask %q)", got.DisplayName, wantName, mask)
		}
		wantDisabled := seeded.Disabled
		if slices.Contains(paths, pathDisabled) {
			wantDisabled = disabled
		}
		if got.Disabled != wantDisabled {
			t.Fatalf("disabled %v stored, want %v (mask %q)", got.Disabled, wantDisabled, mask)
		}
		wantRoles := seeded.Roles
		if slices.Contains(paths, pathRoles) {
			wantRoles = []string{role}
		}
		if !slices.Equal(got.Roles, wantRoles) {
			t.Fatalf("roles %q stored, want %q (mask %q)", got.Roles, wantRoles, mask)
		}
	})
}
