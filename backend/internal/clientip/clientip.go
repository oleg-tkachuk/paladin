// Package clientip resolves the address of the client behind a chain of
// proxies, trusting forwarding headers only from proxies the operator names.
//
// A forwarding header such as X-Forwarded-For is a list the client starts and
// every proxy appends to: "client, proxy1, proxy2". Its leftmost entry is
// whatever the client chose to send, so reading it as the client address lets
// any caller claim any address. The entries a trusted proxy appended are the
// only ones that can be believed, and they are at the right.
//
// Resolve therefore walks the chain from the right, starting at the TCP peer:
// while the hop is a trusted proxy, the entry before it is believed. The first
// hop that is not a trusted proxy is the client. Nothing here is specific to
// one ingress: the header name and the trusted networks are configuration.
package clientip

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolver resolves client addresses for one listener.
type Resolver struct {
	header  string
	trusted []netip.Prefix
}

// New builds a Resolver reading header (X-Forwarded-For, or any single- or
// multi-value header in the same format) from peers inside trusted. trusted
// holds CIDRs or bare addresses. With no trusted entries every header is
// ignored and the TCP peer is the client.
func New(header string, trusted []string) (*Resolver, error) {
	r := &Resolver{header: header}
	for _, t := range trusted {
		p, err := parsePrefix(t)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", t, err)
		}
		r.trusted = append(r.trusted, p)
	}
	return r, nil
}

func parsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		return p.Masked(), err
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

func (r *Resolver) isTrusted(a netip.Addr) bool {
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Resolve returns the client address for a request that arrived from
// remoteAddr ("host:port", as http.Request.RemoteAddr) carrying h. It returns
// the zero Addr when the address cannot be determined: an unparseable peer,
// or an unparseable entry where the chain still had to be believed.
func (r *Resolver) Resolve(remoteAddr string, h http.Header) netip.Addr {
	peer, ok := peerAddr(remoteAddr)
	if !ok {
		return netip.Addr{}
	}
	if !r.isTrusted(peer) || r.header == "" {
		return peer
	}
	var hops []string
	for _, v := range h.Values(r.header) {
		hops = append(hops, strings.Split(v, ",")...)
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if hop == "" {
			continue
		}
		a, err := netip.ParseAddr(hop)
		if err != nil {
			return netip.Addr{}
		}
		client = a.Unmap()
		if !r.isTrusted(client) {
			return client
		}
	}
	// Every hop was a trusted proxy: the leftmost of them is the closest the
	// chain gets to the client.
	return client
}

func peerAddr(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// Middleware stores each request's resolved client address in its context,
// where FromContext reads it.
func (r *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if a := r.Resolve(req.RemoteAddr, req.Header); a.IsValid() {
			req = req.WithContext(WithAddr(req.Context(), a))
		}
		next.ServeHTTP(w, req)
	})
}

type ctxKey struct{}

// WithAddr returns ctx carrying a as the client address.
func WithAddr(ctx context.Context, a netip.Addr) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

// FromContext returns the client address stored by Middleware, and whether
// there is one.
func FromContext(ctx context.Context) (netip.Addr, bool) {
	a, ok := ctx.Value(ctxKey{}).(netip.Addr)
	return a, ok && a.IsValid()
}
