// Package mcp is a hand-rolled minimal JSON-RPC 2.0 / stdio MCP server for
// Junction. It satisfies the MCP 2025-03-26 stdio transport contract with zero
// new external dependencies (NG15).
//
// Transport: one JSON-RPC 2.0 message per line on stdin; responses written as
// single JSON lines to stdout. This is the MCP stdio transport (not HTTP/SSE —
// that is explicitly deferred per NG15).
//
// Supported methods:
//
//   - initialize / notifications/initialized — MCP handshake
//   - tools/list                              — enumerate the four harness.* tools
//   - tools/call                              — invoke a single tool
package mcp

import "encoding/json"

// ─── JSON-RPC 2.0 wire types ─────────────────────────────────────────────────

// Request is a decoded JSON-RPC 2.0 request or notification.
// For notifications (no id), ID is nil.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 success response.
type Response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  json.RawMessage  `json:"result"`
}

// ErrorResponse is a JSON-RPC 2.0 error response.
type ErrorResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Error   RPCError         `json:"error"`
}

// RPCError is the JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// ─── MCP initialize types ─────────────────────────────────────────────────────

// InitializeParams mirrors the MCP 2025-03-26 initialize request params.
type InitializeParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	ClientInfo      ClientInfo `json:"clientInfo"`
	Capabilities    struct{}   `json:"capabilities"`
}

// ClientInfo carries the MCP client name/version.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the server's initialize response.
type InitializeResult struct {
	ProtocolVersion string           `json:"protocolVersion"`
	ServerInfo      ServerInfo       `json:"serverInfo"`
	Capabilities    ServerCapabilities `json:"capabilities"`
}

// ServerInfo carries the server name/version for the MCP handshake.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServerCapabilities declares which MCP capability sets the server supports.
type ServerCapabilities struct {
	Tools ToolsCapability `json:"tools"`
}

// ToolsCapability signals that the server supports tools (empty object per
// MCP 2025-03-26).
type ToolsCapability struct{}

// ─── MCP tools/list types ─────────────────────────────────────────────────────

// ToolsListResult is the result of tools/list.
type ToolsListResult struct {
	Tools []ToolDef `json:"tools"`
}

// ToolDef describes a single MCP tool as required by the tools/list response.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ─── MCP tools/call types ─────────────────────────────────────────────────────

// ToolsCallParams carries the name + arguments of a tools/call request.
type ToolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolsCallResult is the MCP 2025-03-26 tools/call success response shape.
// content is always a single text entry; isError distinguishes handler
// errors from protocol errors.
type ToolsCallResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError"`
}

// ContentItem is a single entry in a tools/call response content list.
// For Junction v0.1 all items are type "text".
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
