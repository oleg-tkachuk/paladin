package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	iamv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1/paladiniamv1connect"
)

// The validation interceptor is what stands between a malformed request and
// every handler behind it, and it had no test file.
//
// Mutation testing found it: inverting `if err := v.Validate(msg); err != nil`
// lets every invalid message through and rejects every valid one, and
// `go test ./internal/middleware/` stayed green. Nothing in the package drove
// a request through this interceptor at all.
//
// Both directions are asserted here for the reason the JWT verifier needed the
// same treatment: an interceptor that rejects everything satisfies a test that
// only checks rejection, and one that accepts everything satisfies a test that
// only checks acceptance.

// countingSettings records whether the handler ran, which is the difference
// between "the interceptor rejected it" and "the handler rejected it".
type countingSettings struct {
	paladiniamv1connect.UnimplementedUserSettingsServiceHandler
	calls int
}

func (s *countingSettings) GetForUser(_ context.Context,
	_ *connect.Request[iamv1.GetForUserRequest],
) (*connect.Response[iamv1.UserSettings], error) {
	s.calls++
	return connect.NewResponse(&iamv1.UserSettings{Name: "users/u-1/settings"}), nil
}

func newValidatingServer(t *testing.T) (paladiniamv1connect.UserSettingsServiceClient, *countingSettings, func()) {
	t.Helper()
	validate, err := ProtoValidate()
	if err != nil {
		t.Fatalf("build interceptor: %v", err)
	}
	svc := &countingSettings{}
	mux := http.NewServeMux()
	path, handler := paladiniamv1connect.NewUserSettingsServiceHandler(svc,
		connect.WithInterceptors(principalInjector(uuid.New()), validate),
	)
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	return paladiniamv1connect.NewUserSettingsServiceClient(srv.Client(), srv.URL), svc, srv.Close
}

// GetForUserRequest.name carries `min_len = 1`, so an empty name must not reach
// the handler.
func TestInvalidMessageIsRejectedBeforeTheHandler(t *testing.T) {
	client, svc, cleanup := newValidatingServer(t)
	defer cleanup()

	_, err := client.GetForUser(context.Background(),
		connect.NewRequest(&iamv1.GetForUserRequest{Name: ""}))
	if err == nil {
		t.Fatal("an empty name passed validation")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want invalid_argument", got)
	}
	// The message names the constraint, which is what turns a 400 into
	// something a caller can act on without reading our proto.
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("error %q does not say what failed", err)
	}
	if svc.calls != 0 {
		t.Errorf("the handler ran %d times for an invalid message — the "+
			"interceptor is not standing in front of it", svc.calls)
	}
}

// The other direction, and the one the surviving mutation needed: an
// interceptor that rejects everything passes the test above.
func TestValidMessageReachesTheHandler(t *testing.T) {
	client, svc, cleanup := newValidatingServer(t)
	defer cleanup()

	res, err := client.GetForUser(context.Background(),
		connect.NewRequest(&iamv1.GetForUserRequest{Name: "users/u-1"}))
	if err != nil {
		t.Fatalf("a valid message was rejected: %v", err)
	}
	if svc.calls != 1 {
		t.Errorf("the handler ran %d times, want 1", svc.calls)
	}
	if res.Msg.Name == "" {
		t.Error("the handler's response did not come back")
	}
}
