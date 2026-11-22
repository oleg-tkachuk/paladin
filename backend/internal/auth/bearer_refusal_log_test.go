package auth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
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

// The failure log's line for a key set that would not load.
const (
	logFailed      = "rpc failed"
	idpStatusText  = "503"
	unservedKeyKid = "k-rotated-in"
)

// A key set that would not load is the server's dependency failing: the
// failure log writes it at Error, with the fetch error as its cause.
func TestAKeySetThatWillNotLoadIsLoggedAsAServerFault(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer idp.Close()
	v := auth.NewJWKSVerifier(idp.URL)
	v.HTTPClient = idp.Client()

	core, logs := observer.New(zapcore.DebugLevel)
	client := paladindatav1connect.NewObjectServiceClient(unarytest.Client(func(s *connect.Server) {
		paladindatav1connect.RegisterObjectServiceHandler(s, caveatHandlers{tenant: "t"})
	}, middleware.LogOutcome(zap.New(core)), auth.InterceptorSkipAPITokens(v)))

	enc := base64.RawURLEncoding
	token := enc.EncodeToString([]byte(`{"alg":"RS256","kid":"`+unservedKeyKid+`"}`)) + "." +
		enc.EncodeToString([]byte(`{}`)) + "." + enc.EncodeToString([]byte("sig"))
	ctx := unarytest.WithHeader(context.Background(), authorizationH, "Bearer "+token)
	_, err := client.GetObject(ctx, &datav1.GetObjectRequest{Name: "x"})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("GetObject = %v, want Unavailable", err)
	}
	lines := logs.FilterMessage(logFailed).All()
	if len(lines) != 1 || lines[0].Level != zapcore.ErrorLevel {
		t.Fatalf("want one %q line at Error, got %v", logFailed, logs.All())
	}
	cause, _ := lines[0].ContextMap()[logCauseField].(string)
	if !strings.Contains(cause, auth.ErrKeySetUnavailable.Error()) || !strings.Contains(cause, idpStatusText) {
		t.Errorf("cause %q does not carry the failure and the identity provider's status", cause)
	}
}
