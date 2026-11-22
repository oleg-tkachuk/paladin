package auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestAnswerKeySetUnavailable(t *testing.T) {
	keysDown := fmt.Errorf("%w: refresh on miss: %w", ErrKeySetUnavailable, errors.New("dial tcp: connection refused"))
	cases := map[string]struct {
		err      error
		answered bool
	}{
		"key set unavailable": {keysDown, true},
		"token refused":       {fmt.Errorf("%w: exp", ErrBearerExpired), false},
		"unknown failure":     {errors.New("unsupported key type"), false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			rec := httptest.NewRecorder()

			got := AnswerKeySetUnavailable(rec, zap.New(core), c.err)

			if got != c.answered {
				t.Fatalf("answered = %v, want %v", got, c.answered)
			}
			if !c.answered {
				if logs.Len() != 0 || rec.Body.Len() != 0 || len(rec.Header()) != 0 {
					t.Fatalf("a refused token was written or logged: %d lines, body %q", logs.Len(), rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503", rec.Code)
			}
			if body := strings.TrimSpace(rec.Body.String()); body != bearerKeysUnavailable {
				t.Errorf("body = %q, want %q", body, bearerKeysUnavailable)
			}
			if h := rec.Header().Get("WWW-Authenticate"); h != "" {
				t.Errorf("WWW-Authenticate = %q, want none: the token was not refused", h)
			}
			lines := logs.FilterMessage(msgKeySetUnavailableLog).All()
			if len(lines) != 1 || lines[0].Level != zapcore.ErrorLevel || lines[0].ContextMap()["error"] != c.err.Error() {
				t.Fatalf("logged %v, want one Error line with the cause", lines)
			}
		})
	}
}
