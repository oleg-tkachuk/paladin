package collectionh

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

func TestCollectionNotEmptyError_Message(t *testing.T) {
	cases := []struct {
		holding CollectionHolding
		want    string
	}{
		{CollectionHolding{Live: 1}, "collection still holds 1 object; delete them first"},
		{CollectionHolding{Live: 3}, "collection still holds 3 objects; delete them first"},
		{CollectionHolding{Trashed: 1}, "collection still holds 1 object in its trash; purge or restore them first"},
		{CollectionHolding{Live: 2, Trashed: 1}, "collection still holds 3 objects, 1 of them in its trash; delete, purge or restore them first"},
	}
	for _, tc := range cases {
		err := &CollectionNotEmptyError{Holding: tc.holding}
		if err.Error() != tc.want {
			t.Errorf("%+v: %q, want %q", tc.holding, err.Error(), tc.want)
		}
		if !errors.Is(err, ErrCollectionHasObjects) {
			t.Errorf("%+v: not an ErrCollectionHasObjects", tc.holding)
		}
	}
}

// A refused delete says what holds the collection, on the wire, in the
// reason's metadata — a trashed object included.
func TestDeleteCollection_SaysWhatHoldsIt(t *testing.T) {
	tid := uuid.New()
	fr := &fakeRepo{
		getFn: func(_ context.Context, tenant uuid.UUID, key string) (Collection, error) {
			return fullKey(tenant, key), nil
		},
		deleteTxFn: func(context.Context, uuid.UUID, string, int64) error { return ErrCollectionHasObjects },
		holdingFn: func(_ context.Context, tenant uuid.UUID, key string) (CollectionHolding, error) {
			if tenant != tid || key != "assets" {
				t.Errorf("counted %s/%s", tenant, key)
			}
			return CollectionHolding{Trashed: 1}, nil
		},
	}
	err := NewHandler(fr, allowAll()).DeleteCollection(authedCtx(tid), uuid.Nil(), "assets", 1)
	wantCode(t, err, connect.CodeFailedPrecondition)
	info := errorInfo(t, err)
	if info.GetReason() != commonv1.ErrorReason_ERROR_REASON_COLLECTION_NOT_EMPTY.String() {
		t.Errorf("reason = %q", info.GetReason())
	}
	if md := info.GetMetadata(); md[MetaLiveObjects] != "0" || md[MetaTrashedObjects] != "1" {
		t.Errorf("metadata = %v", md)
	}
}

// A count that fails leaves the refusal as it was, not hidden behind the
// count's own error.
func TestDeleteCollection_RefusalSurvivesAFailedCount(t *testing.T) {
	fr := &fakeRepo{
		getFn: func(_ context.Context, tenant uuid.UUID, key string) (Collection, error) {
			return fullKey(tenant, key), nil
		},
		deleteTxFn: func(context.Context, uuid.UUID, string, int64) error { return ErrCollectionHasObjects },
		holdingFn: func(context.Context, uuid.UUID, string) (CollectionHolding, error) {
			return CollectionHolding{}, errors.New("db down")
		},
	}
	err := NewHandler(fr, allowAll()).DeleteCollection(authedCtx(uuid.New()), uuid.Nil(), "assets", 1)
	wantCode(t, err, connect.CodeFailedPrecondition)
	if md := errorInfo(t, err).GetMetadata(); len(md) != 0 {
		t.Errorf("metadata = %v, want none", md)
	}
}

func errorInfo(t *testing.T, err error) *errdetails.ErrorInfo {
	t.Helper()
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("not a connect error: %v", err)
	}
	for _, d := range ce.Details() {
		v, derr := connectproto.UnmarshalErrorDetail(d)
		if derr != nil {
			t.Fatalf("detail does not decode: %v", derr)
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	t.Fatalf("no ErrorInfo on %v", err)
	return nil
}
