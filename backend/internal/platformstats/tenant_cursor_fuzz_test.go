package platformstats

import (
	"testing"
)

// fuzzRankedTenants is the ranking the fuzz target pages through, already in
// tenantBefore order: counts descending, ties by tenant id.
var fuzzRankedTenants = []TenantStat{
	{TenantID: "0b6f7c1e-4f6a-4a39-9d55-3a8c2b7e1f00", TotalCount: 9},
	{TenantID: "3a823fd4-0b3d-4ce2-a280-93b8d75cc07b", TotalCount: 9},
	{TenantID: "7d1e2f3a-0b4c-4d5e-8f60-718293a4b5c6", TotalCount: 2},
	{TenantID: "9e8d7c6b-5a49-4382-a170-6f5e4d3c2b1a", TotalCount: 0},
}

// The census cursor round-trips the (count, tenant) it was made from.
func FuzzTenantCursorRoundTrip(f *testing.F) {
	f.Add(int64(9), "3a823fd4-0b3d-4ce2-a280-93b8d75cc07b")
	f.Add(int64(-1), "a/b")
	f.Fuzz(func(t *testing.T, count int64, id string) {
		gotCount, gotID, ok := decodeTenantCursor(encodeTenantCursor(count, id))
		if !ok || gotCount != count || gotID != id {
			t.Fatalf("(%d, %q) decodes back to (%d, %q, %v)", count, id, gotCount, gotID, ok)
		}
	})
}

// Any census page token, at any page size: a page that is a run of the
// ranking, a remainder that counts what follows it, and a next token only
// while something does. A token that does not decode starts from the first
// page (TenantPage.After), never from an index outside the ranking.
func FuzzPageTenants(f *testing.F) {
	for _, after := range []string{
		"",
		encodeTenantCursor(9, "0b6f7c1e-4f6a-4a39-9d55-3a8c2b7e1f00"),
		encodeTenantCursor(-1, ""),
		"%%%",
	} {
		f.Add(after, 1)
	}
	f.Fuzz(func(t *testing.T, after string, size int) {
		page, next, rest := pageTenants(fuzzRankedTenants, TenantPage{Size: size, After: after})
		start := len(fuzzRankedTenants) - int(rest) - len(page)
		if rest < 0 || start < 0 {
			t.Fatalf("after %q: a page of %d with %d following, out of %d", after, len(page), rest, len(fuzzRankedTenants))
		}
		for i, row := range page {
			if row.TenantID != fuzzRankedTenants[start+i].TenantID {
				t.Fatalf("after %q: page row %d is %+v, want %+v", after, i, row, fuzzRankedTenants[start+i])
			}
		}
		if (next != "") != (rest > 0 && len(page) > 0) {
			t.Fatalf("after %q: next %q with %d following a page of %d", after, next, rest, len(page))
		}
	})
}
