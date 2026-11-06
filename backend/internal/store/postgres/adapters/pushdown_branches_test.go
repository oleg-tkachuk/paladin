package adapters

import (
	"cmp"
	"errors"
	"slices"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/filter/cel"
)

func byValue(a, b int) bool { return a < b }
func itself(v int) int      { return v }

// The union of the branches' pages, in order, each row once, cut to the page.
func TestMergeBranchPages(t *testing.T) {
	for name, tc := range map[string]struct {
		pages    [][]int
		pageSize int
		want     []int
	}{
		"one branch kept as it came": {[][]int{{3, 1}}, 2, []int{3, 1}},
		"interleaved":                {[][]int{{1, 4}, {2, 3}}, 4, []int{1, 2, 3, 4}},
		"a row in both, once":        {[][]int{{1, 2}, {2, 5}}, 4, []int{1, 2, 5}},
		"cut to the page":            {[][]int{{1, 3}, {2, 4}}, 2, []int{1, 2}},
		"nothing":                    {[][]int{{}, {}}, 2, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := mergeBranchPages(tc.pages, tc.pageSize, byValue, itself)
			if !slices.Equal(got, tc.want) {
				t.Errorf("merged %v, want %v", got, tc.want)
			}
		})
	}
}

// Walking pages of a fan-out with a cursor returns every row of every branch
// exactly once, however the branches interleave: a branch that filled its page
// cannot hold a row that sorts before the cut.
func TestFanOutWalksEveryRowOnce(t *testing.T) {
	branchRows := [][]int{{1, 5, 6, 9}, {2, 3, 5, 8}, {7}}
	const pageSize = 2
	branches := make([]cel.Pushdown, len(branchRows))
	for i := range branches {
		branches[i] = cel.Pushdown{In: map[string][]string{"branch": {string(rune('a' + i))}}}
	}
	after := 0
	var seen []int
	for range 10 {
		page, err := fanOut(branches, pageSize, func(pd cel.Pushdown) ([]int, error) {
			rows := branchRows[pd.In["branch"][0][0]-'a']
			var out []int
			for _, r := range rows {
				if r > after && len(out) < pageSize {
					out = append(out, r)
				}
			}
			return out, nil
		}, byValue, itself)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, page...)
		if len(page) < pageSize {
			break
		}
		after = page[len(page)-1]
	}
	want := []int{1, 2, 3, 5, 6, 7, 8, 9}
	if !slices.Equal(seen, want) {
		t.Errorf("walked %v, want %v", seen, want)
	}
	if !slices.IsSortedFunc(seen, cmp.Compare[int]) {
		t.Errorf("walk out of order: %v", seen)
	}
}

// A branch's error is the list's error.
func TestFanOutReturnsABranchError(t *testing.T) {
	boom := errors.New("boom")
	_, err := fanOut([]cel.Pushdown{{}, {}}, 2, func(cel.Pushdown) ([]int, error) { return nil, boom }, byValue, itself)
	if !errors.Is(err, boom) {
		t.Errorf("err %v, want %v", err, boom)
	}
}
