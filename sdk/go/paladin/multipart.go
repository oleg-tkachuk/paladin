package paladin

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// The control half of a multipart upload whose bytes another party sends — a
// browser, an edge worker — while this process holds the credentials:
// BeginMultipart opens it, PresignPart signs each part once its sender has
// hashed it, CompleteMultipart assembles the parts, AbortMultipart drops them.
// Upload does all four itself when it holds the bytes.

// ErrNoPartSplit is an InitiateMultipartUpload answer naming no part size or
// part count. Every part URL is signed for the size the server recommended,
// so a sender cannot pick a split of its own; BeginMultipart aborts the
// upload and returns this.
var ErrNoPartSplit = errors.New("paladin: the server named no part size for the multipart upload")

// MultipartInput is the object a multipart upload fills.
type MultipartInput struct {
	Parent      string // the collection's name
	Key         string // empty: the server uses the object's id
	ContentType string
	Size        int64
	Metadata    map[string]string
	Tags        map[string]string
}

// BeginMultipart reserves an object for in.Size bytes and opens a multipart
// upload on it, split as the server recommends: the session's PartSize and
// TotalParts are what the sender must slice the bytes into. Nothing is
// presigned yet — a part URL is signed for the part's checksum, which only
// the holder of the bytes can compute.
func BeginMultipart(ctx context.Context, data *DataPlane, in MultipartInput) (UploadSession, error) {
	init, err := data.MultipartUpload.InitiateMultipartUpload(ctx, connect.NewRequest(&datav1.InitiateMultipartUploadRequest{
		Parent:            in.Parent,
		Key:               in.Key,
		ContentType:       in.ContentType,
		SizeBytes:         in.Size,
		ChecksumAlgorithm: commonv1.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256,
		Metadata:          in.Metadata,
		Tags:              in.Tags,
	}))
	if err != nil {
		return UploadSession{}, err
	}
	session := UploadSession{
		ObjectName: init.Msg.GetObject().GetName(),
		UploadID:   init.Msg.GetUploadId(),
		PartSize:   init.Msg.GetRecommendedPartSize(),
		TotalParts: init.Msg.GetTotalParts(),
	}
	if session.PartSize <= 0 || session.TotalParts <= 0 {
		_ = AbortMultipart(context.WithoutCancel(ctx), data, session)
		return UploadSession{}, ErrNoPartSplit
	}
	return session, nil
}

// PresignPart signs part number (1-based) of an open upload for the part's
// checksum, the base64 SHA-256 of its bytes: storage refuses a body of any
// other size or digest. Call it again for a part whose URL expired. Every
// call mints a fresh URL, so none shares the context's idempotency key.
func PresignPart(ctx context.Context, data *DataPlane, session UploadSession, number int32, checksum string) (*commonv1.PresignedUrl, error) {
	signed, err := data.MultipartUpload.PresignPart(ownKeys(ctx), connect.NewRequest(&datav1.PresignPartRequest{
		ObjectName: session.ObjectName, UploadId: session.UploadID, PartNumber: number, ChecksumValue: checksum,
	}))
	if err != nil {
		return nil, err
	}
	if signed.Msg.GetUploadUrl().GetUrl() == "" {
		return nil, ErrNoUploadURL
	}
	return signed.Msg.GetUploadUrl(), nil
}

// CompleteMultipart assembles the parts — each with the ETag storage answered
// its PUT with, and the checksum it was presigned for — into the object, and
// returns the object as stored. An ETag is taken as a browser reads it from
// the response header, quotes and all. A server that answers with the
// object's name alone, as older releases do, is read back with GetObject
// when the caller may read it, and its answer returned as it is otherwise.
func CompleteMultipart(ctx context.Context, data *DataPlane, session UploadSession, parts []*datav1.CompletedPart) (*datav1.Object, error) {
	completed := make([]*datav1.CompletedPart, len(parts))
	for i, p := range parts {
		completed[i] = &datav1.CompletedPart{
			PartNumber: p.GetPartNumber(), Etag: normalizeETag(p.GetEtag()), ChecksumValue: p.GetChecksumValue(),
		}
	}
	done, err := data.MultipartUpload.CompleteMultipartUpload(ctx, connect.NewRequest(&datav1.CompleteMultipartUploadRequest{
		ObjectName: session.ObjectName, UploadId: session.UploadID, Parts: completed,
	}))
	if err != nil {
		return nil, err
	}
	if done.Msg.GetCollection() != "" {
		return done.Msg, nil
	}
	// Best effort: the upload is complete, and a caller allowed to write but
	// not to read — a put-only capability — must not see it fail here.
	if got, err := data.Object.GetObject(ctx, connect.NewRequest(&datav1.GetObjectRequest{Name: session.ObjectName})); err == nil {
		return got.Msg, nil
	}
	return done.Msg, nil
}

// AbortMultipart closes an open upload and drops its parts. An upload already
// gone — aborted, completed, swept — is not an error: the parts are gone too.
func AbortMultipart(ctx context.Context, data *DataPlane, session UploadSession) error {
	_, err := data.MultipartUpload.AbortMultipartUpload(ctx, connect.NewRequest(&datav1.AbortMultipartUploadRequest{
		ObjectName: session.ObjectName, UploadId: session.UploadID,
	}))
	if connect.CodeOf(err) == connect.CodeNotFound {
		return nil
	}
	return err
}

// normalizeETag is an ETag without the quotes and spaces a header carries.
func normalizeETag(etag string) string {
	return strings.Trim(strings.TrimSpace(etag), etagQuote)
}
