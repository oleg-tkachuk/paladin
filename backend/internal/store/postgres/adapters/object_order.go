package adapters

import (
	"bytes"
	"cmp"
	"strconv"
	"strings"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// objectOrderByID is the order search_object_ids takes for "by id", which an
// empty order_by means.
const objectOrderByID = "id"

// noTerminatedAt is what a row with no terminated_at sorts as; migration 085
// coalesces the column to the same instant.
var noTerminatedAt = time.Unix(0, 0).UTC()

// The kinds a sort value has, and how each compares.
type sortKind int

const (
	sortByID sortKind = iota
	sortByText
	sortByNumber
	sortByTime
)

func objectSortKind(field string) sortKind {
	switch field {
	case objecth.OrderByKey, objecth.OrderByContentType, objecth.OrderByState:
		return sortByText
	case objecth.OrderBySize:
		return sortByNumber
	case objecth.OrderByCreatedAt, objecth.OrderByTerminatedAt:
		return sortByTime
	default:
		return sortByID
	}
}

func objectSortText(field string, o sqlc.Object) string {
	switch field {
	case objecth.OrderByKey:
		return o.Path
	case objecth.OrderByContentType:
		return o.ContentType
	default:
		return string(o.State)
	}
}

func objectSortNumber(o sqlc.Object) int64 {
	if o.SizeBytes == nil {
		return 0
	}
	return *o.SizeBytes
}

func objectSortTime(field string, o sqlc.Object) time.Time {
	ts := o.CreatedAt
	if field == objecth.OrderByTerminatedAt {
		ts = o.TerminatedAt
		if !ts.Valid {
			return noTerminatedAt
		}
	}
	return ts.Time.UTC()
}

// compareObjectRows orders a and b the way search_object_ids does: by the
// sort value — text bytewise, as COLLATE "C" does — then by id, the whole
// reversed for a descending order.
func compareObjectRows(order keyset.Order, a, b sqlc.Object) int {
	var c int
	switch objectSortKind(order.Field) {
	case sortByText:
		c = strings.Compare(objectSortText(order.Field, a), objectSortText(order.Field, b))
	case sortByNumber:
		c = cmp.Compare(objectSortNumber(a), objectSortNumber(b))
	case sortByTime:
		c = objectSortTime(order.Field, a).Compare(objectSortTime(order.Field, b))
	}
	if c == 0 {
		c = bytes.Compare(a.ID.Bytes[:], b.ID.Bytes[:])
	}
	if order.Desc {
		return -c
	}
	return c
}

// objectSortValue is o's sort value as the cursor carries it: text the
// function casts back to the column's type.
func objectSortValue(field string, o sqlc.Object) string {
	switch objectSortKind(field) {
	case sortByText:
		return objectSortText(field, o)
	case sortByNumber:
		return strconv.FormatInt(objectSortNumber(o), 10)
	case sortByTime:
		return objectSortTime(field, o).Format(time.RFC3339Nano)
	default:
		return ""
	}
}
