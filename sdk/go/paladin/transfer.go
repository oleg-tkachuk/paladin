package paladin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// The default transport's bounds. There is no bound on a whole transfer: a
// large object streams for as long as it takes, and the caller's context
// ends it. Each stage that can hang on an unresponsive peer has one.
const (
	DefaultTransferDialTimeout           = 10 * time.Second
	DefaultTransferTLSHandshakeTimeout   = 10 * time.Second
	DefaultTransferResponseHeaderTimeout = 30 * time.Second
	DefaultTransferIdleConnTimeout       = 90 * time.Second
	// DefaultTransferMaxIdleConnsPerHost keeps connections to one storage
	// host open for reuse; http.DefaultTransport keeps two, which makes many
	// concurrent downloads from one host reconnect every time.
	DefaultTransferMaxIdleConnsPerHost = 32
)

// errorBodyLimit bounds how much of a refused transfer's body an error quotes.
const errorBodyLimit = 512

// Errors from NewTransfer.
var (
	ErrInvalidOrigin = errors.New("paladin: an origin must be an absolute http or https URL with no path")
)

// TransferError is a presigned request that storage refused, or answered with
// a redirect: a presigned URL never redirects legitimately, and following one
// would send its signed headers to another host.
type TransferError struct {
	Method string
	// Host is the host the request went to: the URL's, after any rewrite.
	// The URL itself is not kept, because its query is the signature.
	Host   string
	Status int
	// Body is the start of what storage answered, at most errorBodyLimit bytes.
	Body string
}

func (e *TransferError) Error() string {
	return fmt.Sprintf("paladin: storage refused %s %s: %d %s", e.Method, e.Host, e.Status, e.Body)
}

// Transfer sends the requests that move bytes to and from storage through
// presigned URLs: Upload's PUTs and Download's GETs. They go to the storage
// backend, not to Paladin, so the Paladin client's credentials, retries and
// interceptors are not on them.
//
// Declare one with WithTransfer and every Upload and Download through that
// client uses it. A Transfer is safe for concurrent use, and is meant to be
// shared: its connection pool is what makes many concurrent transfers to one
// storage host cheap.
type Transfer struct {
	client  *http.Client
	rewrite func(*url.URL) *url.URL
	observe observer
}

// TransferOption configures a Transfer.
type TransferOption func(*transferConfig) error

type transferConfig struct {
	client  *http.Client
	rewrite func(*url.URL) *url.URL
	hooks   Hooks
	logger  *slog.Logger
}

// WithTransferHTTPClient sends the presigned requests with c — for a proxy, a
// TLS configuration, or instrumentation. A redirect is still refused: c is
// copied, and its CheckRedirect replaced.
func WithTransferHTTPClient(c *http.Client) TransferOption {
	return func(cfg *transferConfig) error {
		cfg.client = c
		return nil
	}
}

// WithSplitHorizon sends a presigned URL signed for signedOrigin to
// internalOrigin instead, keeping the signed Host header — for storage whose
// URLs are signed for a public host but reached inside a cluster at another
// address. The signature covers the Host header, not the address the request
// is sent to, so it still holds. Both are origins: scheme://host[:port].
// A URL for any other origin is sent as signed.
func WithSplitHorizon(signedOrigin, internalOrigin string) TransferOption {
	return func(cfg *transferConfig) error {
		from, err := parseOrigin(signedOrigin)
		if err != nil {
			return err
		}
		to, err := parseOrigin(internalOrigin)
		if err != nil {
			return err
		}
		cfg.rewrite = func(u *url.URL) *url.URL {
			if u.Scheme != from.Scheme || u.Host != from.Host {
				return u
			}
			out := *u
			out.Scheme, out.Host = to.Scheme, to.Host
			return &out
		}
		return nil
	}
}

// WithTransferRewrite sends each presigned URL to the URL rewrite returns,
// keeping the signed Host header. It is WithSplitHorizon for a mapping one
// pair of origins cannot express; the two replace each other.
func WithTransferRewrite(rewrite func(*url.URL) *url.URL) TransferOption {
	return func(cfg *transferConfig) error {
		cfg.rewrite = rewrite
		return nil
	}
}

func parseOrigin(origin string) (*url.URL, error) {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidOrigin, origin)
	}
	return u, nil
}

// NewTransfer returns a Transfer. With no options it sends through a transport
// with DefaultTransfer… bounds and refuses redirects.
func NewTransfer(opts ...TransferOption) (*Transfer, error) {
	cfg := &transferConfig{}
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}
	client := &http.Client{Transport: newTransferTransport()}
	if cfg.client != nil {
		copied := *cfg.client
		client = &copied
	}
	client.CheckRedirect = refuseRedirect
	return &Transfer{client: client, rewrite: cfg.rewrite, observe: observer{hooks: cfg.hooks, logger: cfg.logger}}, nil
}

func refuseRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func newTransferTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: DefaultTransferDialTimeout}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   DefaultTransferTLSHandshakeTimeout,
		ResponseHeaderTimeout: DefaultTransferResponseHeaderTimeout,
		IdleConnTimeout:       DefaultTransferIdleConnTimeout,
		MaxIdleConnsPerHost:   DefaultTransferMaxIdleConnsPerHost,
	}
}

// defaultTransfer is the Transfer of a client built without WithTransfer.
var defaultTransfer = sync.OnceValue(func() *Transfer {
	t, _ := NewTransfer() // no options, so no error
	return t
})

// do sends one presigned request and returns storage's 2xx response; any
// other status, a redirect included, is a *TransferError.
func (t *Transfer) do(ctx context.Context, fallbackMethod string, signed *commonv1.PresignedUrl,
	header http.Header, body io.Reader, size int64,
) (*http.Response, error) {
	method := signed.GetMethod()
	if method == "" {
		method = fallbackMethod
	}
	target, err := url.Parse(signed.GetUrl())
	if err != nil {
		return nil, fmt.Errorf("paladin: presigned URL: %w", err)
	}
	signedHost := target.Host
	if t.rewrite != nil {
		target = t.rewrite(target)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, err
	}
	// The signature covers Host: keep the one it was signed for.
	req.Host = signedHost
	if body != nil {
		req.ContentLength = size
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	// Covered by the signature: storage refuses the request without them.
	for k, v := range signed.GetRequiredHeaders() {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := t.client.Do(req)
	if err != nil {
		t.ended(ctx, method, target.Host, 0, start, err)
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer func() { _ = resp.Body.Close() }()
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
		err := &TransferError{Method: method, Host: target.Host, Status: resp.StatusCode, Body: string(excerpt)}
		t.ended(ctx, method, target.Host, 0, start, err)
		return nil, err
	}
	return resp, nil
}

// ended reports a request that ended to the Transfer's hooks and logger.
func (t *Transfer) ended(ctx context.Context, method, host string, bytes int64, start time.Time, err error) {
	t.observe.transfer(ctx, TransferEvent{
		Method: method, Host: host, Bytes: bytes, Duration: time.Since(start), Err: err,
	})
}
