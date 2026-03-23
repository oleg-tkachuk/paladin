package breaker

import (
	"sync"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/fault"
)

type Factory interface {
	Get(name string) *fault.CircuitBreakerWrapper
	CheckHealth() map[string]string
}

const (
	defaultTimeout        = 10 * time.Second
	defaultWindowDuration = 1 * time.Minute
)

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
			Timeout:             defaultTimeout,
			MaxConsecutiveFails: 3,
			FailureRatio:        0.6,
			WindowDuration:      defaultWindowDuration,
			Persistent:          false,
		})
	})

	return lb.cb
}

func (f *factory) CheckHealth() map[string]string {
	res := make(map[string]string)
	for name, w := range fault.AllBreakers() {
		res[name] = w.State()
	}

	return res
}
