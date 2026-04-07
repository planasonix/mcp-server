package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/planasonix/mcp-server/auth"
	"github.com/planasonix/mcp-server/models"
)

// HTTPClient calls the Planasonix backend REST API, forwarding the
// caller's API key in the Authorization header so the backend's own
// auth middleware enforces org-scoped access.
type HTTPClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ── PlanasonixClient implementation ─────────────────────────────────────────

func (c *HTTPClient) ListPipelines(ctx auth.OrgContext) ([]models.Pipeline, error) {
	var raw []pipelineResponse
	if err := c.get(ctx, "/api/pipelines", &raw); err != nil {
		return nil, err
	}
	out := make([]models.Pipeline, len(raw))
	for i, r := range raw {
		out[i] = r.toModel()
	}
	return out, nil
}

func (c *HTTPClient) GetPipeline(ctx auth.OrgContext, pipelineID string) (*models.Pipeline, error) {
	var raw pipelineResponse
	if err := c.get(ctx, "/api/pipelines/"+pipelineID, &raw); err != nil {
		return nil, err
	}
	m := raw.toModel()
	return &m, nil
}

func (c *HTTPClient) TriggerPipeline(ctx auth.OrgContext, pipelineID string, params map[string]interface{}) (*models.PipelineRun, error) {
	body := map[string]interface{}{}
	if params != nil {
		body["parameters"] = params
	}
	var raw triggerResponse
	if err := c.post(ctx, "/api/pipelines/"+pipelineID+"/run", body, &raw); err != nil {
		return nil, err
	}
	return &models.PipelineRun{
		ID:         raw.RunID,
		PipelineID: pipelineID,
		Status:     "running",
		StartedAt:  time.Now().Format(time.RFC3339),
	}, nil
}

func (c *HTTPClient) PausePipeline(ctx auth.OrgContext, pipelineID string) error {
	return c.patch(ctx, "/api/pipelines/"+pipelineID+"/status", map[string]string{"status": "paused"})
}

func (c *HTTPClient) ResumePipeline(ctx auth.OrgContext, pipelineID string) error {
	return c.patch(ctx, "/api/pipelines/"+pipelineID+"/status", map[string]string{"status": "active"})
}

func (c *HTTPClient) GetRunHistory(ctx auth.OrgContext, pipelineID string, limit int) ([]models.PipelineRun, error) {
	path := fmt.Sprintf("/api/pipelines/%s/runs?limit=%d", pipelineID, limit)
	var raw []runResponse
	if err := c.get(ctx, path, &raw); err != nil {
		return nil, err
	}
	out := make([]models.PipelineRun, len(raw))
	for i, r := range raw {
		out[i] = r.toModel()
	}
	return out, nil
}

func (c *HTTPClient) GetPipelineHealth(ctx auth.OrgContext, pipelineID string) (*models.PipelineHealth, error) {
	// No dedicated health endpoint — derive from the last 50 runs.
	runs, err := c.GetRunHistory(ctx, pipelineID, 50)
	if err != nil {
		return nil, err
	}
	return computeHealth(pipelineID, runs), nil
}

func (c *HTTPClient) ListConnectors(ctx auth.OrgContext) ([]models.Connector, error) {
	var raw []connectorResponse
	if err := c.get(ctx, "/api/connections", &raw); err != nil {
		return nil, err
	}
	out := make([]models.Connector, len(raw))
	for i, r := range raw {
		out[i] = r.toModel()
	}
	return out, nil
}

func (c *HTTPClient) TestConnection(ctx auth.OrgContext, connectorID string) (bool, string, error) {
	// Step 1: Fetch the saved connection to get its type and params.
	var conn struct {
		Type   string            `json:"type"`
		Params map[string]string `json:"params"`
	}
	if err := c.get(ctx, "/api/connections/"+connectorID, &conn); err != nil {
		return false, "", fmt.Errorf("fetching connection: %w", err)
	}

	// Step 2: POST type+params to the test endpoint.
	var result struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := c.post(ctx, "/api/connections/test", conn, &result); err != nil {
		return false, "", err
	}
	return result.Success, result.Message, nil
}

// ── Pipeline CRUD (AI-powered) ──────────────────────────────────────────────

func (c *HTTPClient) CreatePipeline(ctx auth.OrgContext, name, description string) (*models.Pipeline, error) {
	// Step 1: Generate pipeline graph via backend AI.
	var aiResp struct {
		Success            bool          `json:"success"`
		PipelineName       string        `json:"pipelineName"`
		Description        string        `json:"description"`
		Nodes              []interface{} `json:"nodes"`
		Edges              []interface{} `json:"edges"`
		Explanation        string        `json:"explanation"`
		NeedsClarification bool          `json:"needsClarification"`
		ClarifyingQuestion string        `json:"clarifyingQuestion"`
		Error              string        `json:"error"`
	}
	if err := c.postAI(ctx, "/api/ai/generate-pipeline", map[string]interface{}{
		"description": description,
	}, &aiResp); err != nil {
		return nil, fmt.Errorf("AI pipeline generation: %w", err)
	}
	if !aiResp.Success {
		if aiResp.NeedsClarification {
			return nil, fmt.Errorf("AI needs more detail: %s", aiResp.ClarifyingQuestion)
		}
		errMsg := aiResp.Error
		if errMsg == "" {
			errMsg = "AI failed to generate pipeline"
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

	// Step 2: Create the pipeline with the generated nodes/edges.
	pipelineName := name
	if pipelineName == "" {
		pipelineName = aiResp.PipelineName
	}

	body := map[string]interface{}{
		"name":   pipelineName,
		"nodes":  aiResp.Nodes,
		"edges":  aiResp.Edges,
		"status": "paused",
	}

	var raw pipelineResponse
	if err := c.post(ctx, "/api/pipelines", body, &raw); err != nil {
		return nil, fmt.Errorf("creating pipeline: %w", err)
	}

	m := raw.toModel()
	return &m, nil
}

func (c *HTTPClient) UpdatePipelineWithAI(ctx auth.OrgContext, pipelineID, description string) (*models.Pipeline, error) {
	// Step 1: Fetch the current pipeline to get its nodes/edges.
	var current struct {
		Name  string        `json:"name"`
		Nodes []interface{} `json:"nodes"`
		Edges []interface{} `json:"edges"`
	}
	if err := c.get(ctx, "/api/pipelines/"+pipelineID, &current); err != nil {
		return nil, fmt.Errorf("fetching pipeline: %w", err)
	}

	// Step 2: Ask the AI to enhance the pipeline.
	var aiResp struct {
		Success            bool          `json:"success"`
		NewNodes           []interface{} `json:"newNodes"`
		NewEdges           []interface{} `json:"newEdges"`
		Explanation        string        `json:"explanation"`
		NeedsClarification bool          `json:"needsClarification"`
		ClarifyingQuestion string        `json:"clarifyingQuestion"`
		Error              string        `json:"error"`
	}
	if err := c.postAI(ctx, "/api/ai/enhance-pipeline", map[string]interface{}{
		"request":      description,
		"currentNodes": current.Nodes,
		"currentEdges": current.Edges,
		"pipelineName": current.Name,
	}, &aiResp); err != nil {
		return nil, fmt.Errorf("AI pipeline enhancement: %w", err)
	}
	if !aiResp.Success {
		if aiResp.NeedsClarification {
			return nil, fmt.Errorf("AI needs more detail: %s", aiResp.ClarifyingQuestion)
		}
		errMsg := aiResp.Error
		if errMsg == "" {
			errMsg = "AI failed to enhance pipeline"
		}
		return nil, fmt.Errorf("%s", errMsg)
	}

	// Step 3: Merge new nodes/edges with existing ones and save.
	// Defensive copy — append on the original slice could mutate its backing
	// array if Go allocated spare capacity during JSON unmarshal.
	mergedNodes := make([]interface{}, 0, len(current.Nodes)+len(aiResp.NewNodes))
	mergedNodes = append(mergedNodes, current.Nodes...)
	mergedNodes = append(mergedNodes, aiResp.NewNodes...)
	mergedEdges := make([]interface{}, 0, len(current.Edges)+len(aiResp.NewEdges))
	mergedEdges = append(mergedEdges, current.Edges...)
	mergedEdges = append(mergedEdges, aiResp.NewEdges...)

	body := map[string]interface{}{
		"id":    pipelineID,
		"name":  current.Name,
		"nodes": mergedNodes,
		"edges": mergedEdges,
	}

	var raw pipelineResponse
	if err := c.put(ctx, "/api/pipelines/"+pipelineID, body, &raw); err != nil {
		return nil, fmt.Errorf("saving pipeline: %w", err)
	}

	m := raw.toModel()
	return &m, nil
}

func (c *HTTPClient) DeletePipeline(ctx auth.OrgContext, pipelineID string) error {
	return c.del(ctx, "/api/pipelines/"+pipelineID)
}

// ── Schedule Management ─────────────────────────────────────────────────────

func (c *HTTPClient) ListSchedules(ctx auth.OrgContext) ([]models.Schedule, error) {
	var raw struct {
		Data []scheduleResponse `json:"data"`
	}
	if err := c.get(ctx, "/api/schedules", &raw); err != nil {
		return nil, err
	}
	out := make([]models.Schedule, len(raw.Data))
	for i, r := range raw.Data {
		out[i] = r.toModel()
	}
	return out, nil
}

func (c *HTTPClient) CreateSchedule(ctx auth.OrgContext, req models.CreateScheduleRequest) (*models.Schedule, error) {
	var raw scheduleResponse
	if err := c.post(ctx, "/api/schedules", req, &raw); err != nil {
		return nil, err
	}
	m := raw.toModel()
	return &m, nil
}

func (c *HTTPClient) UpdateSchedule(ctx auth.OrgContext, scheduleID string, req models.UpdateScheduleRequest) (*models.Schedule, error) {
	// The backend's UpdateSchedule does a blanket SET on cron_expression,
	// frequency, and timezone — empty values wipe existing data. Fetch
	// current schedule and merge to avoid data loss.
	var current scheduleResponse
	if err := c.get(ctx, "/api/schedules/"+scheduleID, &current); err != nil {
		return nil, fmt.Errorf("fetching schedule: %w", err)
	}

	body := models.CreateScheduleRequest{
		PipelineID:     current.PipelineID,
		CronExpression: current.CronExpression,
		Frequency:      current.Frequency,
		Timezone:       current.Timezone,
	}
	if req.CronExpression != "" {
		body.CronExpression = req.CronExpression
	}
	if req.Frequency != "" {
		body.Frequency = req.Frequency
	}
	if req.Timezone != "" {
		body.Timezone = req.Timezone
	}

	var raw scheduleResponse
	if err := c.put(ctx, "/api/schedules/"+scheduleID, body, &raw); err != nil {
		return nil, err
	}
	m := raw.toModel()
	return &m, nil
}

func (c *HTTPClient) DeleteSchedule(ctx auth.OrgContext, scheduleID string) error {
	return c.del(ctx, "/api/schedules/"+scheduleID)
}

func (c *HTTPClient) EnableSchedule(ctx auth.OrgContext, scheduleID string) error {
	return c.postNoBody(ctx, "/api/schedules/"+scheduleID+"/enable")
}

func (c *HTTPClient) DisableSchedule(ctx auth.OrgContext, scheduleID string) error {
	return c.postNoBody(ctx, "/api/schedules/"+scheduleID+"/disable")
}

// ── Connection Management ───────────────────────────────────────────────────

func (c *HTTPClient) CreateConnection(ctx auth.OrgContext, req models.CreateConnectionRequest) (*models.Connection, error) {
	var raw connectionResponse
	if err := c.post(ctx, "/api/connections", req, &raw); err != nil {
		return nil, err
	}
	m := raw.toConnectionModel()
	return &m, nil
}

func (c *HTTPClient) UpdateConnection(ctx auth.OrgContext, connectionID string, req models.UpdateConnectionRequest) (*models.Connection, error) {
	// The backend PUT expects the full connection; fetch current first and merge.
	var current connectionResponse
	if err := c.get(ctx, "/api/connections/"+connectionID, &current); err != nil {
		return nil, fmt.Errorf("fetching connection: %w", err)
	}

	body := map[string]interface{}{
		"id":       connectionID,
		"name":     current.Name,
		"type":     current.ConnType,
		"category": current.Category,
		"params":   current.Params,
	}
	if req.Name != "" {
		body["name"] = req.Name
	}
	if len(req.Params) > 0 {
		merged := make(map[string]string)
		for k, v := range current.Params {
			merged[k] = v
		}
		for k, v := range req.Params {
			merged[k] = v
		}
		body["params"] = merged
	}

	var raw connectionResponse
	if err := c.put(ctx, "/api/connections/"+connectionID, body, &raw); err != nil {
		return nil, err
	}
	m := raw.toConnectionModel()
	return &m, nil
}

func (c *HTTPClient) DeleteConnection(ctx auth.OrgContext, connectionID string) error {
	return c.del(ctx, "/api/connections/"+connectionID)
}

func (c *HTTPClient) ListConnectionTypes(ctx auth.OrgContext) ([]models.ConnectionType, error) {
	var raw struct {
		Connectors []galleryConnector `json:"connectors"`
	}
	if err := c.get(ctx, "/api/connector-gallery", &raw); err != nil {
		return nil, err
	}
	out := make([]models.ConnectionType, len(raw.Connectors))
	for i, gc := range raw.Connectors {
		out[i] = models.ConnectionType{
			ID:          gc.ID,
			Name:        gc.Name,
			Category:    gc.Category,
			Description: gc.Description,
		}
	}
	return out, nil
}

// ── HTTP helpers ────────────────────────────────────────────────────────────

func (c *HTTPClient) newRequest(ctx auth.OrgContext, method, path string, body interface{}) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+ctx.RawKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if ctx.OrgID != "" {
		req.Header.Set("X-Organization-ID", ctx.OrgID)
	}

	return req, nil
}

func (c *HTTPClient) do(req *http.Request, dst interface{}) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	if dst != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, dst); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}
	return nil
}

func (c *HTTPClient) get(ctx auth.OrgContext, path string, dst interface{}) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return c.do(req, dst)
}

func (c *HTTPClient) post(ctx auth.OrgContext, path string, body, dst interface{}) error {
	req, err := c.newRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	return c.do(req, dst)
}

func (c *HTTPClient) put(ctx auth.OrgContext, path string, body, dst interface{}) error {
	req, err := c.newRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	return c.do(req, dst)
}

func (c *HTTPClient) del(ctx auth.OrgContext, path string) error {
	req, err := c.newRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *HTTPClient) patch(ctx auth.OrgContext, path string, body interface{}) error {
	req, err := c.newRequest(ctx, http.MethodPatch, path, body)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *HTTPClient) postNoBody(ctx auth.OrgContext, path string) error {
	req, err := c.newRequest(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// postAI is like post but uses a longer timeout for AI/LLM endpoints
// that may take 30-90 seconds to respond.
func (c *HTTPClient) postAI(ctx auth.OrgContext, path string, body, dst interface{}) error {
	req, err := c.newRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	aiClient := &http.Client{Timeout: 120 * time.Second}
	resp, err := aiClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	if dst != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, dst); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}
	return nil
}

// ── Backend response types → MCP model mapping ─────────────────────────────
//
// The Planasonix backend serialises structs with camelCase JSON tags
// (e.g. sourceConnectionId, pipelineId, startedAt).  The types below
// must match that exact wire format.

type pipelineResponse struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	SourceConnectionID string `json:"sourceConnectionId"`
	DestConnectionID   string `json:"destConnectionId"`
	Status             string `json:"status"`
}

func (r pipelineResponse) toModel() models.Pipeline {
	return models.Pipeline{
		ID:          r.ID,
		Name:        r.Name,
		Source:      r.SourceConnectionID,
		Destination: r.DestConnectionID,
		Status:      r.Status,
	}
}

type runResponse struct {
	ID             string  `json:"id"`
	PipelineID     string  `json:"pipelineId"`
	Status         string  `json:"status"`
	StartedAt      string  `json:"startedAt"`
	EndedAt        *string `json:"endedAt"`
	RecordsWritten int64   `json:"recordsWritten"`
	ErrorMessage   *string `json:"errorMessage"`
}

func (r runResponse) toModel() models.PipelineRun {
	m := models.PipelineRun{
		ID:         r.ID,
		PipelineID: r.PipelineID,
		Status:     r.Status,
		StartedAt:  r.StartedAt,
		RowsLoaded: r.RecordsWritten,
	}
	if r.EndedAt != nil {
		m.FinishedAt = *r.EndedAt
	}
	if r.ErrorMessage != nil {
		m.ErrorMsg = *r.ErrorMessage
	}
	return m
}

type triggerResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	RunID   string `json:"runId"`
}

type connectorResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ConnType string `json:"type"`     // e.g. "postgres", "salesforce"
	Category string `json:"category"` // "source" or "destination"
}

func (r connectorResponse) toModel() models.Connector {
	return models.Connector{
		ID:   r.ID,
		Name: r.Name,
		Type: r.Category, // "source" / "destination"
		Kind: r.ConnType,  // "postgres", "snowflake", etc.
	}
}

type scheduleResponse struct {
	ID             string  `json:"id"`
	PipelineID     string  `json:"pipelineId"`
	PipelineName   string  `json:"pipelineName"`
	CronExpression string  `json:"cronExpression"`
	Frequency      string  `json:"frequency"`
	Timezone       string  `json:"timezone"`
	Enabled        bool    `json:"enabled"`
	NextRunAt      *string `json:"nextRunAt"`
	LastRunAt      *string `json:"lastRunAt"`
	LastRunStatus  *string `json:"lastRunStatus"`
}

func (r scheduleResponse) toModel() models.Schedule {
	m := models.Schedule{
		ID:             r.ID,
		PipelineID:     r.PipelineID,
		PipelineName:   r.PipelineName,
		CronExpression: r.CronExpression,
		Frequency:      r.Frequency,
		Timezone:       r.Timezone,
		Enabled:        r.Enabled,
	}
	if r.NextRunAt != nil {
		m.NextRunAt = *r.NextRunAt
	}
	if r.LastRunAt != nil {
		m.LastRunAt = *r.LastRunAt
	}
	if r.LastRunStatus != nil {
		m.LastRunStatus = *r.LastRunStatus
	}
	return m
}

type connectionResponse struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	ConnType string            `json:"type"`
	Category string            `json:"category"`
	Params   map[string]string `json:"params"`
}

func (r connectionResponse) toConnectionModel() models.Connection {
	return models.Connection{
		ID:       r.ID,
		Name:     r.Name,
		Type:     r.ConnType,
		Category: r.Category,
	}
}

type galleryConnector struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

// ── Derived health computation ──────────────────────────────────────────────

func computeHealth(pipelineID string, runs []models.PipelineRun) *models.PipelineHealth {
	h := &models.PipelineHealth{PipelineID: pipelineID}
	if len(runs) == 0 {
		return h
	}

	now := time.Now()
	sevenDaysAgo := now.Add(-7 * 24 * time.Hour)
	oneDayAgo := now.Add(-24 * time.Hour)

	var total7d, failed7d int
	var totalLatency int64
	var latencyCount int

	for _, r := range runs {
		startedAt, err := time.Parse(time.RFC3339, r.StartedAt)
		if err != nil {
			continue
		}

		if startedAt.After(sevenDaysAgo) {
			total7d++
			if r.Status == "failed" {
				failed7d++
			}
		}

		if startedAt.After(oneDayAgo) {
			h.RowsLast24h += r.RowsLoaded
		}

		if r.FinishedAt != "" {
			finishedAt, err := time.Parse(time.RFC3339, r.FinishedAt)
			if err == nil {
				totalLatency += finishedAt.Sub(startedAt).Milliseconds()
				latencyCount++
			}
		}
	}

	if total7d > 0 {
		h.ErrorRate7d = float64(failed7d) / float64(total7d)
	}
	if latencyCount > 0 {
		h.AvgLatencyMs = totalLatency / int64(latencyCount)
	}
	h.SLABreached = h.ErrorRate7d > 0.10

	return h
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
