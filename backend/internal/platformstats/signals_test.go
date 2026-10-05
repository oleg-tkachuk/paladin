package platformstats

import (
	"context"
	"net/url"
	"testing"
)

func TestParseSignal(t *testing.T) {
	for _, sig := range []Signal{
		SignalQuotaAtLimit, SignalQuotaNearLimit, SignalCapabilitiesExpiring, SignalAPITokensExpiring,
	} {
		if got, ok := ParseSignal(string(sig)); !ok || got != sig {
			t.Errorf("ParseSignal(%q) = %q, %v; want it back", sig, got, ok)
		}
	}
	for _, bad := range []string{"", "objects", "QUOTA_AT_LIMIT"} {
		if _, ok := ParseSignal(bad); ok {
			t.Errorf("ParseSignal(%q) accepted a signal with no drill-down", bad)
		}
	}
}

func TestSignalQueryRoundTrip(t *testing.T) {
	page := TenantPage{Size: 7, After: encodeTenantCursor(3, "t")}
	q, err := url.ParseQuery(SignalQuery(SignalAPITokensExpiring, page).Encode())
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	sig, gotPage, ok := SignalFromQuery(q)
	if !ok || sig != SignalAPITokensExpiring || gotPage != page {
		t.Errorf("SignalFromQuery = %q, %+v, %v; want %q, %+v", sig, gotPage, ok, SignalAPITokensExpiring, page)
	}
	if _, _, ok := SignalFromQuery(url.Values{}); ok {
		t.Error("a query with no signal was accepted")
	}
}

// An unknown signal is refused before the pool is touched.
func TestCollectSignalTenants_UnknownSignal(t *testing.T) {
	if _, err := CollectSignalTenants(context.Background(), nil, Signal("objects"), TenantPage{}); err == nil {
		t.Error("CollectSignalTenants accepted an unknown signal")
	}
}
