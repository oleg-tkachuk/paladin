package fault

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sony/gobreaker"
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
	cb         *gobreaker.CircuitBreaker
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

	settings := gobreaker.Settings{
		Name:        cfg.Name,
		MaxRequests: 1,
		Timeout:     cfg.Timeout,
		OnStateChange: func(name string, from, to gobreaker.State) {
			breakerStateGauge.WithLabelValues(name).Set(float64(to))
		},
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.ConsecutiveFailures >= cfg.MaxConsecutiveFails {
				return true
			}
			if counts.Requests < 10 {
				return false
			}
			ratio := float64(counts.TotalFailures) / float64(counts.Requests)

			return ratio >= cfg.FailureRatio
		},
	}

	cb := gobreaker.NewCircuitBreaker(settings)
	w := &CircuitBreakerWrapper{cb: cb, lastUsedAt: time.Now(), persistent: cfg.Persistent}
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

	res, err := w.cb.Execute(fn)
	if err != nil {
		breakerFailures.WithLabelValues(w.cb.Name()).Inc()
	} else {
		breakerSuccesses.WithLabelValues(w.cb.Name()).Inc()
	}

	return res, err
}

func (w *CircuitBreakerWrapper) State() gobreaker.State {
	if w == nil || w.cb == nil {
		return gobreaker.StateClosed
	}

	return w.cb.State()
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
