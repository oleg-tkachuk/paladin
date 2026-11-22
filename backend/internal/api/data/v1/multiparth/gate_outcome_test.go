package multiparth

import (
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
)

// The outcome lines a multipart RPC ends with.
const (
	lineRefused = "multipart refused"
	lineFailed  = "multipart failed"
)

// errStoreUnreachable is a bucket lookup the store could not answer.
var errStoreUnreachable = errors.New("connection reset")

// A multipart write to a bucket still provisioning is the gate's refusal and
// logged as one, at warn, as the batch handler logs it; a lookup the store
// could not answer stays the server's fault, at error.
func TestMultipartGateAnswersAreLoggedByWhatTheyAre(t *testing.T) {
	provisioning := bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionPending}
	for _, rpc := range multipartWrites {
		for _, tc := range []struct {
			name      string
			state     bucketgate.State
			lookupErr error
			code      connect.Code
			line      string
			level     zapcore.Level
		}{
			{"provisioning", provisioning, nil, connect.CodeUnavailable, lineRefused, zapcore.WarnLevel},
			{"store unreachable", bucketgate.State{}, errStoreUnreachable, connect.CodeInternal, lineFailed, zapcore.ErrorLevel},
		} {
			t.Run(rpc.name+"/"+tc.name, func(t *testing.T) {
				core, logs := observer.New(zapcore.DebugLevel)
				tid := uuid.New()
				h := newHandler(gatedRepo(sessionForTenant(tid), tc.state, tc.lookupErr), &fakeStorage{}, &recordingAuthorizer{})
				h.SetLogger(zap.New(core))
				wantCode(t, rpc.call(h, authedCtx(tid)), tc.code)
				lines := logs.FilterMessage(tc.line).All()
				if len(lines) != 1 || lines[0].Level != tc.level {
					t.Fatalf("want one %v %q line, got %v", tc.level, tc.line, logs.All())
				}
			})
		}
	}
}
