package paladin_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

func TestDelete(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()

	for _, permanent := range []bool{false, true} {
		obj := srv.Put(srv.Collection(), "", "text/plain", []byte("x"))
		deleted, err := paladin.Delete(ctx, p.Data, obj.GetName(), paladin.DeleteOptions{Permanent: permanent})
		if err != nil || !deleted {
			t.Fatalf("permanent=%v: deleted=%v err=%v", permanent, deleted, err)
		}
		if _, err := p.Data.Object.GetObject(ctx, &datav1.GetObjectRequest{Name: obj.GetName()}); !errors.Is(err, paladin.ErrNotFound) {
			t.Errorf("permanent=%v: the object is still there: %v", permanent, err)
		}
		// Again: already gone is not an error.
		if deleted, err := paladin.Delete(ctx, p.Data, obj.GetName(), paladin.DeleteOptions{Permanent: permanent}); err != nil || deleted {
			t.Errorf("permanent=%v, again: deleted=%v err=%v, want nothing done and no error", permanent, deleted, err)
		}
	}
}

// The object changes between Delete's read and its delete: it reads it again
// and deletes at the new version.
func TestDeleteRereadsAChangedObject(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	obj := srv.Put(srv.Collection(), "", "text/plain", []byte("x"))
	srv.FailRPC("/paladin.data.v1.ObjectService/DeleteObject", 1, connect.CodeAborted)
	deleted, err := paladin.Delete(context.Background(), p.Data, obj.GetName(), paladin.DeleteOptions{Permanent: true})
	if err != nil || !deleted {
		t.Fatalf("deleted=%v err=%v, want the second attempt to delete it", deleted, err)
	}
	if n := len(srv.Calls("/paladin.data.v1.ObjectService/GetObject", nil)); n != 2 {
		t.Errorf("read %d times, want once per attempt (2)", n)
	}
}

// A conflict on every attempt is returned after DeleteAttempts reads.
func TestDeleteGivesUpOnAPersistentConflict(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	obj := srv.Put(srv.Collection(), "", "text/plain", []byte("x"))
	srv.FailRPC("/paladin.data.v1.ObjectService/DeleteObject", paladin.DeleteAttempts, connect.CodeAborted)
	_, err := paladin.Delete(context.Background(), p.Data, obj.GetName(), paladin.DeleteOptions{})
	if !errors.Is(err, paladin.ErrVersionConflict) {
		t.Fatalf("err = %v, want the conflict", err)
	}
	if n := len(srv.Calls("/paladin.data.v1.ObjectService/GetObject", nil)); n != paladin.DeleteAttempts {
		t.Errorf("read %d times, want %d", n, paladin.DeleteAttempts)
	}
}

// A refusal other than a conflict is returned at once: a soft delete in a
// public collection is one.
func TestDeleteReturnsAnyOtherRefusal(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	obj := srv.Put(srv.PublicCollection(), "", "image/jpeg", []byte("x"))
	_, err := paladin.Delete(context.Background(), p.Data, obj.GetName(), paladin.DeleteOptions{})
	if paladin.Reason(err) != commonv1.ErrorReason_ERROR_REASON_PUBLIC_COLLECTION_RULE {
		t.Fatalf("err = %v, want the public collection rule", err)
	}
	if n := len(srv.Calls("/paladin.data.v1.ObjectService/GetObject", nil)); n != 1 {
		t.Errorf("read %d times, want 1", n)
	}
}

// The object goes between Delete's read and its delete: someone else removed
// it, which is what was wanted.
func TestDeleteOfAnObjectThatWentMeanwhile(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	obj := srv.Put(srv.Collection(), "", "text/plain", []byte("x"))
	srv.FailRPC("/paladin.data.v1.ObjectService/DeleteObject", 1, connect.CodeNotFound)
	if deleted, err := paladin.Delete(context.Background(), p.Data, obj.GetName(), paladin.DeleteOptions{}); err != nil || deleted {
		t.Errorf("deleted=%v err=%v, want nothing done and no error", deleted, err)
	}
}
