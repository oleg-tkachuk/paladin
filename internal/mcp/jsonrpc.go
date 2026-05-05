// Package mcp implements a minimal Model Context Protocol server.
//
// MCP is a JSON-RPC 2.0-based protocol for exposing tools/resources/prompts
// to LLM clients. We implement the small subset PALADIN needs:
//
//   - `initialize` / `initialized` lifecycle
//   - `tools/list` + `tools/call`
//   - `resources/list` + `resources/read`
//   - `prompts/list` + `prompts/get`
//   - `ping`
//
// Two transports are supported: stdio (newline-delimited JSON) for desktop
// LLM clients, and HTTP+SSE for remote setups. Both wrap the same core
// Server, which holds the PALADIN Connect clients used to satisfy tool calls.
package mcp

import (
	"encoding/json"
	"fmt"
)

// JSON-RPC 2.0 framing.

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC error codes (subset).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

func newError(id json.RawMessage, code int, message string, data any) jsonrpcResponse {
	return jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: message, Data: data},
	}
}

func newResult(id json.RawMessage, result any) jsonrpcResponse {
	return jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

// isNotification reports whether a request is a JSON-RPC notification (no id).
// Notifications must NOT receive a response.
func (r *jsonrpcRequest) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// MarshalLine renders a response with a trailing newline (stdio transport).
func marshalLine(resp jsonrpcResponse) ([]byte, error) {
	b, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal response: %w", err)
	}
	return append(b, '\n'), nil
}
