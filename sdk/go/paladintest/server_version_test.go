package paladintest_test

import (
	"context"
	"errors"
	"testing"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// The server names its release on every response, an error included, and the
// SDK carries it on the *paladin.Error, so a consumer's code that reports a
// contract skew reads it there. The fake names its own.
func TestAnErrorNamesTheFakesRelease(t *testing.T) {
	fake := paladintest.New(t)
	missing := paladin.ObjectName{CollectionName: fake.Collection(), Object: "00000000-0000-4000-8000-000000000000"}
	_, err := fake.Connect().Data.Object.GetObject(context.Background(), &datav1.GetObjectRequest{Name: missing.String()})
	var pe *paladin.Error
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a *paladin.Error", err)
	}
	if pe.ServerVersion != paladintest.ServerVersion {
		t.Errorf("ServerVersion = %q, want %q", pe.ServerVersion, paladintest.ServerVersion)
	}
}
