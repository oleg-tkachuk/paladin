package quotah

import (
	"fmt"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
)

// The caps an update_mask may name, as SetQuotaRequest spells them.
const (
	CapMaxTotalBytes    = "max_total_bytes"
	CapMaxObjectCount   = "max_object_count"
	CapMaxBytesPerDay   = "max_bytes_per_day"
	CapMaxObjectsPerDay = "max_objects_per_day"
)

// capField is the field of a quota a mask path names.
func capField(q *admindomain.Quota, path string) (**int64, bool) {
	switch path {
	case CapMaxTotalBytes:
		return &q.MaxTotalBytes, true
	case CapMaxObjectCount:
		return &q.MaxObjectCount, true
	case CapMaxBytesPerDay:
		return &q.MaxBytesPerDay, true
	case CapMaxObjectsPerDay:
		return &q.MaxObjectsPerDay, true
	default:
		return nil, false
	}
}

// applyCapMask is current with the caps mask names taken from req: a named
// cap req leaves nil is lifted, and an unnamed one keeps current's. An empty
// mask, or one naming anything but a cap, is InvalidArgument: the caps are
// nil-able now, so writing all four regardless of the mask would lift every
// cap the caller did not mention.
func applyCapMask(current, req admindomain.Quota, mask []string) (admindomain.Quota, error) {
	if len(mask) == 0 {
		return admindomain.Quota{}, connect.NewError(connect.CodeInvalidArgument,
			"update_mask: name at least one cap to change")
	}
	out := current
	for _, path := range mask {
		dst, ok := capField(&out, path)
		if !ok {
			return admindomain.Quota{}, rpcerr.New(connect.CodeInvalidArgument,
				fmt.Errorf("update_mask: %q is not a quota cap", path))
		}
		src, _ := capField(&req, path)
		*dst = *src
	}
	return out, nil
}
