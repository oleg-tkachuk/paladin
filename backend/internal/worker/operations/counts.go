package operations

import "encoding/json"

// BatchCounts is what every batch's response counts, under the same JSON
// names: the items it was given, the ones it did — AlreadyApplied of them
// found done already by an earlier run (see applied.go) — and the ones it
// could not, each with its reason in the response's own failure list. A
// batch that runs to its end succeeds whatever it could not do; Failed is
// what tells the two apart.
type BatchCounts struct {
	Total          int `json:"total"`
	Succeeded      int `json:"succeeded"`
	AlreadyApplied int `json:"already_applied,omitempty"`
	Failed         int `json:"failed"`
}

// failedCount is how many objects a succeeded operation's response says it
// could not act on: 0 for one that is not a batch's.
func failedCount(response []byte) int64 {
	var c BatchCounts
	if json.Unmarshal(response, &c) != nil {
		return 0
	}
	return int64(c.Failed)
}
