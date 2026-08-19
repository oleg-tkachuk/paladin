package eventingest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// WebhookDriver is the receiver-side HTTP transport. It runs an
// http.Server with three endpoints:
//
//	POST /webhook/seaweedfs    — SeaweedFS filer-event JSON
//	POST /webhook/cloudevents  — raw CloudEvents 1.0 envelope
//	GET  /healthz, /readyz     — kube-proxy probes
//
// MinIO is intentionally absent for the first version; once a MinIO
// source adapter lands its handler attaches at /webhook/minio.
//
// Auth: HMAC-SHA256 on the raw body, header configured by the
// operator (default X-Paladin-Signature). Empty SharedSecret disables
// the check — fine for laptop-dev, dangerous in prod. The handler
// constant-time-compares the hex digest to defeat timing oracles.
//
// Acknowledgement model: webhook is at-most-once from the publisher's
// side (SeaweedFS retries on non-2xx, but at-least-once is the
// publisher's responsibility, not the protocol's). The driver
// returns 200 once the worker pipeline returns nil, and 5xx on
// pipeline error so the publisher retries.
type WebhookDriver struct {
	Addr           string
	Sources        map[string]Source // path → source adapter
	SharedSecret   string
	SignatureHdr   string
	MaxBodyBytes   int64
	ReadHdrTimeout time.Duration
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	Logger         *zap.Logger

	// ready flips to true once the http server is listening; /readyz
	// reflects it. Useful for probe-driven rollouts.
	ready atomic.Bool
}

func (d *WebhookDriver) Name() string { return "webhook" }

func (d *WebhookDriver) Run(ctx context.Context, deliver func(context.Context, CloudEvent) error) error {
	if len(d.Sources) == 0 {
		return errors.New("webhook driver: no sources configured")
	}
	mux := http.NewServeMux()
	for path, src := range d.Sources {
		mux.HandleFunc(path, d.handler(src, deliver))
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !d.ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := d.Addr
	if addr == "" {
		addr = ":8100"
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: defaultDur(d.ReadHdrTimeout, 5*time.Second),
		ReadTimeout:       defaultDur(d.ReadTimeout, 30*time.Second),
		WriteTimeout:      defaultDur(d.WriteTimeout, 30*time.Second),
		IdleTimeout:       defaultDur(d.IdleTimeout, 90*time.Second),
	}

	listenErr := make(chan error, 1)
	go func() {
		d.log().Info("webhook driver listening", zap.String("addr", addr))
		d.ready.Store(true)
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		listenErr <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-listenErr:
		return err
	}
}

// handler builds the per-source HTTP handler. Lifted out of Run so
// each route closes over its own Source pointer (range-iteration
// gotcha).
func (d *WebhookDriver) handler(src Source, deliver func(context.Context, CloudEvent) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := readCappedBody(r, d.MaxBodyBytes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}

		if d.SharedSecret != "" {
			sig := r.Header.Get(headerOrDefault(d.SignatureHdr, "X-Paladin-Signature"))
			if !verifyHMAC(body, d.SharedSecret, sig) {
				d.log().Warn("webhook signature mismatch",
					zap.String("source", src.Name()),
					zap.String("path", r.URL.Path),
				)
				http.Error(w, "signature mismatch", http.StatusUnauthorized)
				return
			}
		}

		ev, err := src.Parse(body, r.Header.Get("Content-Type"))
		if err != nil {
			if errors.Is(err, ErrIgnoredEvent) {
				// Adapter recognised the shape but chose to skip
				// (uninteresting event type, non-Paladin path). 200 so
				// the publisher doesn't retry — no work for us.
				w.WriteHeader(http.StatusOK)
				return
			}
			d.log().Warn("source parse failed",
				zap.String("source", src.Name()),
				zap.Int("body_bytes", len(body)),
				zap.Error(err),
			)
			http.Error(w, "unrecognised payload", http.StatusBadRequest)
			return
		}

		if err := deliver(r.Context(), ev); err != nil {
			// 5xx tells the publisher to retry. dedup row already
			// blocks the duplicate; handler-fail surfaces as a
			// genuine retry op.
			http.Error(w, "ingest failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}

func readCappedBody(r *http.Request, max int64) ([]byte, error) {
	if max <= 0 {
		max = 1 << 20 // 1 MiB default
	}
	limited := http.MaxBytesReader(nil, r.Body, max)
	defer r.Body.Close()
	return io.ReadAll(limited)
}

// verifyHMAC compares hex-encoded HMAC-SHA256 over body with the
// supplied header value. Constant-time comparison defeats timing
// oracles. Empty header → reject.
func verifyHMAC(body []byte, secret, header string) bool {
	if header == "" {
		return false
	}
	want, err := hex.DecodeString(stripPrefix(header))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

// stripPrefix tolerates "sha256=<hex>" style headers as well as the
// bare hex form. Standardising on bare hex internally; both inputs are
// accepted to ease integration with publishers that follow the
// "sha256=…" convention from GitHub webhooks.
func stripPrefix(h string) string {
	const p = "sha256="
	if len(h) > len(p) && h[:len(p)] == p {
		return h[len(p):]
	}
	return h
}

func headerOrDefault(name, def string) string {
	if name == "" {
		return def
	}
	return name
}

func defaultDur(v, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return v
}

func (d *WebhookDriver) log() *zap.Logger {
	if d.Logger == nil {
		return zap.NewNop()
	}
	return d.Logger
}
