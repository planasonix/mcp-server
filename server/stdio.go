// Package server — stdio transport for the MCP protocol.
//
// In stdio mode the MCP server reads JSON-RPC messages from stdin (one per
// line) and writes JSON-RPC responses to stdout. This is the transport
// used by Claude Desktop, Cursor, Windsurf, GitHub Copilot, and all other
// local MCP clients.
//
// Auth: the API key is read once from the PLANASONIX_API_KEY environment
// variable at startup and validated against the KeyStore. The resulting
// OrgContext is reused for every request (no per-message auth overhead).
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/planasonix/mcp-server/auth"
	"github.com/planasonix/mcp-server/models"
	"github.com/planasonix/mcp-server/tools"
)

// StdioServer implements the MCP stdio transport.
type StdioServer struct {
	orgCtx      auth.OrgContext
	toolHandler *tools.Handler
	toolDefs    []models.ToolDefinition
}

// NewStdio creates a stdio-mode MCP server. The orgCtx is pre-resolved
// from the API key at startup so every message reuses it.
func NewStdio(orgCtx auth.OrgContext, client tools.PlanasonixClient) *StdioServer {
	return &StdioServer{
		orgCtx:      orgCtx,
		toolHandler: tools.NewHandler(client),
		toolDefs:    buildToolDefinitions(),
	}
}

// Run reads JSON-RPC messages from stdin and writes responses to stdout.
// It blocks until stdin is closed (the parent process exits).
func (s *StdioServer) Run() error {
	// All diagnostic logging goes to stderr so it doesn't corrupt the
	// JSON-RPC stream on stdout.
	log.SetOutput(os.Stderr)

	reader := bufio.NewReader(os.Stdin)
	writer := os.Stdout

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("reading stdin: %w", err)
		}

		if len(line) == 0 || (len(line) == 1 && line[0] == '\n') {
			continue
		}

		resp := s.handleMessage(line)
		if resp == nil {
			// Notifications (no id) don't get a response.
			continue
		}

		out, err := json.Marshal(resp)
		if err != nil {
			log.Printf("[stdio] marshal error: %v", err)
			continue
		}
		out = append(out, '\n')
		if _, err := writer.Write(out); err != nil {
			return fmt.Errorf("writing stdout: %w", err)
		}
	}
}

func (s *StdioServer) handleMessage(raw []byte) *models.MCPResponse {
	var req models.MCPRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return &models.MCPResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &models.MCPError{Code: models.ErrParse, Message: "invalid JSON"},
		}
	}

	// Notifications (id is absent/null) are fire-and-forget per JSON-RPC.
	if req.ID == nil {
		s.handleNotification(req)
		return nil
	}

	if req.JSONRPC != "2.0" {
		return s.rpcError(req.ID, models.ErrInvalidRequest, "jsonrpc must be '2.0'")
	}

	switch req.Method {
	case "initialize":
		return s.rpcResult(req.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{"listChanged": false},
			},
			"serverInfo": map[string]interface{}{
				"name":    "planasonix-mcp",
				"version": "1.0.0",
			},
		})

	case "tools/list":
		return s.rpcResult(req.ID, map[string]interface{}{
			"tools": s.toolDefs,
		})

	case "tools/call":
		toolName := req.Params.Name
		if toolName == "" {
			return s.rpcError(req.ID, models.ErrInvalidParams, "tool name is required")
		}
		result := s.toolHandler.Dispatch(s.orgCtx, toolName, req.Params.Arguments)
		return s.rpcResult(req.ID, result)

	case "ping":
		return s.rpcResult(req.ID, map[string]interface{}{})

	default:
		return s.rpcError(req.ID, models.ErrMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

// handleNotification processes fire-and-forget messages (no response sent).
func (s *StdioServer) handleNotification(req models.MCPRequest) {
	switch req.Method {
	case "notifications/initialized":
		log.Println("[stdio] Client initialized successfully")
	case "notifications/cancelled":
		log.Printf("[stdio] Client cancelled request")
	default:
		log.Printf("[stdio] Unhandled notification: %s", req.Method)
	}
}

func (s *StdioServer) rpcResult(id interface{}, result interface{}) *models.MCPResponse {
	return &models.MCPResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func (s *StdioServer) rpcError(id interface{}, code int, msg string) *models.MCPResponse {
	return &models.MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &models.MCPError{Code: code, Message: msg},
	}
}
