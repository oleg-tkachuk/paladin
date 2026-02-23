package fault_test

import (
	goerrors "errors"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/fault"
	"github.com/sony/gobreaker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCircuitBreaker(t *testing.T) {
	// Reset metrics to avoid panic on duplicate registration in tests if it runs multiple times
	// Actually we should just test if it trips correctly

	fault.InitBreakerMetricsOnce()

	cfg := fault.BreakerConfig{
		Name:                "test-breaker",
		Timeout:             10 * time.Millisecond,
		MaxConsecutiveFails: 2,
		FailureRatio:        0.5,
		WindowDuration:      1 * time.Minute,
		Persistent:          false,
	}

	cb := fault.GetWithConfig(cfg)
	require.NotNil(t, cb)

	assert.Equal(t, gobreaker.StateClosed, cb.State())

	// Test Success Execution
	res, err := fault.Execute(cb, func() (interface{}, error) {
		return "success", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "success", res)
	assert.Equal(t, gobreaker.StateClosed, cb.State())

	// Test Failures
	expectedErr := goerrors.New("failure")

	// Fail 1
	_, err = fault.Execute(cb, func() (interface{}, error) { return nil, expectedErr })
	assert.ErrorIs(t, err, expectedErr)
	assert.Equal(t, gobreaker.StateClosed, cb.State())

	// Fail 2 (trips breaker because MaxConsecutiveFails is 2 for ReadyToTrip but requests count > 10 in some cases? Wait, the ReadyToTrip has `if counts.ConsecutiveFailures >= cfg.MaxConsecutiveFails { return true }`)
	_, err = fault.Execute(cb, func() (interface{}, error) { return nil, expectedErr })
	assert.ErrorIs(t, err, expectedErr)
	assert.Equal(t, gobreaker.StateOpen, cb.State())

	// Next execution should fail with ErrOpenState immediately without calling fn
	_, err = fault.Execute(cb, func() (interface{}, error) {
		assert.Fail(t, "should not be called")
		return nil, nil
	})
	assert.ErrorIs(t, err, gobreaker.ErrOpenState)

	// Test All / AllBreakers
	all := fault.AllBreakers()
	found, ok := all["test-breaker"]
	assert.True(t, ok)
	assert.NotNil(t, found)

	// Execute with Nil Wrapper explicitly
	nilRes, nilErr := fault.Execute(nil, func() (interface{}, error) { return "nil-wrapper-fallback", nil })
	assert.NoError(t, nilErr)
	assert.Equal(t, "nil-wrapper-fallback", nilRes)

	var nilWrapper *fault.CircuitBreakerWrapper
	assert.Equal(t, gobreaker.StateClosed, nilWrapper.State())
}
