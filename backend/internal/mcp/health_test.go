package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
)

// detailsOf is a component's details, by group and name.
func detailsOf(c health.Check) map[string]string {
	out := map[string]string{}
	for _, d := range c.Describe(context.Background()) {
		out[d.Group+"/"+d.Name] = d.Value
	}
	return out
}

func TestSessionsComponent(t *testing.T) {
	reg := NewSessionRegistry()
	reg.open("s1", "agent:a")
	reg.open("s2", "agent:a")
	reg.open("s3", "")
	reg.touch("s1", "agent:a", true)
	c := SessionsComponent(reg)
	if err := c.Func(context.Background()); err != nil || c.Critical {
		t.Fatalf("sessions: err %v, critical %t", err, c.Critical)
	}
	if d := detailsOf(c); d["/open"] != "3" || d["/agents"] != "1" || d["/tool calls"] != "1" {
		t.Errorf("details %v", d)
	}
}

// A plane the bridge cannot reach makes the component unhealthy and is
// named; a plane with no URL is not probed at all.
func TestUpstreamsComponent(t *testing.T) {
	down := errors.New("connection refused")
	probed := map[string]bool{}
	probe := func(_ context.Context, url string) error {
		probed[url] = true
		if url == "http://data" {
			return down
		}
		return nil
	}
	c := UpstreamsComponent([]UpstreamTarget{
		{Name: "admin", URL: "http://admin"},
		{Name: "data", URL: "http://data"},
		{Name: "iam", URL: ""},
	}, probe)
	err := c.Func(context.Background())
	if err == nil || !strings.Contains(err.Error(), "data") || strings.Contains(err.Error(), "admin") {
		t.Errorf("err = %v, want data named alone", err)
	}
	if probed[""] || len(probed) != 2 {
		t.Errorf("probed %v, want admin and data only", probed)
	}
	d := detailsOf(c)
	if d["admin/"] != upstreamReachable || d["data/"] != upstreamUnreachable || !strings.HasSuffix(d["admin/in"], "ms") {
		t.Errorf("details %v", d)
	}
	if _, ok := d["iam/"]; ok {
		t.Error("a plane with no URL is listed")
	}
}

// ProbeUpstreams answers in the targets' order and bounds each probe, so a
// plane that hangs does not hold the others.
func TestProbeUpstreamsBoundsEachProbe(t *testing.T) {
	hang := func(ctx context.Context, url string) error {
		if url == "http://slow" {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	start := time.Now()
	got := ProbeUpstreams(context.Background(), []UpstreamTarget{{Name: "slow", URL: "http://slow"}, {Name: "fast", URL: "http://fast"}}, hang)
	if time.Since(start) > upstreamProbeTimeout+time.Second {
		t.Errorf("took %s, past the per-probe bound", time.Since(start))
	}
	if got[0].Name != "slow" || got[0].Reachable || got[1].Name != "fast" || !got[1].Reachable {
		t.Errorf("got %+v", got)
	}
}
