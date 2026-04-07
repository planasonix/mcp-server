// Package tools implements all MCP tool handlers.
//
// Security contract:
//   - Every handler receives an OrgContext resolved from the API key — never
//     from client-supplied parameters.
//   - Resource IDs passed as tool arguments are ALWAYS re-validated against
//     the org before use. A 404 (not "403") is returned when a resource exists
//     but belongs to a different org, to avoid confirming resource existence.
//   - No handler returns raw credentials, connection strings, or PII.
package tools

import (
	"encoding/json"
	"fmt"

	"github.com/planasonix/mcp-server/auth"
	"github.com/planasonix/mcp-server/models"
)

// PlanasonixClient is the interface your tools use to talk to the Planasonix
// internal API. The OrgContext carries the authenticated org identity and the
// raw API key for backend forwarding.
type PlanasonixClient interface {
	// Existing pipeline read/control
	ListPipelines(ctx auth.OrgContext) ([]models.Pipeline, error)
	GetPipeline(ctx auth.OrgContext, pipelineID string) (*models.Pipeline, error)
	TriggerPipeline(ctx auth.OrgContext, pipelineID string, params map[string]interface{}) (*models.PipelineRun, error)
	PausePipeline(ctx auth.OrgContext, pipelineID string) error
	ResumePipeline(ctx auth.OrgContext, pipelineID string) error
	GetRunHistory(ctx auth.OrgContext, pipelineID string, limit int) ([]models.PipelineRun, error)
	GetPipelineHealth(ctx auth.OrgContext, pipelineID string) (*models.PipelineHealth, error)
	ListConnectors(ctx auth.OrgContext) ([]models.Connector, error)
	TestConnection(ctx auth.OrgContext, connectorID string) (bool, string, error)

	// Pipeline CRUD (AI-powered creation/modification)
	CreatePipeline(ctx auth.OrgContext, name, description string) (*models.Pipeline, error)
	UpdatePipelineWithAI(ctx auth.OrgContext, pipelineID, description string) (*models.Pipeline, error)
	DeletePipeline(ctx auth.OrgContext, pipelineID string) error

	// Schedule management
	ListSchedules(ctx auth.OrgContext) ([]models.Schedule, error)
	CreateSchedule(ctx auth.OrgContext, req models.CreateScheduleRequest) (*models.Schedule, error)
	UpdateSchedule(ctx auth.OrgContext, scheduleID string, req models.UpdateScheduleRequest) (*models.Schedule, error)
	DeleteSchedule(ctx auth.OrgContext, scheduleID string) error
	EnableSchedule(ctx auth.OrgContext, scheduleID string) error
	DisableSchedule(ctx auth.OrgContext, scheduleID string) error

	// Connection management
	CreateConnection(ctx auth.OrgContext, req models.CreateConnectionRequest) (*models.Connection, error)
	UpdateConnection(ctx auth.OrgContext, connectionID string, req models.UpdateConnectionRequest) (*models.Connection, error)
	DeleteConnection(ctx auth.OrgContext, connectionID string) error
	ListConnectionTypes(ctx auth.OrgContext) ([]models.ConnectionType, error)
}

// Handler dispatches tool calls to the appropriate function.
type Handler struct {
	client PlanasonixClient
}

func NewHandler(client PlanasonixClient) *Handler {
	return &Handler{client: client}
}

// Dispatch routes a tool call to the correct handler. The OrgContext is
// resolved before this is called and is never overridable by tool arguments.
func (h *Handler) Dispatch(ctx auth.OrgContext, toolName string, args map[string]interface{}) models.ToolResult {
	switch toolName {
	case "list_pipelines":
		return h.listPipelines(ctx, args)
	case "get_pipeline":
		return h.getPipeline(ctx, args)
	case "trigger_pipeline":
		return h.triggerPipeline(ctx, args)
	case "pause_pipeline":
		return h.pausePipeline(ctx, args)
	case "resume_pipeline":
		return h.resumePipeline(ctx, args)
	case "get_run_history":
		return h.getRunHistory(ctx, args)
	case "get_pipeline_health":
		return h.getPipelineHealth(ctx, args)
	case "list_connectors":
		return h.listConnectors(ctx, args)
	case "test_connection":
		return h.testConnection(ctx, args)
	case "create_pipeline":
		return h.createPipeline(ctx, args)
	case "update_pipeline":
		return h.updatePipeline(ctx, args)
	case "delete_pipeline":
		return h.deletePipeline(ctx, args)
	case "list_schedules":
		return h.listSchedules(ctx, args)
	case "create_schedule":
		return h.createSchedule(ctx, args)
	case "update_schedule":
		return h.updateSchedule(ctx, args)
	case "delete_schedule":
		return h.deleteSchedule(ctx, args)
	case "enable_schedule":
		return h.enableSchedule(ctx, args)
	case "disable_schedule":
		return h.disableSchedule(ctx, args)
	case "create_connection":
		return h.createConnection(ctx, args)
	case "update_connection":
		return h.updateConnection(ctx, args)
	case "delete_connection":
		return h.deleteConnection(ctx, args)
	case "list_connection_types":
		return h.listConnectionTypes(ctx, args)
	default:
		return errResult(fmt.Sprintf("unknown tool: %s", toolName))
	}
}

// ── Tool Implementations ────────────────────────────────────────────────────

func (h *Handler) listPipelines(ctx auth.OrgContext, _ map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("pipelines:read") {
		return scopeError("pipelines:read")
	}

	pipelines, err := h.client.ListPipelines(ctx)
	if err != nil {
		return errResult("failed to list pipelines")
	}

	return jsonResult(map[string]interface{}{
		"org":       ctx.OrgName,
		"count":     len(pipelines),
		"pipelines": pipelines,
	})
}

func (h *Handler) getPipeline(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("pipelines:read") {
		return scopeError("pipelines:read")
	}

	pipelineID, ok := stringArg(args, "pipeline_id")
	if !ok {
		return errResult("pipeline_id is required")
	}

	pipeline, err := h.client.GetPipeline(ctx, pipelineID)
	if err != nil {
		return errResult("pipeline not found")
	}

	return jsonResult(pipeline)
}

func (h *Handler) triggerPipeline(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("pipelines:write") {
		return scopeError("pipelines:write")
	}

	pipelineID, ok := stringArg(args, "pipeline_id")
	if !ok {
		return errResult("pipeline_id is required")
	}

	params, _ := args["params"].(map[string]interface{})

	run, err := h.client.TriggerPipeline(ctx, pipelineID, params)
	if err != nil {
		return errResult(fmt.Sprintf("failed to trigger pipeline: %v", err))
	}

	return jsonResult(map[string]interface{}{
		"message": "Pipeline triggered successfully",
		"run":     run,
	})
}

func (h *Handler) pausePipeline(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("pipelines:write") {
		return scopeError("pipelines:write")
	}

	pipelineID, ok := stringArg(args, "pipeline_id")
	if !ok {
		return errResult("pipeline_id is required")
	}

	if err := h.client.PausePipeline(ctx, pipelineID); err != nil {
		return errResult("failed to pause pipeline")
	}

	return textResult(fmt.Sprintf("Pipeline %s paused successfully.", pipelineID))
}

func (h *Handler) resumePipeline(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("pipelines:write") {
		return scopeError("pipelines:write")
	}

	pipelineID, ok := stringArg(args, "pipeline_id")
	if !ok {
		return errResult("pipeline_id is required")
	}

	if err := h.client.ResumePipeline(ctx, pipelineID); err != nil {
		return errResult("failed to resume pipeline")
	}

	return textResult(fmt.Sprintf("Pipeline %s resumed successfully.", pipelineID))
}

func (h *Handler) getRunHistory(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("pipelines:read") {
		return scopeError("pipelines:read")
	}

	pipelineID, ok := stringArg(args, "pipeline_id")
	if !ok {
		return errResult("pipeline_id is required")
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 && l <= 100 {
		limit = int(l)
	}

	runs, err := h.client.GetRunHistory(ctx, pipelineID, limit)
	if err != nil {
		return errResult("failed to get run history")
	}

	return jsonResult(map[string]interface{}{
		"pipeline_id": pipelineID,
		"runs":        runs,
	})
}

func (h *Handler) getPipelineHealth(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("pipelines:read") {
		return scopeError("pipelines:read")
	}

	pipelineID, ok := stringArg(args, "pipeline_id")
	if !ok {
		return errResult("pipeline_id is required")
	}

	health, err := h.client.GetPipelineHealth(ctx, pipelineID)
	if err != nil {
		return errResult("failed to get pipeline health")
	}

	return jsonResult(health)
}

func (h *Handler) listConnectors(ctx auth.OrgContext, _ map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("connectors:read") {
		return scopeError("connectors:read")
	}

	connectors, err := h.client.ListConnectors(ctx)
	if err != nil {
		return errResult("failed to list connectors")
	}

	return jsonResult(map[string]interface{}{
		"count":      len(connectors),
		"connectors": connectors,
	})
}

func (h *Handler) testConnection(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("connectors:read") {
		return scopeError("connectors:read")
	}

	connectorID, ok := stringArg(args, "connector_id")
	if !ok {
		return errResult("connector_id is required")
	}

	ok, message, err := h.client.TestConnection(ctx, connectorID)
	if err != nil {
		return errResult("connection test failed")
	}

	status := "success"
	if !ok {
		status = "failed"
	}

	return jsonResult(map[string]interface{}{
		"status":  status,
		"message": message,
	})
}

// ── Pipeline CRUD ───────────────────────────────────────────────────────────

func (h *Handler) createPipeline(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("pipelines:write") {
		return scopeError("pipelines:write")
	}

	name, ok := stringArg(args, "name")
	if !ok {
		return errResult("name is required")
	}
	description, ok := stringArg(args, "description")
	if !ok {
		return errResult("description is required")
	}

	pipeline, err := h.client.CreatePipeline(ctx, name, description)
	if err != nil {
		return errResult(fmt.Sprintf("failed to create pipeline: %v", err))
	}

	return jsonResult(map[string]interface{}{
		"message":  "Pipeline created successfully",
		"pipeline": pipeline,
	})
}

func (h *Handler) updatePipeline(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("pipelines:write") {
		return scopeError("pipelines:write")
	}

	pipelineID, ok := stringArg(args, "pipeline_id")
	if !ok {
		return errResult("pipeline_id is required")
	}
	description, ok := stringArg(args, "description")
	if !ok {
		return errResult("description is required")
	}

	pipeline, err := h.client.UpdatePipelineWithAI(ctx, pipelineID, description)
	if err != nil {
		return errResult(fmt.Sprintf("failed to update pipeline: %v", err))
	}

	return jsonResult(map[string]interface{}{
		"message":  "Pipeline updated successfully",
		"pipeline": pipeline,
	})
}

func (h *Handler) deletePipeline(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("pipelines:write") {
		return scopeError("pipelines:write")
	}

	pipelineID, ok := stringArg(args, "pipeline_id")
	if !ok {
		return errResult("pipeline_id is required")
	}

	if err := h.client.DeletePipeline(ctx, pipelineID); err != nil {
		return errResult(fmt.Sprintf("failed to delete pipeline: %v", err))
	}

	return textResult(fmt.Sprintf("Pipeline %s deleted successfully.", pipelineID))
}

// ── Schedule Management ─────────────────────────────────────────────────────

func (h *Handler) listSchedules(ctx auth.OrgContext, _ map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("schedules:read") {
		return scopeError("schedules:read")
	}

	schedules, err := h.client.ListSchedules(ctx)
	if err != nil {
		return errResult("failed to list schedules")
	}

	return jsonResult(map[string]interface{}{
		"count":     len(schedules),
		"schedules": schedules,
	})
}

func (h *Handler) createSchedule(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("schedules:write") {
		return scopeError("schedules:write")
	}

	pipelineID, ok := stringArg(args, "pipeline_id")
	if !ok {
		return errResult("pipeline_id is required")
	}
	cronExpr, ok := stringArg(args, "cron_expression")
	if !ok {
		return errResult("cron_expression is required")
	}
	frequency, ok := stringArg(args, "frequency")
	if !ok {
		return errResult("frequency is required")
	}

	timezone, _ := stringArg(args, "timezone")

	schedule, err := h.client.CreateSchedule(ctx, models.CreateScheduleRequest{
		PipelineID:     pipelineID,
		CronExpression: cronExpr,
		Frequency:      frequency,
		Timezone:       timezone,
	})
	if err != nil {
		return errResult(fmt.Sprintf("failed to create schedule: %v", err))
	}

	return jsonResult(map[string]interface{}{
		"message":  "Schedule created successfully",
		"schedule": schedule,
	})
}

func (h *Handler) updateSchedule(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("schedules:write") {
		return scopeError("schedules:write")
	}

	scheduleID, ok := stringArg(args, "schedule_id")
	if !ok {
		return errResult("schedule_id is required")
	}

	req := models.UpdateScheduleRequest{}
	if v, ok := stringArg(args, "cron_expression"); ok {
		req.CronExpression = v
	}
	if v, ok := stringArg(args, "frequency"); ok {
		req.Frequency = v
	}
	if v, ok := stringArg(args, "timezone"); ok {
		req.Timezone = v
	}

	schedule, err := h.client.UpdateSchedule(ctx, scheduleID, req)
	if err != nil {
		return errResult(fmt.Sprintf("failed to update schedule: %v", err))
	}

	return jsonResult(map[string]interface{}{
		"message":  "Schedule updated successfully",
		"schedule": schedule,
	})
}

func (h *Handler) deleteSchedule(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("schedules:write") {
		return scopeError("schedules:write")
	}

	scheduleID, ok := stringArg(args, "schedule_id")
	if !ok {
		return errResult("schedule_id is required")
	}

	if err := h.client.DeleteSchedule(ctx, scheduleID); err != nil {
		return errResult(fmt.Sprintf("failed to delete schedule: %v", err))
	}

	return textResult(fmt.Sprintf("Schedule %s deleted successfully.", scheduleID))
}

func (h *Handler) enableSchedule(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("schedules:write") {
		return scopeError("schedules:write")
	}

	scheduleID, ok := stringArg(args, "schedule_id")
	if !ok {
		return errResult("schedule_id is required")
	}

	if err := h.client.EnableSchedule(ctx, scheduleID); err != nil {
		return errResult(fmt.Sprintf("failed to enable schedule: %v", err))
	}

	return textResult(fmt.Sprintf("Schedule %s enabled successfully.", scheduleID))
}

func (h *Handler) disableSchedule(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("schedules:write") {
		return scopeError("schedules:write")
	}

	scheduleID, ok := stringArg(args, "schedule_id")
	if !ok {
		return errResult("schedule_id is required")
	}

	if err := h.client.DisableSchedule(ctx, scheduleID); err != nil {
		return errResult(fmt.Sprintf("failed to disable schedule: %v", err))
	}

	return textResult(fmt.Sprintf("Schedule %s disabled successfully.", scheduleID))
}

// ── Connection Management ───────────────────────────────────────────────────

func (h *Handler) createConnection(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("connectors:write") {
		return scopeError("connectors:write")
	}

	name, ok := stringArg(args, "name")
	if !ok {
		return errResult("name is required")
	}
	connType, ok := stringArg(args, "type")
	if !ok {
		return errResult("type is required")
	}
	category, ok := stringArg(args, "category")
	if !ok {
		return errResult("category is required")
	}

	params := extractStringMap(args, "params")

	conn, err := h.client.CreateConnection(ctx, models.CreateConnectionRequest{
		Name:     name,
		Type:     connType,
		Category: category,
		Params:   params,
	})
	if err != nil {
		return errResult(fmt.Sprintf("failed to create connection: %v", err))
	}

	return jsonResult(map[string]interface{}{
		"message":    "Connection created successfully",
		"connection": conn,
	})
}

func (h *Handler) updateConnection(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("connectors:write") {
		return scopeError("connectors:write")
	}

	connectionID, ok := stringArg(args, "connection_id")
	if !ok {
		return errResult("connection_id is required")
	}

	req := models.UpdateConnectionRequest{}
	if v, ok := stringArg(args, "name"); ok {
		req.Name = v
	}
	if p := extractStringMap(args, "params"); len(p) > 0 {
		req.Params = p
	}

	conn, err := h.client.UpdateConnection(ctx, connectionID, req)
	if err != nil {
		return errResult(fmt.Sprintf("failed to update connection: %v", err))
	}

	return jsonResult(map[string]interface{}{
		"message":    "Connection updated successfully",
		"connection": conn,
	})
}

func (h *Handler) deleteConnection(ctx auth.OrgContext, args map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("connectors:write") {
		return scopeError("connectors:write")
	}

	connectionID, ok := stringArg(args, "connection_id")
	if !ok {
		return errResult("connection_id is required")
	}

	if err := h.client.DeleteConnection(ctx, connectionID); err != nil {
		return errResult(fmt.Sprintf("failed to delete connection: %v", err))
	}

	return textResult(fmt.Sprintf("Connection %s deleted successfully.", connectionID))
}

func (h *Handler) listConnectionTypes(ctx auth.OrgContext, _ map[string]interface{}) models.ToolResult {
	if !ctx.HasScope("connectors:read") {
		return scopeError("connectors:read")
	}

	types, err := h.client.ListConnectionTypes(ctx)
	if err != nil {
		return errResult("failed to list connection types")
	}

	return jsonResult(map[string]interface{}{
		"count": len(types),
		"types": types,
	})
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func stringArg(args map[string]interface{}, key string) (string, bool) {
	v, ok := args[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

func textResult(text string) models.ToolResult {
	return models.ToolResult{
		Content: []models.ToolContent{{Type: "text", Text: text}},
	}
}

func jsonResult(v interface{}) models.ToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult("serialization error")
	}
	return models.ToolResult{
		Content: []models.ToolContent{{Type: "text", Text: string(b)}},
	}
}

func errResult(msg string) models.ToolResult {
	return models.ToolResult{
		IsError: true,
		Content: []models.ToolContent{{Type: "text", Text: msg}},
	}
}

func extractStringMap(args map[string]interface{}, key string) map[string]string {
	raw, ok := args[key].(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func scopeError(scope string) models.ToolResult {
	return models.ToolResult{
		IsError: true,
		Content: []models.ToolContent{{
			Type: "text",
			Text: fmt.Sprintf("Insufficient permissions. This API key does not have the '%s' scope.", scope),
		}},
	}
}
