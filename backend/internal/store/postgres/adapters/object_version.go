package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres/sqlc"
)

type ObjectVersionRepo struct {
	q *sqlc.Queries
}

func NewObjectVersionRepo(q *sqlc.Queries) *ObjectVersionRepo {
	return &ObjectVersionRepo{q: q}
}

var _ object.VersionRepository = (*ObjectVersionRepo)(nil)

func (r *ObjectVersionRepo) Insert(ctx context.Context, v object.ObjectVersion) error {
	if v.VersionID == uuid.Nil {
		v.VersionID = uuid.Must(uuid.NewV7())
	}
	var sizeBytes *int64
	if v.SizeBytes > 0 {
		s := v.SizeBytes
		sizeBytes = &s
	}
	var retain pgtype.Timestamptz
	if v.LockRetainUntil != nil {
		retain = pgTS(*v.LockRetainUntil)
	}
	return r.q.InsertObjectVersion(ctx,
		pgUUID(v.VersionID),
		pgUUID(v.ObjectID),
		v.IsDeleteMarker,
		v.S3Key,
		sizeBytes,
		strPtr(v.ETag),
		checksumAlgoInt(v.ChecksumAlgo),
		strPtr(v.Checksum),
		strPtr(v.ContentType),
		encodeMap(v.Metadata),
		encodeMap(v.Tags),
		v.LockMode,
		retain,
		v.LegalHold,
	)
}

func (r *ObjectVersionRepo) Get(ctx context.Context, versionID uuid.UUID) (object.ObjectVersion, error) {
	row, err := r.q.GetObjectVersion(ctx, pgUUID(versionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return object.ObjectVersion{}, object.ErrVersionNotFound
		}
		return object.ObjectVersion{}, err
	}
	return versionFromSQLC(row), nil
}

func (r *ObjectVersionRepo) List(ctx context.Context, objectID uuid.UUID, pageSize int32, pageToken string) ([]object.ObjectVersion, string, error) {
	if pageSize <= 0 || pageSize > 1000 {
		pageSize = 50
	}
	var (
		afterAt pgtype.Timestamptz
		afterID pgtype.UUID
	)
	if pageToken != "" {
		at, id, err := decodeVersionCursor(pageToken)
		if err != nil {
			return nil, "", fmt.Errorf("invalid page_token: %w", err)
		}
		if !at.IsZero() {
			afterAt = pgTS(at)
		}
		afterID = pgUUID(id)
	}
	rows, err := r.q.ListObjectVersions(ctx, pgUUID(objectID), afterAt, afterID, pageSize)
	if err != nil {
		return nil, "", fmt.Errorf("list versions: %w", err)
	}
	currentID, _ := r.CurrentVersionID(ctx, objectID)
	out := make([]object.ObjectVersion, 0, len(rows))
	for _, row := range rows {
		v := versionFromSQLC(row)
		if v.VersionID == currentID {
			v.IsCurrent = true
		}
		out = append(out, v)
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		last := out[len(out)-1]
		next = encodeVersionCursor(last.CreatedAt, last.VersionID)
	}
	return out, next, nil
}

func (r *ObjectVersionRepo) CurrentVersionID(ctx context.Context, objectID uuid.UUID) (uuid.UUID, error) {
	id, err := r.q.GetCurrentVersionID(ctx, pgUUID(objectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, err
	}
	return uuidFrom(id), nil
}

func (r *ObjectVersionRepo) SetCurrentVersionID(ctx context.Context, objectID, versionID uuid.UUID) error {
	return r.q.SetCurrentVersionID(ctx, pgUUID(objectID), pgUUID(versionID))
}

func versionFromSQLC(row sqlc.ObjectVersion) object.ObjectVersion {
	var size int64
	if row.SizeBytes != nil {
		size = *row.SizeBytes
	}
	return object.ObjectVersion{
		VersionID:       uuidFrom(row.VersionID),
		ObjectID:        uuidFrom(row.ObjectID),
		IsDeleteMarker:  row.IsDeleteMarker,
		S3Key:           row.S3Key,
		SizeBytes:       size,
		ETag:            derefStr(row.Etag),
		ChecksumAlgo:    checksumAlgoName(row.ChecksumAlgorithm),
		Checksum:        derefStr(row.Checksum),
		ContentType:     derefStr(row.ContentType),
		Metadata:        decodeMap(row.Metadata),
		Tags:            decodeMap(row.Tags),
		LockMode:        row.LockMode,
		LockRetainUntil: timePtr(row.LockRetainUntil),
		LegalHold:       row.LegalHold,
		CreatedAt:       timeFrom(row.CreatedAt),
	}
}

// encodeVersionCursor / decodeVersionCursor format: "{rfc3339nano}/{uuid}".
func encodeVersionCursor(at time.Time, id uuid.UUID) string {
	return at.UTC().Format(time.RFC3339Nano) + "/" + id.String()
}

func decodeVersionCursor(tok string) (time.Time, uuid.UUID, error) {
	idx := strings.LastIndex(tok, "/")
	if idx <= 0 {
		return time.Time{}, uuid.Nil, errors.New("malformed cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, tok[:idx])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	id, err := uuid.Parse(tok[idx+1:])
	if err != nil {
		return time.Time{}, uuid.Nil, err
	}
	return at, id, nil
}
