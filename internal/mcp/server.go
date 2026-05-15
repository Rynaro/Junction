package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// scannerBufSize is 4 MiB — large enough for tools/list responses that embed
// full inputSchema JSON for all four tools.
const scannerBufSize = 4 * 1024 * 1024

// mcpProtocolVersion is the MCP spec version this server implements.
const mcpProtocolVersion = "2025-03-26"

// Server is a hand-rolled minimal JSON-RPC 2.0 / stdio MCP server.
// It implements the MCP 2025-03-26 stdio transport with zero new external
// dependencies (NG15).
//
// The server is single-threaded over stdio: one goroutine reads from in,
// processes each message, and writes the response to out. There is no
// concurrent dispatch — this is sufficient for the four pure request/response
// tools in §7.4 (assumption A3, verified and HELD).
type Server struct {
	version string
	tools   *Registry
}

// NewServer constructs a Server using the given binary version string (from
// version.go / -ldflags) and the provided tool registry.
// Pass the result of NewRegistry() for the tool registry.
func NewServer(version string, tools *Registry) *Server {
	return &Server{version: version, tools: tools}
}

// Serve reads JSON-RPC 2.0 messages from in (one per line, MCP stdio
// transport) and writes responses to out until in is exhausted or ctx is
// cancelled.
//
// Errors returned are I/O errors on in/out; JSON-RPC protocol errors are
// returned as error responses on out, not as Go errors.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, scannerBufSize), scannerBufSize)

	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("mcp: reading stdin: %w", err)
			}
			// EOF — client closed the connection.
			return nil
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		resp := s.handle(ctx, line)
		if resp == nil {
			// Notification — no response required.
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("mcp: writing response: %w", err)
		}
	}
}

// handle processes one raw JSON line and returns the value to encode as the
// response, or nil for notifications (no id).
func (s *Server) handle(ctx context.Context, line []byte) interface{} {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return &ErrorResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   RPCError{Code: CodeParseError, Message: "parse error: " + err.Error()},
		}
	}

	// Notifications have no id — process but do not reply.
	if req.ID == nil {
		s.handleNotification(ctx, &req)
		return nil
	}

	result, rpcErr := s.dispatch(ctx, &req)
	if rpcErr != nil {
		return &ErrorResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   *rpcErr,
		}
	}

	raw, err := json.Marshal(result)
	if err != nil {
		return &ErrorResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   RPCError{Code: CodeInternalError, Message: "internal error: marshal result: " + err.Error()},
		}
	}
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  raw,
	}
}

// handleNotification processes a JSON-RPC notification (no id, no response).
func (s *Server) handleNotification(_ context.Context, req *Request) {
	// The only notification we expect is "notifications/initialized" — no action
	// needed beyond acknowledging it internally.
	_ = req.Method
}

// dispatch routes a method call to the appropriate handler.
func (s *Server) dispatch(ctx context.Context, req *Request) (interface{}, *RPCError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList()
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		return nil, &RPCError{
			Code:    CodeMethodNotFound,
			Message: fmt.Sprintf("method not found: %q", req.Method),
		}
	}
}

// handleInitialize responds to the MCP initialize handshake.
func (s *Server) handleInitialize(_ *Request) (interface{}, *RPCError) {
	version := s.version
	if version == "" {
		version = "dev"
	}
	return &InitializeResult{
		ProtocolVersion: mcpProtocolVersion,
		ServerInfo: ServerInfo{
			Name:    "junction",
			Version: version,
		},
		Capabilities: ServerCapabilities{
			Tools: ToolsCapability{},
		},
	}, nil
}

// handleToolsList returns the full tool catalog.
func (s *Server) handleToolsList() (interface{}, *RPCError) {
	return &ToolsListResult{Tools: s.tools.Definitions()}, nil
}

// handleToolsCall dispatches a tools/call request to the named tool handler.
func (s *Server) handleToolsCall(ctx context.Context, req *Request) (interface{}, *RPCError) {
	var params ToolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, &RPCError{
			Code:    CodeInvalidParams,
			Message: "invalid params for tools/call: " + err.Error(),
		}
	}

	handler, ok := s.tools.Handler(params.Name)
	if !ok {
		return nil, &RPCError{
			Code:    CodeMethodNotFound,
			Message: fmt.Sprintf("tool not found: %q", params.Name),
		}
	}

	result, err := handler(ctx, params.Arguments)
	if err != nil {
		// Handler error → isError: true, but still a valid tools/call response
		// (not a JSON-RPC protocol error).
		return &ToolsCallResult{
			Content: []ContentItem{{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}

	// Encode the handler's result as a JSON string in the text content item.
	// The LLM receives the JSON payload as text it can parse.
	return &ToolsCallResult{
		Content: []ContentItem{{Type: "text", Text: string(result)}},
		IsError: false,
	}, nil
}
