package objecth

import (
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"
)

// The fields ListObjects sorts by, as order_by names them: Object's own field
// names. search_object_ids (migration 085) maps each to its column, and an
// integration test sorts by every one, so a name the function does not know
// fails there rather than at a caller.
const (
	OrderByKey          = "key"
	OrderByContentType  = "content_type"
	OrderBySize         = "size_bytes"
	OrderByState        = "state"
	OrderByCreatedAt    = "created_at"
	OrderByTerminatedAt = "terminated_at"
)

// orderByID is the order of an empty order_by: id, which is UUIDv7, so the
// oldest object first. Not a name a caller sends.
const orderByID = "id"

// ObjectOrderFields lists every order_by ListObjects accepts.
var ObjectOrderFields = []string{
	OrderByKey, OrderByContentType, OrderBySize, OrderByState, OrderByCreatedAt, OrderByTerminatedAt,
}

// objectOrder is the sort a request names, or InvalidArgument naming the
// fields it may name instead. order_by was once accepted and ignored, so the
// console's sortable columns sorted nothing; an unknown name is now refused.
func objectOrder(orderBy string, desc bool) (keyset.Order, error) {
	if orderBy == "" {
		return keyset.Order{Field: orderByID, Desc: desc}, nil
	}
	if !slices.Contains(ObjectOrderFields, orderBy) {
		return keyset.Order{}, connect.NewError(connect.CodeInvalidArgument,
			fmt.Sprintf("order_by %q: objects sort by one of %s", orderBy, strings.Join(ObjectOrderFields, ", ")))
	}
	return keyset.Order{Field: orderBy, Desc: desc}, nil
}
