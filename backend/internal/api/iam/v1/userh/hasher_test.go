package userh

import (
	"bytes"
	"testing"
	"uuid"

	"golang.org/x/crypto/bcrypt"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// testHashCost is the bcrypt cost the tests hash at: the lowest bcrypt
// accepts, so a fuzz search tries many passwords a second.
const testHashCost = bcrypt.MinCost

// testHasher hashes at testHashCost.
func testHasher(plaintext string) ([]byte, error) {
	return auth.HashPasswordCost(plaintext, testHashCost)
}

// testPassword is a password the tests create and reset users with.
const testPassword = "s3cret"

// The hashes a handler stores, by how it was built.
func TestPasswordHasher(t *testing.T) {
	stub := []byte("stub-hash")
	for _, tc := range []struct {
		name string
		with PasswordHasher
		// check judges a stored hash.
		check func(t *testing.T, hash []byte)
	}{
		{"default hashes at the production cost", nil, func(t *testing.T, hash []byte) {
			t.Helper()
			if got, err := bcrypt.Cost(hash); err != nil || got != auth.PasswordHashCost {
				t.Fatalf("cost = %d, %v; want %d", got, err, auth.PasswordHashCost)
			}
		}},
		{"injected hasher sets the cost", testHasher, func(t *testing.T, hash []byte) {
			t.Helper()
			if got, err := bcrypt.Cost(hash); err != nil || got != testHashCost {
				t.Fatalf("cost = %d, %v; want %d", got, err, testHashCost)
			}
			if err := auth.CheckPassword(hash, testPassword); err != nil {
				t.Fatalf("stored hash does not verify the password: %v", err)
			}
		}},
		{"injected hasher is what is stored", func(string) ([]byte, error) { return stub, nil }, func(t *testing.T, hash []byte) {
			t.Helper()
			if !bytes.Equal(hash, stub) {
				t.Fatalf("stored %q, want the injected hasher's %q", hash, stub)
			}
		}},
	} {
		t.Run(tc.name+"/create", func(t *testing.T) {
			caller := uuid.New()
			repo := &fakeUserRepo{}
			h := NewHandler(repo, allowAuthorizer{}).WithPasswordHasher(tc.with)
			if _, err := h.CreateUser(ctxAs(caller, apiutil.RolePlatformAdmin), CreateUserInput{
				TenantID: caller, Subject: "u1", InitialPassword: testPassword,
			}); err != nil {
				t.Fatal(err)
			}
			tc.check(t, repo.created.PasswordHash)
		})
		t.Run(tc.name+"/reset", func(t *testing.T) {
			repo := &fakeUserRepo{user: authstore.User{UserID: uuid.New(), TenantID: uuid.New()}}
			h := NewHandler(repo, allowAuthorizer{}).WithPasswordHasher(tc.with)
			if _, err := h.ResetPassword(ctxAs(uuid.New(), apiutil.RolePlatformAdmin), ref(repo.user), testPassword); err != nil {
				t.Fatal(err)
			}
			tc.check(t, repo.pwdHash)
		})
	}
}
