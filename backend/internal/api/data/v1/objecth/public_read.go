package objecth

import (
	"context"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
)

// AdmitPublicUpload applies a public collection's rules to an object about
// to be created there (ADR-0027): the server names it — a key the client
// chose is refused, since a public object is authorised by its URL alone —
// and only passive content may be served to anyone. Outside a public
// collection it changes nothing.
func AdmitPublicUpload(meta BucketMeta, key *string, contentType string) error {
	if !meta.PublicRead {
		return nil
	}
	if *key != "" {
		return apiutil.MapError(publicread.Rulef("a public collection names its objects itself; leave the key empty"))
	}
	if publicread.IsActive(contentType) {
		return apiutil.MapError(publicread.Rulef("content type %q is executed or rendered by a browser and may not be served publicly", contentType))
	}
	k, err := publicread.NewKey()
	if err != nil {
		return apiutil.MapError(err)
	}
	*key = k
	return nil
}

// publicURL is the address of a new object at key, in a public collection;
// "" elsewhere.
func (h *Handler) publicURL(ctx context.Context, meta BucketMeta, tenantID uuid.UUID, collection, key string) (string, error) {
	if !meta.PublicRead {
		return "", nil
	}
	u, err := h.storage.PublicURL(ctx, meta.BackendID, meta.BucketName, meta.PublicBaseURL, tenantID, collection, key)
	if err != nil {
		return "", rpcerr.New(connect.CodeInternal, fmt.Errorf("public url: %w", err))
	}
	return u, nil
}
