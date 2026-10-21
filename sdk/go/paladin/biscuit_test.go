package paladin_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// sealed stands in for a Biscuit the server sealed.
const sealed = "biscuit-token"

func TestBiscuitOf(t *testing.T) {
	got, err := paladin.BiscuitOf(&adminv1.CapabilityServiceIssueResponse{
		Biscuit: sealed, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED,
	})
	if err != nil || got != sealed {
		t.Fatalf("an issued Biscuit = %q, %v", got, err)
	}
	for _, status := range []adminv1.BiscuitStatus{
		adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND,
		adminv1.BiscuitStatus_BISCUIT_STATUS_NOT_YET_VALID,
		// A server too old to say: no Biscuit is no Biscuit.
		adminv1.BiscuitStatus_BISCUIT_STATUS_UNSPECIFIED,
	} {
		got, err := paladin.BiscuitOf(&adminv1.CapabilityServiceIssueResponse{BiscuitStatus: status})
		if got != "" || !errors.Is(err, paladin.ErrNoBiscuit) || !strings.Contains(err.Error(), status.String()) {
			t.Errorf("%v: %q, %v; want ErrNoBiscuit naming the status", status, got, err)
		}
	}
}

// The Biscuit when the status says it is issued, the token otherwise, each
// with the status; neither is an error.
func TestCredentialOf(t *testing.T) {
	const token = "jwt-token"
	cases := map[string]struct {
		resp *adminv1.CapabilityServiceIssueResponse
		want paladin.Credential
	}{
		"issued": {
			&adminv1.CapabilityServiceIssueResponse{Token: token, Biscuit: sealed, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED},
			paladin.Credential{Value: sealed, Form: paladin.FormBiscuit, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED},
		},
		"key-bound": {
			&adminv1.CapabilityServiceIssueResponse{Token: token, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND},
			paladin.Credential{Value: token, Form: paladin.FormToken, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_KEY_BOUND},
		},
		"not yet valid": {
			&adminv1.CapabilityServiceIssueResponse{Token: token, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_NOT_YET_VALID},
			paladin.Credential{Value: token, Form: paladin.FormToken, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_NOT_YET_VALID},
		},
		// A server too old to name a status: a Biscuit beside it is not
		// trusted to be one, and the token is presented.
		"unspecified": {
			&adminv1.CapabilityServiceIssueResponse{Token: token, Biscuit: sealed},
			paladin.Credential{Value: token, Form: paladin.FormToken},
		},
	}
	for name, tc := range cases {
		got, err := paladin.CredentialOf(tc.resp)
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v, %v; want %+v", name, got, err, tc.want)
		}
	}
	if _, err := paladin.CredentialOf(&adminv1.CapabilityServiceIssueResponse{}); !errors.Is(err, paladin.ErrNoCredential) {
		t.Errorf("an empty answer: %v, want ErrNoCredential", err)
	}
	if paladin.FormToken.String() != "token" || paladin.FormBiscuit.String() != "biscuit" {
		t.Errorf("forms are named %q and %q", paladin.FormToken, paladin.FormBiscuit)
	}
}

// The token, even beside an issued Biscuit; an answer without one is
// ErrNoCredential.
func TestTokenOf(t *testing.T) {
	const token = "jwt-token"
	got, err := paladin.TokenOf(&adminv1.CapabilityServiceIssueResponse{
		Token: token, Biscuit: sealed, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED,
	})
	if err != nil || got != token {
		t.Fatalf("TokenOf = %q, %v; want the token", got, err)
	}
	got, err = paladin.TokenOf(&adminv1.CapabilityServiceIssueResponse{
		Biscuit: sealed, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED,
	})
	if got != "" || !errors.Is(err, paladin.ErrNoCredential) {
		t.Errorf("an answer with only a Biscuit: %q, %v; want ErrNoCredential", got, err)
	}
}

// A credential logs by name, through either slog handler, and never logs the
// capability it carries.
func TestCredentialLogsByNameWithoutItsValue(t *testing.T) {
	cred := paladin.Credential{Value: sealed, Form: paladin.FormBiscuit, BiscuitStatus: adminv1.BiscuitStatus_BISCUIT_STATUS_ISSUED}
	for name, handler := range map[string]func(*bytes.Buffer) slog.Handler{
		"json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
		"text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
	} {
		var buf bytes.Buffer
		slog.New(handler(&buf)).Info("issued", "credential", cred, "form", cred.Form)
		out := buf.String()
		if strings.Contains(out, sealed) {
			t.Errorf("%s: the log holds the capability: %s", name, out)
		}
		for _, want := range []string{"biscuit", "BISCUIT_STATUS_ISSUED"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: the log does not name %q: %s", name, want, out)
			}
		}
	}
	var line struct {
		Form string `json:"form"`
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("issued", "form", paladin.FormToken)
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil || line.Form != "token" {
		t.Errorf("a form logs as %q (%v), want \"token\"", line.Form, err)
	}
	if b, err := json.Marshal(paladin.FormBiscuit); err != nil || string(b) != `"biscuit"` {
		t.Errorf("a form marshals as %s (%v)", b, err)
	}
}
