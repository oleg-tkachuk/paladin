package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	datav1connect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

// presignPlane records the TTL each presign RPC receives.
type presignPlane struct {
	datav1connect.UnimplementedPresignServiceHandler
	datav1connect.UnimplementedMultipartUploadServiceHandler
	ttls map[string]*durationpb.Duration
}

func (p *presignPlane) PresignDownload(_ context.Context, r *datav1.PresignDownloadRequest) (*datav1.PresignDownloadResponse, error) {
	p.ttls[toolPresignDownload] = r.GetTtl()
	return &datav1.PresignDownloadResponse{}, nil
}

func (p *presignPlane) RegenerateUploadUrl(_ context.Context, r *datav1.RegenerateUploadUrlRequest) (*datav1.RegenerateUploadUrlResponse, error) {
	p.ttls[toolRegenerateUploadURL] = r.GetTtl()
	return &datav1.RegenerateUploadUrlResponse{}, nil
}

func (p *presignPlane) PresignPart(_ context.Context, r *datav1.PresignPartRequest) (*datav1.PresignPartResponse, error) {
	p.ttls[toolPresignPart] = r.GetTtl()
	return &datav1.PresignPartResponse{}, nil
}

const (
	toolPresignDownload     = "paladin_presign_download"
	toolRegenerateUploadURL = "paladin_regenerate_upload_url"
	toolPresignPart         = "paladin_presign_part"
	testUploadID            = "upload-1"
	firstPart               = 1
	// testPartChecksum is a base64 digest; the plane fake does not check it.
	testPartChecksum = "AAAAAA=="
)

// presignToolArgs is each presign tool's required arguments, without a TTL.
func presignToolArgs() map[string]map[string]any {
	return map[string]map[string]any{
		toolPresignDownload:     {"name": testObjectName},
		toolRegenerateUploadURL: {"name": testObjectName},
		toolPresignPart: {
			"object_name": testObjectName, "upload_id": testUploadID, "part_number": firstPart,
			"checksum_value": testPartChecksum,
		},
	}
}

func callPresignTool(t *testing.T, plane *presignPlane, tool string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	mux := http.NewServeMux()
	server := connect.NewServer()
	datav1connect.RegisterPresignServiceHandler(server, plane)
	datav1connect.RegisterMultipartUploadServiceHandler(server, plane)
	connecthttp.Mount(mux, server)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	cs := dialInProcess(t, mustClients(t)(NewClients(ts.Client(), ts.URL, ts.URL, ts.URL, "tok")))
	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A ttl_seconds a duration cannot hold is refused before any plane is called,
// naming the argument and the limit. It was multiplied as it came, so the
// lifetime wrapped and the plane refused a TTL the agent did not send.
func TestPresignToolsRefuseATTLPastWhatADurationHolds(t *testing.T) {
	limit := strconv.FormatInt(apiutil.MaxTTLSeconds, 10)
	for tool, args := range presignToolArgs() {
		for _, ttl := range []int64{apiutil.MaxTTLSeconds + 1, 1 << 62} {
			plane := &presignPlane{ttls: map[string]*durationpb.Duration{}}
			args[ttlSecondsArg] = ttl
			res := callPresignTool(t, plane, tool, args)
			if !res.IsError {
				t.Fatalf("%s ttl %d: succeeded, want a refusal", tool, ttl)
			}
			text := res.Content[0].(*mcpsdk.TextContent).Text
			for _, want := range []string{connect.CodeInvalidArgument.String(), ttlSecondsArg, limit} {
				if !strings.Contains(text, want) {
					t.Errorf("%s ttl %d: %q does not name %q", tool, ttl, text, want)
				}
			}
			if _, called := plane.ttls[tool]; called {
				t.Errorf("%s ttl %d: the plane was called", tool, ttl)
			}
		}
	}
}

// A ttl_seconds within the limit reaches the plane as that lifetime, and none
// leaves the plane's default in place.
func TestPresignToolsSendTheTTLAsked(t *testing.T) {
	for tool, args := range presignToolArgs() {
		for _, tc := range []struct {
			seconds int64
			want    *durationpb.Duration
		}{
			{0, nil},
			{300, durationpb.New(5 * time.Minute)},
			{apiutil.MaxTTLSeconds, durationpb.New(time.Duration(apiutil.MaxTTLSeconds) * time.Second)},
		} {
			plane := &presignPlane{ttls: map[string]*durationpb.Duration{}}
			args[ttlSecondsArg] = tc.seconds
			if res := callPresignTool(t, plane, tool, args); res.IsError {
				t.Fatalf("%s ttl %d: %v", tool, tc.seconds, res.Content[0].(*mcpsdk.TextContent).Text)
			}
			got := plane.ttls[tool]
			if got.AsDuration() != tc.want.AsDuration() || (got == nil) != (tc.want == nil) {
				t.Errorf("%s ttl %d: plane got %v, want %v", tool, tc.seconds, got, tc.want)
			}
		}
	}
}

// No plane sees a ttl_seconds the bridge refuses, so the bridge logs the
// refusal itself, at Warn, naming the tool; a TTL it accepts logs nothing.
func TestPresignTTLLogsTheRefusal(t *testing.T) {
	for _, tc := range []struct {
		seconds int64
		logged  bool
	}{
		{apiutil.MaxTTLSeconds + 1, true},
		{apiutil.MaxTTLSeconds, false},
		{0, false},
	} {
		core, logs := observer.New(zap.DebugLevel)
		ctx := logger.WithContext(context.Background(), zap.New(core))
		_, err := presignTTL(ctx, toolPresignPart, tc.seconds)
		if (err != nil) != tc.logged {
			t.Errorf("ttl %d: err = %v, want refused=%v", tc.seconds, err, tc.logged)
		}
		got := logs.FilterMessage(ttlRefusedMessage).All()
		if logged := len(got) == 1; logged != tc.logged {
			t.Fatalf("ttl %d: logged = %v, want %v", tc.seconds, logged, tc.logged)
		}
		if tc.logged && (got[0].Level != zapcore.WarnLevel || got[0].ContextMap()["tool"] != toolPresignPart) {
			t.Errorf("ttl %d: entry = %v %v", tc.seconds, got[0].Level, got[0].ContextMap())
		}
	}
}
