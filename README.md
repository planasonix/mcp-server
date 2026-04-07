# planasonix-mcp

MCP (Model Context Protocol) server for Planasonix — exposes pipeline management
tools to Claude and other MCP-compatible AI clients, with full multi-tenant
isolation baked in from the ground up.

---

## Project Structure

```
planasonix-mcp/
├── main.go                  # Entry point, config, graceful shutdown
├── Dockerfile
├── auth/
│   └── auth.go              # API key validation → OrgContext resolution
├── middleware/
│   └── middleware.go        # RequestID, Logger, Recover middleware
├── models/
│   └── models.go            # MCP protocol types + Planasonix domain types
├── server/
│   └── server.go            # HTTP/SSE handler, auth enforcement, rate limiter
└── tools/
    ├── tools.go             # All MCP tool handlers (org-scoped)
    └── stub_client.go       # Stub PlanasonixClient — replace with real HTTP client
```

---

## Quick Start

```bash
# 1. Install dependencies
go mod tidy

# 2. Run locally (uses stub client with fake data)
PORT=8080 go run .

# 3. Test the health endpoint
curl http://localhost:8080/health

# 4. Test a tool call with a valid API key
curl -X POST http://localhost:8080/messages \
  -H "Authorization: Bearer plx_live_examplekey123" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "list_pipelines",
      "arguments": {}
    }
  }'
```

---

## Security Model

### The golden rule
**Tenant identity is ALWAYS derived from the API key — never from client-supplied parameters.**

The flow:
1. Client sends `Authorization: Bearer plx_live_<key>`
2. `auth.KeyStore.Validate()` resolves the key to an `OrgContext` (`org_id`, `org_name`, `scopes`)
3. `OrgContext` is attached to the request `context.Context`
4. Every tool handler receives `OrgContext` — it never reads `org_id` from tool arguments
5. All DB/API queries are scoped to `orgCtx.OrgID` at the call site

### Defense layers
| Layer | Mechanism |
|-------|-----------|
| Authentication | Bearer token → OrgContext on every request |
| Authorization | Per-scope checks (`pipelines:read`, `pipelines:write`, `connectors:read`) |
| Data isolation | All internal API calls include `orgID` derived from auth context |
| Resource validation | IDs from tool args are re-verified against org before use |
| Rate limiting | Token bucket per `org_id`, 60 req/min default |
| Prompt injection defense | Tool argument `org_id` is always ignored; auth context is authoritative |
| Error messages | Cross-org resource access returns 404, not 403 (avoids confirming existence) |

---

## Available Tools

| Tool | Scope required | Description |
|------|---------------|-------------|
| `list_pipelines` | `pipelines:read` | List all org pipelines with status |
| `get_pipeline` | `pipelines:read` | Get details for a specific pipeline |
| `trigger_pipeline` | `pipelines:write` | Trigger an immediate pipeline run |
| `pause_pipeline` | `pipelines:write` | Pause a pipeline's schedule |
| `resume_pipeline` | `pipelines:write` | Resume a paused pipeline |
| `get_run_history` | `pipelines:read` | Get recent run history with errors |
| `get_pipeline_health` | `pipelines:read` | Error rate, latency, SLA status |
| `list_connectors` | `connectors:read` | List source/destination connectors |
| `test_connection` | `connectors:read` | Test connector reachability |

---

## Wiring to Your Real Planasonix API

The `tools/stub_client.go` implements `PlanasonixClient` with fake data.
To wire it to your real internal API:

1. Create `tools/http_client.go` implementing `PlanasonixClient`
2. Make authenticated HTTP calls to your internal Planasonix API
3. In `server/server.go`, swap `tools.NewStubClient()` for `tools.NewHTTPClient(cfg.PlanasonixAPI, cfg.InternalToken)`

Example:
```go
type HTTPClient struct {
    baseURL string
    token   string
    http    *http.Client
}

func (c *HTTPClient) ListPipelines(orgID string) ([]models.Pipeline, error) {
    req, _ := http.NewRequest("GET", c.baseURL+"/internal/pipelines", nil)
    req.Header.Set("Authorization", "Bearer "+c.token)
    req.Header.Set("X-Org-ID", orgID)
    // ... parse response
}
```

---

## Production DB-backed Key Store

Replace `auth.NewInMemoryKeyStore()` with a DB implementation:

```go
// Your api_keys table schema (suggested):
// CREATE TABLE api_keys (
//     key_hash     TEXT PRIMARY KEY,   -- SHA-256 of the raw key
//     org_id       TEXT NOT NULL,
//     org_name     TEXT NOT NULL,
//     scopes       TEXT[] NOT NULL,
//     created_at   TIMESTAMPTZ DEFAULT NOW(),
//     expires_at   TIMESTAMPTZ,
//     revoked_at   TIMESTAMPTZ
// );
```

See the commented-out `DBKeyStore` in `auth/auth.go` for the implementation stub.

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP listen port |
| `PLANASONIX_API_URL` | `http://localhost:9000` | Internal Planasonix API base URL |
| `PLANASONIX_INTERNAL_TOKEN` | `` | Service-to-service auth token |

---

## Claude Desktop Config

Add to your `claude_desktop_config.json` to connect locally:

```json
{
  "mcpServers": {
    "planasonix": {
      "command": "curl",
      "args": ["-s", "-N", "-H", "Authorization: Bearer plx_live_yourkey", "http://localhost:8080/sse"]
    }
  }
}
```

For a remote/hosted deployment use the `url` transport type instead:
```json
{
  "mcpServers": {
    "planasonix": {
      "type": "url",
      "url": "https://mcp.planasonix.com/sse",
      "headers": {
        "Authorization": "Bearer plx_live_yourkey"
      }
    }
  }
}
```

---

## Adding New Tools

1. Add the method to `PlanasonixClient` interface in `tools/tools.go`
2. Implement it in `tools/stub_client.go` (stub) and your HTTP client
3. Add the handler function in `tools/tools.go` following the same org-scope pattern
4. Register the case in `Handler.Dispatch()`
5. Add the `ToolDefinition` in `server/buildToolDefinitions()`
