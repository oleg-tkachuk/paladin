// Package valkey counts rate-limit windows in Valkey (ADR-0046). It is the
// one package that imports the Valkey client, so nothing that does not count
// in Valkey links it.
package valkey

import (
	"context"
	"crypto/tls"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"

	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
)

//go:embed window.lua
var windowScript string

// keyPrefix scopes every key this package writes, so a Valkey shared with
// other uses holds Paladin's windows apart.
const keyPrefix = "paladin:ratelimit:"

// keyTTL keeps a window for as long as its previous bucket still counts:
// past two windows of silence both buckets are empty, so the key can go.
const keyTTL = 2 * ratecount.Window

// Script modes; they must match window.lua's ARGV[2].
const (
	modeRead       = "0"
	modeBumpAlways = "1"
	modeBumpIfFits = "2"
)

// replyFields is how many integers window.lua returns.
const replyFields = 5

// Options is what a connection needs, independent of the config package.
type Options struct {
	Addr     string
	Username string
	Password string
	TLS      bool
	// Timeout bounds dialing and each call. A call past it is an error, and
	// the caller decides the request in its own process (ratecount.Degrading)
	// instead of waiting on a store that stopped answering.
	Timeout time.Duration
}

// Dial opens a client. Client-side caching is off: every call here is a
// script that writes, which caching cannot serve.
func Dial(o Options) (valkeygo.Client, error) {
	if o.Addr == "" {
		return nil, errors.New("valkey: addr is required")
	}
	opt := valkeygo.ClientOption{
		InitAddress:       []string{o.Addr},
		Username:          o.Username,
		Password:          o.Password,
		DisableCache:      true,
		ForceSingleClient: true,
		Dialer:            net.Dialer{Timeout: o.Timeout},
		ConnWriteTimeout:  o.Timeout,
	}
	if o.TLS {
		opt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	c, err := valkeygo.NewClient(opt)
	if err != nil {
		return nil, fmt.Errorf("valkey: connect %s: %w", o.Addr, err)
	}
	return c, nil
}

// Counter is a ratecount.Counter in Valkey.
type Counter struct {
	client  valkeygo.Client
	script  *valkeygo.Lua
	timeout time.Duration
}

// New counts through client; timeout bounds each call.
func New(client valkeygo.Client, timeout time.Duration) *Counter {
	return &Counter{client: client, script: valkeygo.NewLuaScript(windowScript), timeout: timeout}
}

// Bump implements ratecount.Counter.
func (c *Counter) Bump(ctx context.Context, key string) (ratecount.Counts, error) {
	return c.run(ctx, key, modeBumpAlways, 0)
}

// BumpIfFits implements ratecount.Counter.
func (c *Counter) BumpIfFits(ctx context.Context, key string, capacity int) (ratecount.Counts, error) {
	return c.run(ctx, key, modeBumpIfFits, capacity)
}

// Read implements ratecount.Counter.
func (c *Counter) Read(ctx context.Context, key string) (ratecount.Counts, error) {
	return c.run(ctx, key, modeRead, 0)
}

func (c *Counter) run(ctx context.Context, key, mode string, capacity int) (ratecount.Counts, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	args := []string{
		strconv.FormatInt(ratecount.Window.Microseconds(), 10),
		mode,
		strconv.Itoa(capacity),
		strconv.FormatInt(keyTTL.Milliseconds(), 10),
	}
	vals, err := c.script.Exec(ctx, c.client, []string{keyPrefix + key}, args).AsIntSlice()
	if err != nil {
		return ratecount.Counts{}, fmt.Errorf("valkey: window %s: %w", key, err)
	}
	if len(vals) != replyFields {
		return ratecount.Counts{}, fmt.Errorf("valkey: window %s: %d fields in reply, want %d", key, len(vals), replyFields)
	}
	admitted, cur, prev, elapsedUS, index := vals[0], vals[1], vals[2], vals[3], vals[4]
	return ratecount.Counts{
		Current:     cur,
		Previous:    prev,
		BucketStart: time.UnixMicro(index * ratecount.Window.Microseconds()).UTC(),
		Elapsed:     time.Duration(elapsedUS) * time.Microsecond,
		Admitted:    admitted == 1,
	}, nil
}
