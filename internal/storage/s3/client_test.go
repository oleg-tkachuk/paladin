package s3_test

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestS3Client_Init(t *testing.T) {
	logger := zap.NewNop()
	cfg := config.S3{
		Endpoint: "http://localhost:9000",
		Bucket:   "test-bucket",
		Region:   "us-east-1",
	}

	client, err := s3.New(context.Background(), cfg, logger)
	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.Equal(t, "test-bucket", client.BucketName())
}
