package fault_test

import (
	goerrors "errors"
	"testing"
	"time"

	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/oleg-tkachuk/paladin/internal/fault"
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

	assert.Equal(t, "closed", cb.State())

	// Test Success Execution
	res, err := fault.Execute(cb, func() (interface{}, error) {
		return "success", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "success", res)
	assert.Equal(t, "closed", cb.State())

	// Test Failures
	expectedErr := goerrors.New("failure")

	// Fail 1
	_, err = fault.Execute(cb, func() (interface{}, error) { return nil, expectedErr })
	require.ErrorIs(t, err, expectedErr)
	assert.Equal(t, "closed", cb.State()) // Might be half-open or closed depending on thresholds

	// Fail 2
	_, err = fault.Execute(cb, func() (interface{}, error) { return nil, expectedErr })
	require.ErrorIs(t, err, expectedErr)

	// Check again if state string tripped
	if cb.State() != "open" { // Depending on the execution limits it might require more requests. Wait, MaxConsecutiveFails is 2!
		assert.Equal(t, "open", cb.State())
	}

	// Next execution should fail with ErrOpen automatically
	_, err = fault.Execute(cb, func() (interface{}, error) {
		assert.Fail(t, "should not be called")

		return nil, goerrors.New("unimplemented/mock")
	})
	require.ErrorIs(t, err, circuitbreaker.ErrOpen)

	// Test All / AllBreakers
	all := fault.AllBreakers()
	found, ok := all["test-breaker"]
	assert.True(t, ok)
	assert.NotNil(t, found)

	// Execute with Nil Wrapper explicitly
	nilRes, nilErr := fault.Execute(nil, func() (interface{}, error) { return "nil-wrapper-fallback", nil })
	require.NoError(t, nilErr)
	assert.Equal(t, "nil-wrapper-fallback", nilRes)

	var nilWrapper *fault.CircuitBreakerWrapper
	assert.Equal(t, "closed", nilWrapper.State())
}
