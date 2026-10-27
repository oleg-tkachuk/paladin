package adapters

import (
	"math/rand/v2"
	"slices"
	"sort"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// The orders the walk is tried under: by id, and by a text and a number field
// whose values repeat, so the id tie-breaker carries the keyset.
var mergeOrders = []keyset.Order{
	{Field: objectOrderByID}, {Field: objectOrderByID, Desc: true},
	{Field: objecth.OrderByKey}, {Field: objecth.OrderByKey, Desc: true},
	{Field: objecth.OrderBySize}, {Field: objecth.OrderBySize, Desc: true},
}

// Walking every page of a multi-branch listing yields exactly the union of
// the branches, in order, once each — whatever the order, the page size and
// however the branches overlap. The hazard is a branch that fills its page
// while another, sparser branch reaches further: merging past the full
// branch's last row would skip that branch's next rows for good.
func TestMergeCandidatePagesWalksTheExactUnion(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for trial := 0; trial < 600; trial++ {
		order := mergeOrders[trial%len(mergeOrders)]
		universe := make([]sqlc.Object, 40+rng.IntN(60))
		for i := range universe {
			size := int64(rng.IntN(5))
			universe[i] = sqlc.Object{
				ID:        pgtype.UUID{Bytes: uuid.NewV7(), Valid: true},
				Path:      []string{"a", "b", "B", "c/d"}[rng.IntN(4)],
				SizeBytes: &size,
			}
		}
		sortObjects(order, universe)

		nBranches := 1 + rng.IntN(4)
		branches := make([][]sqlc.Object, nBranches)
		want := map[[16]byte]bool{}
		for b := range branches {
			density := rng.Float64()
			for _, o := range universe {
				if rng.Float64() < density {
					branches[b] = append(branches[b], o)
					want[o.ID.Bytes] = true
				}
			}
		}
		pageSize := 1 + rng.IntN(12)

		var got []sqlc.Object
		var after *keyset.Cursor
		for pages := 0; ; pages++ {
			if pages > len(universe)+2 {
				t.Fatalf("trial %d: walk did not terminate", trial)
			}
			fetched := make([][]sqlc.ListObjectsRow, nBranches)
			for b, objs := range branches {
				// The query's ANY() returns a page in no particular order.
				page := dbPage(order, objs, after, pageSize)
				rng.Shuffle(len(page), func(i, j int) { page[i], page[j] = page[j], page[i] })
				fetched[b] = page
			}
			rows, next, more := mergeCandidatePages(fetched, pageSize, order)
			if len(rows) > pageSize {
				t.Fatalf("trial %d: page of %d > pageSize %d", trial, len(rows), pageSize)
			}
			for _, r := range rows {
				got = append(got, r.Object)
			}
			if !more {
				break
			}
			if next.Order != order {
				t.Fatalf("trial %d: cursor minted for %+v, listing %+v", trial, next.Order, order)
			}
			after = &next
		}

		if len(got) != len(want) {
			t.Fatalf("trial %d (%+v, page %d, %d branches): walked %d rows, union has %d",
				trial, order, pageSize, nBranches, len(got), len(want))
		}
		for i, o := range got {
			if !want[o.ID.Bytes] {
				t.Fatalf("trial %d: %x is in no branch", trial, o.ID.Bytes)
			}
			if i > 0 && compareObjectRows(order, got[i-1], o) >= 0 {
				t.Fatalf("trial %d (%+v): rows out of order or repeated at %d", trial, order, i)
			}
		}
	}
}

// dbPage is what one branch's query returns: its first n rows after the
// cursor, in order — the keyset search_object_ids applies.
func dbPage(order keyset.Order, objs []sqlc.Object, after *keyset.Cursor, n int) []sqlc.ListObjectsRow {
	var out []sqlc.ListObjectsRow
	for _, o := range objs {
		if after != nil && !pastCursor(order, o, *after) {
			continue
		}
		out = append(out, sqlc.ListObjectsRow{Object: o})
		if len(out) == n {
			break
		}
	}
	return out
}

// pastCursor reports whether o sorts after the cursor's row, which it knows
// by its sort value and id only — as the database does.
func pastCursor(order keyset.Order, o sqlc.Object, c keyset.Cursor) bool {
	probe := sqlc.Object{ID: pgtype.UUID{Bytes: c.ID, Valid: true}}
	switch order.Field {
	case objecth.OrderByKey:
		probe.Path = c.Value
	case objecth.OrderBySize:
		var n int64
		for _, d := range c.Value {
			n = n*10 + int64(d-'0')
		}
		probe.SizeBytes = &n
	}
	return compareObjectRows(order, o, probe) > 0
}

func sortObjects(order keyset.Order, objs []sqlc.Object) {
	sort.Slice(objs, func(i, j int) bool { return compareObjectRows(order, objs[i], objs[j]) < 0 })
}

// Text sorts bytewise, as COLLATE "C" does: capitals before lowercase, and a
// '/' before letters.
func TestCompareObjectRows_TextIsBytewise(t *testing.T) {
	keys := []string{"b", "a", "B", "a/z", "A"}
	objs := make([]sqlc.Object, len(keys))
	for i, k := range keys {
		objs[i] = sqlc.Object{Path: k, ID: pgtype.UUID{Bytes: uuid.NewV7(), Valid: true}}
	}
	sortObjects(keyset.Order{Field: objecth.OrderByKey}, objs)
	got := make([]string, len(objs))
	for i, o := range objs {
		got[i] = o.Path
	}
	if want := []string{"A", "B", "a", "a/z", "b"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}
