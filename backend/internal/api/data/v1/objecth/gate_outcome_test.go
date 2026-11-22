package objecth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
)

// The outcome lines an RPC ends with.
const (
	lineRefused = "object rpc refused"
	lineFailed  = "object rpc failed"
)

// The bucket gate's refusals are refusals whatever their code — a bucket
// still provisioning answers Unavailable so the caller retries — while an
// Unavailable the server answers of its own is its fault.
func TestServerFaultSparesTheBucketGatesRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"a provisioning bucket, as the caller is told", MapResolveErr(bucketgate.ErrBucketProvisioning), false},
		{"a provisioning bucket, wrapped", fmt.Errorf("resolve: %w", bucketgate.ErrBucketProvisioning), false},
		{"a disabled backend", MapResolveErr(bucketgate.ErrBackendDisabled), false},
		{"the server unavailable", connect.NewError(connect.CodeUnavailable, msgBytesUnchecked), true},
		{"the server at fault", connect.NewError(connect.CodeInternal, "boom"), true},
		{"an unknown failure", errors.New("boom"), true},
		{"a missing object", connect.NewError(connect.CodeNotFound, "object not found"), false},
	} {
		if got := serverFault(tc.err); got != tc.want {
			t.Errorf("%s: serverFault = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A write to a bucket still provisioning is logged as refused at warn, as the
// batch handler logs the same refusal; it was logged as failed at error,
// paging for a state the reconciler leaves on its own.
func TestAProvisioningBucketIsLoggedAsARefusal(t *testing.T) {
	t.Parallel()
	core, logs := observer.New(zapcore.DebugLevel)
	h := &Handler{
		repo:    &gatedRepo{state: bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionPending}},
		storage: noopStorage{}, policy: &denyRecorder{}, presign: testPresignConfig(),
	}
	h.SetLogger(zap.New(core))
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: uuid.New()})
	_, err := h.UploadObject(ctx, UploadObjectInput{
		Collection: gateTestCollection, Key: "a.txt", ContentType: "text/plain",
		ChecksumAlgo: checksum.SHA256, ChecksumValue: testChecksumValue,
	})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("UploadObject = %v, want the gate's Unavailable", err)
	}
	if n := logs.FilterMessage(lineFailed).Len(); n != 0 {
		t.Errorf("%d %q lines, want none", n, lineFailed)
	}
	refused := logs.FilterMessage(lineRefused).All()
	if len(refused) != 1 || refused[0].Level != zapcore.WarnLevel {
		t.Fatalf("want one warn %q line, got %v", lineRefused, logs.All())
	}
}
