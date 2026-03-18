package middleware

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	domainmocks "github.com/oleg-tkachuk/paladin/internal/domain/mocks"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAuditBatchWriter_Batching(t *testing.T) {
	repo := &domainmocks.MockAuditLogRepository{}
	logger := zap.NewNop()

	writer := NewAuditBatchWriter(repo, logger)
	defer writer.Close()

	// Case 1: Batch flush when full
	const testBatchSize = 100 // defined in audit_log.go
	var wg sync.WaitGroup
	wg.Add(testBatchSize)

	repo.On("Create", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		wg.Done()
	})

	for i := 0; i < testBatchSize; i++ {
		writer.send(domain.AuditLog{ID: uuid.New()})
	}

	// Wait for batch flush
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for batch flush")
	}

	repo.AssertNumberOfCalls(t, "Create", testBatchSize)
}

func TestAuditBatchWriter_FlushOnTimer(t *testing.T) {
	repo := &domainmocks.MockAuditLogRepository{}
	logger := zap.NewNop()

	writer := NewAuditBatchWriter(repo, logger)
	defer writer.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	repo.On("Create", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		wg.Done()
	})

	writer.send(domain.AuditLog{ID: uuid.New()})

	// Wait for timer flush (auditFlushDelay is 500ms)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for timer flush")
	}

	repo.AssertExpectations(t)
}

func TestAuditBatchWriter_Close(t *testing.T) {
	repo := &domainmocks.MockAuditLogRepository{}
	logger := zap.NewNop()

	writer := NewAuditBatchWriter(repo, logger)

	repo.On("Create", mock.Anything, mock.Anything).Return(nil)

	writer.send(domain.AuditLog{ID: uuid.New()})
	writer.send(domain.AuditLog{ID: uuid.New()})

	// Close should flush remaining
	writer.Close()

	repo.AssertNumberOfCalls(t, "Create", 2)
}

type mockAuditBatchWriter struct {
	entries []domain.AuditLog
	mu      sync.Mutex
}

func (m *mockAuditBatchWriter) send(entry domain.AuditLog) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entry)
}

func TestConnectAuditLogInterceptor(t *testing.T) {
	repo := &domainmocks.MockAuditLogRepository{}
	logger := zap.NewNop()
	writer := NewAuditBatchWriter(repo, logger)

	interceptor := ConnectAuditLogInterceptor(writer)

	// Mocking Connect RPC call
	next := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return &connect.Response[any]{}, nil
	}

	req := &connect.Request[any]{}
	req.Header().Set("User-Agent", "test-agent")
	req.Header().Set("X-Request-ID", "test-rid")
	req.Header().Set("X-Tenant-ID", "test-tenant")

	ctx := utils.WithTenantID(context.Background(), "test-tenant")

	// Setup repo expectation for the entry that will be sent
	repo.On("Create", mock.Anything, mock.MatchedBy(func(log domain.AuditLog) bool {
		return log.UserAgent != nil && *log.UserAgent == "test-agent" &&
			log.TenantID == "test-tenant"
	})).Return(nil)

	fn := interceptor.WrapUnary(next)
	_, err := fn(ctx, req)
	require.NoError(t, err)

	// Wait a bit for async send/flush or just close writer to force flush

	writer.Close()
	repo.AssertExpectations(t)
}

func TestConnectAuditLogInterceptor_Error(t *testing.T) {
	repo := &domainmocks.MockAuditLogRepository{}
	logger := zap.NewNop()
	writer := NewAuditBatchWriter(repo, logger)

	interceptor := ConnectAuditLogInterceptor(writer)

	// Mocking Connect RPC call with error
	next := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}

	req := &connect.Request[any]{}
	ctx := utils.WithTenantID(context.Background(), "test-tenant")
	repo.On("Create", mock.Anything, mock.MatchedBy(func(log domain.AuditLog) bool {
		return log.ResponseStatus != nil && *log.ResponseStatus == "error" &&
			log.ResponseCode != nil && *log.ResponseCode == "not_found" &&
			log.TenantID == "test-tenant"
	})).Return(nil)

	fn := interceptor.WrapUnary(next)
	_, err := fn(ctx, req)
	require.Error(t, err)

	writer.Close()
	repo.AssertExpectations(t)
}
