package adapters

// defaultPageSize / maxPageSize bound every List query in this package.
//
// The proto already constrains page_size to [0, 1000] (buf.validate on
// common.v1.PageRequest), so a wire caller never reaches the upper bound here
// — protovalidate refuses 1001 with InvalidArgument before the repo runs. The
// bound is kept for callers that build args directly, and because a limit
// that exists in one place is cheaper to change than one asserted in eleven.
const (
	defaultPageSize int32 = 50
	maxPageSize     int32 = 1000
)

// pageSizeOrDefault normalises a caller's page size.
//
// Zero is not a page size, it is an absence: the admin and data shims forward
// req.Msg.GetPage().GetPageSize() unchanged, and an unset `page` message is a
// zero. This function is therefore the only thing standing between "the
// client omitted page_size" and LIMIT 0 — a query that returns an empty page
// for a table with rows in it, which a client reasonably reads as "there is
// nothing here".
//
// It used to be eleven copies of `if pageSize <= 0 { pageSize = 50 }`, five of
// which had quietly lost the upper bound.
func pageSizeOrDefault(n int32) int32 {
	if n <= 0 || n > maxPageSize {
		return defaultPageSize
	}
	return n
}
