package operations

import (
	"encoding/json"
	"testing"
)

// Every batch's response counts under the same names: the runner and the
// console read failed from any of them.
func TestEveryBatchResponseCountsUnderTheSameNames(t *testing.T) {
	counts := BatchCounts{Total: 3, Succeeded: 2, AlreadyApplied: 1, Failed: 1}
	for name, resp := range map[string]any{
		"delete":      BatchDeleteResponse{BatchCounts: counts},
		"copy":        BatchCopyResponse{BatchCounts: counts},
		"restore":     BatchRestoreResponse{BatchCounts: counts},
		"update tags": BatchUpdateTagsResponse{BatchCounts: counts},
	} {
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]float64{"total": 3, "succeeded": 2, "already_applied": 1, "failed": 1} {
			if fields[key] != want {
				t.Errorf("%s: %s = %v, want %v in %s", name, key, fields[key], want, raw)
			}
		}
		if failedCount(raw) != 1 {
			t.Errorf("%s: failedCount = %d, want 1", name, failedCount(raw))
		}
	}
}

func TestFailedCountOfWhatIsNoBatch(t *testing.T) {
	for _, raw := range []string{``, `not json`, `[]`, `{"error":"boom","code":"EXEC_FAILED"}`} {
		if got := failedCount([]byte(raw)); got != 0 {
			t.Errorf("failedCount(%q) = %d, want 0", raw, got)
		}
	}
}
