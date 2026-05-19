package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/Rynaro/Junction/internal/dispatch"
)

// scannerBufSize is 4 MiB — large enough for tools/list responses that embed
// full inputSchema JSON for all four tools.
const scannerBufSize = 4 * 1024 * 1024

// mcpProtocolVersion is the MCP spec version this server implements.
const mcpProtocolVersion = "2025-03-26"

// rawResponse carries either the result bytes or an RPCError for a
// server-initiated request awaiting a client response.
type rawResponse struct {
	result json.RawMessage
	err    *RPCError
}

// Server is a hand-rolled minimal JSON-RPC 2.0 / stdio MCP server.
// It implements the MCP 2025-03-26 stdio transport with zero new external
// dependencies (NG15).
//
// v0.2: the server now supports server-initiated requests (sampling/createMessage)
// alongside the existing client→server request path. The read loop demuxes
// incoming messages by shape: request, notification, or response to a
// server-initiated request.
type Server struct {
	version string
	tools   *Registry

	// ─── v0.2: client capabilities ───────────────────────────────────────────
	// clientCapabilities is populated by handleInitialize. nil until initialize
	// completes.
	clientCapabilities *ClientCapabilities

	// ─── v0.2: server-initiated request support ──────────────────────────────
	// pendingRequests tracks server-initiated requests awaiting client responses.
	// Keyed by JSON-RPC id (string form of "srv-<n>"). Guarded by mu.
	pendingRequests map[string]chan rawResponse
	nextRequestID   int64 // incremented with atomic.AddInt64

	// mu guards clientCapabilities and pendingRequests.
	mu sync.Mutex

	// outEncMu guards outEnc so that SendRequest and the response-write path
	// can both safely write to the out stream. The read loop holds no lock;
	// only writers need outEncMu.
	outEncMu sync.Mutex
	outEnc   *json.Encoder

	// reasoningStep stores the reasoning closure wired by cmd/junction mcp serve.
	// nil until SetReasoningStep is called (harness.run uses noopReasoningStep
	// when nil).
	reasoningStep dispatch.ReasoningStepFunc
}

// NewServer constructs a Server using the given binary version string (from
// version.go / -ldflags) and the provided tool registry.
// Pass the result of NewRegistry() for the tool registry.
func NewServer(version string, tools *Registry) *Server {
	return &Server{
		version:         version,
		tools:           tools,
		pendingRequests: make(map[string]chan rawResponse),
	}
}

// SetTools replaces the tool registry. Called by cmd/junction mcp serve when
// the registry is constructed after the server (to allow bidirectional wiring).
func (s *Server) SetTools(tools *Registry) {
	s.mu.Lock()
	s.tools = tools
	s.mu.Unlock()
}

// SetReasoningStep stores the reasoning closure for use by harness.run.
// Called by cmd/junction mcp serve after constructing the provider.
func (s *Server) SetReasoningStep(fn dispatch.ReasoningStepFunc) {
	s.mu.Lock()
	s.reasoningStep = fn
	s.mu.Unlock()
}

// ReasoningStep returns the stored reasoning closure, or nil if none was set.
func (s *Server) ReasoningStep() dispatch.ReasoningStepFunc {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reasoningStep
}

// ClientCapabilities returns a snapshot of the capabilities the client
// declared during initialize. Returns nil if initialize has not yet completed.
func (s *Server) ClientCapabilities() *ClientCapabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientCapabilities
}

// SendRequest issues a server-initiated JSON-RPC request to the client and
// blocks until the client responds or ctx is cancelled. The result is the raw
// JSON of the response's "result" field.
//
// Per MCP lifecycle spec: senders SHOULD establish per-request timeouts; the
// caller's ctx carries this timeout.
func (s *Server) SendRequest(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := fmt.Sprintf("srv-%d", atomic.AddInt64(&s.nextRequestID, 1))

	ch := make(chan rawResponse, 1)
	s.mu.Lock()
	s.pendingRequests[id] = ch
	s.mu.Unlock()

	// Build the request frame.
	rawID, _ := json.Marshal(id)
	rawIDMsg := json.RawMessage(rawID)
	rawParams, err := json.Marshal(params)
	if err != nil {
		s.mu.Lock()
		delete(s.pendingRequests, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("mcp: SendRequest: marshal params: %w", err)
	}
	req := &Request{
		JSONRPC: "2.0",
		ID:      &rawIDMsg,
		Method:  method,
		Params:  rawParams,
	}

	s.outEncMu.Lock()
	encErr := s.outEnc.Encode(req)
	s.outEncMu.Unlock()
	if encErr != nil {
		s.mu.Lock()
		delete(s.pendingRequests, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("mcp: SendRequest: writing request: %w", encErr)
	}

	// Wait for the client response.
	select {
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pendingRequests, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("mcp: SendRequest %q: %w", method, ctx.Err())
	case resp := <-ch:
		if resp.err != nil {
			return nil, fmt.Errorf("mcp: SendRequest %q: RPC error %d: %s", method, resp.err.Code, resp.err.Message)
		}
		return resp.result, nil
	}
}

// Serve reads JSON-RPC 2.0 messages from in (one per line, MCP stdio
// transport) and writes responses to out until in is exhausted or ctx is
// cancelled.
//
// v0.2: the read loop demuxes incoming messages:
//   - Has method + id      → client request (dispatch to handler).
//   - Has method, no id    → notification (no response).
//   - Has id, result/error → response to a server-initiated request.
//
// Errors returned are I/O errors on in/out; JSON-RPC protocol errors are
// returned as error responses on out, not as Go errors.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, scannerBufSize), scannerBufSize)

	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)

	// Capture the encoder under outEncMu so SendRequest can share the same
	// out stream without races.
	s.outEncMu.Lock()
	s.outEnc = enc
	s.outEncMu.Unlock()

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

		// Peek at the raw message to determine its shape before a full
		// unmarshal into Request.
		var peek struct {
			JSONRPC string           `json:"jsonrpc"`
			ID      *json.RawMessage `json:"id"`
			Method  string           `json:"method"`
			Result  json.RawMessage  `json:"result"`
			Error   *RPCError        `json:"error"`
		}
		if err := json.Unmarshal(line, &peek); err != nil {
			// Parse error — respond with a parse-error response if possible.
			resp := &ErrorResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   RPCError{Code: CodeParseError, Message: "parse error: " + err.Error()},
			}
			s.outEncMu.Lock()
			_ = enc.Encode(resp)
			s.outEncMu.Unlock()
			continue
		}

		// Response to a server-initiated request: has id, no method.
		if peek.ID != nil && peek.Method == "" {
			idStr := ""
			_ = json.Unmarshal(*peek.ID, &idStr)
			s.mu.Lock()
			ch, ok := s.pendingRequests[idStr]
			if ok {
				delete(s.pendingRequests, idStr)
			}
			s.mu.Unlock()
			if ok {
				rr := rawResponse{result: peek.Result, err: peek.Error}
				select {
				case ch <- rr:
				default:
				}
			}
			continue
		}

		// Client request or notification — use the existing handle path.
		resp := s.handle(ctx, line)
		if resp == nil {
			// Notification — no response required.
			continue
		}
		s.outEncMu.Lock()
		encErr := enc.Encode(resp)
		s.outEncMu.Unlock()
		if encErr != nil {
			return fmt.Errorf("mcp: writing response: %w", encErr)
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
// v0.2: also caches the client's declared capabilities.
func (s *Server) handleInitialize(req *Request) (interface{}, *RPCError) {
	if len(req.Params) > 0 {
		var params InitializeParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &RPCError{Code: CodeInvalidParams, Message: "initialize: " + err.Error()}
		}
		s.mu.Lock()
		s.clientCapabilities = &params.Capabilities
		s.mu.Unlock()
	}

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
