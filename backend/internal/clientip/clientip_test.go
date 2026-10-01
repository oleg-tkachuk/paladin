package clientip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

const xff = "X-Forwarded-For"

func TestResolve(t *testing.T) {
	ingress := []string{"10.0.0.0/8"}
	for _, tc := range []struct {
		name    string
		header  string
		trusted []string
		peer    string
		values  []string
		want    string
	}{
		{"untrusted peer: header ignored", xff, ingress, "203.0.113.9:4000", []string{"198.51.100.1"}, "203.0.113.9"},
		{"no trusted proxies: header ignored", xff, nil, "10.0.0.5:4000", []string{"198.51.100.1"}, "10.0.0.5"},
		{"one trusted proxy", xff, ingress, "10.0.0.5:4000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"chain through trusted proxies", xff, ingress, "10.0.0.5:4000", []string{"198.51.100.1, 10.1.0.2, 10.2.0.3"}, "198.51.100.1"},
		// The client prepends a fake address; the proxy appends the real one.
		{"a spoofed leftmost entry is not believed", xff, ingress, "10.0.0.5:4000", []string{"192.0.2.66, 198.51.100.1"}, "198.51.100.1"},
		{"every hop trusted: the leftmost", xff, ingress, "10.0.0.5:4000", []string{"10.9.9.9, 10.1.0.2"}, "10.9.9.9"},
		{"no header from a trusted peer: the peer", xff, ingress, "10.0.0.5:4000", nil, "10.0.0.5"},
		{"unparseable hop to believe: unknown", xff, ingress, "10.0.0.5:4000", []string{"198.51.100.1, garbage"}, ""},
		{"unparseable hop past the client: ignored", xff, ingress, "10.0.0.5:4000", []string{"garbage, 198.51.100.1"}, "198.51.100.1"},
		{"IPv6 peer with brackets", xff, []string{"fd00::/8"}, "[fd00::1]:4000", []string{"2001:db8::7"}, "2001:db8::7"},
		{"IPv4-mapped peer", xff, ingress, "[::ffff:10.0.0.5]:4000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"another single-value header", "X-Real-Ip", ingress, "10.0.0.5:4000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"repeated header lines form one chain", xff, ingress, "10.0.0.5:4000", []string{"198.51.100.1", "10.1.0.2"}, "198.51.100.1"},
		{"a bare address as a trusted proxy", xff, []string{"10.0.0.5"}, "10.0.0.5:4000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"unparseable peer: unknown", xff, ingress, "not-an-address", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New(tc.header, tc.trusted)
			if err != nil {
				t.Fatal(err)
			}
			h := http.Header{}
			for _, v := range tc.values {
				h.Add(tc.header, v)
			}
			got := r.Resolve(tc.peer, h)
			if want := tc.want; (want == "" && got.IsValid()) || (want != "" && got != netip.MustParseAddr(want)) {
				t.Errorf("Resolve = %v, want %q", got, want)
			}
		})
	}
}

func TestNewRejectsAnInvalidTrustedProxy(t *testing.T) {
	if _, err := New(xff, []string{"10.0.0.0/33"}); err == nil {
		t.Error("accepted an invalid CIDR")
	}
	if _, err := New(xff, []string{"ingress"}); err == nil {
		t.Error("accepted a name")
	}
}

func TestMiddlewareStoresTheAddress(t *testing.T) {
	r, _ := New(xff, []string{"10.0.0.0/8"})
	var got netip.Addr
	var ok bool
	h := r.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		got, ok = FromContext(req.Context())
	}))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:4000"
	req.Header.Set(xff, "198.51.100.1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !ok || got != netip.MustParseAddr("198.51.100.1") {
		t.Errorf("context address = %v (%v), want 198.51.100.1", got, ok)
	}
}

func TestFromContextWithoutAnAddress(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Error("reported an address that was never stored")
	}
}
