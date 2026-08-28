package cedar

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// A stored policy that will not compile must be reported as a state of the
// data, not as a server fault.
//
// It used to surface as `internal: cedar: compile policy: parser error` on
// every read, delete and authorization touching the entity — indistinguishable
// from an outage, and naming neither the cause nor the cure. An operator whose
// collection had one could see nothing to act on.
//
// The write path refuses such text now, so the product no longer creates these
// rows; a restored backup or a stricter future grammar still can.
type brokenPolicyStore struct{ text string }

func (s brokenPolicyStore) Fetch(context.Context, uuid.UUID, string) (string, []byte, string, error) {
	return s.text, []byte("hash"), "slug", nil
}

func (brokenPolicyStore) Watch(ctx context.Context) (<-chan ChangeEvent, error) {
	ch := make(chan ChangeEvent)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func TestUnparseableStoredPolicyIsTypedNotInternal(t *testing.T) {
	e := NewEngine(brokenPolicyStore{text: "permit(principal);"}, 0)

	_, _, err := e.compiledFor(context.Background(), uuid.New(), "docs")
	if err == nil {
		t.Fatal("an uncompilable stored policy was accepted")
	}
	if !errors.Is(err, ErrPolicyUnparseable) {
		t.Fatalf("error %v does not carry ErrPolicyUnparseable — the API layer maps on that sentinel, so it would still answer Internal", err)
	}
	// The parser's own message is the half that tells the operator what to fix.
	if !errors.Is(err, ErrPolicyUnparseable) || err.Error() == ErrPolicyUnparseable.Error() {
		t.Error("the parser's diagnostic was dropped; the message has to say what did not parse")
	}
}

func TestCompilablePolicyStillLoads(t *testing.T) {
	e := NewEngine(brokenPolicyStore{text: "permit(principal, action, resource);"}, 0)

	set, _, err := e.compiledFor(context.Background(), uuid.New(), "docs")
	if err != nil {
		t.Fatalf("a valid stored policy was rejected: %v", err)
	}
	if set == nil {
		t.Fatal("no policy set returned")
	}
}
