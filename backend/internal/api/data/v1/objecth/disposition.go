package objecth

import (
	"fmt"
	"mime"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
)

// maxContentDispositionBytes bounds the response-content-disposition a
// presigned GET carries. The value rides in the URL's query; a filename
// longer than this is not a filename.
const maxContentDispositionBytes = 1024

// Disposition types a download may ask for (RFC 6266).
const (
	dispositionInline     = "inline"
	dispositionAttachment = "attachment"
	// dispositionFilename is the only parameter accepted; mime.ParseMediaType
	// folds the RFC 2231 filename* form into it.
	dispositionFilename = "filename"
)

// NormalizeContentDisposition validates a caller-supplied Content-Disposition
// and returns it in canonical form, or "" for none.
//
// The value is signed into the URL as response-content-disposition and the
// object store answers with it verbatim, so it is a header the caller writes
// into a response served from the store's origin. Only inline or attachment
// with an optional filename is accepted; anything else — another type, an
// unknown parameter, a value that does not parse — is InvalidArgument rather
// than passed through. Re-encoding through mime.FormatMediaType quotes the
// filename and switches a non-ASCII one to the RFC 2231 form, so what is
// signed is always a well-formed header.
func NormalizeContentDisposition(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxContentDispositionBytes {
		return "", connect.Errorf(connect.CodeInvalidArgument,
			"content_disposition is %d bytes, at most %d", len(raw), maxContentDispositionBytes)
	}
	dtype, params, err := mime.ParseMediaType(raw)
	if err != nil {
		return "", rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("content_disposition: %w", err))
	}
	if dtype != dispositionInline && dtype != dispositionAttachment {
		return "", connect.Errorf(connect.CodeInvalidArgument,
			"content_disposition type %q: want %s or %s", dtype, dispositionInline, dispositionAttachment)
	}
	for k := range params {
		if k != dispositionFilename {
			return "", connect.Errorf(connect.CodeInvalidArgument,
				"content_disposition parameter %q: only %s is accepted", k, dispositionFilename)
		}
	}
	if name, ok := params[dispositionFilename]; ok && strings.ContainsAny(name, "/\\") {
		return "", connect.NewError(connect.CodeInvalidArgument,
			"content_disposition filename must not contain a path separator")
	}
	out := mime.FormatMediaType(dtype, params)
	if out == "" {
		return "", connect.NewError(connect.CodeInvalidArgument, "content_disposition cannot be encoded")
	}
	return out, nil
}
