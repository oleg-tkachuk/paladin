package auth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

// The pieces of the failure log line this test reads.
const (
	logRejected    = "rpc rejected"
	logCauseField  = "cause"
	parserText     = "jws."
	malformedText  = "malformed bearer token"
	bearerNotJWT   = "Bearer not-a-token"
	authorizationH = "Authorization"
)

// The caller is told only the reason; the JWT parser's own words, which it
// used to be told, are what the failure log records as the cause — the log
// line an operator reads lost nothing when the message lost them.
func TestBearerRefusalLogsTheParserErrorAsItsCause(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	core, logs := observer.New(zapcore.DebugLevel)
	client := paladindatav1connect.NewObjectServiceClient(unarytest.Client(func(s *connect.Server) {
		paladindatav1connect.RegisterObjectServiceHandler(s, caveatHandlers{tenant: "t"})
	}, middleware.LogOutcome(zap.New(core)), auth.InterceptorSkipAPITokens(&auth.JWTVerifier{Key: &key.PublicKey})))

	ctx := unarytest.WithHeader(context.Background(), authorizationH, bearerNotJWT)
	_, err = client.GetObject(ctx, &datav1.GetObjectRequest{Name: "x"})
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("GetObject = %v, want Unauthenticated", err)
	}
	lines := logs.FilterMessage(logRejected).All()
	if len(lines) != 1 {
		t.Fatalf("want one %q line, got %v", logRejected, logs.All())
	}
	cause, _ := lines[0].ContextMap()[logCauseField].(string)
	if !strings.Contains(cause, malformedText) || !strings.Contains(cause, parserText) {
		t.Errorf("cause %q does not carry the reason and the parser's text", cause)
	}
}
