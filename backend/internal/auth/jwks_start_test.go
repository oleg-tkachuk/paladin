package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// The waits the tests run with: the policy's shape is under test, not its
// production durations.
const (
	testRetryDelay    = time.Millisecond
	testMaxRetryDelay = 4 * time.Millisecond
	testEventually    = 2 * time.Second
	testTick          = 5 * time.Millisecond
)

// identityProvider serves the set only after refusing its first `down`
// requests, as an IAM role does while it is still starting.
func identityProvider(t *testing.T, down int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, rsaTestBits)
	require.NoError(t, err)
	body := map[string]any{"keys": []map[string]any{rsaJWK(priv, "k1", "sig")}}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) <= down {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func startingVerifier(srv *httptest.Server, log *zap.Logger) *JWKSVerifier {
	v := NewJWKSVerifier(srv.URL)
	v.HTTPClient = srv.Client()
	v.RetryDelay = testRetryDelay
	v.MaxRetryDelay = testMaxRetryDelay
	v.Logger = log
	return v
}

// A role that starts before the identity provider used to give up on the
// background loop: it never refreshed the set again, and said nothing when
// the keys did become available.
func TestJWKSStart_KeepsTryingAfterAFailedFirstFetch(t *testing.T) {
	srv, _ := identityProvider(t, 3)
	core, logs := observer.New(zapcore.DebugLevel)
	v := startingVerifier(srv, zap.New(core))

	require.Error(t, v.Start(t.Context()), "the first fetch failing is reported")

	require.Eventually(t, v.loaded, testEventually, testTick,
		"the set arrives without a token asking for it")

	fetched := logs.FilterMessage("jwks fetched; verifying by it").AllUntimed()
	require.Len(t, fetched, 1)
	assert.Equal(t, zapcore.InfoLevel, fetched[0].Level)

	retries := logs.FilterMessage("jwks still unreachable; retrying").AllUntimed()
	require.Len(t, retries, 2, "the first failure is the caller's to log; each retry after it is logged")
	var waits []time.Duration
	for _, e := range retries {
		waits = append(waits, e.ContextMap()["retry_in"].(time.Duration))
	}
	assert.Equal(t, []time.Duration{2 * testRetryDelay, testMaxRetryDelay}, waits)
}

func TestJWKSStart_RetriesStopWithTheContext(t *testing.T) {
	srv, hits := identityProvider(t, 1<<30)
	v := startingVerifier(srv, nil)
	ctx, cancel := context.WithCancel(t.Context())

	require.Error(t, v.Start(ctx))
	require.Eventually(t, func() bool { return hits.Load() > 2 }, testEventually, testTick)
	cancel()

	time.Sleep(10 * testMaxRetryDelay)
	settled := hits.Load()
	time.Sleep(10 * testMaxRetryDelay)
	assert.Equal(t, settled, hits.Load(), "no fetch after the context ends")
}

func TestJWKSStart_FirstFetchSucceeds(t *testing.T) {
	srv, hits := identityProvider(t, 0)
	core, logs := observer.New(zapcore.DebugLevel)
	v := startingVerifier(srv, zap.New(core))

	require.NoError(t, v.Start(t.Context()))
	assert.True(t, v.loaded())
	assert.Equal(t, int32(1), hits.Load())
	assert.Zero(t, logs.Len(), "nothing to say when the first fetch works")
}
