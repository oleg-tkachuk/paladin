package platformstats

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"reflect"
	"testing"
)

// ranked builds n tenants already in sortTenants' order: counts n..1, so
// every count is distinct unless a test sets otherwise.
func ranked(n int) []TenantStat {
	out := make([]TenantStat, n)
	for i := range out {
		out[i] = TenantStat{TenantID: fmt.Sprintf("t%03d", i), TotalCount: int64(n - i)}
	}
	return out
}

func ids(ts []TenantStat) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.TenantID
	}
	return out
}

// Walking every page from the first must visit each tenant exactly once, in
// rank order, with the remaining count falling to zero on the last page.
func TestPageTenants_WalksEveryTenantOnce(t *testing.T) {
	cases := []struct {
		name    string
		tenants []TenantStat
		size    int
		pages   int
	}{
		{"empty", nil, 2, 1},
		{"fewer than a page", ranked(1), 2, 1},
		{"exactly a page", ranked(2), 2, 1},
		{"one past a page", ranked(3), 2, 2},
		{"equal counts break ties on tenant_id", []TenantStat{
			{TenantID: "a", TotalCount: 5}, {TenantID: "b", TotalCount: 5},
			{TenantID: "c", TotalCount: 5}, {TenantID: "d", TotalCount: 1},
		}, 1, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen []string
			page := TenantPage{Size: tc.size}
			for n := 1; ; n++ {
				got, next, rest := pageTenants(tc.tenants, page)
				seen = append(seen, ids(got)...)
				if want := int64(len(tc.tenants) - len(seen)); rest != want {
					t.Fatalf("page %d: rest = %d, want %d", n, rest, want)
				}
				if next == "" {
					if n != tc.pages {
						t.Fatalf("ended after %d page(s), want %d", n, tc.pages)
					}
					break
				}
				if n >= tc.pages {
					t.Fatalf("page %d still has a next token, want %d page(s)", n, tc.pages)
				}
				page.After = next
			}
			if want := ids(tc.tenants); len(want) != 0 && !reflect.DeepEqual(seen, want) {
				t.Errorf("walked %v, want %v", seen, want)
			}
		})
	}
}

func TestPageTenants_ClampsTheSize(t *testing.T) {
	tenants := ranked(maxTenantRows + 1)
	for _, size := range []int{0, -1, maxTenantRows + 1} {
		got, next, rest := pageTenants(tenants, TenantPage{Size: size})
		if len(got) != maxTenantRows || rest != 1 || next == "" {
			t.Errorf("size %d: %d row(s), rest %d, next %q — want the cap, one left, and a token",
				size, len(got), rest, next)
		}
	}
}

// A token that does not decode starts over rather than failing the census.
func TestPageTenants_ABadTokenStartsFromTheFirstPage(t *testing.T) {
	tenants := ranked(3)
	for _, token := range []string{"%%%", encodeRaw("no-separator"), encodeRaw("x/t001")} {
		got, _, _ := pageTenants(tenants, TenantPage{Size: 1, After: token})
		if len(got) != 1 || got[0].TenantID != "t000" {
			t.Errorf("token %q: got %v, want the first tenant", token, ids(got))
		}
	}
}

// The cursor names a row, not a position: a tenant that leaves the ranking
// ahead of the cursor does not make the next page skip one.
func TestPageTenants_ResumesAfterTheCursorRowNotAnOffset(t *testing.T) {
	tenants := ranked(4)
	_, next, _ := pageTenants(tenants, TenantPage{Size: 2})

	got, _, _ := pageTenants(tenants[1:], TenantPage{Size: 2, After: next})
	if want := []string{"t002", "t003"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("after the first tenant vanished: %v, want %v", ids(got), want)
	}
}

// A token past the last tenant is an empty last page, not the first page.
func TestPageTenants_ATokenPastTheEndIsAnEmptyLastPage(t *testing.T) {
	got, next, rest := pageTenants(ranked(2), TenantPage{After: encodeTenantCursor(0, "zzz")})
	if len(got) != 0 || next != "" || rest != 0 {
		t.Errorf("got %v, next %q, rest %d — want nothing more", ids(got), next, rest)
	}
}

func encodeRaw(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

// The admin pod writes the page into the worker's URL and the worker reads
// it back; the two must agree on every field.
func TestTenantPage_RoundTripsThroughTheQuery(t *testing.T) {
	for _, p := range []TenantPage{
		{},
		{Size: 3},
		{After: encodeTenantCursor(7, "t001")},
		{Size: 3, After: encodeTenantCursor(7, "t001")},
	} {
		if got := TenantPageFromQuery(p.Query()); got != p {
			t.Errorf("round trip of %+v = %+v", p, got)
		}
	}
	if got := TenantPageFromQuery(url.Values{tenantPageSizeParam: {"many"}}); got.Size != 0 {
		t.Errorf("an unparsable size = %d, want 0 (the default)", got.Size)
	}
}
