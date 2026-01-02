package breaker

import (
	"sync"
	"time"

	"paladin/internal/config"
	"paladin/internal/fault"
)

type Factory interface {
	Get(name string) *fault.CircuitBreakerWrapper
}

type lazyBreaker struct {
	once sync.Once
	cb   *fault.CircuitBreakerWrapper
}

type factory struct {
	cfg      config.Config
	registry map[string]*lazyBreaker
	mu       sync.Mutex
}

func NewFactory(cfg config.Config) Factory {
	return &factory{cfg: cfg, registry: map[string]*lazyBreaker{}}
}

func (f *factory) Get(name string) *fault.CircuitBreakerWrapper {
	// If you want to disable breakers globally, add a config flag here.
	f.mu.Lock()

	lb, ok := f.registry[name]
	if !ok {
		lb = &lazyBreaker{}
		f.registry[name] = lb
	}
	f.mu.Unlock()

	lb.once.Do(func() {
		lb.cb = fault.GetWithConfig(fault.BreakerConfig{
			Name:                name,
			Timeout:             10 * time.Second,
			MaxConsecutiveFails: 3,
			FailureRatio:        0.6,
			WindowDuration:      1 * time.Minute,
			Persistent:          false,
		})
	})

	return lb.cb
}
