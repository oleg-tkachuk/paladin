package service_test

import (
	"context"
	"errors"
	"paladin/internal/service"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type MockPinger struct {
	err error
}

func (m *MockPinger) Ping(ctx context.Context) error {
	return m.err
}

type MockS3HealthChecker struct {
	err error
}

func (m *MockS3HealthChecker) Health(ctx context.Context) error {
	return m.err
}

var _ = Describe("HealthService", func() {
	var (
		mockPinger *MockPinger
		mockS3     *MockS3HealthChecker
		mockBrk    *MockBreakerFactory
		svc        *service.HealthService
		ctx        context.Context
	)

	BeforeEach(func() {
		mockPinger = &MockPinger{}
		mockS3 = &MockS3HealthChecker{}
		mockBrk = &MockBreakerFactory{}
		svc = service.NewHealthService(mockPinger, mockS3, mockBrk)
		ctx = context.Background()
	})

	It("should report ready when all dependencies are ok", func() {
		mockBrk.On("CheckHealth").Return(map[string]string{"db": "closed"})

		ready, status := svc.CheckReady(ctx)

		Expect(ready).To(BeTrue())
		Expect(status.PostgreSQL).To(Equal("ok"))
		Expect(status.SeaweedFS).To(Equal("ok"))
	})

	It("should report not ready when postgres fails", func() {
		mockPinger.err = errors.New("db error")
		mockBrk.On("CheckHealth").Return(map[string]string{"db": "closed"})

		ready, status := svc.CheckReady(ctx)

		Expect(ready).To(BeFalse())
		Expect(status.PostgreSQL).To(Equal("db error"))
	})

	It("should report not ready when a circuit breaker is open", func() {
		mockBrk.On("CheckHealth").Return(map[string]string{"api": "open"})

		ready, _ := svc.CheckReady(ctx)

		Expect(ready).To(BeFalse())
	})
})
