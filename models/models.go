// Package models defines shared types for MCP JSON-RPC messages and
// Planasonix domain objects returned by tools.
package models

// ── MCP Protocol Types ──────────────────────────────────────────────────────

type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  MCPRequestParams `json:"params,omitempty"`
}

type MCPRequestParams struct {
	Name      string                 `json:"name,omitempty"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Standard JSON-RPC error codes
const (
	ErrParse          = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternal       = -32603
	ErrUnauthorized   = -32001 // Custom: auth failure
	ErrForbidden      = -32002 // Custom: scope/permission failure
	ErrNotFound       = -32003 // Custom: resource not found (also used to avoid org enumeration)
)

type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"` // "text" or "resource"
	Text string `json:"text,omitempty"`
}

type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

type Property struct {
	Type                 string              `json:"type"`
	Description          string              `json:"description"`
	Enum                 []string            `json:"enum,omitempty"`
	Properties           map[string]Property `json:"properties,omitempty"`
	AdditionalProperties *Property           `json:"additionalProperties,omitempty"`
}

// ── Planasonix Domain Types ──────────────────────────────────────────────────

type Pipeline struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Status      string `json:"status"` // "active", "paused", "error"
	Schedule    string `json:"schedule"`
	LastRunAt   string `json:"last_run_at,omitempty"`
	LastRunStatus string `json:"last_run_status,omitempty"` // "success", "failed", "running"
}

type PipelineRun struct {
	ID         string `json:"id"`
	PipelineID string `json:"pipeline_id"`
	Status     string `json:"status"` // "running", "success", "failed"
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	RowsLoaded int64  `json:"rows_loaded,omitempty"`
	ErrorMsg   string `json:"error_message,omitempty"`
}

type Connector struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // "source" or "destination"
	Kind string `json:"kind"` // e.g. "postgres", "salesforce", "s3"
}

type PipelineHealth struct {
	PipelineID    string  `json:"pipeline_id"`
	ErrorRate7d   float64 `json:"error_rate_7d"`   // 0.0–1.0
	AvgLatencyMs  int64   `json:"avg_latency_ms"`
	RowsLast24h   int64   `json:"rows_last_24h"`
	SLABreached   bool    `json:"sla_breached"`
	LastAlertAt   string  `json:"last_alert_at,omitempty"`
}

// ── Schedule Types ───────────────────────────────────────────────────────────

type Schedule struct {
	ID             string `json:"id"`
	PipelineID     string `json:"pipeline_id"`
	PipelineName   string `json:"pipeline_name,omitempty"`
	CronExpression string `json:"cron_expression"`
	Frequency      string `json:"frequency"`
	Timezone       string `json:"timezone,omitempty"`
	Enabled        bool   `json:"enabled"`
	NextRunAt      string `json:"next_run_at,omitempty"`
	LastRunAt      string `json:"last_run_at,omitempty"`
	LastRunStatus  string `json:"last_run_status,omitempty"`
}

type CreateScheduleRequest struct {
	PipelineID     string `json:"pipelineId"`
	CronExpression string `json:"cronExpression"`
	Frequency      string `json:"frequency"`
	Timezone       string `json:"timezone,omitempty"`
}

type UpdateScheduleRequest struct {
	CronExpression string `json:"cronExpression,omitempty"`
	Frequency      string `json:"frequency,omitempty"`
	Timezone       string `json:"timezone,omitempty"`
}

// ── Connection Types ─────────────────────────────────────────────────────────

type Connection struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`     // e.g. "postgres", "snowflake"
	Category  string            `json:"category"` // "source" or "destination"
	CreatedAt string            `json:"created_at,omitempty"`
}

type CreateConnectionRequest struct {
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Category string            `json:"category"`
	Params   map[string]string `json:"params"`
}

type UpdateConnectionRequest struct {
	Name   string            `json:"name,omitempty"`
	Params map[string]string `json:"params,omitempty"`
}

type ConnectionType struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
}
