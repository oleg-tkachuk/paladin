package objecth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
)

// lookupRepo answers every object lookup with err.
type lookupRepo struct {
	fakeObjectRepo
	err error
}

func (r *lookupRepo) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	return Object{}, r.err
}

// A missing object is NotFound in the API's words. Every lookup error used to
// become NotFound carrying the driver's text — "no rows in result set" — and a
// store that was down read as an object that was not there.
func TestDeleteObjectLookupFailures(t *testing.T) {
	cases := map[string]struct {
		err  error
		code connect.Code
	}{
		"no such object":    {ErrObjectNotFound, connect.CodeNotFound},
		"the store failing": {errors.New("connection refused"), connect.CodeInternal},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tenantID := uuid.New()
			h := &Handler{repo: &lookupRepo{err: tc.err}, storage: noopStorage{}, policy: allowAll{}, presign: testPresignConfig()}
			err := h.DeleteObject(purgeCtx(tenantID), "docs", uuid.NewString(), "1", false, false)
			if got := connect.CodeOf(err); got != tc.code {
				t.Fatalf("code = %v (%v), want %v", got, err, tc.code)
			}
			if strings.Contains(err.Error(), "no rows") {
				t.Errorf("the driver's text reached the caller: %v", err)
			}
		})
	}
}

func TestObjectLookupError(t *testing.T) {
	if err := objectLookupError(ErrObjectNotFound); connect.CodeOf(err) != connect.CodeNotFound ||
		!strings.HasSuffix(err.Error(), ErrObjectNotFound.Error()) {
		t.Errorf("not found = %v, want NotFound: %v", err, ErrObjectNotFound)
	}
	// A store may wrap it with its own detail; the caller still gets the
	// API's words, not the store's.
	wrapped := fmt.Errorf("get object: no rows in result set: %w", ErrObjectNotFound)
	if err := objectLookupError(wrapped); connect.CodeOf(err) != connect.CodeNotFound ||
		strings.Contains(err.Error(), "no rows") {
		t.Errorf("a wrapped not-found = %v, want NotFound without the store's text", err)
	}
	if err := objectLookupError(errors.New("timeout")); connect.CodeOf(err) != connect.CodeInternal {
		t.Errorf("a store error = %v, want Internal", err)
	}
}
