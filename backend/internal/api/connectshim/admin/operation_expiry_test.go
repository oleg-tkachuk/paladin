package admin

import (
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
)

// When the reaper removes an operation reaches the caller, and an operation
// with no such time carries none.
func TestOperationExpireTime(t *testing.T) {
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	got := operationToProto(&operationh.Operation{State: operationh.StateSucceeded, ExpireAt: &at})
	if !got.GetExpireTime().AsTime().Equal(at) {
		t.Errorf("expire_time = %v, want %v", got.GetExpireTime().AsTime(), at)
	}
	if none := operationToProto(&operationh.Operation{State: operationh.StateRunning}); none.GetExpireTime() != nil {
		t.Errorf("expire_time = %v, want unset", none.GetExpireTime())
	}
}
