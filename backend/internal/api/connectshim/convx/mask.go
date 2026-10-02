package convx

import (
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
)

// CheckMask refuses an update_mask path the RPC does not apply. Before it, an
// unknown or misspelled path was skipped and the RPC reported success for an
// update that changed nothing — which is how a proto rename, or a client using
// a different name for a field, failed silently. supported holds the message's
// proto field names, as clients send them.
func CheckMask(paths, supported []string) error {
	var unknown []string
	for _, p := range paths {
		if !slices.Contains(supported, p) {
			unknown = append(unknown, p)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return connect.NewError(connect.CodeInvalidArgument,
		fmt.Errorf("update_mask: unsupported path(s) %s; this RPC updates %s",
			strings.Join(unknown, ", "), strings.Join(supported, ", ")))
}
