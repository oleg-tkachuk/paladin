package paladin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

const testServerVersion = "4.99.0"

// failing answers GetVersion with err, names a release, and sends
// Retry-After when retryAfter is set.
type failing struct {
	paladiniamv1connect.UnimplementedHealthServiceHandler
	err        *connect.Error
	retryAfter string
}

func (f failing) GetVersion(ctx context.Context, _ *iamv1.GetVersionRequest) (*iamv1.VersionInfo, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	info.ResponseHeader().Set(paladin.HeaderServerVersion, testServerVersion)
	if f.retryAfter != "" {
		info.ResponseHeader().Set(paladin.HeaderRetryAfter, f.retryAfter)
	}
	return nil, f.err
}

func withInfo(code connect.Code, domain, reason string) *connect.Error {
	e := connect.NewError(code, "refused")
	d, _ := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Domain: domain, Reason: reason})
	return e.WithDetail(d)
}

func callFailing(t *testing.T, h http.Handler) error {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := paladin.Connect(paladin.Endpoints{IAM: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.IAM.Health.GetVersion(context.Background(), &iamv1.GetVersionRequest{})
	return err
}

func serveFailing(f failing) http.Handler {
	mux := http.NewServeMux()
	server := connect.NewServer()
	paladiniamv1connect.RegisterHealthServiceHandler(server, f)
	connecthttp.Mount(mux, server)
	return mux
}

func TestErrorsMatchTheirKindAndCarryTheReason(t *testing.T) {
	bucket := commonv1.ErrorReason_ERROR_REASON_BUCKET_NOT_FOUND
	cases := []struct {
		name   string
		err    *connect.Error
		kind   error
		reason commonv1.ErrorReason
	}{
		{"not found, with its reason", withInfo(connect.CodeNotFound, paladin.ErrorDomain, bucket.String()), paladin.ErrNotFound, bucket},
		{"a reason this SDK predates", withInfo(connect.CodeNotFound, paladin.ErrorDomain, "ERROR_REASON_FROM_THE_FUTURE"), paladin.ErrNotFound, 0},
		{"another domain's reason", withInfo(connect.CodeNotFound, "elsewhere", bucket.String()), paladin.ErrNotFound, 0},
		{"invalid argument, with its reason", withInfo(connect.CodeInvalidArgument, paladin.ErrorDomain, commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT.String()), paladin.ErrInvalidArgument, commonv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT},
		{"no detail at all", connect.NewError(connect.CodeAlreadyExists, "x"), paladin.ErrAlreadyExists, 0},
		{"permission denied", connect.NewError(connect.CodePermissionDenied, "x"), paladin.ErrPermissionDenied, 0},
		{"failed precondition", connect.NewError(connect.CodeFailedPrecondition, "x"), paladin.ErrFailedPrecondition, 0},
		{"aborted is a version conflict", withInfo(connect.CodeAborted, paladin.ErrorDomain, commonv1.ErrorReason_ERROR_REASON_VERSION_CONFLICT.String()), paladin.ErrVersionConflict, commonv1.ErrorReason_ERROR_REASON_VERSION_CONFLICT},
		{"unauthenticated", connect.NewError(connect.CodeUnauthenticated, "x"), paladin.ErrUnauthenticated, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := callFailing(t, serveFailing(failing{err: tc.err}))
			if !errors.Is(err, tc.kind) {
				t.Errorf("errors.Is(%v, %v) = false", err, tc.kind)
			}
			if got := paladin.Reason(err); got != tc.reason {
				t.Errorf("Reason = %v, want %v", got, tc.reason)
			}
			if connect.CodeOf(err) != tc.err.Code() {
				t.Errorf("CodeOf = %v, want %v: the Connect error must still unwrap", connect.CodeOf(err), tc.err.Code())
			}
			var pe *paladin.Error
			if !errors.As(err, &pe) || pe.ServerVersion != testServerVersion || pe.Procedure != paladiniamv1connect.HealthServiceGetVersionProcedure {
				t.Errorf("*Error = %+v; want the procedure and the server's release", pe)
			}
			for _, other := range []error{paladin.ErrInvalidArgument, paladin.ErrNotFound, paladin.ErrAlreadyExists, paladin.ErrContractSkew} {
				if other != tc.kind && errors.Is(err, other) {
					t.Errorf("also matches %v", other)
				}
			}
		})
	}
}

func TestResourceExhaustedCarriesRetryAfter(t *testing.T) {
	e := connect.NewError(connect.CodeResourceExhausted, "slow down")
	err := callFailing(t, serveFailing(failing{err: e, retryAfter: "7"}))
	var pe *paladin.Error
	if !errors.Is(err, paladin.ErrResourceExhausted) || !errors.As(err, &pe) || pe.RetryAfter != 7*time.Second {
		t.Fatalf("err = %v, RetryAfter %v; want ResourceExhausted waiting 7s", err, pe.RetryAfter)
	}
}

func TestDetailsAreDecoded(t *testing.T) {
	err := callFailing(t, serveFailing(failing{err: withInfo(connect.CodeNotFound, paladin.ErrorDomain, "x")}))
	var pe *paladin.Error
	if !errors.As(err, &pe) || len(pe.Details) != 1 {
		t.Fatalf("details = %v, want the ErrorInfo", pe.Details)
	}
	if _, ok := pe.Details[0].(*errdetails.ErrorInfo); !ok {
		t.Errorf("detail is %T, want *errdetails.ErrorInfo", pe.Details[0])
	}
}

// A server older than the SDK does not serve the procedure: the error names
// it and both releases.
func TestContractSkewNamesTheProcedureAndBothVersions(t *testing.T) {
	cases := []struct {
		name    string
		handler http.Handler
		version string
	}{
		{"a server that names its release", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(paladin.HeaderServerVersion, testServerVersion)
			_ = connecthttp.NewErrorWriter().Write(w, r, connect.NewError(connect.CodeUnimplemented, "no such procedure"))
		}), testServerVersion},
		{"a bare 404", http.NotFoundHandler(), "unknown version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := callFailing(t, tc.handler)
			if !errors.Is(err, paladin.ErrContractSkew) {
				t.Fatalf("err = %v, want ErrContractSkew", err)
			}
			msg := err.Error()
			for _, want := range []string{paladiniamv1connect.HealthServiceGetVersionProcedure, tc.version, "sdk"} {
				if !strings.Contains(msg, want) {
					t.Errorf("%q does not name %q", msg, want)
				}
			}
		})
	}
}

func TestReasonOfAnotherError(t *testing.T) {
	if got := paladin.Reason(errors.New("plain")); got != commonv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
		t.Errorf("Reason = %v, want UNSPECIFIED", got)
	}
}
