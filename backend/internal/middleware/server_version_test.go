package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

const testServerVersion = "4.99.0"

type versionHealth struct {
	paladiniamv1connect.UnimplementedHealthServiceHandler
	fail bool
}

func (h versionHealth) GetVersion(context.Context, *iamv1.GetVersionRequest) (*iamv1.VersionInfo, error) {
	if h.fail {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "refused")
	}
	return &iamv1.VersionInfo{}, nil
}

func TestServerVersionStampsEveryResponse(t *testing.T) {
	cases := []struct {
		name    string
		version string
		fail    bool
		// mounted false serves nothing: the call is to a procedure this
		// release lacks, which a client reads as Unimplemented.
		mounted  bool
		want     string
		wantCode connect.Code
	}{
		{"a success", testServerVersion, false, true, testServerVersion, 0},
		{"an error", testServerVersion, true, true, testServerVersion, connect.CodeFailedPrecondition},
		{"a procedure this release lacks", testServerVersion, false, false, testServerVersion, connect.CodeUnimplemented},
		{"a build without a version", "", false, true, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.Handle(UnknownProcedurePattern, UnknownProcedure())
			if tc.mounted {
				server := connect.NewServer()
				paladiniamv1connect.RegisterHealthServiceHandler(server, versionHealth{fail: tc.fail})
				connecthttp.Mount(mux, server, connecthttp.WithReadMaxBytes(0))
			}
			srv := httptest.NewServer(ServerVersion(tc.version, mux))
			defer srv.Close()
			// The response headers are the call's, whether it succeeded or
			// failed.
			ctx, info := connect.NewClientContext(context.Background())
			_, err := paladiniamv1connect.NewHealthServiceClient(connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL, connecthttp.WithReadMaxBytes(0)))).
				GetVersion(ctx, &iamv1.GetVersionRequest{})
			if tc.wantCode != 0 && connect.CodeOf(err) != tc.wantCode {
				t.Fatalf("err = %v, want %v", err, tc.wantCode)
			}
			if got := info.ResponseHeader().Get(paladin.HeaderServerVersion); got != tc.want {
				t.Errorf("%s = %q, want %q", paladin.HeaderServerVersion, got, tc.want)
			}
		})
	}
}

// A request in no RPC protocol still gets a plain 404.
func TestUnknownProcedureLeavesOtherRequestsA404(t *testing.T) {
	srv := httptest.NewServer(UnknownProcedure())
	defer srv.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/upload", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
