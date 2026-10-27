package middleware

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// The row names the client a call came from, as its User-Agent says: the SDK
// and the application's suffix. One the caller made too long is cut at the
// bound, and never inside a character.
func TestAuditRecordsTheUserAgent(t *testing.T) {
	const sdk = "paladin-sdk-go/0.97.0 gateway/1.4"
	// A two-byte rune straddling the bound, so a byte cut would split it.
	long := strings.Repeat("a", MaxAuditUserAgentBytes-1) + "é" + "tail"
	for name, tc := range map[string]struct{ sent, want string }{
		"an SDK's": {sdk, sdk},
		"too long": {long, long[:MaxAuditUserAgentBytes-1]},
		"at bound": {strings.Repeat("b", MaxAuditUserAgentBytes), strings.Repeat("b", MaxAuditUserAgentBytes)},
	} {
		t.Run(name, func(t *testing.T) {
			w := &recordingAuditWriter{}
			client := paladiniamv1connect.NewUserSettingsServiceClient(unarytest.Client(func(s *connect.Server) {
				paladiniamv1connect.RegisterUserSettingsServiceHandler(s, &stubSettings{})
			}, AuditWithMirror(w, "paladin-iam", false, nil)))
			ctx := unarytest.WithHeader(principalCtx(uuid.New()), HeaderUserAgent, tc.sent)
			if _, err := client.UpdateMine(ctx, &iamv1.UpdateMineRequest{}); err != nil {
				t.Fatalf("call: %v", err)
			}
			if len(w.rows) != 1 {
				t.Fatalf("%d rows written, want 1", len(w.rows))
			}
			got := w.rows[0].UserAgent
			if got != tc.want {
				t.Errorf("row names %q, want %q", got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("row holds a cut character: %q", got)
			}
		})
	}
}

// Outside a call there is no header to read, and no client to name.
func TestUserAgentOutsideACall(t *testing.T) {
	if got := userAgent(context.Background()); got != "" {
		t.Errorf("userAgent = %q, want empty", got)
	}
}

// The row holds the request the call was made with, as JSON — what the
// action was asked to do, not the resource's state after it.
func TestAuditRecordsTheRequest(t *testing.T) {
	const theme = "navy"
	w := &recordingAuditWriter{}
	client := paladiniamv1connect.NewUserSettingsServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterUserSettingsServiceHandler(s, &stubSettings{})
	}, AuditWithMirror(w, "paladin-iam", false, nil)))
	if _, err := client.UpdateMine(principalCtx(uuid.New()), &iamv1.UpdateMineRequest{Theme: theme}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if len(w.rows) != 1 {
		t.Fatalf("%d rows written, want 1", len(w.rows))
	}
	var got struct {
		Theme string `json:"theme"`
	}
	if err := json.Unmarshal(w.rows[0].RequestJSON, &got); err != nil || got.Theme != theme {
		t.Errorf("request = %s (%v), want the theme %q", w.rows[0].RequestJSON, err, theme)
	}
}
