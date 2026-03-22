package paladinapi

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestObjectToProto(t *testing.T) {
	id := uuid.New()
	now := time.Now()
	etag := "test-etag"

	obj := &domain.Object{
		ID:          id,
		ObjectKey:   "test-key",
		Bucket:      "test-bucket",
		ContentType: "text/plain",
		SizeBytes:   123,
		Status:      domain.ObjectComplete,
		Labels:      map[string]string{"foo": "bar"},
		Tags:        map[string]string{"tag1": "value1"},
		CreatedAt:   now,
		UpdatedAt:   now,
		StoredETag:  &etag,
	}

	proto := objectToProto(obj)
	assert.NotNil(t, proto)
	assert.Equal(t, id.String(), proto.ObjectId)
	assert.Equal(t, "test-key", proto.Key)
	assert.Equal(t, "test-bucket", proto.Bucket)
	assert.Equal(t, "text/plain", proto.ContentType)
	assert.Equal(t, int64(123), proto.SizeBytes)
	assert.Equal(t, ObjectStatus_OBJECT_STATUS_AVAILABLE, proto.Status)
	assert.Equal(t, "bar", proto.Metadata["foo"])
	assert.Equal(t, etag, proto.Etag)
}

func TestObjectStatusMapping(t *testing.T) {
	tests := []struct {
		domain domain.ObjectStatus
		proto  ObjectStatus
	}{
		{domain.ObjectPending, ObjectStatus_OBJECT_STATUS_PENDING},
		{domain.ObjectUploading, ObjectStatus_OBJECT_STATUS_UPLOADING},
		{domain.ObjectComplete, ObjectStatus_OBJECT_STATUS_AVAILABLE},
		{domain.ObjectSoftDeleted, ObjectStatus_OBJECT_STATUS_DELETED},
		{domain.ObjectAborted, ObjectStatus_OBJECT_STATUS_ARCHIVED},
	}

	for _, tc := range tests {
		assert.Equal(t, tc.proto, objectStatusToProto(tc.domain))
		assert.Equal(t, tc.domain, protoStatusToDomain(tc.proto))
	}
}
