package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/httppaths"
	pbiam "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// The role's paths. Every one sits under PathPrefix, which the console's
// ingress routes here.
const (
	PathPrefix       = "/session"
	PathLogin        = PathPrefix + "/login"
	PathLogout       = PathPrefix + "/logout"
	PathRefresh      = PathPrefix + "/refresh"
	PathSwitchTenant = PathPrefix + "/switch-tenant"
	// PathRPC prefixes a forwarded Connect call: PathRPC + "/<package>.<Service>/<Method>".
	PathRPC = PathPrefix + "/rpc"
	// PathAuditStream forwards the admin plane's audit stream.
	PathAuditStream = PathPrefix + "/audit/stream"
)

// The headers the role reads or writes.
const (
	headerOrigin        = "Origin"
	headerForwardedHost = "X-Forwarded-Host"
	headerAuthorization = "Authorization"
	headerCookie        = "Cookie"
	headerForwardedFor  = "X-Forwarded-For"
	headerUserAgent     = "User-Agent"
	headerContentType   = "Content-Type"
	bearerPrefix        = "Bearer "
	contentTypeJSON     = "application/json"
)

// ConsoleUserAgentProduct names the console to the planes, which record the
// caller's User-Agent in the audit log.
const ConsoleUserAgentProduct = "paladin-console"

// maxSessionBody bounds a login or tenant-switch body: a subject, a
// password, a tenant id.
const maxSessionBody = 64 << 10

// RefreshAges reads when a refresh token was issued, from claims whose
// signature it has checked by the iam role's JWKS. An error says only that it
// could not: a JWKS not fetched yet, as well as a token that does not verify.
type RefreshAges interface {
	IssuedAt(ctx context.Context, token string) (time.Time, error)
}

// Options builds a Handler.
type Options struct {
	// IAM mints and revokes the session's tokens.
	IAM paladiniamv1connect.AuthServiceClient
	// Planes are the base URLs the role forwards each plane's calls to.
	Planes map[Plane]*url.URL
	// Transport dials the planes for forwarded calls.
	Transport http.RoundTripper
	// Refresh reads a refresh token's verified age (audience paladin-iam).
	Refresh RefreshAges
	// Origins are the console's origins, when it is served from more than the
	// host a call arrives on. A state-changing call must carry one of them, or
	// an Origin naming the host the call was made to.
	Origins []string
	// RotateAfter is how old a refresh token gets before it is rotated.
	RotateAfter time.Duration
	// Version is the build the role names itself by upstream.
	Version string
	Log     *zap.Logger
	// Now is injectable for tests.
	Now func() time.Time
}

// Handler serves the session endpoints and forwards the console's calls.
type Handler struct {
	iam         paladiniamv1connect.AuthServiceClient
	planes      map[Plane]*url.URL
	refresh     RefreshAges
	origins     map[string]bool
	rotateAfter time.Duration
	userAgent   string
	log         *zap.Logger
	now         func() time.Time
	cache       *accessCache
	proxy       *httputil.ReverseProxy
	mux         *http.ServeMux
}

// New builds the Handler. It refuses options it could not serve with: no
// console origin would refuse every call that changes state.
func New(o Options) (*Handler, error) {
	if o.RotateAfter <= 0 {
		return nil, errors.New("session: rotate_after must be positive")
	}
	for _, p := range []Plane{PlaneData, PlaneAdmin, PlaneIAM} {
		if o.Planes[p] == nil {
			return nil, fmt.Errorf("session: no URL for the %s plane", p)
		}
	}
	if o.IAM == nil || o.Refresh == nil {
		return nil, errors.New("session: an IAM client and a refresh-token verifier are required")
	}
	if o.Log == nil {
		o.Log = zap.NewNop()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	h := &Handler{
		iam:         o.IAM,
		planes:      o.Planes,
		refresh:     o.Refresh,
		origins:     make(map[string]bool, len(o.Origins)),
		rotateAfter: o.RotateAfter,
		userAgent:   ConsoleUserAgentProduct + "/" + o.Version,
		log:         o.Log,
		now:         o.Now,
		cache:       newAccessCache(o.Now),
	}
	for _, origin := range o.Origins {
		h.origins[origin] = true
	}
	h.proxy = &httputil.ReverseProxy{
		Rewrite:        h.rewrite,
		ModifyResponse: h.forwarded,
		Transport:      o.Transport,
		// Streams (the audit feed, a download) reach the browser as they come.
		FlushInterval: -1,
		ErrorHandler:  h.proxyError,
	}
	h.mux = http.NewServeMux()
	h.mux.HandleFunc("POST "+PathLogin, h.login)
	h.mux.HandleFunc("POST "+PathLogout, h.logout)
	h.mux.HandleFunc("POST "+PathRefresh, h.rotate)
	h.mux.HandleFunc("POST "+PathSwitchTenant, h.switchTenant)
	h.mux.HandleFunc(PathRPC+"/", h.forwardRPC)
	h.mux.HandleFunc("GET "+PathAuditStream, h.forwardAuditStream)
	return h, nil
}

// ServeHTTP refuses a state-changing call from anywhere but the console,
// then routes.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if changesState(r.Method) && !h.originAllowed(r) {
		h.log.Warn("session: refused a call from a foreign origin",
			zap.String("method", r.Method), zap.String("path", r.URL.Path),
			zap.String("origin", r.Header.Get(headerOrigin)))
		writeError(w, connect.CodePermissionDenied, "origin not allowed")
		return
	}
	h.mux.ServeHTTP(w, r)
}

// originAllowed is the CSRF check for a call that changes state: its Origin
// is a configured console origin, or names the host the call was made to —
// the console's own, as the ingress forwards it. A browser sets Origin itself,
// so a page elsewhere cannot claim the console's.
func (h *Handler) originAllowed(r *http.Request) bool {
	origin := r.Header.Get(headerOrigin)
	if origin == "" {
		return false
	}
	if h.origins[origin] {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := r.Header.Get(headerForwardedHost)
	if host == "" {
		host = r.Host
	}
	return strings.EqualFold(u.Host, host)
}

func changesState(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// ─── Session endpoints ──────────────────────────────────────────────────────

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req pbiam.LoginRequest
	if !readProto(w, r, &req) {
		return
	}
	// The session's refresh token is the iam plane's; every other plane's
	// access token is exchanged from it.
	req.RequestedAudience = paladin.AudienceIAM
	ctx, info := connect.NewClientContext(r.Context())
	h.upstreamHeaders(info.RequestHeader(), r)
	res, err := h.iam.Login(ctx, &req)
	if err != nil {
		h.log.Info("session: login refused", zap.String("subject", req.GetSubject()), zap.Error(err))
		writeConnectError(w, err)
		return
	}
	setCookie(w, res.GetTokens().GetRefreshToken(), seconds(res.GetTokens().GetRefreshExpiresInSeconds()))
	h.log.Info("session: signed in", zap.String("subject", req.GetSubject()))
	res.Tokens = nil
	writeProto(w, res)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	refresh := refreshToken(r)
	clearCookie(w)
	if refresh == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Revoke wants the caller's own iam access token, as any iam call does.
	token, err := h.access(r.Context(), r, refresh, paladin.AudienceIAM)
	h.cache.forget(refresh)
	if err == nil {
		ctx, info := connect.NewClientContext(r.Context())
		h.upstreamHeaders(info.RequestHeader(), r)
		info.RequestHeader().Set(headerAuthorization, bearerPrefix+token)
		_, err = h.iam.Revoke(ctx, &pbiam.RevokeRequest{Token: refresh})
	}
	switch {
	case err == nil:
		h.log.Info("session: signed out")
	case connect.CodeOf(err) == connect.CodeUnauthenticated:
		// Already ended at IAM: nothing left to revoke.
		h.log.Info("session: signed out of a session IAM had already ended")
	default:
		// The cookie is gone either way, but the refresh token lives on
		// until it expires: an operator should know.
		h.log.Error("session: revoking the refresh token at logout failed", zap.Error(err))
	}
	w.WriteHeader(http.StatusNoContent)
}

// rotate rotates the session's refresh token once it is RotateAfter old.
// A younger one is left alone: rotation is the one step whose answer cannot
// be lost, so it is kept off the quick path every navigation takes.
func (h *Handler) rotate(w http.ResponseWriter, r *http.Request) {
	refresh := refreshToken(r)
	if refresh == "" {
		writeError(w, connect.CodeUnauthenticated, "no session")
		return
	}
	issuedAt, err := h.refresh.IssuedAt(r.Context(), refresh)
	if err != nil {
		// Not a verdict on the session: the JWKS may not be fetched yet, as
		// when iam restarts. IAM judges the token on the session's next call,
		// and signs it out then if it must; rotation waits for a later try.
		h.log.Warn("session: cannot read the refresh token's age; not rotating", zap.Error(err))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !issuedAt.IsZero() && h.now().Sub(issuedAt) < h.rotateAfter {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx, info := connect.NewClientContext(r.Context())
	h.upstreamHeaders(info.RequestHeader(), r)
	res, err := h.iam.RefreshToken(ctx, &pbiam.RefreshTokenRequest{RefreshToken: refresh, RequestedAudience: paladin.AudienceIAM})
	switch {
	case err == nil:
		setCookie(w, res.GetTokens().GetRefreshToken(), seconds(res.GetTokens().GetRefreshExpiresInSeconds()))
		h.log.Debug("session: refresh token rotated", zap.Time("issued_at", issuedAt))
		w.WriteHeader(http.StatusNoContent)
	case connect.CodeOf(err) == connect.CodeAborted:
		// Another request rotated it first, and its answer carries the new
		// cookie. This one keeps working: exchange accepts a token
		// superseded moments ago.
		h.log.Debug("session: rotation lost to a concurrent one", zap.Time("issued_at", issuedAt))
		w.WriteHeader(http.StatusNoContent)
	case connect.CodeOf(err) == connect.CodeUnauthenticated:
		h.log.Info("session: refresh token refused at rotation; signing out", zap.Time("issued_at", issuedAt), zap.Error(err))
		clearCookie(w)
		writeConnectError(w, err)
	default:
		h.log.Error("session: rotating the refresh token failed", zap.Time("issued_at", issuedAt), zap.Error(err))
		writeConnectError(w, err)
	}
}

func (h *Handler) switchTenant(w http.ResponseWriter, r *http.Request) {
	refresh := refreshToken(r)
	if refresh == "" {
		writeError(w, connect.CodeUnauthenticated, "no session")
		return
	}
	var req pbiam.SwitchTenantRequest
	if !readProto(w, r, &req) {
		return
	}
	req.RequestedAudience = paladin.AudienceIAM
	token, ok := h.accessOrRefuse(w, r, refresh, paladin.AudienceIAM)
	if !ok {
		return
	}
	ctx, info := connect.NewClientContext(r.Context())
	h.upstreamHeaders(info.RequestHeader(), r)
	info.RequestHeader().Set(headerAuthorization, bearerPrefix+token)
	res, err := h.iam.SwitchTenant(ctx, &req)
	if err != nil {
		h.log.Info("session: tenant switch refused", zap.String("tenant", req.GetTargetTenantId()), zap.Error(err))
		writeConnectError(w, err)
		return
	}
	setCookie(w, res.GetTokens().GetRefreshToken(), seconds(res.GetTokens().GetRefreshExpiresInSeconds()))
	h.log.Info("session: switched tenant", zap.String("tenant", req.GetTargetTenantId()))
	res.Tokens = nil
	writeProto(w, res)
}

// ─── Forwarding ─────────────────────────────────────────────────────────────

// forward is what rewrite needs to send a call on: where, and as whom.
type forward struct {
	target *url.URL
	path   string
	token  string
}

type forwardKey struct{}

func (h *Handler) forwardRPC(w http.ResponseWriter, r *http.Request) {
	procedure := strings.TrimPrefix(r.URL.Path, PathRPC)
	plane, ok := planeOf(procedure)
	if !ok {
		h.log.Warn("session: no plane serves the procedure", zap.String("procedure", procedure))
		writeError(w, connect.CodeNotFound, "no such procedure")
		return
	}
	if tokenProcedures[procedure] {
		h.log.Warn("session: refused to forward a token procedure", zap.String("procedure", procedure))
		writeError(w, connect.CodePermissionDenied, "this procedure is the session role's own")
		return
	}
	h.forwardTo(w, r, plane, procedure)
}

// forwardAuditStream forwards the admin plane's audit stream, which lasts
// as long as the page keeps it open: the listener's write timeout, meant for
// one answer, would cut it off.
func (h *Handler) forwardAuditStream(w http.ResponseWriter, r *http.Request) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		h.log.Warn("session: cannot lift the write deadline for the audit stream", zap.Error(err))
	}
	h.forwardTo(w, r, PlaneAdmin, httppaths.AuditStream)
}

func (h *Handler) forwardTo(w http.ResponseWriter, r *http.Request, plane Plane, path string) {
	refresh := refreshToken(r)
	if refresh == "" {
		writeError(w, connect.CodeUnauthenticated, "no session")
		return
	}
	token, ok := h.accessOrRefuse(w, r, refresh, planeAudience[plane])
	if !ok {
		return
	}
	ctx := context.WithValue(r.Context(), forwardKey{}, forward{target: h.planes[plane], path: path, token: token})
	h.proxy.ServeHTTP(w, r.WithContext(ctx))
}

// rewrite sends the call to its plane as the session's user: the browser's
// cookie and any Authorization it sent are dropped, the access token put in
// their place.
func (h *Handler) rewrite(pr *httputil.ProxyRequest) {
	f := pr.In.Context().Value(forwardKey{}).(forward)
	pr.Out.URL.Scheme = f.target.Scheme
	pr.Out.URL.Host = f.target.Host
	pr.Out.URL.Path = strings.TrimSuffix(f.target.Path, "/") + f.path
	pr.Out.URL.RawPath = ""
	pr.Out.Host = f.target.Host
	pr.Out.Header.Del(headerCookie)
	// The planes serve no browser: the console's origin is this role's check.
	pr.Out.Header.Del(headerOrigin)
	pr.Out.Header.Set(headerAuthorization, bearerPrefix+f.token)
	h.upstreamHeaders(pr.Out.Header, pr.In)
}

// forwarded logs each call the role forwarded and how the plane answered.
func (h *Handler) forwarded(res *http.Response) error {
	f, _ := res.Request.Context().Value(forwardKey{}).(forward)
	h.log.Debug("session: forwarded", zap.String("path", f.path), zap.Int("status", res.StatusCode))
	return nil
}

func (h *Handler) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	f, _ := r.Context().Value(forwardKey{}).(forward)
	h.log.Error("session: forwarding failed", zap.String("path", f.path), zap.Error(err))
	writeError(w, connect.CodeUnavailable, "the plane did not answer")
}

// headerSetter is what upstreamHeaders writes to: the outbound HTTP header of
// a forwarded call, or the request header of a call to the iam plane.
type headerSetter interface{ Set(key, value string) }

// upstreamHeaders names the console and the browser's address to a plane:
// the planes record the caller's User-Agent and rate-limit and audit by the
// forwarded address.
func (h *Handler) upstreamHeaders(out headerSetter, in *http.Request) {
	out.Set(headerUserAgent, h.userAgent)
	if chain := in.Header.Get(headerForwardedFor); chain != "" {
		out.Set(headerForwardedFor, chain)
	} else if host, _, err := net.SplitHostPort(in.RemoteAddr); err == nil {
		out.Set(headerForwardedFor, host)
	}
}

// ─── Tokens ─────────────────────────────────────────────────────────────────

// access is an access token for audience, minted from the session's refresh
// token and kept until it nears expiry. Exchange consumes nothing, so two
// replicas minting the same one is harmless.
func (h *Handler) access(ctx context.Context, in *http.Request, refresh, audience string) (string, error) {
	if token, ok := h.cache.get(refresh, audience); ok {
		return token, nil
	}
	ctx, info := connect.NewClientContext(ctx)
	h.upstreamHeaders(info.RequestHeader(), in)
	res, err := h.iam.ExchangeAudience(ctx, &pbiam.ExchangeAudienceRequest{RefreshToken: refresh, TargetAudience: audience})
	if err != nil {
		return "", err
	}
	h.cache.put(refresh, audience, res.GetAccessToken(), seconds(int64(res.GetAccessExpiresInSeconds())))
	return res.GetAccessToken(), nil
}

// accessOrRefuse is access, answering the browser itself when there is no
// token to give: a session IAM no longer honours is signed out.
func (h *Handler) accessOrRefuse(w http.ResponseWriter, r *http.Request, refresh, audience string) (string, bool) {
	token, err := h.access(r.Context(), r, refresh, audience)
	if err == nil {
		return token, true
	}
	if connect.CodeOf(err) == connect.CodeUnauthenticated {
		h.log.Info("session: refresh token refused; signing out", zap.String("audience", audience), zap.Error(err))
		clearCookie(w)
	} else {
		h.log.Error("session: minting an access token failed", zap.String("audience", audience), zap.Error(err))
	}
	writeConnectError(w, err)
	return "", false
}

func seconds[T int32 | int64](n T) time.Duration { return time.Duration(n) * time.Second }

// ─── Wire ───────────────────────────────────────────────────────────────────

// readProto reads a JSON body into m, answering the browser itself when it
// cannot.
func readProto(w http.ResponseWriter, r *http.Request, m proto.Message) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSessionBody))
	if err == nil {
		err = protojson.Unmarshal(body, m)
	}
	if err != nil {
		writeError(w, connect.CodeInvalidArgument, "unreadable request")
		return false
	}
	return true
}

func writeProto(w http.ResponseWriter, m proto.Message) {
	body, err := protojson.Marshal(m)
	if err != nil {
		writeError(w, connect.CodeInternal, "unwritable response")
		return
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	_, _ = w.Write(body)
}

// httpStatus is the status a session endpoint answers a Connect code with,
// as the Connect protocol maps them.
var httpStatus = map[connect.Code]int{
	connect.CodeInvalidArgument:    http.StatusBadRequest,
	connect.CodeUnauthenticated:    http.StatusUnauthorized,
	connect.CodePermissionDenied:   http.StatusForbidden,
	connect.CodeNotFound:           http.StatusNotFound,
	connect.CodeAborted:            http.StatusConflict,
	connect.CodeAlreadyExists:      http.StatusConflict,
	connect.CodeFailedPrecondition: http.StatusPreconditionFailed,
	connect.CodeResourceExhausted:  http.StatusTooManyRequests,
	connect.CodeUnavailable:        http.StatusServiceUnavailable,
	connect.CodeDeadlineExceeded:   http.StatusGatewayTimeout,
	connect.CodeUnimplemented:      http.StatusNotImplemented,
}

// writeError answers in the Connect error shape the console already reads.
func writeError(w http.ResponseWriter, code connect.Code, message string) {
	status, ok := httpStatus[code]
	if !ok {
		status = http.StatusInternalServerError
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q}`, code.String(), message)
}

func writeConnectError(w http.ResponseWriter, err error) {
	var ce *connect.Error
	if errors.As(err, &ce) {
		writeError(w, ce.Code(), ce.Message())
		return
	}
	writeError(w, connect.CodeUnavailable, "the iam plane did not answer")
}
