package postgres

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/safecast"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// mapToDomainObject converts sqlc.Object to domain.Object
func mapToDomainObject(obj sqlc.Object) (domain.Object, error) {
	id, err := uuidFromPgtype(obj.ID)
	if err != nil {
		return domain.Object{}, fmt.Errorf("convert object id: %w", err)
	}

	labels, err := unmarshalStringMap(obj.Labels)
	if err != nil {
		return domain.Object{}, fmt.Errorf("unmarshal labels: %w", err)
	}

	tags, err := unmarshalStringMap(obj.Tags)
	if err != nil {
		return domain.Object{}, fmt.Errorf("unmarshal tags: %w", err)
	}

	return domain.Object{
		ID:              id,
		TenantID:        obj.TenantID,
		ObjectKey:       obj.ObjectKey,
		Bucket:          obj.Bucket,
		ContentType:     obj.ContentType,
		SizeBytes:       obj.SizeBytes,
		ChecksumSHA256:  obj.ChecksumSha256,
		Status:          domain.ObjectStatus(obj.Status),
		Labels:          labels,
		Tags:            tags,
		ExternalRef:     obj.ExternalRef,
		StoredETag:      obj.StoredEtag,
		StoredSizeBytes: obj.StoredSizeBytes,
		CreatedAt:       timestampFromPgtype(obj.CreatedAt),
		UpdatedAt:       timestampFromPgtype(obj.UpdatedAt),
		ExpiresAt:       timestampPtrFromPgtype(obj.ExpiresAt),
		CompletedAt:     timestampPtrFromPgtype(obj.CompletedAt),
		DeletedAt:       timestampPtrFromPgtype(obj.DeletedAt),
		Category:        obj.Category,
		Subpath:         obj.Subpath,
	}, nil
}

// mapToDomainMultipart converts sqlc.MultipartUpload to domain.Multipart
func mapToDomainMultipart(mp sqlc.MultipartUpload) (domain.Multipart, error) {
	id, err := uuidFromPgtype(mp.ID)
	if err != nil {
		return domain.Multipart{}, fmt.Errorf("convert multipart id: %w", err)
	}

	objectID, err := uuidFromPgtype(mp.ObjectID)
	if err != nil {
		return domain.Multipart{}, fmt.Errorf("convert object id: %w", err)
	}

	return domain.Multipart{
		ID:          id,
		TenantID:    mp.TenantID,
		ObjectID:    objectID,
		UploadID:    mp.UploadID,
		Bucket:      mp.Bucket,
		ObjectKey:   mp.ObjectKey,
		ContentType: mp.ContentType,
		PartSize:    mp.PartSizeBytes,
		Status:      domain.MultipartStatus(mp.Status),
		CreatedAt:   timestampFromPgtype(mp.CreatedAt),
		UpdatedAt:   timestampFromPgtype(mp.UpdatedAt),
		ExpiresAt:   timestampFromPgtype(mp.ExpiresAt),
	}, nil
}

// mapToDomainMultipartPart converts sqlc.MultipartPart to domain.MultipartPart
func mapToDomainMultipartPart(part sqlc.MultipartPart) (domain.MultipartPart, error) {
	multipartID, err := uuidFromPgtype(part.MultipartID)
	if err != nil {
		return domain.MultipartPart{}, fmt.Errorf("convert multipart id: %w", err)
	}

	return domain.MultipartPart{
		MultipartID: multipartID,
		PartNumber:  safecast.IntFrom32(part.PartNumber),
		ETag:        part.Etag,
		SizeBytes:   part.SizeBytes,
		CreatedAt:   timestampFromPgtype(part.CreatedAt),
	}, nil
}

// mapToDomainIdempotency converts sqlc.IdempotencyKey to domain.IdempotencyRecord
func mapToDomainIdempotency(key sqlc.IdempotencyKey) domain.IdempotencyRecord {
	return domain.IdempotencyRecord{
		TenantID:     key.TenantID,
		Key:          key.IdempotencyKey,
		RequestPath:  key.RequestPath,
		RequestHash:  key.RequestHash,
		ResponseCode: safecast.IntFrom32(key.ResponseCode),
		ResponseBody: key.ResponseBody,
		CreatedAt:    timestampFromPgtype(key.CreatedAt),
		ExpiresAt:    timestampFromPgtype(key.ExpiresAt),
	}
}

// mapToDomainCategory converts sqlc.ObjectCategory to domain.Category
func mapToDomainCategory(c sqlc.ObjectCategory) domain.Category {
	return domain.Category{
		ID:          uuid.UUID(c.ID.Bytes),
		TenantID:    c.TenantID,
		Slug:        c.Slug,
		Name:        c.Name,
		Description: c.Description,
		CreatedAt:   timestampFromPgtype(c.CreatedAt),
		UpdatedAt:   timestampFromPgtype(c.UpdatedAt),
	}
}

// mapToDomainAuditLog converts sqlc.AuditLog to domain.AuditLog
func mapToDomainAuditLog(log sqlc.AuditLog) (domain.AuditLog, error) {
	id, err := uuidFromPgtype(log.ID)
	if err != nil {
		return domain.AuditLog{}, fmt.Errorf("convert audit log id: %w", err)
	}

	queryParams, err := unmarshalJSONB(log.QueryParams)
	if err != nil {
		return domain.AuditLog{}, fmt.Errorf("unmarshal query params: %w", err)
	}

	requestHeaders, err := unmarshalJSONB(log.RequestHeaders)
	if err != nil {
		return domain.AuditLog{}, fmt.Errorf("unmarshal request headers: %w", err)
	}

	var httpStatus *int
	if log.HttpStatus != nil {
		status := safecast.IntFrom32(*log.HttpStatus)
		httpStatus = &status
	}

	var responseTimeMS *int
	if log.ResponseTimeMs != nil {
		ms := safecast.IntFrom32(*log.ResponseTimeMs)
		responseTimeMS = &ms
	}

	return domain.AuditLog{
		ID:                id,
		TenantID:          log.TenantID,
		RequestID:         log.RequestID,
		IdempotencyKey:    log.IdempotencyKey,
		ActorSubject:      log.ActorSubject,
		ActorType:         domain.ActorType(log.ActorType),
		ClientIP:          clientIPFromNetipAddr(log.ClientIp),
		UserAgent:         log.UserAgent,
		Method:            log.Method,
		Path:              log.Path,
		QueryParams:       queryParams,
		RequestHeaders:    requestHeaders,
		RequestBodySHA256: log.RequestBodySha256,
		RequestSizeBytes:  log.RequestSizeBytes,
		HTTPStatus:        httpStatus,
		ResponseCode:      log.ResponseCode,
		ResponseStatus:    log.ResponseStatus,
		ResponseTimeMS:    responseTimeMS,
		CreatedAt:         timestampFromPgtype(log.CreatedAt),
	}, nil
}

// Helper functions for type conversion

func uuidFromPgtype(u pgtype.UUID) (uuid.UUID, error) {
	if !u.Valid {
		return uuid.Nil, fmt.Errorf("invalid uuid")
	}

	return uuid.UUID(u.Bytes), nil
}

func uuidToPgtype(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{
		Bytes: [16]byte(u),
		Valid: true,
	}
}

func timestampFromPgtype(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}

	return t.Time
}

func timestampPtrFromPgtype(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	result := t.Time

	return &result
}

func timestampToPgtype(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{
		Time:  t,
		Valid: !t.IsZero(),
	}
}

func timestampPtrToPgtype(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{Valid: false}
	}

	return pgtype.Timestamptz{
		Time:  *t,
		Valid: true,
	}
}

func clientIPFromNetipAddr(ip *netip.Addr) *string {
	if ip == nil {
		return nil
	}
	str := ip.String()

	return &str
}
