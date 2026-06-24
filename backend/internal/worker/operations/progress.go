package operations

import "context"

type progressKey struct{}

// ProgressFunc reports that `processed` of `total` items are done.
type ProgressFunc func(processed, total int)

// WithProgress attaches a progress reporter to ctx. The Runner installs one
// (see runner.withProgress) that throttles writes to the operation row so a
// polling client sees `processed: N / total: M` mid-flight instead of waiting
// for the terminal state.
func WithProgress(ctx context.Context, fn ProgressFunc) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

// ReportProgress reports incremental progress when a reporter is installed.
// No-op otherwise (unit tests, ad-hoc executor calls) — executors can call it
// unconditionally in their loops.
func ReportProgress(ctx context.Context, processed, total int) {
	if fn, ok := ctx.Value(progressKey{}).(ProgressFunc); ok && fn != nil {
		fn(processed, total)
	}
}
