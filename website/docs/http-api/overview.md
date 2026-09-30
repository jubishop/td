---
sidebar_position: 1
---

# HTTP API Overview

`td serve` runs a local HTTP API server that exposes td's issue tracker over REST. It provides JSON endpoints for creating, reading, updating, and managing issues, boards, sessions, and more -- designed for building web UIs and integrations on top of td.

## Starting the Server

```bash
td serve
```

This starts the server on a random available port. The actual port is printed to stderr and written to a discovery file.

```text
td serve listening on http://localhost:54321
  base dir:   /Users/marcus/code/td
  database:   /Users/marcus/code/td/.todos/issues.db
  session:    ses_a1b2c3 (web)
  port file:  /Users/marcus/code/td/.todos/serve-port
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-p, --port` | `0` (auto) | Port to listen on |
| `-a, --addr` | `localhost` | Address to bind to |
| `--token` | _(none)_ | Bearer token for authentication |
| `--cors` | _(none)_ | Allowed CORS origin for browser clients |
| `--interval` | `2s` | Poll interval for SSE change detection |

### Examples

```bash
# Auto-assign port (default)
td serve

# Fixed port
td serve --port 8080

# With auth and CORS for a React dev server
td serve --token my-secret --cors http://localhost:3000
```

## Discovery Mechanism

Each `td serve` process writes a JSON port file at `.todos/serve-port` for programmatic discovery:

```json
{
  "port": 54321,
  "pid": 91234,
  "started_at": "2026-02-27T05:10:11Z",
  "instance_id": "srv_8f3b2c"
}
```

### Consumer Discovery Flow

1. Run `td -w /path/to/project info --json` and read `base_dir`. This resolves
   worktrees, `.td-root`, and directory associations using td itself.
2. Read `<base_dir>/.todos/serve-port` and parse the JSON.
3. Call `GET /health` on the recorded port.
4. Reuse the process when healthy.
5. Start `td serve` when the file is missing, the PID is dead, or `/health` fails.

Use authenticated `GET /v1/project` to verify the project path and required
API capabilities before sending writes. Health alone does not establish
identity or compatibility. When a client starts a server, it owns its lifecycle;
clients that reuse a server must leave it running when they exit.

:::tip
Run one `td serve` process per td project. Do not coordinate multiple projects inside one server instance.
:::

## Response Format

All endpoints return a standard JSON envelope.

**Success:**

```json
{ "ok": true, "data": { ... } }
```

**Error:**

```json
{
  "ok": false,
  "error": {
    "code": "validation_error",
    "message": "request validation failed",
    "details": {
      "fields": [
        {
          "field": "title",
          "rule": "min_length",
          "value": 2,
          "expected": 3,
          "message": "title must be at least 3 characters"
        }
      ]
    }
  }
}
```

### Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| `validation_error` | 400 | Invalid request data |
| `unauthorized` | 401 | Missing or invalid auth token |
| `forbidden` | 403 | Access denied |
| `not_found` | 404 | Resource does not exist |
| `conflict` | 409 | Invalid state transition |
| `internal` | 500 | Server error |

## JSON Serialization Rules

The API enforces consistent JSON output:

- All documented keys are always present in responses.
- Unset optional references and timestamps serialize as `null`.
- Collections serialize as `[]` when empty, never `null`.
- Freeform text fields serialize as `""` when empty.

## Session Model

The server uses a single web session for write attribution. This session is created automatically on startup with:

- `agent_type = "web"`
- `name = "td-serve-web"`
- `branch = "default"`

All writes through the HTTP API are attributed to this session. The session's `last_activity` is bumped periodically while the server is running.

## Graceful Shutdown

The server handles `SIGINT` and `SIGTERM` gracefully:

1. Stops accepting new requests.
2. Drains in-flight requests.
3. Closes SSE client connections.
4. Deletes the `.todos/serve-port` file.
5. Force-closes after a 10-second timeout.
