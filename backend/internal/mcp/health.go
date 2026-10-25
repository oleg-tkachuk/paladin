package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
)

// Detail values the upstreams component reports a plane as.
const (
	upstreamReachable   = "reachable"
	upstreamUnreachable = "unreachable"
)

// SessionsComponent is the bridge's sessions on the health page: how many are
// open, how many name the agent that opened them, and the tool calls they have
// made. Never failing: no session is a quiet bridge, not a broken one.
func SessionsComponent(reg *SessionRegistry) health.Check {
	return health.Check{
		Name:     "sessions",
		Category: health.CategorySubsystem,
		Func:     func(context.Context) error { return nil },
		Describe: func(context.Context) []health.Detail {
			all := reg.Snapshot()
			agents := map[string]bool{}
			var calls int64
			for _, s := range all {
				if s.AgentSubject != "" {
					agents[s.AgentSubject] = true
				}
				calls += s.ToolCallCount
			}
			return []health.Detail{
				{Name: "open", Value: strconv.Itoa(len(all))},
				{Name: "agents", Value: strconv.Itoa(len(agents))},
				{Name: "tool calls", Value: strconv.FormatInt(calls, 10)},
			}
		},
	}
}

// UpstreamsComponent is whether the bridge reaches the planes it proxies to,
// each on its own line with its probe's round trip, probed as /status probes
// them. Unhealthy while one is unreachable; never critical, as the bridge
// still serves the planes it does reach. A plane the bridge is given no URL
// for is not one it proxies to, and is left out.
func UpstreamsComponent(targets []UpstreamTarget, probe func(ctx context.Context, url string) error) health.Check {
	targets = slices.DeleteFunc(slices.Clone(targets), func(t UpstreamTarget) bool { return t.URL == "" })
	var (
		mu   sync.Mutex
		last []UpstreamStatus
	)
	return health.Check{
		Name:     "upstreams",
		Category: health.CategoryUpstream,
		Func: func(ctx context.Context) error {
			got := ProbeUpstreams(ctx, targets, probe)
			mu.Lock()
			last = got
			mu.Unlock()
			var errs []error
			for _, u := range got {
				if !u.Reachable {
					errs = append(errs, fmt.Errorf("%s: %s", u.Name, u.Error))
				}
			}
			return errors.Join(errs...)
		},
		Describe: func(context.Context) []health.Detail {
			mu.Lock()
			defer mu.Unlock()
			var d []health.Detail
			for _, u := range last {
				state := upstreamReachable
				if !u.Reachable {
					state = upstreamUnreachable
				}
				d = append(d,
					health.Detail{Group: u.Name, Name: "", Value: state},
					health.Detail{Group: u.Name, Name: "in", Value: strconv.FormatInt(u.LatencyMs, 10) + "ms"},
				)
			}
			return d
		},
	}
}
