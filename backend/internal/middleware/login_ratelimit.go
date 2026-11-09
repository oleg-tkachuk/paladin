package middleware

import (
	"context"
	"time"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// KeyedLimiter holds each key to a per-window ceiling. ratecount.KeyLimiter
// is one, on the store middleware.rate_limit.backend names, so every replica
// counts against one ceiling (ADR-0046).
type KeyedLimiter interface {
	// Allow counts one call under key when it fits capacity, and otherwise
	// reports how long until one would. An error decides nothing.
	Allow(ctx context.Context, key string, capacity int) (allowed bool, retryAfter time.Duration, err error)
}

// Built-in caps per window, kept when a caller passes none.
const (
	defaultLoginPerSubjectMax = 10
	defaultLoginPerIPMax      = 60
)

// The procedures throttled when a caller names none.
const (
	procedureLogin        = paladiniamv1connect.AuthServiceLoginProcedure
	procedureRefreshToken = paladiniamv1connect.AuthServiceRefreshTokenProcedure
)

// msgTooManyAttempts is what a refused attempt is told.
const msgTooManyAttempts = "too many attempts; try again later"

// The layer a refusal came from, as its log line names it.
const (
	loginLayerIP      = "ip"
	loginLayerSubject = "subject"
)

// LoginRateLimiter is a Connect interceptor that throttles credential-
// bearing IAM RPCs (Login + RefreshToken) with two layered windows of
// ratecount.Window, counted in the shared store so the ceiling holds across
// replicas:
//
//  1. Per-IP cap — checked first; defends against enumeration where the
//     attacker varies `subject` to dodge the tighter cap.
//  2. Per-(subject, IP) cap — defends one account against credential
//     stuffing.
//
// A request must clear both. A request refused at the IP layer is not
// counted against the subject, so an IP flood cannot lock a user out; one
// refused at the subject layer has been counted against its IP, which only
// tightens the IP's cap on a caller already over a subject's. Successful
// logins are NOT exempted — bots would otherwise game the limit by
// occasionally guessing right.
//
// Keys are digests (ratecount.LoginIPKey, LoginSubjectKey): no login name
// reaches the store in clear, and keys expire with their window there, so a
// botnet rotating subjects cannot grow this process's memory.
type LoginRateLimiter struct {
	limiter       KeyedLimiter
	perSubjectMax int
	perIPMax      int
	procedures    map[string]struct{}
	log           *zap.Logger
}

// NewLoginRateLimiter constructs a limiter on limiter. perSubjectMax /
// perIPMax are the per-(subject,IP) and per-IP caps per window; either <= 0
// keeps the built-in default, so callers that don't tune them get the safe
// defaults, while a deployment that needs to relax them (e.g. an e2e stack
// logging in repeatedly as one account) can pass a high value. The per-IP key
// is the client address the listener resolved from its trusted proxies
// (clientip.Middleware); a forwarding header's leftmost entry is the caller's
// to choose and would let it pick its own bucket. procedures lists the RPC
// paths to throttle; empty defaults to Login + RefreshToken.
func NewLoginRateLimiter(limiter KeyedLimiter, log *zap.Logger, perSubjectMax, perIPMax int, procedures ...string) *LoginRateLimiter {
	if perSubjectMax <= 0 {
		perSubjectMax = defaultLoginPerSubjectMax
	}
	if perIPMax <= 0 {
		perIPMax = defaultLoginPerIPMax
	}
	if len(procedures) == 0 {
		procedures = []string{procedureLogin, procedureRefreshToken}
	}
	procSet := make(map[string]struct{}, len(procedures))
	for _, p := range procedures {
		procSet[p] = struct{}{}
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &LoginRateLimiter{
		limiter:       limiter,
		perSubjectMax: perSubjectMax,
		perIPMax:      perIPMax,
		procedures:    procSet,
		log:           log,
	}
}

// Interceptor is the limiter as a Connect server interceptor. It reads the
// subject from the request message, so it acts on unary calls; Login and
// RefreshToken are unary.
func (l *LoginRateLimiter) Interceptor() connect.ServerInterceptor {
	return unary.Interceptor(func(next unary.Func) unary.Func {
		return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
			if _, throttled := l.procedures[spec.Procedure]; !throttled {
				return next(ctx, spec, req)
			}
			subject, ip := l.coords(ctx, req)
			if ok, retryAfter := l.admit(ctx, spec.Procedure, subject, ip); !ok {
				unary.Info(ctx).ResponseHeader().Set(paladin.HeaderRetryAfter, retryAfterHeader(retryAfter.Seconds()))
				return nil, connect.NewError(connect.CodeResourceExhausted, msgTooManyAttempts)
			}
			return next(ctx, spec, req)
		}
	}, nil)
}

// coords extracts (subject, ip). Subject from the proto body; IP from the
// resolved client address. Both empty when absent — the limiter still
// throttles unidentified clients under the shared empty-address key.
func (l *LoginRateLimiter) coords(ctx context.Context, req proto.Message) (string, string) {
	type subjectGetter interface{ GetSubject() string }
	subject := ""
	if m, ok := req.(subjectGetter); ok {
		subject = m.GetSubject()
	}
	ip := ""
	if a, ok := clientip.FromContext(ctx); ok {
		ip = a.String()
	}
	return subject, ip
}

// admit checks the IP layer, then the subject layer, counting each call it
// admits, and reports how long a refused call should wait. A store error
// refuses the call: these are credential-bearing calls, and the shared
// store already falls back to this process's count rather than failing.
func (l *LoginRateLimiter) admit(ctx context.Context, procedure, subject, ip string) (bool, time.Duration) {
	for _, layer := range []struct {
		name     string
		key      string
		capacity int
	}{
		{loginLayerIP, ratecount.LoginIPKey(ip), l.perIPMax},
		{loginLayerSubject, ratecount.LoginSubjectKey(subject, ip), l.perSubjectMax},
	} {
		ok, retryAfter, err := l.limiter.Allow(ctx, layer.key, layer.capacity)
		if err != nil {
			l.log.Error("login rate limit could not be decided; refusing the attempt",
				zap.String("procedure", procedure), zap.String("layer", layer.name), zap.Error(err))
			return false, 0
		}
		if !ok {
			l.log.Warn("login attempt refused by its rate limit",
				zap.String("procedure", procedure), zap.String("layer", layer.name),
				zap.Duration("retry_after", retryAfter))
			return false, retryAfter
		}
	}
	return true, 0
}
