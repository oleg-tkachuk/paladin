package middleware

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// putFailing keeps nothing, as a store with its database down.
type putFailing struct{ *memStore }

func (putFailing) Put(context.Context, uuid.UUID, string, string, IdempotencyRecord, time.Time) error {
	return errors.New("pool closed")
}

// A response the store could not keep still reaches the caller, and the loss
// is logged: a retry with the same key will run the call again.
func TestIdempotentResponseNotKeptIsLogged(t *testing.T) {
	client, _, ctx := newSystemServer(t, putFailing{newMemStore()}, IdempotencyConfig{TTL: time.Minute})
	core, logs := observer.New(zap.WarnLevel)
	ctx = logger.WithContext(ctx, zap.New(core))

	if _, err := client.UpdateMine(withIdempotencyKey(ctx, uuid.New().String()), &iamv1.UpdateMineRequest{}); err != nil {
		t.Fatalf("the call must not fail on the store: %v", err)
	}
	if logs.FilterMessage(idempotencyNotKeptMessage).Len() != 1 {
		t.Errorf("logged %v, want one line", logs.All())
	}
}
