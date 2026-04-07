// Package server implements the MCP HTTP/SSE transport layer.
//
// Endpoints:
//   GET  /sse          — SSE stream for server-sent events (MCP transport)
//   POST /messages     — JSON-RPC tool call endpoint
//   GET  /health       — Health check (no auth required)
//
// Auth flow:
//   1. Client sends Authorization: Bearer plx_live_<key> header
//   2. Server validates key → resolves OrgContext
//   3. OrgContext is attached to request context
//   4. Tool handlers receive OrgContext — never raw org_id from request body
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/planasonix/mcp-server/auth"
	"github.com/planasonix/mcp-server/models"
	"github.com/planasonix/mcp-server/tools"
)

type contextKey string

const orgContextKey contextKey = "org_ctx"

// Config holds server-level settings passed from main.
type Config struct {
	Port            string
	PlanasonixAPI   string
	RateLimitRPM    int
}

// Server is the MCP HTTP handler.
type Server struct {
	keyStore     auth.KeyStore
	toolHandler  *tools.Handler
	rateLimiter  *rateLimiter
	toolDefs     []models.ToolDefinition
}

func New(cfg Config, keyStore auth.KeyStore, client tools.PlanasonixClient) *Server {
	handler := tools.NewHandler(client)

	rpm := cfg.RateLimitRPM
	if rpm <= 0 {
		rpm = 60
	}

	s := &Server{
		keyStore:    keyStore,
		toolHandler: handler,
	}
	s.rateLimiter = newRateLimiter(rpm)
	s.toolDefs = buildToolDefinitions()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/health" && r.Method == http.MethodGet:
		s.handleHealth(w, r)
	case r.URL.Path == "/sse" && r.Method == http.MethodGet:
		s.withAuth(s.handleSSE)(w, r)
	case r.URL.Path == "/messages" && r.Method == http.MethodPost:
		s.withAuth(s.handleMessage)(w, r)
	default:
		http.NotFound(w, r)
	}
}

// ── Auth Middleware ──────────────────────────────────────────────────────────

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := extractBearerToken(r)
		if apiKey == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}

		orgCtx, err := s.keyStore.Validate(apiKey)
		if err != nil {
			// Always return 401 — never leak whether the key format was wrong vs key unknown
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		// Rate limit per org
		if !s.rateLimiter.Allow(orgCtx.OrgID) {
			writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}

		// Attach org context — only source of truth for downstream handlers
		ctx := r.Context()
		ctx = contextWithOrg(ctx, *orgCtx)
		next(w, r.WithContext(ctx))
	}
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","service":"planasonix-mcp","ts":"%s"}`, time.Now().Format(time.RFC3339))
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send initial capabilities event
	caps := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{"listChanged": false},
		},
		"serverInfo": map[string]interface{}{
			"name":    "planasonix-mcp",
			"version": "1.0.0",
		},
	}
	sendSSEEvent(w, "endpoint", "/messages")
	sendSSEJSON(w, "initialize", caps)

	// Keep connection alive
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	orgCtx, ok := orgFromContext(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req models.MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, models.ErrParse, "invalid JSON")
		return
	}

	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, models.ErrInvalidRequest, "jsonrpc must be '2.0'")
		return
	}

	switch req.Method {
	case "tools/list":
		s.handleToolsList(w, req)
	case "tools/call":
		s.handleToolCall(w, req, orgCtx)
	case "initialize":
		s.handleInitialize(w, req)
	default:
		writeRPCError(w, req.ID, models.ErrMethodNotFound, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *Server) handleInitialize(w http.ResponseWriter, req models.MCPRequest) {
	writeRPCResult(w, req.ID, map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{"listChanged": false},
		},
		"serverInfo": map[string]interface{}{
			"name":    "planasonix-mcp",
			"version": "1.0.0",
		},
	})
}

func (s *Server) handleToolsList(w http.ResponseWriter, req models.MCPRequest) {
	writeRPCResult(w, req.ID, map[string]interface{}{
		"tools": s.toolDefs,
	})
}

func (s *Server) handleToolCall(w http.ResponseWriter, req models.MCPRequest, orgCtx auth.OrgContext) {
	toolName := req.Params.Name
	if toolName == "" {
		writeRPCError(w, req.ID, models.ErrInvalidParams, "tool name is required")
		return
	}

	// !! Critical: org_id is NEVER read from req.Params.Arguments !!
	// It is always sourced from the validated OrgContext above.
	result := s.toolHandler.Dispatch(orgCtx, toolName, req.Params.Arguments)
	writeRPCResult(w, req.ID, result)
}

// ── Tool Definitions ─────────────────────────────────────────────────────────

func buildToolDefinitions() []models.ToolDefinition {
	return []models.ToolDefinition{
		{
			Name:        "list_pipelines",
			Description: "List all ETL pipelines for the authenticated organization, including their status, schedule, and last run result.",
			InputSchema: models.InputSchema{Type: "object", Properties: map[string]models.Property{}},
		},
		{
			Name:        "get_pipeline",
			Description: "Get detailed information about a specific pipeline by its ID.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"pipeline_id"},
				Properties: map[string]models.Property{
					"pipeline_id": {Type: "string", Description: "The ID of the pipeline to retrieve."},
				},
			},
		},
		{
			Name:        "trigger_pipeline",
			Description: "Trigger an immediate run of a pipeline. Optionally pass override parameters such as date ranges.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"pipeline_id"},
				Properties: map[string]models.Property{
					"pipeline_id": {Type: "string", Description: "The ID of the pipeline to trigger."},
				},
			},
		},
		{
			Name:        "pause_pipeline",
			Description: "Pause a pipeline's scheduled runs without deleting its configuration.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"pipeline_id"},
				Properties: map[string]models.Property{
					"pipeline_id": {Type: "string", Description: "The ID of the pipeline to pause."},
				},
			},
		},
		{
			Name:        "resume_pipeline",
			Description: "Resume a previously paused pipeline.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"pipeline_id"},
				Properties: map[string]models.Property{
					"pipeline_id": {Type: "string", Description: "The ID of the pipeline to resume."},
				},
			},
		},
		{
			Name:        "get_run_history",
			Description: "Retrieve the recent run history for a pipeline, including status, row counts, and error messages.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"pipeline_id"},
				Properties: map[string]models.Property{
					"pipeline_id": {Type: "string", Description: "The ID of the pipeline."},
					"limit":       {Type: "number", Description: "Number of recent runs to return (default 10, max 100)."},
				},
			},
		},
		{
			Name:        "get_pipeline_health",
			Description: "Get health metrics for a pipeline: error rate, average latency, rows processed, and SLA status.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"pipeline_id"},
				Properties: map[string]models.Property{
					"pipeline_id": {Type: "string", Description: "The ID of the pipeline."},
				},
			},
		},
		{
			Name:        "list_connectors",
			Description: "List all configured source and destination connectors for the authenticated organization.",
			InputSchema: models.InputSchema{Type: "object", Properties: map[string]models.Property{}},
		},
		{
			Name:        "test_connection",
			Description: "Test connectivity for a configured connector and return latency and status.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"connector_id"},
				Properties: map[string]models.Property{
					"connector_id": {Type: "string", Description: "The ID of the connector to test."},
				},
			},
		},

		// ── Pipeline CRUD ───────────────────────────────────────────────
		{
			Name:        "create_pipeline",
			Description: "Create a new ETL pipeline from a natural language description. The backend AI generates the pipeline graph (nodes, transforms, edges) automatically. Created pipelines start in 'paused' status.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"name", "description"},
				Properties: map[string]models.Property{
					"name":        {Type: "string", Description: "Name for the new pipeline."},
					"description": {Type: "string", Description: "Natural language description of what the pipeline should do. Example: 'Extract orders from PostgreSQL, filter orders from the last 7 days, deduplicate by order_id, and load into Snowflake'."},
				},
			},
		},
		{
			Name:        "update_pipeline",
			Description: "Modify an existing pipeline using a natural language description of the changes. The backend AI determines which nodes/edges to add or change.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"pipeline_id", "description"},
				Properties: map[string]models.Property{
					"pipeline_id": {Type: "string", Description: "The ID of the pipeline to modify."},
					"description": {Type: "string", Description: "Natural language description of the changes. Example: 'Add a filter step after the source to only include records where status is active'."},
				},
			},
		},
		{
			Name:        "delete_pipeline",
			Description: "Delete a pipeline (soft delete — moves to trash). The pipeline can be restored from the UI.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"pipeline_id"},
				Properties: map[string]models.Property{
					"pipeline_id": {Type: "string", Description: "The ID of the pipeline to delete."},
				},
			},
		},

		// ── Schedule Management ─────────────────────────────────────────
		{
			Name:        "list_schedules",
			Description: "List all pipeline schedules for the authenticated organization.",
			InputSchema: models.InputSchema{Type: "object", Properties: map[string]models.Property{}},
		},
		{
			Name:        "create_schedule",
			Description: "Create a new schedule for a pipeline. The schedule will be enabled by default.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"pipeline_id", "cron_expression", "frequency"},
				Properties: map[string]models.Property{
					"pipeline_id":     {Type: "string", Description: "The ID of the pipeline to schedule."},
					"cron_expression": {Type: "string", Description: "Cron expression for the schedule. Example: '0 */6 * * *' for every 6 hours."},
					"frequency":       {Type: "string", Description: "Human-readable frequency label.", Enum: []string{"hourly", "daily", "weekly", "monthly", "custom"}},
					"timezone":        {Type: "string", Description: "IANA timezone for the schedule. Example: 'America/New_York'. Defaults to UTC."},
				},
			},
		},
		{
			Name:        "update_schedule",
			Description: "Update an existing pipeline schedule's cron expression, frequency, or timezone.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"schedule_id"},
				Properties: map[string]models.Property{
					"schedule_id":     {Type: "string", Description: "The ID of the schedule to update."},
					"cron_expression": {Type: "string", Description: "New cron expression."},
					"frequency":       {Type: "string", Description: "New frequency label.", Enum: []string{"hourly", "daily", "weekly", "monthly", "custom"}},
					"timezone":        {Type: "string", Description: "New IANA timezone."},
				},
			},
		},
		{
			Name:        "delete_schedule",
			Description: "Permanently delete a pipeline schedule.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"schedule_id"},
				Properties: map[string]models.Property{
					"schedule_id": {Type: "string", Description: "The ID of the schedule to delete."},
				},
			},
		},
		{
			Name:        "enable_schedule",
			Description: "Enable a previously disabled pipeline schedule so it resumes running.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"schedule_id"},
				Properties: map[string]models.Property{
					"schedule_id": {Type: "string", Description: "The ID of the schedule to enable."},
				},
			},
		},
		{
			Name:        "disable_schedule",
			Description: "Disable a pipeline schedule without deleting it. The schedule can be re-enabled later.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"schedule_id"},
				Properties: map[string]models.Property{
					"schedule_id": {Type: "string", Description: "The ID of the schedule to disable."},
				},
			},
		},

		// ── Connection Management ───────────────────────────────────────
		{
			Name:        "create_connection",
			Description: "Create a new data source or destination connection. Use list_connection_types to discover available types and their required parameters.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"name", "type", "category", "params"},
				Properties: map[string]models.Property{
					"name":     {Type: "string", Description: "Display name for the connection."},
					"type":     {Type: "string", Description: "Connector type. Example: 'postgres', 'snowflake', 'bigquery', 's3', 'salesforce'."},
					"category": {Type: "string", Description: "Whether this is a data source or destination.", Enum: []string{"source", "destination"}},
					"params": {
						Type:                 "object",
						Description:          "Connection parameters (host, port, database, user, password, etc.). Varies by connector type.",
						AdditionalProperties: &models.Property{Type: "string", Description: "Parameter value."},
					},
				},
			},
		},
		{
			Name:        "update_connection",
			Description: "Update an existing connection's name or parameters. Only provided fields are changed; others remain unchanged.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"connection_id"},
				Properties: map[string]models.Property{
					"connection_id": {Type: "string", Description: "The ID of the connection to update."},
					"name":          {Type: "string", Description: "New display name for the connection."},
					"params": {
						Type:                 "object",
						Description:          "Updated connection parameters. Only include parameters you want to change.",
						AdditionalProperties: &models.Property{Type: "string", Description: "Parameter value."},
					},
				},
			},
		},
		{
			Name:        "delete_connection",
			Description: "Delete a saved connection. This will fail if the connection is in use by any pipeline.",
			InputSchema: models.InputSchema{
				Type:     "object",
				Required: []string{"connection_id"},
				Properties: map[string]models.Property{
					"connection_id": {Type: "string", Description: "The ID of the connection to delete."},
				},
			},
		},
		{
			Name:        "list_connection_types",
			Description: "List all available connector types (e.g., PostgreSQL, Snowflake, S3) with their categories. Useful for discovering what types can be used with create_connection.",
			InputSchema: models.InputSchema{Type: "object", Properties: map[string]models.Property{}},
		},
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(auth, "Bearer ")
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":"%s"}`, msg)
}

func writeRPCError(w http.ResponseWriter, id interface{}, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	resp := models.MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &models.MCPError{Code: code, Message: msg},
	}
	json.NewEncoder(w).Encode(resp)
}

func writeRPCResult(w http.ResponseWriter, id interface{}, result interface{}) {
	w.Header().Set("Content-Type", "application/json")
	resp := models.MCPResponse{JSONRPC: "2.0", ID: id, Result: result}
	json.NewEncoder(w).Encode(resp)
}

func sendSSEEvent(w http.ResponseWriter, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func sendSSEJSON(w http.ResponseWriter, event string, v interface{}) {
	b, _ := json.Marshal(v)
	sendSSEEvent(w, event, string(b))
}

// ── Context helpers ───────────────────────────────────────────────────────────

func contextWithOrg(ctx context.Context, orgCtx auth.OrgContext) context.Context {
	return context.WithValue(ctx, orgContextKey, orgCtx)
}

func orgFromContext(ctx context.Context) (auth.OrgContext, bool) {
	v, ok := ctx.Value(orgContextKey).(auth.OrgContext)
	return v, ok
}

// ── Rate Limiter (token bucket per org) ──────────────────────────────────────

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rpm     int
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

func newRateLimiter(rpm int) *rateLimiter {
	rl := &rateLimiter{buckets: make(map[string]*bucket), rpm: rpm}
	go rl.cleanup()
	return rl
}

func (rl *rateLimiter) Allow(orgID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[orgID]
	if !ok {
		rl.buckets[orgID] = &bucket{tokens: float64(rl.rpm) - 1, lastSeen: now}
		return true
	}

	// Refill tokens based on elapsed time
	elapsed := now.Sub(b.lastSeen).Minutes()
	b.tokens += elapsed * float64(rl.rpm)
	if b.tokens > float64(rl.rpm) {
		b.tokens = float64(rl.rpm)
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// cleanup evicts buckets that haven't been seen in 10 minutes to prevent
// unbounded memory growth from unique org IDs.
func (rl *rateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for id, b := range rl.buckets {
			if b.lastSeen.Before(cutoff) {
				delete(rl.buckets, id)
			}
		}
		rl.mu.Unlock()
	}
}
