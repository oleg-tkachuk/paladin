package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestInitialize(t *testing.T) {
	s := NewServer("paladin-mcp-test", "0.0.0", nil)
	resp, err := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Result struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
			ServerInfo      map[string]any `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatal(err)
	}
	if got.Result.ProtocolVersion == "" {
		t.Error("missing protocolVersion")
	}
	if got.Result.ServerInfo["name"] != "paladin-mcp-test" {
		t.Errorf("server name: %v", got.Result.ServerInfo)
	}
}

func TestToolsListAndCall(t *testing.T) {
	s := NewServer("t", "0.0.0", nil)
	s.Tools.Register(Tool{
		Name:        "echo",
		Description: "echoes input",
		InputSchema: map[string]any{"type": "object"},
		Handler: func(_ context.Context, args json.RawMessage) (any, error) {
			return TextResult(string(args)), nil
		},
	})

	listResp, _ := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if !strings.Contains(string(listResp), `"name":"echo"`) {
		t.Errorf("tools/list missing echo: %s", listResp)
	}

	callResp, _ := s.Handle(context.Background(), []byte(
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"hello":"world"}}}`,
	))
	// JSON-in-JSON: the inner object gets escaped inside `text`, so the
	// substring search uses the escaped form.
	if !strings.Contains(string(callResp), `\"hello\":\"world\"`) {
		t.Errorf("tools/call did not echo args: %s", callResp)
	}
}

func TestUnknownMethod(t *testing.T) {
	s := NewServer("t", "0.0.0", nil)
	resp, _ := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"nope"}`))
	if !strings.Contains(string(resp), `"code":-32601`) {
		t.Errorf("expected method not found: %s", resp)
	}
}

func TestNotificationSuppressed(t *testing.T) {
	s := NewServer("t", "0.0.0", nil)
	resp, err := s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","method":"initialized"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp != nil {
		t.Errorf("expected nil response for notification, got %s", resp)
	}
}

func TestParseError(t *testing.T) {
	s := NewServer("t", "0.0.0", nil)
	resp, _ := s.Handle(context.Background(), []byte(`not json`))
	if !strings.Contains(string(resp), `"code":-32700`) {
		t.Errorf("expected parse error: %s", resp)
	}
}
