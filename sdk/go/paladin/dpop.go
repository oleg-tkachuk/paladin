package paladin

import (
	"context"
	"crypto"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes/dpop"
)

// WithDPoP proves possession of key on every call that presents a
// capability: each request carries a fresh DPoP proof, signed by key over the
// request's method and URL and the capability token. A capability issued
// with confirmation_jkt = DPoPThumbprint(key.Public()) is refused without
// one, so a copy of the token is useless to anyone without the key.
//
// key is any crypto.Signer — an in-memory key or one in a KMS — whose public
// key is Ed25519 or ECDSA P-256. A retried call signs a new proof per
// attempt, since the server refuses a proof it has seen. Proofs are signed
// for POST, so do not combine WithDPoP with connect.WithHTTPGet.
//
// A server that asks for nonces (RFC 9449 §8) sends one with each response;
// the client keeps the latest and signs it into the next proof. A unary call
// refused for want of a fresh one is sent once more with it. A stream signs
// the latest nonce the client holds and is not resent.
func WithDPoP(key crypto.Signer) Option {
	return func(cfg *config) { cfg.dpopKey = key }
}

// DPoPThumbprint is the RFC 7638 thumbprint of pub: the confirmation_jkt to
// issue or delegate a capability with, binding it to the holder of the
// matching private key. It is limes's, which the server checks it against.
func DPoPThumbprint(pub crypto.PublicKey) (string, error) {
	jkt, err := dpop.Thumbprint(pub)
	if err != nil {
		return "", fmt.Errorf("paladin: %w", err)
	}
	return jkt, nil
}

// dpopNonceChallenge is the error a DPoP challenge names when the proof
// lacked the nonce the server asks for (RFC 9449 §8).
const dpopNonceChallenge = "use_dpop_nonce"

// dpopProof signs one proof through limes, with the server's nonce when
// there is one. The server compares htu by path, so the base URL's scheme
// and host need not be the ones it sees behind a proxy.
func dpopProof(ctx context.Context, key crypto.Signer, method, htu, token, nonce string, now time.Time) (string, error) {
	proof, err := dpop.Sign(ctx, key, dpop.SignRequest{Method: method, URL: htu, Token: token, Nonce: nonce, IssuedAt: now})
	if err != nil {
		return "", fmt.Errorf("paladin: DPoP proof: %w", err)
	}
	return proof, nil
}

// dpopAuth signs a proof for each call that carries a capability. It sits
// inside the retry interceptor, so every attempt gets its own.
type dpopAuth struct {
	key     crypto.Signer
	baseURL string
	now     func() time.Time
	// nonce is the latest nonce the server sent, shared by every call of
	// the client.
	nonce atomic.Pointer[string]
}

func (a *dpopAuth) set(ctx context.Context, h *connect.Header, procedure string) error {
	token := h.Get(HeaderCapability)
	if token == "" {
		return nil
	}
	nonce := ""
	if n := a.nonce.Load(); n != nil {
		nonce = *n
	}
	proof, err := dpopProof(ctx, a.key, http.MethodPost, a.baseURL+procedure, token, nonce, a.now())
	if err != nil {
		return err
	}
	h.Set(HeaderDPoP, proof)
	return nil
}

func (a *dpopAuth) interceptor() connect.ClientInterceptor {
	return interceptor(a.unary, a.stream)
}

// unary signs each attempt afresh: innermost, inside the retry interceptor,
// so a retried call never repeats a proof the server has seen. A call
// refused for want of a fresh nonce is sent once more, with the nonce the
// refusal carried.
func (a *dpopAuth) unary(next unaryFunc) unaryFunc {
	return func(ctx context.Context, spec connect.Spec, req, res any) error {
		err := a.attempt(ctx, spec, req, res, next)
		if info := callInfo(ctx); nonceChallenged(info, err) {
			clearResponse(info)
			err = a.attempt(ctx, spec, req, res, next)
		}
		return err
	}
}

// attempt signs a proof, runs the call, and keeps the nonce the server sent
// back, refused or not.
func (a *dpopAuth) attempt(ctx context.Context, spec connect.Spec, req, res any, next unaryFunc) error {
	info := callInfo(ctx)
	if err := a.set(ctx, info.RequestHeader(), spec.Procedure); err != nil {
		return connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	err := next(ctx, spec, req, res)
	if nonce := responseMeta(info, HeaderDPoPNonce); nonce != "" {
		a.nonce.Store(&nonce)
	}
	return err
}

// nonceChallenged reports whether err refused the proof for want of the
// server's nonce, and the response carries one to retry with.
func nonceChallenged(info *connect.CallInfo, err error) bool {
	if connect.CodeOf(err) != connect.CodeUnauthenticated || responseMeta(info, HeaderDPoPNonce) == "" {
		return false
	}
	for _, h := range []*connect.Header{info.ResponseHeader(), info.ResponseTrailer()} {
		for _, challenge := range h.Values(HeaderWWWAuthenticate) {
			if strings.Contains(challenge, dpopNonceChallenge) {
				return true
			}
		}
	}
	return false
}

func (a *dpopAuth) stream(next connect.ClientFunc) connect.ClientFunc {
	return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
		if err := a.set(ctx, callInfo(ctx).RequestHeader(), spec.Procedure); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
		}
		return next(ctx, spec)
	}
}
