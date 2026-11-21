package oauthclienth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	oauthpkg "github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// observe makes h log, at level and above, to the lines it returns.
func observe(h *Handler, level zapcore.Level) *observer.ObservedLogs {
	core, logs := observer.New(level)
	h.SetLogger(zap.New(core))
	return logs
}

// A delete is logged at Info naming the client, whether it was there, and
// who deleted it.
func TestDeleteIsLogged(t *testing.T) {
	h := newHandler(&fakeClients{}, &fakeAuthorizer{decision: cedar.DecisionAllow})
	logs := observe(h, zapcore.InfoLevel)
	if err := del(h, ctxAs(apiutil.RolePlatformAdmin), registered); err != nil {
		t.Fatal(err)
	}
	lines := logs.FilterMessage(msgClientDeleted).All()
	if len(lines) != 1 {
		t.Fatalf("want one %q line, got %v", msgClientDeleted, logs.All())
	}
	if m := lines[0].ContextMap(); m["client_id"] != registered || m["actor"] != "tester" || m["found"] != true {
		t.Errorf("line = %v", m)
	}
}

// Each refusal keeps its code and is logged once at Warn.
func TestDeleteRefusalsWarn(t *testing.T) {
	allow := &fakeAuthorizer{decision: cedar.DecisionAllow}
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		policy  *fakeAuthorizer
		clients *fakeClients
		id      string
		want    connect.Code
	}{
		{"anonymous", context.Background(), allow, &fakeClients{}, registered, connect.CodeUnauthenticated},
		{"tenant admin", ctxAs("tenant.admin"), allow, &fakeClients{}, registered, connect.CodePermissionDenied},
		{"forbidden by policy", ctxAs(apiutil.RolePlatformAdmin), &fakeAuthorizer{decision: cedar.DecisionDeny}, &fakeClients{}, registered, connect.CodePermissionDenied},
		{"seeded", ctxAs(apiutil.RolePlatformAdmin), allow, &fakeClients{}, seeded, connect.CodeFailedPrecondition},
		{"unknown", ctxAs(apiutil.RolePlatformAdmin), allow, &fakeClients{err: oauthpkg.ErrClientNotFound}, registered, connect.CodeNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(tc.clients, tc.policy)
			logs := observe(h, zapcore.WarnLevel)
			if err := del(h, tc.ctx, tc.id); connect.CodeOf(err) != tc.want {
				t.Fatalf("code = %v (%v), want %v", connect.CodeOf(err), err, tc.want)
			}
			lines := logs.All()
			if len(lines) != 1 || lines[0].Level != zapcore.WarnLevel || lines[0].ContextMap()["client_id"] != tc.id {
				t.Fatalf("want one warning naming %s, got %v", tc.id, lines)
			}
		})
	}
}

// A client store that fails is internal, logged once at Error: never a
// client the caller is told is not there.
func TestDeleteStoreFailureIsInternalAndLogged(t *testing.T) {
	boom := errors.New("pool closed")
	h := newHandler(&fakeClients{err: boom}, &fakeAuthorizer{decision: cedar.DecisionAllow})
	logs := observe(h, zapcore.WarnLevel)
	err := del(h, ctxAs(apiutil.RolePlatformAdmin), registered)
	if connect.CodeOf(err) != connect.CodeInternal || !errors.Is(err, boom) {
		t.Fatalf("err = %v (%v), want internal wrapping the store's", err, connect.CodeOf(err))
	}
	lines := logs.All()
	if len(lines) != 1 || lines[0].Level != zapcore.ErrorLevel || lines[0].ContextMap()["client_id"] != registered {
		t.Fatalf("want one error line naming the client, got %v", lines)
	}
}

// A revocation that fails leaves a deleted client's capabilities live: that
// is logged at Error, naming the client and who deleted it, whatever code
// the revocation is answered by.
func TestDeleteRevocationFailureIsLogged(t *testing.T) {
	for name, revokeErr := range map[string]error{
		"store down": fmt.Errorf("pgstore: %w: connection refused", limes.ErrStoreUnavailable),
		"other":      errors.New("revocation exploded"),
	} {
		t.Run(name, func(t *testing.T) {
			h := revokingHandler(&fakeClients{}, &fakeRevoker{err: revokeErr})
			logs := observe(h, zapcore.WarnLevel)
			if err := del(h, ctxAs(apiutil.RolePlatformAdmin), registered); err == nil || !errors.Is(err, revokeErr) {
				t.Fatalf("err = %v, want the revocation's", err)
			}
			lines := logs.All()
			if len(lines) != 1 || lines[0].Level != zapcore.ErrorLevel {
				t.Fatalf("want one error line, got %v", lines)
			}
			if m := lines[0].ContextMap(); m["client_id"] != registered || m["actor"] != "tester" {
				t.Errorf("line = %v", m)
			}
		})
	}
}

// A nil logger keeps the one the handler has.
func TestSetLoggerIgnoresNil(t *testing.T) {
	h := newHandler(&fakeClients{err: errors.New("pool closed")}, &fakeAuthorizer{decision: cedar.DecisionAllow})
	h.SetLogger(nil)
	if err := del(h, ctxAs(apiutil.RolePlatformAdmin), registered); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want internal", connect.CodeOf(err))
	}
}
