package platformstats

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/oleg-tkachuk/limes"
)

// pagedCensus answers CountRecords one count a page, and records each
// request; with repeat it hands back the cursor it was asked for.
type pagedCensus struct {
	counts   []limes.RecordCount
	repeat   bool
	requests []limes.CountRecordsRequest
}

func (c *pagedCensus) CountRecords(_ context.Context, req limes.CountRecordsRequest) (limes.Page[limes.RecordCount], error) {
	c.requests = append(c.requests, req)
	if c.repeat {
		return limes.Page[limes.RecordCount]{Items: c.counts[:1], Next: "again"}, nil
	}
	from := 0
	if req.Cursor != "" {
		from, _ = strconv.Atoi(req.Cursor)
	}
	page := limes.Page[limes.RecordCount]{Items: c.counts[from : from+1]}
	if from+1 < len(c.counts) {
		page.Next = strconv.Itoa(from + 1)
	}
	return page, nil
}

// Every page is read, across every tenant, through limes's walk, which
// refuses a census that repeats its cursor rather than reading forever.
func TestCountRecordsReadsEveryPage(t *testing.T) {
	census := &pagedCensus{counts: []limes.RecordCount{{Active: 1}, {Active: 2}, {Active: 3}}}
	got, err := countRecords(context.Background(), census, limes.CountRecordsRequest{ByPrincipalType: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2].Active != 3 {
		t.Errorf("counts %+v, want all three", got)
	}
	for _, r := range census.requests {
		if !r.AllTenants || !r.ByPrincipalType || r.Limit != limes.MaxListLimit || r.ExpiringWithin != capabilityExpiringWindow {
			t.Errorf("request %+v", r)
		}
	}

	_, err = countRecords(context.Background(), &pagedCensus{counts: census.counts, repeat: true}, limes.CountRecordsRequest{})
	if !errors.Is(err, limes.ErrCursorRepeated) {
		t.Errorf("a repeated cursor: err = %v, want ErrCursorRepeated", err)
	}
}
