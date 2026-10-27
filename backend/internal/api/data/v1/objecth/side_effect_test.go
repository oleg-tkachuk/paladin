package objecth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// A side effect that failed after its transition committed is logged with
// what it was and whose; one that succeeded writes nothing.
func TestLogSideEffect(t *testing.T) {
	id := uuid.New()
	for name, err := range map[string]error{"failed": errors.New("pool closed"), "succeeded": nil} {
		t.Run(name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			LogSideEffect(logger.WithContext(context.Background(), zap.New(core)), EffectVersion, id, err)
			if (logs.Len() == 1) != (err != nil) {
				t.Fatalf("lines = %d for err %v", logs.Len(), err)
			}
			if err != nil {
				f := logs.All()[0].ContextMap()
				if f["effect"] != EffectVersion || f["object_id"] != id.String() {
					t.Errorf("fields = %v", f)
				}
			}
		})
	}
}
