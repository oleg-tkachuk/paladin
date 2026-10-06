package paladin

import (
	"context"
	"errors"
	"strconv"
	"sync"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// DefaultBulkConcurrency is how many objects DownloadMany and UploadMany
// move at once when asked for no other number.
const DefaultBulkConcurrency = 8

// ErrBulkSession is UploadMany given OnSession or Resume: each names one
// multipart upload, so they belong to Upload.
var ErrBulkSession = errors.New("paladin: UploadMany takes no OnSession or Resume; resume one upload with Upload")

// DownloadMany downloads names, concurrency at a time (DefaultBulkConcurrency
// when it is not positive), handing each one's reader to fn as it opens. fn
// runs concurrently with itself, reads the reader and need not close it. It
// returns the names that failed — to open, or in fn — with their errors,
// and nil when none did: one bad object does not stop the rest. A cancelled
// ctx stops opening more.
func DownloadMany(ctx context.Context, data *DataPlane, names []string, concurrency int,
	fn func(name string, r *ObjectReader) error,
) map[string]error {
	failed := bounded(ctx, len(names), concurrency, func(i int) error {
		r, err := Download(ctx, data, names[i], DownloadOptions{})
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		return fn(names[i], r)
	})
	if failed == nil {
		return nil
	}
	failures := make(map[string]error, len(failed))
	for i, err := range failed {
		failures[names[i]] = err
	}
	return failures
}

// UploadMany uploads inputs, concurrency at a time (DefaultBulkConcurrency
// when it is not positive), each as Upload does with opts, and returns the
// objects in the order of inputs — nil for one that failed — with the
// indexes of those that failed and their errors, nil when none did: one bad
// input does not stop the rest, and each is completed or, multipart, aborted
// on its own. A cancelled ctx stops starting more.
//
// Up to concurrency uploads are in flight, each with up to
// opts.PartConcurrency parts; a Stream input holds its parts in flight in
// memory. opts.OnSession and opts.Resume are refused with ErrBulkSession:
// an upload to resume after a crash goes through Upload. A WithIdempotencyKey
// key on ctx becomes key + "/" + the input's index for each input, so
// repeating the same UploadMany repeats each upload rather than colliding.
func UploadMany(ctx context.Context, data *DataPlane, inputs []UploadInput, concurrency int,
	opts UploadOptions,
) ([]*datav1.Object, map[int]error) {
	objects := make([]*datav1.Object, len(inputs))
	if opts.OnSession != nil || opts.Resume != nil {
		failures := make(map[int]error, len(inputs))
		for i := range inputs {
			failures[i] = ErrBulkSession
		}
		return objects, failures
	}
	failures := bounded(ctx, len(inputs), concurrency, func(i int) error {
		obj, err := Upload(itemKey(ctx, strconv.Itoa(i)), data, inputs[i], opts)
		objects[i] = obj
		return err
	})
	return objects, failures
}

// bounded runs fn for every index below n, concurrency at a time
// (DefaultBulkConcurrency when it is not positive), and returns the indexes
// whose fn failed with their errors, nil when none did. Once ctx is done no
// more are started; those left fail with its error.
func bounded(ctx context.Context, n, concurrency int, fn func(i int) error) map[int]error {
	if concurrency <= 0 {
		concurrency = DefaultBulkConcurrency
	}
	var (
		mu       sync.Mutex
		failures map[int]error
		wg       sync.WaitGroup
	)
	fail := func(i int, err error) {
		mu.Lock()
		defer mu.Unlock()
		if failures == nil {
			failures = map[int]error{}
		}
		failures[i] = err
	}
	slots := make(chan struct{}, concurrency)
	for i := range n {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			fail(i, ctx.Err())
			continue
		}
		wg.Go(func() {
			defer func() { <-slots }()
			if err := fn(i); err != nil {
				fail(i, err)
			}
		})
	}
	wg.Wait()
	return failures
}
