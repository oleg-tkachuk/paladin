package auditstream

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// heartbeatEvery keeps intermediaries (ingress, BFF proxy) from idling the
// connection out between real events. SSE comments (": ...") are ignored by
// EventSource clients.
const heartbeatEvery = 25 * time.Second

// SSEHandler streams the caller's tenant's audit entries as Server-Sent
// Events. Auth is a bearer access token with the admin audience — the same
// token the audit console's RPCs use; the stream is scoped to the JWT's
// tenant claim, mirroring how ListAuditLog is tenant-scoped. This is a live
// VIEW only (no replay): on connect the client renders the paginated list
// via ListAuditLog and layers new entries from the stream on top, so a
// reconnect gap is filled by the next list fetch, not by the stream.
func (h *Hub) SSEHandler(verifier auth.TokenVerifier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if verifier == nil {
			http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
			return
		}
		p, ok := h.verifyBearer(w, r, verifier)
		if !ok {
			return
		}
		if p.TenantID == uuid.Nil() {
			// Tenant-less super-admin principals have no single tenant to
			// scope the stream to; the console they use is tenant-scoped too.
			h.log.Warn(msgNoTenant, zap.String(fieldSubject, p.Subject))
			http.Error(w, "token carries no tenant", http.StatusForbidden)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			h.log.Error(msgNoFlusher)
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		h.stream(w, flusher, r, p.TenantID.String())
	})
}

// msgNoFlusher is the line of a response writer that cannot stream: the
// server's wiring, never the caller's fault.
const msgNoFlusher = "audit stream: the response writer cannot flush"

// stream sends tenant's audit entries to w as Server-Sent Events, with a
// heartbeat between them, until the request ends or a write fails.
func (h *Hub) stream(w http.ResponseWriter, flusher http.Flusher, r *http.Request, tenant string) {
	events, cancel := h.Subscribe(tenant)
	defer cancel()

	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("Connection", "keep-alive")
	// Defensive: some reverse proxies buffer responses unless told not to.
	hdr.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// Initial comment both confirms liveness to the client and forces the
	// header flush through any buffering intermediary.
	_, _ = w.Write([]byte(": connected\n\n"))
	flusher.Flush()

	heartbeat := time.NewTicker(heartbeatEvery)
	defer heartbeat.Stop()
	enc := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case e, ok := <-events:
			if !ok || !h.writeEvent(w, enc, e) {
				return
			}
			flusher.Flush()
		}
	}
}

// writeEvent writes e as one audit event frame, reporting whether the
// stream may go on.
func (h *Hub) writeEvent(w http.ResponseWriter, enc *json.Encoder, e Entry) bool {
	if _, err := w.Write([]byte("event: audit\ndata: ")); err != nil {
		return false
	}
	// Encode writes the trailing \n; one more terminates the frame.
	if err := enc.Encode(e); err != nil {
		h.log.Warn("audit stream: encode failed", zap.Error(err))
		return false
	}
	_, err := w.Write([]byte("\n"))
	return err == nil
}

// The lines verifyBearer leaves for a stream it refuses.
const (
	msgNoBearer      = "audit stream refused: no bearer token"      // #nosec G101 -- a log message, not a credential
	msgBearerRefused = "audit stream refused: bearer token refused" // #nosec G101 -- a log message, not a credential
	msgNoTenant      = "audit stream refused: the token carries no tenant"
)

// fieldSubject is the log key of the verified subject a stream was refused.
const fieldSubject = "subject"

// The texts a refused stream is answered with.
const (
	textNoBearer      = "missing bearer token" // #nosec G101 -- a refusal's message, not a credential
	textBearerRefused = "invalid token"
)

// verifyBearer is the principal of the request's bearer token, answering
// the request itself when there is none to give: 401 for no token or one
// refused, and 503 for a key set that would not load, which says nothing of
// the token.
func (h *Hub) verifyBearer(w http.ResponseWriter, r *http.Request, verifier auth.TokenVerifier) (*auth.Principal, bool) {
	token, ok := bearerToken(r)
	if !ok {
		h.log.Warn(msgNoBearer)
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, textNoBearer, http.StatusUnauthorized)
		return nil, false
	}
	p, err := verifier.Verify(r.Context(), token)
	if err == nil {
		return p, true
	}
	if !auth.AnswerKeySetUnavailable(w, h.log, err) {
		h.log.Warn(msgBearerRefused, zap.Error(err))
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, textBearerRefused, http.StatusUnauthorized)
	}
	return nil, false
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	v := r.Header.Get("Authorization")
	if len(v) > len(prefix) && strings.EqualFold(v[:len(prefix)], prefix) {
		return v[len(prefix):], true
	}
	return "", false
}
