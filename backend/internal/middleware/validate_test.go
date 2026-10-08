package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/google/uuid"

	validatepb "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
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
	_ *iamv1.GetForUserRequest,
) (*iamv1.UserSettings, error) {
	s.calls++
	return &iamv1.UserSettings{Name: "users/u-1/settings"}, nil
}

func newValidatingServer(t *testing.T) (paladiniamv1connect.UserSettingsServiceClient, *countingSettings, context.Context) {
	t.Helper()
	validate, err := ProtoValidate()
	if err != nil {
		t.Fatalf("build interceptor: %v", err)
	}
	svc := &countingSettings{}
	client := paladiniamv1connect.NewUserSettingsServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterUserSettingsServiceHandler(s, svc)
	}, validate))
	return client, svc, principalCtx(uuid.New())
}

// GetForUserRequest.name carries `min_len = 1`, so an empty name must not reach
// the handler.
func TestInvalidMessageIsRejectedBeforeTheHandler(t *testing.T) {
	client, svc, ctx := newValidatingServer(t)

	_, err := client.GetForUser(ctx,
		&iamv1.GetForUserRequest{Name: ""})
	if err == nil {
		t.Fatal("an empty name passed validation")
	}
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want invalid_argument", got)
	}
	// The message names the constraint, which is what turns a 400 into
	// something a caller can act on without reading our proto.
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error %q does not name the field", err)
	}
	// And the violation travels as data, for a client that acts on it.
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("not a connect error: %v", err)
	}
	if !hasViolation(t, cerr, "name") {
		t.Errorf("no buf.validate.Violations detail naming the field: %v", cerr.Details())
	}
	if svc.calls != 0 {
		t.Errorf("the handler ran %d times for an invalid message — the "+
			"interceptor is not standing in front of it", svc.calls)
	}
}

// The other direction, and the one the surviving mutation needed: an
// interceptor that rejects everything passes the test above.
func TestValidMessageReachesTheHandler(t *testing.T) {
	client, svc, ctx := newValidatingServer(t)

	res, err := client.GetForUser(ctx,
		&iamv1.GetForUserRequest{Name: "users/u-1"})
	if err != nil {
		t.Fatalf("a valid message was rejected: %v", err)
	}
	if svc.calls != 1 {
		t.Errorf("the handler ran %d times, want 1", svc.calls)
	}
	if res.Name == "" {
		t.Error("the handler's response did not come back")
	}
}

// hasViolation reports whether err carries a buf.validate.Violations detail
// with a violation on field.
func hasViolation(t *testing.T, err *connect.Error, field string) bool {
	t.Helper()
	for _, d := range err.Details() {
		msg, derr := connectproto.UnmarshalErrorDetail(d)
		if derr != nil {
			t.Fatalf("detail %s: %v", d.Type, derr)
		}
		v, ok := msg.(*validatepb.Violations)
		if !ok {
			continue
		}
		for _, one := range v.GetViolations() {
			for _, el := range one.GetField().GetElements() {
				if el.GetFieldName() == field {
					return true
				}
			}
		}
	}
	return false
}
