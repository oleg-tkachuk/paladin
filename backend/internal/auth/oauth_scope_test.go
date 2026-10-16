package auth

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
)

const testWriteProcedure = "/paladin.admin.v1.CollectionService/CreateCollection"

func TestRequireOAuthWriteScope(t *testing.T) {
	console := &Principal{Subject: "u"}
	readOnly := &Principal{Subject: "u", OAuthClientID: "desktop-agent", OAuthScopes: []string{OAuthScopeRead}}
	noScope := &Principal{Subject: "u", OAuthClientID: "desktop-agent"}
	writer := &Principal{Subject: "u", OAuthClientID: "desktop-agent", OAuthScopes: []string{OAuthScopeRead, OAuthScopeWrite}}

	for _, tc := range []struct {
		name      string
		principal *Principal
		level     connect.IdempotencyLevel
		refused   bool
	}{
		{"no principal, write", nil, connect.IdempotencyUnknown, false},
		{"console session, write", console, connect.IdempotencyUnknown, false},
		{"read grant, read", readOnly, connect.IdempotencyNoSideEffects, false},
		{"read grant, write", readOnly, connect.IdempotencyUnknown, true},
		{"read grant, idempotent write", readOnly, connect.IdempotencyIdempotent, true},
		{"no scope, read", noScope, connect.IdempotencyNoSideEffects, false},
		{"no scope, write", noScope, connect.IdempotencyUnknown, true},
		{"write grant, write", writer, connect.IdempotencyUnknown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.principal != nil {
				ctx = WithPrincipal(ctx, tc.principal)
			}
			called := false
			next := func(context.Context, connect.Spec, connect.ServerStream) error { called = true; return nil }
			spec := connect.Spec{Procedure: testWriteProcedure, IdempotencyLevel: tc.level}
			err := RequireOAuthWriteScope()(next)(ctx, spec, nil)
			if tc.refused {
				if connect.CodeOf(err) != connect.CodePermissionDenied || called {
					t.Fatalf("err = %v, called = %v; want PermissionDenied before the handler", err, called)
				}
				return
			}
			if err != nil || !called {
				t.Fatalf("err = %v, called = %v; want the handler to run", err, called)
			}
		})
	}
}
