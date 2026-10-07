package paladin_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// The cookbook's uploadDurably against what an earlier attempt can leave at
// its key: each case is a retry after a lost answer or a crash.

const durableKey = "report.pdf"

func durableInput(srv *paladintest.Server, body []byte) paladin.UploadInput {
	return paladin.UploadInput{
		Parent: srv.Collection().String(), Key: durableKey, ContentType: "application/pdf",
		Size: int64(len(body)), Body: bytes.NewReader(body),
	}
}

// registered registers body at durableKey as an attempt that died after
// UploadObject would: PENDING, and with put its bytes stored as well.
func registered(t *testing.T, srv *paladintest.Server, p *paladin.Paladin, body []byte, put bool) *datav1.Object {
	t.Helper()
	sum := sha256Of(body)
	resp, err := p.Data.Object.UploadObject(context.Background(), &datav1.UploadObjectRequest{
		Parent: srv.Collection().String(), Key: durableKey, ContentType: "application/pdf",
		SizeHintBytes: int64(len(body)), ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
		ChecksumValue: sum, Metadata: map[string]string{contentSHA256: sum},
	})
	if err != nil {
		t.Fatal(err)
	}
	if put {
		if _, err := p.Data.Transfer().Put(context.Background(), resp.GetUploadUrl(), bytes.NewReader(body), int64(len(body))); err != nil {
			t.Fatal(err)
		}
	}
	return resp.GetObject()
}

func durably(t *testing.T, srv *paladintest.Server, p *paladin.Paladin, body []byte) (*datav1.Object, int, error) {
	t.Helper()
	return uploadDurably(context.Background(), p.Data, durableInput(srv, body),
		&sessionStore{sessions: map[string]paladin.UploadSession{}}, 3)
}

func stored(t *testing.T, srv *paladintest.Server, obj *datav1.Object, want []byte) {
	t.Helper()
	if got, ok := srv.Content(obj.GetName()); !ok || !bytes.Equal(got, want) {
		t.Errorf("%s holds %q, want %q", obj.GetName(), got, want)
	}
}

func TestDurableUploadTakesTheObjectAnAnswerWasLostFor(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	body := []byte("quarterly report")
	first, _, err := durably(t, srv, p, body) // completed; say its answer never came back
	if err != nil {
		t.Fatal(err)
	}
	again, attempts, err := durably(t, srv, p, body)
	if err != nil || again.GetName() != first.GetName() || attempts != 1 {
		t.Fatalf("retry = %s after %d attempts, %v; want %s, 1, nil", again.GetName(), attempts, err, first.GetName())
	}
	stored(t, srv, again, body)
}

func TestDurableUploadCompletesAnObjectWhoseBytesLanded(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	body := []byte("quarterly report")
	pending := registered(t, srv, p, body, true)
	obj, attempts, err := durably(t, srv, p, body)
	if err != nil || obj.GetName() != pending.GetName() || attempts != 1 {
		t.Fatalf("got %s after %d attempts, %v; want the pending %s, 1, nil", obj.GetName(), attempts, err, pending.GetName())
	}
	stored(t, srv, obj, body)
}

func TestDurableUploadReplacesAnAttemptThatStoredNothing(t *testing.T) {
	for name, fail := range map[string]bool{"pending": false, "failed": true} {
		t.Run(name, func(t *testing.T) {
			srv := paladintest.New(t)
			p := srv.Connect()
			body := []byte("quarterly report")
			stale := registered(t, srv, p, body, false)
			if fail {
				srv.MarkFailed(stale.GetName())
			}
			obj, attempts, err := durably(t, srv, p, body)
			if err != nil || obj.GetName() == stale.GetName() || attempts != 2 {
				t.Fatalf("got %s after %d attempts, %v; want a new object on the second", obj.GetName(), attempts, err)
			}
			stored(t, srv, obj, body)
		})
	}
}

func TestDurableUploadLeavesAnotherObjectAtItsKey(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	if _, _, err := durably(t, srv, p, []byte("last quarter")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := durably(t, srv, p, []byte("this quarter")); !errors.Is(err, errKeyTaken) {
		t.Fatalf("err = %v, want errKeyTaken", err)
	}
}

func TestDurableUploadLeavesTheTrashAlone(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	body := []byte("quarterly report")
	obj, _, err := durably(t, srv, p, body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Data.Object.DeleteObject(context.Background(), &datav1.DeleteObjectRequest{
		Name: obj.GetName(), ResourceVersion: obj.GetResourceVersion(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := durably(t, srv, p, body); !errors.Is(err, errKeyTaken) {
		t.Fatalf("err = %v, want errKeyTaken: restoring or purging is not a retry's call", err)
	}
}

func TestDurableUploadNeedsAKeyAndABody(t *testing.T) {
	srv := paladintest.New(t)
	in := durableInput(srv, []byte("x"))
	in.Key = ""
	if _, _, err := uploadDurably(context.Background(), srv.Connect().Data, in,
		&sessionStore{sessions: map[string]paladin.UploadSession{}}, 3); !errors.Is(err, errNoKey) {
		t.Fatalf("err = %v, want errNoKey", err)
	}
}
