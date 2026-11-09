package ratecount

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
)

// Every limiter keyed by what a caller sends counts apart from the others,
// and from the tenants' and tokens'.
func TestCallerKeysNeverMeet(t *testing.T) {
	const value = "198.51.100.7"
	keys := map[string]string{
		"login ip":       LoginIPKey(value),
		"login subject":  LoginSubjectKey(value, value),
		"oauth token":    OAuthTokenKey(value),
		"oauth exchange": OAuthExchangeKey(value),
	}
	seen := map[string]string{}
	for name, key := range keys {
		if other, ok := seen[key]; ok {
			t.Errorf("%s and %s share the key %q", name, other, key)
		}
		seen[key] = name
	}
}

// A key built from what a caller chose holds none of it in clear and is as
// long however long the input was, so a login name never reaches the store
// and an attacker cannot grow its keys.
func TestCallerKeysAreDigests(t *testing.T) {
	const subject = "alice@example.com"
	long := strings.Repeat("x", 10_000)
	for name, key := range map[string]string{
		"login ip":       LoginIPKey(subject),
		"login subject":  LoginSubjectKey(subject, "198.51.100.7"),
		"oauth token":    OAuthTokenKey(subject),
		"oauth exchange": OAuthExchangeKey(subject),
	} {
		if strings.Contains(key, subject) {
			t.Errorf("%s key %q holds the caller's value", name, key)
		}
	}
	if len(OAuthTokenKey(long)) != len(OAuthTokenKey("c")) {
		t.Error("a longer client_id made a longer key")
	}
}

// One subject from two addresses is two keys, and neither part can be moved
// into the other to land on someone else's key.
func TestLoginSubjectKeySeparatesItsParts(t *testing.T) {
	if LoginSubjectKey("alice", "198.51.100.7") == LoginSubjectKey("alice", "198.51.100.8") {
		t.Error("one subject from two addresses shares a key")
	}
	if LoginSubjectKey("a", "b|c") == LoginSubjectKey("a|b", "c") {
		t.Error("moving a separator between the parts lands on the same key")
	}
}

func TestKeyLimiterAdmitsUpToCapacity(t *testing.T) {
	const capacity = 2
	c := &clock{t: bucketAt.Add(20 * time.Second)}
	l := NewKeyLimiter(newLocal(c.now))
	ctx := context.Background()

	if ok, retry, err := l.Allow(ctx, testKey, 0); !ok || retry != 0 || err != nil {
		t.Fatalf("an uncapped key: admitted=%v retry=%v %v", ok, retry, err)
	}
	for i := 0; i < capacity; i++ {
		if ok, retry, err := l.Allow(ctx, testKey, capacity); !ok || retry != 0 || err != nil {
			t.Fatalf("call %d: admitted=%v retry=%v %v", i, ok, retry, err)
		}
	}
	ok, retry, err := l.Allow(ctx, testKey, capacity)
	if err != nil || ok {
		t.Fatalf("over capacity: admitted=%v %v", ok, err)
	}
	if want := ratelimit.RetryAfter(capacity, 0, 20*time.Second, capacity); retry != want {
		t.Fatalf("retry after %v, want %v", retry, want)
	}
	if ok, _, _ := l.Allow(ctx, "other", capacity); !ok {
		t.Fatal("another key was refused for this one's count")
	}
}

func TestKeyLimiterReturnsTheStoresError(t *testing.T) {
	boom := errors.New("boom")
	ok, retry, err := NewKeyLimiter(failing{boom}).Allow(context.Background(), testKey, 1)
	if ok || retry != 0 || !errors.Is(err, boom) {
		t.Fatalf("admitted=%v retry=%v err=%v, want the store's error, a refusal and no wait", ok, retry, err)
	}
}
