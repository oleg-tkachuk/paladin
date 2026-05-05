package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"go.uber.org/zap"
)

// Server is the protocol-agnostic MCP core. Transports (stdio, HTTP+SSE)
// feed it raw JSON-RPC payloads and consume the marshalled responses.
type Server struct {
	Name        string
	Version     string
	Logger      *zap.Logger
	Tools       *ToolRegistry
	Resources   *ResourceRegistry
	Prompts     *PromptRegistry
	initialized bool
}

func NewServer(name, version string, logger *zap.Logger) *Server {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Server{
		Name:      name,
		Version:   version,
		Logger:    logger,
		Tools:     NewToolRegistry(),
		Resources: NewResourceRegistry(),
		Prompts:   NewPromptRegistry(),
	}
}

// initializeResult is the response payload for `initialize`.
type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      map[string]any `json:"serverInfo"`
}

// Handle processes one JSON-RPC frame. Returns the marshalled response, or
// nil + nil for notifications. Returns an error only on parse failures.
func (s *Server) Handle(ctx context.Context, raw []byte) ([]byte, error) {
	var req jsonrpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		resp := newError(nil, codeParseError, "parse error: "+err.Error(), nil)
		return marshalLine(resp)
	}
	if req.JSONRPC != "2.0" {
		resp := newError(req.ID, codeInvalidRequest, "jsonrpc field must be '2.0'", nil)
		return marshalLine(resp)
	}

	resp, err := s.dispatch(ctx, req)
	if err != nil {
		return marshalLine(newError(req.ID, codeInternalError, err.Error(), nil))
	}
	if req.isNotification() {
		// Suppress responses to notifications per JSON-RPC.
		return nil, nil
	}
	return marshalLine(resp)
}

func (s *Server) dispatch(ctx context.Context, req jsonrpcRequest) (jsonrpcResponse, error) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req), nil
	case "initialized":
		s.initialized = true
		return jsonrpcResponse{}, nil
	case "ping":
		return newResult(req.ID, map[string]any{}), nil

	case "tools/list":
		return newResult(req.ID, map[string]any{"tools": s.Tools.List()}), nil
	case "tools/call":
		return s.handleToolCall(ctx, req), nil

	case "resources/list":
		return newResult(req.ID, map[string]any{"resources": s.Resources.List()}), nil
	case "resources/read":
		return s.handleResourceRead(ctx, req), nil

	case "prompts/list":
		return newResult(req.ID, map[string]any{"prompts": s.Prompts.List()}), nil
	case "prompts/get":
		return s.handlePromptGet(ctx, req), nil

	case "shutdown":
		return newResult(req.ID, nil), nil
	}
	return newError(req.ID, codeMethodNotFound, "method not found: "+req.Method, nil), nil
}

func (s *Server) handleInitialize(req jsonrpcRequest) jsonrpcResponse {
	return newResult(req.ID, initializeResult{
		ProtocolVersion: "2025-03-26",
		Capabilities: map[string]any{
			"tools":     map[string]any{"listChanged": false},
			"resources": map[string]any{"listChanged": false, "subscribe": false},
			"prompts":   map[string]any{"listChanged": false},
		},
		ServerInfo: map[string]any{
			"name":    s.Name,
			"version": s.Version,
		},
	})
}

func (s *Server) handleToolCall(ctx context.Context, req jsonrpcRequest) jsonrpcResponse {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newError(req.ID, codeInvalidParams, "tools/call params: "+err.Error(), nil)
	}
	tool, ok := s.Tools.Get(p.Name)
	if !ok {
		return newError(req.ID, codeMethodNotFound, "no such tool: "+p.Name, nil)
	}
	out, err := tool.Handler(ctx, p.Arguments)
	if err != nil {
		// Tool errors are reported as result with isError=true per MCP spec.
		return newResult(req.ID, toolErrorResult(err))
	}
	return newResult(req.ID, out)
}

func (s *Server) handleResourceRead(ctx context.Context, req jsonrpcRequest) jsonrpcResponse {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newError(req.ID, codeInvalidParams, "resources/read params: "+err.Error(), nil)
	}
	out, err := s.Resources.Read(ctx, p.URI)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return newError(req.ID, codeMethodNotFound, "resource not found: "+p.URI, nil)
		}
		return newError(req.ID, codeInternalError, err.Error(), nil)
	}
	return newResult(req.ID, out)
}

func (s *Server) handlePromptGet(ctx context.Context, req jsonrpcRequest) jsonrpcResponse {
	var p struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newError(req.ID, codeInvalidParams, "prompts/get params: "+err.Error(), nil)
	}
	out, err := s.Prompts.Render(ctx, p.Name, p.Arguments)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return newError(req.ID, codeMethodNotFound, "no such prompt: "+p.Name, nil)
		}
		return newError(req.ID, codeInternalError, err.Error(), nil)
	}
	return newResult(req.ID, out)
}

// ServeStdio runs the server against stdio (newline-delimited JSON).
// Each frame is one JSON-RPC message. Blocks until stdin closes or ctx is
// cancelled.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	dec := json.NewDecoder(in)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("mcp: read: %w", err)
		}
		resp, herr := s.Handle(ctx, raw)
		if herr != nil {
			s.Logger.Error("handle frame failed", zap.Error(herr))
			continue
		}
		if resp == nil {
			continue
		}
		if _, err := out.Write(resp); err != nil {
			return fmt.Errorf("mcp: write: %w", err)
		}
	}
}

// ErrNotFound is returned by registries when a name/URI doesn't resolve.
var ErrNotFound = errors.New("mcp: not found")

// toolErrorResult wraps a Go error as an MCP "isError" result. The LLM sees
// the message as text content and can decide how to react.
func toolErrorResult(err error) any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": err.Error()}},
		"isError": true,
	}
}
