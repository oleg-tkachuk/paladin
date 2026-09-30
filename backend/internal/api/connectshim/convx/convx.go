// Package convx holds the proto-conversion helpers every connectshim plane
// needs.
//
// These existed as per-plane copies — admin, data and iam each carried their
// own parseRV, pageResponseProto, tsProto and tsPtrProto, along with their own
// tests for them. Most were byte-identical, so the duplication was merely
// noise. parseRV was not:
//
//	admin/data:  strconv.ParseInt(s, 10, 64)
//	iam:         a hand-rolled digit loop
//
// The hand-rolled one overflowed silently — "9223372036854775808" parsed to
// -9223372036854775808 with no error — and rejected "-1" that the others
// accepted. The same resource_version therefore meant different things
// depending on which plane received it, which is not a thing an OCC guard can
// afford. One implementation removes the divergence by construction.
package convx

import (
	"fmt"
	"strconv"

	"time"

	commonpb "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/common/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ParseRV decodes a resource_version string into the int64 the domain uses.
//
// An empty string is 0, which every caller reads as "no OCC guard supplied" —
// the handlers decide whether that is allowed. Anything else must be a base-10
// integer that fits in an int64; out-of-range input is an error rather than a
// wrapped value.
func ParseRV(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid resource_version %q: %w", s, err)
	}
	return n, nil
}

// ResourceVersion renders a version for the wire. Zero becomes "" — the
// absent-guard form ParseRV round-trips.
func ResourceVersion(v int64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatInt(v, 10)
}

// PageResponseProto returns nil for an empty cursor, so a final page carries
// no page field at all rather than an empty token a client might re-send.
func PageResponseProto(next string) *commonpb.PageResponse {
	if next == "" {
		return nil
	}
	return &commonpb.PageResponse{NextPageToken: next}
}

// TsProto converts a time to a protobuf timestamp; the zero time becomes nil,
// so "unset" stays distinguishable from "the epoch".
func TsProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// TsPtrProto is TsProto for an optional time.
func TsPtrProto(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return TsProto(*t)
}
