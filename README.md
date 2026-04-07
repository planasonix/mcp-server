# Planasonix MCP Server

[![Release](https://img.shields.io/github/v/release/planasonix/mcp-server)](https://github.com/planasonix/mcp-server/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Connect [Planasonix](https://planasonix.com) to Claude, Cursor, Windsurf, VS Code, and any AI assistant that supports the [Model Context Protocol](https://modelcontextprotocol.io).

Manage ETL pipelines, create connections, set schedules, troubleshoot failures, and more — all through natural language.

---

## Quick Start

### 1. Download

Grab the latest binary for your platform from [Releases](https://github.com/planasonix/mcp-server/releases/latest):

| Platform | File |
|----------|------|
| macOS (Apple Silicon) | `planasonix-mcp-darwin-arm64` |
| macOS (Intel) | `planasonix-mcp-darwin-amd64` |
| Linux (x86_64) | `planasonix-mcp-linux-amd64` |
| Linux (ARM) | `planasonix-mcp-linux-arm64` |
| Windows | `planasonix-mcp-windows-amd64.exe` |

```bash
# macOS / Linux
chmod +x planasonix-mcp-darwin-arm64
sudo mv planasonix-mcp-darwin-arm64 /usr/local/bin/planasonix-mcp
```

### 2. Get Your API Key

1. Log in to your [Planasonix dashboard](https://app.planasonix.com)
2. Go to **Settings → API Keys**
3. Click **Generate New Key** and select the scopes you need
4. Copy the key (it's only shown once)

### 3. Configure Your AI Client

<details>
<summary><strong>Claude Desktop</strong></summary>

Edit `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "planasonix": {
      "command": "/usr/local/bin/planasonix-mcp",
      "args": ["--stdio"],
      "env": {
        "PLANASONIX_API_KEY": "flx_your_key_here",
        "PLANASONIX_API_URL": "https://api.planasonix.com"
      }
    }
  }
}
```

Restart Claude Desktop. A hammer icon (🔨) confirms MCP tools are active.
</details>

<details>
<summary><strong>Cursor</strong></summary>

Create `.cursor/mcp.json` in your project root or `~/.cursor/mcp.json` globally:

```json
{
  "mcpServers": {
    "planasonix": {
      "command": "/usr/local/bin/planasonix-mcp",
      "args": ["--stdio"],
      "env": {
        "PLANASONIX_API_KEY": "flx_your_key_here",
        "PLANASONIX_API_URL": "https://api.planasonix.com"
      }
    }
  }
}
```
</details>

<details>
<summary><strong>Windsurf</strong></summary>

Edit `~/.codeium/windsurf/mcp_config.json`:

```json
{
  "mcpServers": {
    "planasonix": {
      "command": "/usr/local/bin/planasonix-mcp",
      "args": ["--stdio"],
      "env": {
        "PLANASONIX_API_KEY": "flx_your_key_here",
        "PLANASONIX_API_URL": "https://api.planasonix.com"
      }
    }
  }
}
```
</details>

<details>
<summary><strong>VS Code (GitHub Copilot)</strong></summary>

Add to your VS Code `settings.json`:

```json
{
  "mcp": {
    "servers": {
      "planasonix": {
        "command": "/usr/local/bin/planasonix-mcp",
        "args": ["--stdio"],
        "env": {
          "PLANASONIX_API_KEY": "flx_your_key_here",
          "PLANASONIX_API_URL": "https://api.planasonix.com"
        }
      }
    }
  }
}
```
</details>

### 4. Verify

Ask your AI assistant:

```
List my Planasonix pipelines
```

If you see pipeline data, you're connected.

---

## What You Can Do

```
"List all my active pipelines"
"Create a pipeline that extracts orders from PostgreSQL, deduplicates by order_id, and loads into Snowflake"
"Why did my Salesforce sync fail last night?"
"Add a filter step to only include records where status is active"
"Set up a daily schedule for the billing pipeline at 2am EST"
"Create a new Snowflake connection"
"What's the error rate on my pipelines this week?"
"Pause the hourly sync pipeline"
```

---

## Available Tools (22)

### Pipeline Operations

| Tool | Scope | Description |
|------|-------|-------------|
| `list_pipelines` | `pipelines:read` | List all pipelines with status and schedule |
| `get_pipeline` | `pipelines:read` | Get details for a specific pipeline |
| `get_run_history` | `pipelines:read` | View recent run history with error details |
| `get_pipeline_health` | `pipelines:read` | Error rate, latency, rows processed, SLA status |
| `trigger_pipeline` | `pipelines:write` | Trigger an immediate pipeline run |
| `pause_pipeline` | `pipelines:write` | Pause a pipeline's schedule |
| `resume_pipeline` | `pipelines:write` | Resume a paused pipeline |
| `create_pipeline` | `pipelines:write` | Create a pipeline from natural language description |
| `update_pipeline` | `pipelines:write` | Modify a pipeline using natural language |
| `delete_pipeline` | `pipelines:write` | Soft-delete a pipeline (recoverable from UI) |

### Schedule Management

| Tool | Scope | Description |
|------|-------|-------------|
| `list_schedules` | `schedules:read` | List all pipeline schedules |
| `create_schedule` | `schedules:write` | Create a cron-based schedule for a pipeline |
| `update_schedule` | `schedules:write` | Update a schedule's cron, frequency, or timezone |
| `delete_schedule` | `schedules:write` | Permanently delete a schedule |
| `enable_schedule` | `schedules:write` | Re-enable a disabled schedule |
| `disable_schedule` | `schedules:write` | Disable a schedule without deleting it |

### Connection Management

| Tool | Scope | Description |
|------|-------|-------------|
| `list_connectors` | `connectors:read` | List configured source/destination connectors |
| `test_connection` | `connectors:read` | Test connector reachability and latency |
| `list_connection_types` | `connections:read` | Discover available connector types |
| `create_connection` | `connections:write` | Create a new data source or destination |
| `update_connection` | `connections:write` | Update a connection's name or parameters |
| `delete_connection` | `connections:write` | Delete a saved connection |

---

## API Key Scopes

| Scope | Grants |
|-------|--------|
| `pipelines:read` | View pipelines, run history, health metrics |
| `pipelines:write` | Create, modify, trigger, pause, resume, delete pipelines |
| `connectors:read` | View and test connectors |
| `connections:read` | View connections and connection types |
| `connections:write` | Create, update, delete connections |
| `schedules:read` | View pipeline schedules |
| `schedules:write` | Create, update, enable, disable, delete schedules |

---

## Security

- **Full tenant isolation** — your API key scopes every request to your organization. It is impossible to access another org's data.
- **Scope-based access control** — read-only keys cannot modify pipelines or connections.
- **Audit trail** — all MCP requests are logged in your Planasonix dashboard.
- **No data passthrough** — the MCP server is a thin control plane proxy. Your pipeline data flows directly between sources and destinations, never through MCP.

---

## Enterprise: Remote Deployment (HTTP+SSE)

For organizations that don't allow local binary execution, Planasonix supports a remote HTTP+SSE transport:

```json
{
  "mcpServers": {
    "planasonix": {
      "type": "url",
      "url": "https://mcp.planasonix.com/sse",
      "headers": {
        "Authorization": "Bearer flx_your_key_here"
      }
    }
  }
}
```

Self-hosted Docker deployment is also available for Enterprise customers. See the [full integration guide](https://docs.planasonix.com/mcp) for details.

---

## Troubleshooting

| Problem | Solution |
|---------|----------|
| AI client doesn't show MCP tools | Verify config JSON is valid. Check the binary path is correct and executable. |
| "Unauthorized" error | API key may be invalid or revoked. Generate a new one in Settings → API Keys. |
| "Insufficient permissions" | Your key is missing the required scope. Generate a new key with the right scopes. |
| Pipeline creation times out | AI generation can take up to 2 min. Simplify the description or break into steps. |
| macOS quarantine warning | Run: `xattr -d com.apple.quarantine /usr/local/bin/planasonix-mcp` |

---

## Support

- Documentation: [docs.planasonix.com/mcp](https://docs.planasonix.com/mcp)
- Email: [support@planasonix.com](mailto:support@planasonix.com)
- Issues: [github.com/planasonix/mcp-server/issues](https://github.com/planasonix/mcp-server/issues)

---

## License

[MIT](LICENSE)
