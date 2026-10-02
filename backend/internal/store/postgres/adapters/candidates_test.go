package adapters

import (
	"bytes"
	"math/rand/v2"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// Walking every page of a multi-branch listing yields exactly the union of
// the branches, in id order, once each — whatever the page size and however
// the branches overlap. The hazard is a branch that fills its page while
// another, sparser branch reaches further: merging past the full branch's
// last id would skip that branch's next rows for good.
func TestMergeCandidatePagesWalksTheExactUnion(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := 0; trial < 500; trial++ {
		universe := make([]uuid.UUID, 40+rng.IntN(60))
		for i := range universe {
			universe[i] = uuid.Must(uuid.NewV7())
		}
		sortIDs(universe)

		nBranches := 1 + rng.IntN(4)
		branches := make([][]uuid.UUID, nBranches)
		want := map[uuid.UUID]bool{}
		for b := range branches {
			density := rng.Float64()
			for _, id := range universe {
				if rng.Float64() < density {
					branches[b] = append(branches[b], id)
					want[id] = true
				}
			}
		}
		pageSize := 1 + rng.IntN(12)

		var got []uuid.UUID
		var after uuid.UUID
		for pages := 0; ; pages++ {
			if pages > len(universe)+2 {
				t.Fatalf("trial %d: walk did not terminate", trial)
			}
			fetched := make([][]sqlc.ListObjectsRow, nBranches)
			for b, ids := range branches {
				fetched[b] = dbPage(ids, after, pageSize)
			}
			rows, next, more := mergeCandidatePages(fetched, pageSize)
			if len(rows) > pageSize {
				t.Fatalf("trial %d: page of %d > pageSize %d", trial, len(rows), pageSize)
			}
			for _, r := range rows {
				got = append(got, uuid.UUID(r.Object.ID.Bytes))
			}
			if !more {
				break
			}
			after = next
		}

		if len(got) != len(want) {
			t.Fatalf("trial %d (page %d, %d branches): walked %d ids, union has %d",
				trial, pageSize, nBranches, len(got), len(want))
		}
		for i, id := range got {
			if !want[id] {
				t.Fatalf("trial %d: %s is in no branch", trial, id)
			}
			if i > 0 && bytes.Compare(got[i-1][:], id[:]) >= 0 {
				t.Fatalf("trial %d: ids out of order or repeated at %d", trial, i)
			}
		}
	}
}

// dbPage is what one branch's query returns: its first n ids after `after`.
func dbPage(ids []uuid.UUID, after uuid.UUID, n int) []sqlc.ListObjectsRow {
	var out []sqlc.ListObjectsRow
	for _, id := range ids {
		if after != (uuid.UUID{}) && bytes.Compare(id[:], after[:]) <= 0 {
			continue
		}
		out = append(out, sqlc.ListObjectsRow{Object: sqlc.Object{ID: pgtype.UUID{Bytes: id, Valid: true}}})
		if len(out) == n {
			break
		}
	}
	return out
}

func sortIDs(ids []uuid.UUID) {
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
}
