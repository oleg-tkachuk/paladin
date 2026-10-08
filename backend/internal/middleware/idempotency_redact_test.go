package middleware

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// The idempotency cache stored each memoized response whole for 24 hours,
// which for a minted API token, capability token or generated password meant
// a plaintext credential in idempotency_keys. The cache now keeps the response
// with every debug_redact field cleared, and refuses to replay a type that
// carries one.

const generatedPassword = "generated-Pa55word-that-must-not-be-cached"

type stubUsers struct {
	paladiniamv1connect.UnimplementedUserServiceHandler
	mu    sync.Mutex
	calls int
}

func (s *stubUsers) ResetPassword(context.Context, *iamv1.ResetPasswordRequest) (*iamv1.ResetPasswordResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return &iamv1.ResetPasswordResponse{GeneratedPassword: generatedPassword}, nil
}

func newUserServer(t *testing.T, store IdempotencyStore) (paladiniamv1connect.UserServiceClient, *stubUsers) {
	t.Helper()
	svc := &stubUsers{}
	return paladiniamv1connect.NewUserServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterUserServiceHandler(s, svc)
	}, NewIdempotencyInterceptor(store, IdempotencyConfig{TTL: time.Minute}))), svc
}

// resetTenant is the tenant every ResetPassword below is called in, so that
// a repeated key finds the row the first call memoised.
var resetTenant = uuid.New()

func resetWithKey(client paladiniamv1connect.UserServiceClient, key string) (*iamv1.ResetPasswordResponse, error) {
	req := &iamv1.ResetPasswordRequest{Name: "users/alice"}
	return client.ResetPassword(withIdempotencyKey(principalCtx(resetTenant), key), req)
}

func TestCredentialResponsesAreStoredRedacted(t *testing.T) {
	store := newMemStore()
	client, _ := newUserServer(t, store)

	first, err := resetWithKey(client, uuid.NewString())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if first.GetGeneratedPassword() != generatedPassword {
		t.Fatal("the first caller did not receive the credential")
	}
	if len(store.data) != 1 {
		t.Fatalf("cached %d responses, want 1", len(store.data))
	}
	for _, e := range store.data {
		if strings.Contains(string(e.Response), generatedPassword) {
			t.Fatal("the cached response holds the generated password")
		}
	}
}

func TestCredentialResponsesAreNotReplayed(t *testing.T) {
	client, svc := newUserServer(t, newMemStore())
	key := uuid.NewString()
	if _, err := resetWithKey(client, key); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err := resetWithKey(client, key)
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("repeat: code = %v, want AlreadyExists", connect.CodeOf(err))
	}
	if svc.calls != 1 {
		t.Errorf("handler ran %d times; a repeated key must not mint a second credential", svc.calls)
	}
}

func TestCarriesCredentials(t *testing.T) {
	for _, tc := range []struct {
		msg  interface{ ProtoReflect() protoreflect.Message }
		want bool
	}{
		{&adminv1.CapabilityServiceIssueResponse{}, true},
		{&adminv1.APITokenServiceCreateResponse{}, true},
		{&iamv1.LoginResponse{}, true}, // through its TokenPair
		{&iamv1.ResetPasswordResponse{}, true},
		{&iamv1.UserSettings{}, false},
		{&adminv1.Bucket{}, false},
	} {
		md := tc.msg.ProtoReflect().Descriptor()
		if got := carriesCredentials(md); got != tc.want {
			t.Errorf("carriesCredentials(%s) = %v, want %v", md.FullName(), got, tc.want)
		}
	}
}
