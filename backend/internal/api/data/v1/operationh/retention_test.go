package operationh

import (
	"context"
	"testing"
	"time"
	"uuid"
)

const testRetention = 14 * 24 * time.Hour

func TestExpiresAt(t *testing.T) {
	done := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		doneAt    *time.Time
		retention time.Duration
		want      *time.Time
	}{
		{"finished, kept for a while", &done, testRetention, new(done.Add(testRetention))},
		{"still running", nil, testRetention, nil},
		{"kept indefinitely", &done, 0, nil},
		{"negative retention keeps them too", &done, -time.Hour, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expiresAt(tc.doneAt, tc.retention)
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("expiresAt = %v, want nil", *got)
			case tc.want != nil && (got == nil || !got.Equal(*tc.want)):
				t.Errorf("expiresAt = %v, want %v", got, *tc.want)
			}
		})
	}
}

// Get and List both report when each finished operation will be removed, by
// the retention the handler was given.
func TestOperationsReportTheirExpiry(t *testing.T) {
	tid := uuid.New()
	done := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	finished := Operation{OperationID: uuid.New(), TenantID: tid, State: StateSucceeded, DoneAt: &done}
	running := Operation{OperationID: uuid.New(), TenantID: tid, State: StateRunning}
	fr := &fakeRepo{
		getFn: func(context.Context, uuid.UUID, uuid.UUID) (Operation, error) { return finished, nil },
		listFn: func(context.Context, ListArgs) ([]Operation, string, error) {
			return []Operation{finished, running}, "", nil
		},
	}
	h := NewHandler(fr, allowAuthorizer())
	h.SetFinishedRetention(testRetention)
	want := done.Add(testRetention)

	got, err := h.GetOperation(authedCtx(tid), finished.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpireAt == nil || !got.ExpireAt.Equal(want) {
		t.Errorf("get: ExpireAt = %v, want %v", got.ExpireAt, want)
	}

	list, _, err := h.ListOperations(authedCtx(tid), ListInput{PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if list[0].ExpireAt == nil || !list[0].ExpireAt.Equal(want) {
		t.Errorf("list: finished ExpireAt = %v, want %v", list[0].ExpireAt, want)
	}
	if list[1].ExpireAt != nil {
		t.Errorf("list: running ExpireAt = %v, want nil", *list[1].ExpireAt)
	}
}
