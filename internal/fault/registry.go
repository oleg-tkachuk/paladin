package fault

import (
	"sync"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	breakerStateGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "circuit_breaker_state", Help: "0=closed,1=open,2=half-open"},
		[]string{"name"},
	)
	breakerFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "circuit_breaker_failures_total", Help: "Breaker failures"},
		[]string{"name"},
	)
	breakerSuccesses = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "circuit_breaker_successes_total", Help: "Breaker successes"},
		[]string{"name"},
	)
	metricsOnce sync.Once
)

type CircuitBreakerWrapper struct {
	cb         circuitbreaker.CircuitBreaker[any]
	exec       failsafe.Executor[any]
	name       string
	lastUsedAt time.Time
	persistent bool
}

type Registry struct {
	mu       sync.Mutex
	breakers map[string]*CircuitBreakerWrapper
}

var defaultRegistry = &Registry{breakers: map[string]*CircuitBreakerWrapper{}}

type BreakerConfig struct {
	Name                string
	Timeout             time.Duration
	MaxConsecutiveFails uint32
	FailureRatio        float64
	WindowDuration      time.Duration
	Persistent          bool
}

func InitBreakerMetricsOnce() {
	metricsOnce.Do(func() {
		prometheus.MustRegister(breakerStateGauge, breakerFailures, breakerSuccesses)
	})
}

func GetWithConfig(cfg BreakerConfig) *CircuitBreakerWrapper {
	InitBreakerMetricsOnce()

	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()

	if w, ok := defaultRegistry.breakers[cfg.Name]; ok {
		w.lastUsedAt = time.Now()
		return w
	}

	cb := circuitbreaker.NewBuilder[any]().
		WithFailureThreshold(uint(cfg.MaxConsecutiveFails)).
		WithDelay(cfg.Timeout).
		OnStateChanged(func(e circuitbreaker.StateChangedEvent) {
			var state float64
			switch e.NewState {
			case circuitbreaker.ClosedState:
				state = 0
			case circuitbreaker.OpenState:
				state = 1
			case circuitbreaker.HalfOpenState:
				state = 2
			}
			breakerStateGauge.WithLabelValues(cfg.Name).Set(state)
		}).
		Build()

	exec := failsafe.With[any](cb)

	w := &CircuitBreakerWrapper{
		cb:         cb,
		exec:       exec,
		name:       cfg.Name,
		lastUsedAt: time.Now(),
		persistent: cfg.Persistent,
	}
	defaultRegistry.breakers[cfg.Name] = w

	breakerStateGauge.WithLabelValues(cfg.Name).Set(0)

	return w
}

func Execute(w *CircuitBreakerWrapper, fn func() (interface{}, error)) (interface{}, error) {
	if w == nil {
		return fn()
	}

	defaultRegistry.mu.Lock()
	w.lastUsedAt = time.Now()
	defaultRegistry.mu.Unlock()

	res, err := w.exec.Get(fn)
	if err != nil {
		breakerFailures.WithLabelValues(w.name).Inc()
	} else {
		breakerSuccesses.WithLabelValues(w.name).Inc()
	}

	return res, err
}

func (w *CircuitBreakerWrapper) State() string {
	if w == nil || w.cb == nil {
		return "closed"
	}
	state := w.cb.State()
	switch state {
	case circuitbreaker.OpenState:
		return "open"
	case circuitbreaker.HalfOpenState:
		return "half-open"
	default:
		return "closed"
	}
}

func (r *Registry) All() map[string]*CircuitBreakerWrapper {
	r.mu.Lock()
	defer r.mu.Unlock()

	res := make(map[string]*CircuitBreakerWrapper, len(r.breakers))
	for k, v := range r.breakers {
		res[k] = v
	}

	return res
}

func AllBreakers() map[string]*CircuitBreakerWrapper {
	return defaultRegistry.All()
}
