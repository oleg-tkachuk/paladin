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

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

type ObjectVersionRepo struct {
	q *sqlc.Queries
}

func NewObjectVersionRepo(q *sqlc.Queries) *ObjectVersionRepo {
	return &ObjectVersionRepo{q: q}
}

var _ objecth.VersionRepository = (*ObjectVersionRepo)(nil)

func (r *ObjectVersionRepo) Insert(ctx context.Context, v objecth.ObjectVersion) error {
	if v.VersionID == uuid.Nil {
		v.VersionID = uuid.Must(uuid.NewV7())
	}
	var sizeBytes *int64
	if v.SizeBytes > 0 {
		s := v.SizeBytes
		sizeBytes = &s
	}
	return r.q.InsertObjectVersion(ctx,
		pgUUID(v.VersionID),
		pgUUID(v.ObjectID),
		v.IsDeleteMarker,
		v.StoragePath,
		sizeBytes,
		strPtr(v.ETag),
		checksumAlgoInt(v.ChecksumAlgo),
		strPtr(v.Checksum),
		strPtr(v.ContentType),
		encodeMap(v.Metadata),
		encodeMap(v.Tags),
	)
}

func (r *ObjectVersionRepo) Get(ctx context.Context, versionID uuid.UUID) (objecth.ObjectVersion, error) {
	row, err := r.q.GetObjectVersion(ctx, pgUUID(versionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return objecth.ObjectVersion{}, objecth.ErrVersionNotFound
		}
		return objecth.ObjectVersion{}, err
	}
	return versionFromSQLC(row.ObjectVersion, lockModeFromSQL(row.LockMode), row.LockRetainUntil, row.LegalHold), nil
}

func (r *ObjectVersionRepo) List(ctx context.Context, objectID uuid.UUID, pageSize int32, pageToken string) ([]objecth.ObjectVersion, string, error) {
	pageSize = pageSizeOrDefault(pageSize)
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
	out := make([]objecth.ObjectVersion, 0, len(rows))
	for _, row := range rows {
		v := versionFromSQLC(row.ObjectVersion, lockModeFromSQL(row.LockMode), row.LockRetainUntil, row.LegalHold)
		if v.VersionID == currentID {
			v.IsCurrent = true
		}
		out = append(out, v)
	}
	var next string
	if len(out) == int(pageSize) && len(out) > 0 {
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

// lock* arrive from the LEFT JOIN to object_locks: most versions have no lock
// row at all, so they are passed separately rather than assumed present.
func versionFromSQLC(row sqlc.ObjectVersion, lockMode string, lockRetainUntil pgtype.Timestamptz, legalHold bool) objecth.ObjectVersion {
	var size int64
	if row.SizeBytes != nil {
		size = *row.SizeBytes
	}
	return objecth.ObjectVersion{
		VersionID:       uuidFrom(row.ID),
		ObjectID:        uuidFrom(row.ObjectID),
		IsDeleteMarker:  row.IsDeleteMarker,
		StoragePath:     row.StoragePath,
		SizeBytes:       size,
		ETag:            derefStr(row.Etag),
		ChecksumAlgo:    checksumAlgoName(row.ChecksumAlgorithm),
		Checksum:        derefStr(row.Checksum),
		ContentType:     derefStr(row.ContentType),
		Metadata:        decodeMap(row.Metadata),
		Tags:            decodeMap(row.Tags),
		LockMode:        lockMode,
		LockRetainUntil: timePtr(lockRetainUntil),
		LegalHold:       legalHold,
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
