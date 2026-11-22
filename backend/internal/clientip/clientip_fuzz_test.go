package clientip

import (
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

// hopSep separates the entries of a forwarding header.
const hopSep = ","

// fuzzTrusted are the proxies the fuzz target's resolver believes.
var fuzzTrusted = []string{"10.0.0.0/8", "fd00::/8", "192.0.2.7"}

// Whatever peer and forwarding header a request arrives with: an untrusted
// peer is the client whatever the header claims, a trusted one yields the
// peer or an entry of the header, and the result is never an IPv4 address in
// IPv6 form, which would miss every IPv4 trust and rate-limit key.
func FuzzResolve(f *testing.F) {
	for _, c := range [][2]string{
		{"203.0.113.9:4000", "198.51.100.1"},
		{"10.0.0.5:4000", "198.51.100.1, 10.1.0.2, 10.2.0.3"},
		{"10.0.0.5:4000", "192.0.2.66, 198.51.100.1"},
		{"10.0.0.5:4000", "198.51.100.1, garbage"},
		{"[fd00::1]:4000", "2001:db8::7"},
		{"[::ffff:10.0.0.5]:4000", "::ffff:198.51.100.1"},
		{"10.0.0.5:4000", "fe80::1%eth0, ,"},
		{"not-an-address", ""},
	} {
		f.Add(c[0], c[1])
	}
	r, err := New(xff, fuzzTrusted)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, remoteAddr, header string) {
		h := http.Header{}
		h.Set(xff, header)
		got := r.Resolve(remoteAddr, h)
		peer, ok := peerAddr(remoteAddr)
		if !ok {
			if got.IsValid() {
				t.Fatalf("Resolve(%q) = %s from a peer that does not parse", remoteAddr, got)
			}
			return
		}
		if got.Is4In6() {
			t.Fatalf("Resolve(%q, %q) = %s, an IPv4 address in IPv6 form", remoteAddr, header, got)
		}
		if !r.isTrusted(peer) {
			if got != peer {
				t.Fatalf("Resolve(%q, %q) = %s, believed a header from untrusted peer %s", remoteAddr, header, got, peer)
			}
			return
		}
		if !got.IsValid() || got == peer {
			return
		}
		for hop := range strings.SplitSeq(header, hopSep) {
			if a, err := netip.ParseAddr(strings.TrimSpace(hop)); err == nil && a.Unmap() == got {
				return
			}
		}
		t.Fatalf("Resolve(%q, %q) = %s, neither the peer nor an entry of the header", remoteAddr, header, got)
	})
}

// Any trusted-proxy entry an operator writes: refused, or a prefix that holds
// the address it was written from.
func FuzzNew(f *testing.F) {
	for _, s := range []string{"10.0.0.0/8", "10.0.0.5", " fd00::/8 ", "::ffff:10.0.0.5", "10.0.0.0/33", "garbage", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, entry string) {
		r, err := New(xff, []string{entry})
		if err != nil {
			return
		}
		if len(r.trusted) != 1 || !r.trusted[0].IsValid() {
			t.Fatalf("New(%q) trusts %v", entry, r.trusted)
		}
		if a, err := netip.ParseAddr(strings.TrimSpace(entry)); err == nil && !r.isTrusted(a.Unmap()) {
			t.Fatalf("New(%q) does not trust %s itself", entry, a)
		}
	})
}
