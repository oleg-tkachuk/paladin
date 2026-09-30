package auditstream

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
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
		token, ok := bearerToken(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		p, err := verifier.Verify(r.Context(), token)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		if p.TenantID == uuid.Nil {
			// Tenant-less super-admin principals have no single tenant to
			// scope the stream to; the console they use is tenant-scoped too.
			http.Error(w, "token carries no tenant", http.StatusForbidden)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		events, cancel := h.Subscribe(p.TenantID.String())
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
				if !ok {
					return
				}
				if _, err := w.Write([]byte("event: audit\ndata: ")); err != nil {
					return
				}
				// Encode writes the trailing \n; one more terminates the frame.
				if err := enc.Encode(e); err != nil {
					h.log.Warn("audit stream: encode failed", zap.Error(err))
					return
				}
				if _, err := w.Write([]byte("\n")); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	v := r.Header.Get("Authorization")
	if len(v) > len(prefix) && strings.EqualFold(v[:len(prefix)], prefix) {
		return v[len(prefix):], true
	}
	return "", false
}
