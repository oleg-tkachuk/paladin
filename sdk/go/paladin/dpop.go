package paladin

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"connectrpc.com/connect"
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
func WithDPoP(key crypto.Signer) Option {
	return func(cfg *config) { cfg.dpopKey = key }
}

// DPoPThumbprint is the RFC 7638 thumbprint of pub: the confirmation_jkt to
// issue or delegate a capability with, binding it to the holder of the
// matching private key.
func DPoPThumbprint(pub crypto.PublicKey) (string, error) {
	j, err := dpopPublicJWK(pub)
	if err != nil {
		return "", err
	}
	var canonical string // RFC 7638: required members, lexicographic order
	if j.Kty == "OKP" {
		canonical = fmt.Sprintf(`{"crv":%q,"kty":"OKP","x":%q}`, j.Crv, j.X)
	} else {
		canonical = fmt.Sprintf(`{"crv":%q,"kty":"EC","x":%q,"y":%q}`, j.Crv, j.X, j.Y)
	}
	sum := sha256.Sum256([]byte(canonical))
	return b64url(sum[:]), nil
}

type dpopJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y,omitempty"`
}

func dpopPublicJWK(pub crypto.PublicKey) (dpopJWK, error) {
	switch k := pub.(type) {
	case ed25519.PublicKey:
		return dpopJWK{Kty: "OKP", Crv: "Ed25519", X: b64url(k)}, nil
	case *ecdsa.PublicKey:
		if k.Curve != elliptic.P256() {
			return dpopJWK{}, errors.New("paladin: DPoP EC keys must be P-256")
		}
		raw, err := k.Bytes() // 0x04 || X || Y
		if err != nil {
			return dpopJWK{}, fmt.Errorf("paladin: DPoP EC key: %w", err)
		}
		return dpopJWK{Kty: "EC", Crv: "P-256", X: b64url(raw[1:33]), Y: b64url(raw[33:])}, nil
	}
	return dpopJWK{}, fmt.Errorf("paladin: unsupported DPoP key type %T", pub)
}

// dpopProof signs one proof. The server compares htu by path, so the base
// URL's scheme and host need not be the ones it sees behind a proxy.
func dpopProof(key crypto.Signer, method, htu, token string, now time.Time) (string, error) {
	jwk, err := dpopPublicJWK(key.Public())
	if err != nil {
		return "", err
	}
	alg := "EdDSA"
	if jwk.Kty == "EC" {
		alg = "ES256"
	}
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", fmt.Errorf("paladin: DPoP jti: %w", err)
	}
	ath := sha256.Sum256([]byte(token))
	header, err := json.Marshal(struct {
		Typ string  `json:"typ"`
		Alg string  `json:"alg"`
		JWK dpopJWK `json:"jwk"`
	}{"dpop+jwt", alg, jwk})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(struct {
		JTI string `json:"jti"`
		HTM string `json:"htm"`
		HTU string `json:"htu"`
		IAT int64  `json:"iat"`
		ATH string `json:"ath"`
	}{b64url(jti), method, htu, now.Unix(), b64url(ath[:])})
	if err != nil {
		return "", err
	}
	input := b64url(header) + "." + b64url(claims)
	var sig []byte
	if jwk.Kty == "OKP" {
		sig, err = key.Sign(rand.Reader, []byte(input), crypto.Hash(0))
	} else {
		digest := sha256.Sum256([]byte(input))
		var der []byte
		if der, err = key.Sign(rand.Reader, digest[:], crypto.SHA256); err == nil {
			sig, err = ecdsaRaw(der)
		}
	}
	if err != nil {
		return "", fmt.Errorf("paladin: DPoP sign: %w", err)
	}
	return input + "." + b64url(sig), nil
}

// ecdsaRaw turns a DER ECDSA signature into JWS ES256's r || s.
func ecdsaRaw(der []byte) ([]byte, error) {
	var rs struct{ R, S *big.Int }
	if rest, err := asn1.Unmarshal(der, &rs); err != nil || len(rest) > 0 {
		return nil, errors.New("malformed ECDSA signature")
	}
	sig := make([]byte, 64)
	rs.R.FillBytes(sig[:32])
	rs.S.FillBytes(sig[32:])
	return sig, nil
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// dpopAuth signs a proof for each call that carries a capability. It sits
// inside the retry interceptor, so every attempt gets its own.
type dpopAuth struct {
	key     crypto.Signer
	baseURL string
	now     func() time.Time
}

func (a *dpopAuth) set(h http.Header, procedure string) error {
	token := h.Get(HeaderCapability)
	if token == "" {
		return nil
	}
	proof, err := dpopProof(a.key, http.MethodPost, a.baseURL+procedure, token, a.now())
	if err != nil {
		return err
	}
	h.Set(HeaderDPoP, proof)
	return nil
}

func (a *dpopAuth) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := a.set(req.Header(), req.Spec().Procedure); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return next(ctx, req)
	}
}

func (a *dpopAuth) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		// A stream has nowhere to return the error before it is used; a
		// missing proof surfaces as the server's PermissionDenied.
		_ = a.set(conn.RequestHeader(), spec.Procedure)
		return conn
	}
}

func (a *dpopAuth) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
