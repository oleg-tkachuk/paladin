package auth

import "testing"

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
