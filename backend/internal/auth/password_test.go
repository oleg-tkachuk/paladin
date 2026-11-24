package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestPasswordHashAndCheck(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPassword(hash, "correct-horse-battery-staple"); err != nil {
		t.Errorf("matching password rejected: %v", err)
	}
	if err := CheckPassword(hash, "wrong"); err == nil {
		t.Error("non-matching password accepted")
	}
}

func TestEmptyPasswordRejected(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Error("expected error on empty password")
	}
}

// HashPassword hashes at the production cost; HashPasswordCost at the cost
// it is given, and refuses an empty password as HashPassword does.
func TestPasswordHashCost(t *testing.T) {
	const password = "correct-horse-battery-staple"
	for _, tc := range []struct {
		name string
		hash func(string) ([]byte, error)
		want int
	}{
		{"production", HashPassword, PasswordHashCost},
		{"min", func(p string) ([]byte, error) { return HashPasswordCost(p, bcrypt.MinCost) }, bcrypt.MinCost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := tc.hash(password)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := bcrypt.Cost(hash); err != nil || got != tc.want {
				t.Fatalf("cost = %d, %v; want %d", got, err, tc.want)
			}
			if err := CheckPassword(hash, password); err != nil {
				t.Fatalf("matching password rejected: %v", err)
			}
			if _, err := tc.hash(""); err == nil {
				t.Fatal("expected error on empty password")
			}
		})
	}
}

func TestApiKeySecretGeneration(t *testing.T) {
	a, prefixA, err := GenerateApiKeySecret()
	if err != nil {
		t.Fatal(err)
	}
	b, prefixB, err := GenerateApiKeySecret()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two generated secrets collided")
	}
	if len(prefixA) != 8 || len(prefixB) != 8 {
		t.Errorf("prefix length: a=%d b=%d", len(prefixA), len(prefixB))
	}
	hash, err := HashApiKeySecret(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckApiKeySecret(hash, a); err != nil {
		t.Errorf("matching secret rejected: %v", err)
	}
	if err := CheckApiKeySecret(hash, b); err == nil {
		t.Error("non-matching secret accepted")
	}
}
