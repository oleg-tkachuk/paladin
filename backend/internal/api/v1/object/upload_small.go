// UploadSmall is a client-streaming RPC for objects ≤ 100 MiB where the client
// doesn't want to bother with presigned URLs (e.g. from a mobile app, or an
// edge function with no direct S3 access). PALADIN acts as a bounded proxy.
//
// The stream contract:
//
//   - Message #1 carries InitMetadata (objectKey, key, content_type, size_hint,
//     tags, …).
//   - Subsequent messages carry raw chunk bytes (recommended 256 KiB each).
//   - The terminal message sets Final=true and may include a client-computed
//     checksum.
//
// Behavior:
//
//   - Bytes are streamed straight into the storage backend via PutObject or
//     multipart (based on SizeHint). There is no buffering beyond one chunk.
//   - On success, the resulting object is promoted to AVAILABLE before the
//     response is returned — clients don't need to call CompleteObject.
//   - Cancellation mid-stream aborts any in-flight multipart upload and
//     leaves the PENDING row for the reconciler to mark FAILED.

package object

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

// StreamSink is the storage-side sink for UploadSmall. Implementations may
// pick PutObject (small) or multipart (large) based on total size.
type StreamSink interface {
	Open(ctx context.Context, bucket string, tenantID uuid.UUID, objectKey, key, contentType string, sizeHint int64) (StreamWriter, error)
}

// StreamWriter is the per-upload handle returned by StreamSink.Open.
type StreamWriter interface {
	io.Writer
	// Close finalizes the upload. Returns authoritative etag/size/checksum.
	// If the writer is Closed without finalize, implementations should
	// abort any in-flight multipart.
	Close() (etag string, sizeBytes int64, checksum string, err error)
	Abort() error
}

// StreamInit mirrors the proto `UploadSmall.InitMetadata` message. Decoded by
// the Connect adapter before handing off here.
type StreamInit struct {
	ObjectKey    string
	Key          string
	ContentType  string
	SizeHint     int64
	ChecksumAlgo string
	Metadata     map[string]string
	Tags         map[string]string
	ExternalRef  string
}

// StreamChunk is one payload frame. Final=true marks the last frame.
type StreamChunk struct {
	Data     []byte
	Final    bool
	Checksum string
}

// StreamSource abstracts over the Connect server stream so this code is
// testable without spinning up a real transport.
type StreamSource interface {
	RecvInit() (StreamInit, error)
	RecvChunk() (StreamChunk, error)
}

// UploadSmallSink is the storage seam stored on Handler. Wire-up injects a
// concrete implementation backed by the S3 client.
type UploadSmallDeps struct {
	Sink StreamSink
}

// UploadSmall executes the streaming upload flow. Returns the final Object.
func (h *Handler) UploadSmall(ctx context.Context, stream StreamSource, deps UploadSmallDeps) (*Object, error) {
	tenantID, err := auth.TenantFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	principal, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	init, err := stream.RecvInit()
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("init metadata: %w", err))
	}

	// Authorize with the declared size as a context attribute — Cedar policy
	// can reject oversized uploads at the start rather than after N chunks.
	decision, err := h.policy.IsAuthorized(ctx,
		&cedar.Principal{Subject: principal.Subject, TenantID: tenantID, TenantSlug: principal.TenantSlug, Roles: principal.Roles, Scopes: apiutil.ScopeStrings(principal.Scopes)},
		cedar.ActionPutObject,
		&cedar.Resource{
			TenantID: tenantID, ObjectKey: init.ObjectKey, Key: init.Key,
			ContentType: init.ContentType, SizeBytes: init.SizeHint, Tags: init.Tags,
		},
		cedar.RequestContext{
			SizeBytes: init.SizeHint, ContentType: init.ContentType, Now: time.Now(),
		},
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("denied by policy"))
	}

	// Pre-allocate the PENDING row — on success the stream writer gives us
	// authoritative values and the SM promotes it.
	objectID := uuid.Must(uuid.NewV7())
	key := init.Key
	if key == "" {
		key = objectID.String()
	}
	ttl := h.presign.DefaultTTL
	obj, err := h.repo.CreateObject(ctx, CreateObjectArgs{
		TenantID: tenantID, ObjectKey: init.ObjectKey, Key: key,
		ContentType: init.ContentType, SizeHint: init.SizeHint,
		ChecksumAlgo: init.ChecksumAlgo, Metadata: init.Metadata,
		Tags: init.Tags, ExternalRef: init.ExternalRef,
		PresignExpiresAt: time.Now().Add(ttl),
	})
	if err != nil {
		return nil, mapCreateErr(err)
	}

	bucket, err := h.repo.LookupBucket(ctx, tenantID, init.ObjectKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	writer, err := deps.Sink.Open(ctx, bucket, tenantID, init.ObjectKey, key, init.ContentType, init.SizeHint)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("open sink: %w", err))
	}

	var received int64
	for {
		chunk, err := stream.RecvChunk()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			_ = writer.Abort()
			return nil, connect.NewError(connect.CodeCanceled, err)
		}
		if len(chunk.Data) > 0 {
			if _, werr := writer.Write(chunk.Data); werr != nil {
				_ = writer.Abort()
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("write chunk: %w", werr))
			}
			received += int64(len(chunk.Data))
		}
		if chunk.Final {
			break
		}
	}
	if init.SizeHint > 0 && received != init.SizeHint {
		_ = writer.Abort()
		return nil, connect.NewError(connect.CodeDataLoss,
			fmt.Errorf("received %d bytes, declared %d", received, init.SizeHint))
	}

	etag, finalSize, checksum, err := writer.Close()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("finalize: %w", err))
	}

	if _, err := h.sm.PromoteToAvailable(ctx, obj.ObjectID, etag, finalSize, checksum, "", statemachine.SourceRPC); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	fresh, err := h.repo.FindByName(ctx, tenantID, init.ObjectKey, obj.ObjectID.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &fresh, nil
}
