package worker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/health"
)

// stalledAfterIntervals is how many intervals a job may go without a tick
// before the jobs component calls it stalled: one late tick is a slow run,
// two is a loop that stopped.
const stalledAfterIntervals = 2

// maxTickError is the most of a failed tick's error the health page shows;
// the log has all of it.
const maxTickError = 120

// JobTick is what the pod knows of one periodic job: how often it runs, when
// it last finished a tick and how that went.
type JobTick struct {
	Name     string
	Interval time.Duration
	// Last is when the last tick started, zero before the first; Took is
	// how long it ran and Err what it failed with, "" for none.
	Last time.Time
	Took time.Duration
	Err  string
}

// TickLog is the pod's own record of its periodic jobs' ticks, as RunTicker
// runs them: what the health page shows a worker doing, beside the metrics
// the stalled-worker alert reads.
type TickLog struct {
	mu      sync.Mutex
	jobs    map[string]*JobTick
	started time.Time
	now     func() time.Time
}

// NewTickLog is an empty log, its jobs counted from now.
func NewTickLog() *TickLog {
	return &TickLog{jobs: map[string]*JobTick{}, started: time.Now(), now: time.Now}
}

// Ticks is the log RunTicker writes to: every job of the pod.
var Ticks = NewTickLog()

func (l *TickLog) register(name string, interval time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.jobs[name] = &JobTick{Name: name, Interval: interval}
}

func (l *TickLog) record(name string, start time.Time, took time.Duration, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	j, ok := l.jobs[name]
	if !ok {
		return
	}
	j.Last, j.Took, j.Err = start, took, ""
	if err != nil {
		j.Err = err.Error()
	}
}

// Jobs is every job the log knows, by name.
func (l *TickLog) Jobs() []JobTick {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]JobTick, 0, len(l.jobs))
	for _, j := range l.jobs {
		out = append(out, *j)
	}
	slices.SortFunc(out, func(a, b JobTick) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// stalled is whether j has gone stalledAfterIntervals intervals without a
// tick, counted from the pod's start before its first.
func (l *TickLog) stalled(j JobTick) bool {
	since := j.Last
	if since.IsZero() {
		since = l.started
	}
	return l.now().Sub(since) > stalledAfterIntervals*j.Interval
}

// JobsComponent is the health page's view of a pod's periodic jobs: each
// job's last tick, how long it took and how it went, on its own line, and
// unhealthy when one has stalled. Never critical: a stalled job is
// housekeeping falling behind, not a pod out of service.
func JobsComponent(log *TickLog) health.Check {
	return health.Check{
		Name:     "jobs",
		Category: health.CategorySubsystem,
		Func: func(context.Context) error {
			var errs []error
			for _, j := range log.Jobs() {
				if log.stalled(j) {
					errs = append(errs, fmt.Errorf("%s has not run for over %s", j.Name, stalledAfterIntervals*j.Interval))
				}
			}
			return errors.Join(errs...)
		},
		Describe: func(context.Context) []health.Detail {
			var d []health.Detail
			for _, j := range log.Jobs() {
				d = append(d, health.Detail{Group: j.Name, Name: "every", Value: j.Interval.String()})
				if j.Last.IsZero() {
					d = append(d, health.Detail{Group: j.Name, Name: "last run", Value: "not yet"})
					continue
				}
				d = append(d,
					health.Detail{Group: j.Name, Name: "last run", Value: log.now().Sub(j.Last).Truncate(time.Second).String() + " ago"},
					health.Detail{Group: j.Name, Name: "took", Value: j.Took.Truncate(time.Millisecond).String()},
					health.Detail{Group: j.Name, Name: "outcome", Value: outcomeOf(j.Err)},
				)
			}
			return d
		},
	}
}

// outcomeOf is a tick's outcome in words: ok, or what it failed with, cut to
// maxTickError.
func outcomeOf(err string) string {
	if err == "" {
		return "ok"
	}
	if len(err) > maxTickError {
		err = err[:maxTickError] + "…"
	}
	return "failed: " + err
}
