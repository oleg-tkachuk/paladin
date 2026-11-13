package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// The bridge starts in the same rollout as the IAM role and can reach it
// first. Its agent verifier was dropped when the first JWKS fetch failed, and
// stayed dropped until the pod restarted; it is kept, and fetches later.
func TestBuildAgentVerifier_KeptWhenJWKSIsNotUpYet(t *testing.T) {
	iam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "starting", http.StatusServiceUnavailable)
	}))
	defer iam.Close()
	core, logs := observer.New(zapcore.DebugLevel)

	v := buildAgentVerifier(t.Context(), config.Auth{JWKSURL: iam.URL}, nil, zap.New(core))

	require.NotNil(t, v, "a verifier that retries, not none")
	assert.Equal(t, 1, logs.FilterMessage("mcp token verifier: jwks not fetched yet; retrying").Len())
}

func TestBuildAgentVerifier_NoneWithoutKeysOrJWKS(t *testing.T) {
	assert.Nil(t, buildAgentVerifier(t.Context(), config.Auth{}, nil, zap.NewNop()))
}
