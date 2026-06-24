package operations

import (
	"context"
	"testing"
)

func TestReportProgress(t *testing.T) {
	// No reporter installed → no-op, no panic.
	ReportProgress(context.Background(), 1, 2)

	var got [][2]int
	ctx := WithProgress(context.Background(), func(p, total int) {
		got = append(got, [2]int{p, total})
	})
	ReportProgress(ctx, 1, 3)
	ReportProgress(ctx, 3, 3)

	if len(got) != 2 {
		t.Fatalf("reporter called %d times, want 2", len(got))
	}
	if got[0] != [2]int{1, 3} || got[1] != [2]int{3, 3} {
		t.Errorf("progress = %v, want [[1 3] [3 3]]", got)
	}
}
