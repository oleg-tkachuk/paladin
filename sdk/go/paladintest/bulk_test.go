package paladintest_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// overlap is long enough that downloads started together overlap.
const overlap = 20 * time.Millisecond

func TestDownloadManyReadsEveryObjectAndReportsEachFailure(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	var names []string
	want := map[string]string{}
	for i := range 6 {
		o := srv.Put(srv.Collection(), fmt.Sprintf("k%d", i), "text/plain", []byte(fmt.Sprintf("body %d", i)))
		names = append(names, o.GetName())
		want[o.GetName()] = fmt.Sprintf("body %d", i)
	}
	missing := srv.Collection().String() + "/objects/00000000-0000-4000-8000-000000000000"
	var (
		mu  sync.Mutex
		got = map[string]string{}
	)
	failures := paladin.DownloadMany(context.Background(), p.Data, append(names, missing), 2,
		func(name string, r *paladin.ObjectReader) error {
			b, err := io.ReadAll(r)
			mu.Lock()
			defer mu.Unlock()
			got[name] = string(b)
			return err
		})
	for name, body := range want {
		if got[name] != body {
			t.Errorf("%s = %q, want %q", name, got[name], body)
		}
	}
	if len(failures) != 1 || !errors.Is(failures[missing], paladin.ErrNotFound) {
		t.Errorf("failures = %v, want only the missing object, as NotFound", failures)
	}
}

func TestDownloadManyKeepsToItsConcurrency(t *testing.T) {
	const limit = 3
	srv := paladintest.New(t)
	p := srv.Connect()
	var names []string
	for i := range 9 {
		names = append(names, srv.Put(srv.Collection(), fmt.Sprint(i), "text/plain", []byte("x")).GetName())
	}
	var active, peak atomic.Int32
	failures := paladin.DownloadMany(context.Background(), p.Data, names, limit, func(string, *paladin.ObjectReader) error {
		n := active.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(overlap)
		active.Add(-1)
		return nil
	})
	if failures != nil || peak.Load() != limit {
		t.Errorf("failures %v, peak %d; want none and %d at once", failures, peak.Load(), limit)
	}
}

func TestDownloadManyStopsOpeningWhenCancelled(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failures := paladin.DownloadMany(ctx, p.Data, []string{"a", "b"}, 1, func(string, *paladin.ObjectReader) error { return nil })
	if len(failures) != 2 {
		t.Errorf("failures = %v, want both names, cancelled or failed", failures)
	}
}

func TestUploadManyStoresEveryInputAndReportsEachFailure(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	var inputs []paladin.UploadInput
	for i := range 6 {
		body := []byte(fmt.Sprintf("body %d", i))
		inputs = append(inputs, paladin.UploadInput{
			Parent: srv.Collection().String(), Key: fmt.Sprintf("k%d", i), ContentType: "text/plain",
			Size: int64(len(body)), Body: bytes.NewReader(body),
		})
	}
	inputs = append(inputs, paladin.UploadInput{Parent: srv.Collection().String(), Key: "bad", Size: 1}) // no body
	objects, failures := paladin.UploadMany(context.Background(), p.Data, inputs, 2, paladin.UploadOptions{})
	for i := range 6 {
		got, ok := srv.Content(objects[i].GetName())
		if want := fmt.Sprintf("body %d", i); !ok || string(got) != want {
			t.Errorf("input %d stored %q, want %q", i, got, want)
		}
	}
	if objects[6] != nil || len(failures) != 1 || !errors.Is(failures[6], paladin.ErrUploadBody) {
		t.Errorf("objects[6] = %v, failures = %v; want nil and only input 6, as ErrUploadBody", objects[6], failures)
	}
}

func TestUploadManyAbortsOnlyTheMultipartUploadThatFailed(t *testing.T) {
	srv := paladintest.New(t)
	transfer, err := paladin.NewTransfer(paladin.WithTransferAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	p := srv.Connect(paladin.WithTransfer(transfer))
	body := bytes.Repeat([]byte("x"), paladintest.PartSize+1) // two parts
	inputs := []paladin.UploadInput{
		{Parent: srv.Collection().String(), Key: "good", Size: int64(len(body)), Body: bytes.NewReader(body)},
		{Parent: srv.Collection().String(), Key: "bad", Size: int64(len(body)), Body: bytes.NewReader(body)},
	}
	// One at a time, so the first upload's two parts are the first two PUTs;
	// every later one is refused.
	var puts atomic.Int32
	srv.FailStorage(func(r *http.Request) (int, string) {
		if r.Method == http.MethodPut && puts.Add(1) > 2 {
			return http.StatusForbidden, "refused"
		}
		return 0, ""
	})
	objects, failures := paladin.UploadMany(context.Background(), p.Data, inputs, 1,
		paladin.UploadOptions{MultipartThreshold: paladintest.PartSize})
	if got, ok := srv.Content(objects[0].GetName()); !ok || !bytes.Equal(got, body) {
		t.Error("the first upload was not stored whole")
	}
	if objects[1] != nil || len(failures) != 1 || failures[1] == nil {
		t.Errorf("objects[1] = %v, failures = %v; want nil and only input 1", objects[1], failures)
	}
	aborts := 0
	for _, r := range srv.Requests() {
		if strings.HasSuffix(r.Procedure, "/AbortMultipartUpload") {
			aborts++
		}
	}
	if aborts != 1 {
		t.Errorf("%d multipart uploads aborted, want the failed one", aborts)
	}
}

func TestUploadManyKeepsToItsConcurrency(t *testing.T) {
	const limit = 3
	srv := paladintest.New(t)
	var active, peak atomic.Int32
	srv.FailStorage(func(*http.Request) (int, string) {
		n := active.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(overlap)
		active.Add(-1)
		return 0, ""
	})
	p := srv.Connect()
	var inputs []paladin.UploadInput
	for i := range 9 {
		inputs = append(inputs, paladin.UploadInput{
			Parent: srv.Collection().String(), Key: fmt.Sprint(i), Size: 1, Body: bytes.NewReader([]byte("x")),
		})
	}
	_, failures := paladin.UploadMany(context.Background(), p.Data, inputs, limit, paladin.UploadOptions{})
	if failures != nil || peak.Load() != limit {
		t.Errorf("failures %v, peak %d; want none and %d at once", failures, peak.Load(), limit)
	}
}

func TestUploadManyRefusesSessionOptions(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	inputs := []paladin.UploadInput{{Parent: srv.Collection().String(), Size: 1, Body: bytes.NewReader([]byte("x"))}}
	_, failures := paladin.UploadMany(context.Background(), p.Data, inputs, 1,
		paladin.UploadOptions{OnSession: func(paladin.UploadSession) {}})
	if !errors.Is(failures[0], paladin.ErrBulkSession) {
		t.Errorf("failures = %v, want ErrBulkSession", failures)
	}
	if len(srv.Requests()) != 0 {
		t.Error("a refused UploadMany made calls")
	}
}

func TestUploadManyStopsStartingWhenCancelled(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	inputs := []paladin.UploadInput{
		{Parent: srv.Collection().String(), Size: 1, Body: bytes.NewReader([]byte("x"))},
		{Parent: srv.Collection().String(), Size: 1, Body: bytes.NewReader([]byte("y"))},
	}
	_, failures := paladin.UploadMany(ctx, p.Data, inputs, 1, paladin.UploadOptions{})
	if len(failures) != 2 {
		t.Errorf("failures = %v, want both inputs, cancelled or failed", failures)
	}
}
