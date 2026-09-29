---
sidebar_position: 2
---

# API Reference

All endpoints are relative to the server base URL (e.g., `http://localhost:54321`). Responses use the standard `{"ok": true, "data": {...}}` envelope described in the [overview](./overview.md).

## Health

### `GET /health`

Check server status. Always exempt from authentication.

```bash
curl http://localhost:54321/health
```

```json
{
  "ok": true,
  "data": {
    "status": "ok",
    "session_id": "ses_a1b2c3",
    "change_token": "1821"
  }
}
```

The `change_token` is a monotonically increasing value derived from the action log. Use it with SSE to detect changes.

---

## Monitor

### `GET /v1/monitor`

Full board/list/activity snapshot -- the primary read endpoint for dashboard UIs.

**Query parameters:**

| Param | Default | Description |
|-------|---------|-------------|
| `include_closed` | `false` | Include closed issues |
| `sort` | `priority` | Sort mode: `priority`, `created`, `updated` |
| `search` | _(empty)_ | Search query |
| `search_mode` | `auto` | Search mode: `auto`, `text`, `tdq` |

```bash
curl "http://localhost:54321/v1/monitor?sort=priority&search=auth"
```

**Response `data` shape:**

```json
{
  "monitor": {
    "timestamp": "2026-02-27T04:20:00Z",
    "focused_issue": null,
    "in_progress": [],
    "task_list": {
      "reviewable": [],
      "needs_rework": [],
      "in_progress": [],
      "ready": [],
      "pending_review": [],
      "blocked": [],
      "closed": []
    },
    "activity": [],
    "recent_handoffs": [],
    "active_sessions": []
  },
  "session_id": "ses_a1b2c3",
  "change_token": "1824"
}
```

---

## Issues

### `GET /v1/issues`

List issues with filtering, search, sorting, and pagination.

**Query parameters:**

| Param | Default | Description |
|-------|---------|-------------|
| `status` | _(all open)_ | Filter by status (repeatable) |
| `type` | _(all)_ | Filter by type (repeatable) |
| `priority` | _(all)_ | Filter by priority (repeatable) |
| `search` | _(empty)_ | Search query |
| `search_mode` | `auto` | `auto`, `text`, or `tdq` |
| `include_closed` | `false` | Include closed issues |
| `sort` | `priority` | Sort by: `priority`, `created`, `updated`, `id` |
| `order` | _(depends)_ | `asc` or `desc` (default: `asc` for priority/id, `desc` for created/updated) |
| `limit` | `200` | Results per page (max `1000`) |
| `offset` | `0` | Pagination offset |

```bash
curl "http://localhost:54321/v1/issues?status=open&type=bug&sort=priority&limit=50"
```

```json
{
  "ok": true,
  "data": {
    "issues": [],
    "total": 42,
    "limit": 50,
    "offset": 0,
    "has_more": false
  }
}
```

### `GET /v1/issues/{id}`

Get a single issue with its logs, comments, handoff, dependencies, and direct
child issues.

Issue objects include an opaque `revision` string. Clients can send that value
in an `If-Match` header when editing, deleting, or applying a workflow action.
A stale revision returns `409 conflict` without applying the change. Fetch the
current task and compare it with the user's draft before resubmitting. Existing
clients can continue to omit the header.

The detail response also includes `children`, an array of direct child issues.

```bash
curl http://localhost:54321/v1/issues/td-abc123
```

```json
{
  "ok": true,
  "data": {
    "issue": { "id": "td-abc123", "title": "Fix auth", "status": "open", "..." : "..." },
    "logs": [],
    "comments": [],
    "latest_handoff": null,
    "children": [],
    "dependencies": [
      {
        "dep_id": "dep_a1b2c3d4",
        "issue_id": "td-abc123",
        "depends_on_id": "td-111",
        "relation_type": "depends_on"
      }
    ],
    "blocked_by": []
  }
}
```

- `dependencies` -- outgoing edges: issues that `{id}` depends on.
- `blocked_by` -- incoming edges: issues that depend on `{id}`.

### `POST /v1/issues`

Create a new issue.

```bash
curl -X POST http://localhost:54321/v1/issues \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Fix auth timeout",
    "type": "bug",
    "priority": "P1",
    "description": "Token expires too early",
    "points": 3,
    "labels": ["auth", "backend"]
  }'
```

**Request body fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `title` | string | yes | 3-200 characters |
| `type` | string | no | `bug`, `feature`, `task`, `epic`, `chore` (default: `task`) |
| `priority` | string | no | `P0`-`P4` (default: `P2`) |
| `description` | string | no | Issue description |
| `acceptance` | string | no | Acceptance criteria |
| `points` | int | no | Fibonacci: `1,2,3,5,8,13,21` |
| `labels` | string[] | no | Label tags |
| `parent_id` | string | no | Parent issue ID (must exist) |
| `sprint` | string | no | Sprint name |
| `minor` | bool | no | Mark as minor |
| `defer_until` | string | no | `YYYY-MM-DD` or `null` |
| `due_date` | string | no | `YYYY-MM-DD` or `null` |

**Response:**

```json
{ "ok": true, "data": { "issue": { "..." : "..." } } }
```

### `PATCH /v1/issues/{id}`

Partial update -- only include fields you want to change.

```bash
curl -X PATCH http://localhost:54321/v1/issues/td-abc123 \
  -H "Content-Type: application/json" \
  -d '{"priority": "P0", "labels": ["auth", "urgent"]}'
```

### `DELETE /v1/issues/{id}`

Soft-delete an issue (can be restored via CLI).

```bash
curl -X DELETE http://localhost:54321/v1/issues/td-abc123
```

```json
{ "ok": true, "data": { "deleted": true } }
```

---

## Status Transitions

All transition endpoints accept `POST` and an optional JSON body with a `reason` field. The reason is logged as a progress entry.

```bash
curl -X POST http://localhost:54321/v1/issues/td-abc123/start \
  -H "Content-Type: application/json" \
  -d '{"reason": "Starting auth work"}'
```

### Transition Matrix

| Endpoint | Valid From | Target Status |
|----------|-----------|---------------|
| `POST /v1/issues/{id}/start` | `open` | `in_progress` |
| `POST /v1/issues/{id}/review` | `open`, `in_progress` | `in_review` |
| `POST /v1/issues/{id}/approve` | `in_review` | `closed` |
| `POST /v1/issues/{id}/reject` | `in_review` | `open` |
| `POST /v1/issues/{id}/block` | `open`, `in_progress` | `blocked` |
| `POST /v1/issues/{id}/unblock` | `blocked` | `open` |
| `POST /v1/issues/{id}/close` | `open`, `in_progress`, `blocked`, `in_review` | `closed` |
| `POST /v1/issues/{id}/reopen` | `closed` | `open` |

Invalid transitions return `409 conflict`.

### Cascade Behavior

Some transitions trigger cascades:

- **review** -- if all siblings of a parent are reviewable, the parent cascades to `in_review`.
- **approve/close** -- parent cascades to `closed` when all children qualify, and blocked dependents are automatically unblocked.

The response includes cascade details:

```json
{
  "ok": true,
  "data": {
    "issue": { "..." : "..." },
    "cascades": {
      "parent_status_updates": [],
      "auto_unblocked": []
    }
  }
}
```

---

## Comments

### `POST /v1/issues/{id}/comments`

Add a comment to an issue.

```bash
curl -X POST http://localhost:54321/v1/issues/td-abc123/comments \
  -H "Content-Type: application/json" \
  -d '{"text": "Needs a test for the token refresh edge case."}'
```

```json
{
  "ok": true,
  "data": {
    "comment": {
      "id": "cmt_123",
      "issue_id": "td-abc123",
      "session_id": "ses_a1b2c3",
      "text": "Needs a test for the token refresh edge case.",
      "created_at": "2026-02-27T04:30:00Z"
    }
  }
}
```

### `DELETE /v1/issues/{id}/comments/{comment_id}`

Permanently delete a comment. Both the issue ID and comment ID must match.

```bash
curl -X DELETE http://localhost:54321/v1/issues/td-abc123/comments/cmt_123
```

```json
{ "ok": true, "data": { "deleted": true } }
```

---

## Dependencies

### `POST /v1/issues/{id}/dependencies`

Declare that `{id}` depends on another issue.

```bash
curl -X POST http://localhost:54321/v1/issues/td-abc123/dependencies \
  -H "Content-Type: application/json" \
  -d '{"depends_on": "td-222"}'
```

```json
{
  "ok": true,
  "data": {
    "dependency": {
      "dep_id": "dep_a1b2c3d4",
      "issue_id": "td-abc123",
      "depends_on_id": "td-222",
      "relation_type": "depends_on"
    }
  }
}
```

### `DELETE /v1/issues/{id}/dependencies/{dep_id}`

Remove a dependency using its `dep_id`. The dependency must belong to `{id}`.

```bash
curl -X DELETE http://localhost:54321/v1/issues/td-abc123/dependencies/dep_a1b2c3d4
```

```json
{ "ok": true, "data": { "removed": true } }
```

---

## Focus

### `PUT /v1/focus`

Set or clear the focused issue. This is stored in the project config, not the database.

```bash
# Set focus
curl -X PUT http://localhost:54321/v1/focus \
  -H "Content-Type: application/json" \
  -d '{"issue_id": "td-abc123"}'

# Clear focus
curl -X PUT http://localhost:54321/v1/focus \
  -H "Content-Type: application/json" \
  -d '{"issue_id": null}'
```

```json
{ "ok": true, "data": { "focused_issue_id": "td-abc123" } }
```

---

## Boards

### `GET /v1/boards`

List all boards.

```bash
curl http://localhost:54321/v1/boards
```

```json
{ "ok": true, "data": { "boards": [] } }
```

### `GET /v1/boards/{id}`

Get a board with its resolved issues. Accepts `include_closed=true` query param.

```bash
curl "http://localhost:54321/v1/boards/sprint-12?include_closed=true"
```

```json
{
  "ok": true,
  "data": {
    "board": {
      "id": "brd_abc",
      "name": "Sprint 12",
      "query": "sprint:sprint-12",
      "is_builtin": false,
      "view_mode": "list",
      "last_viewed_at": null,
      "created_at": "...",
      "updated_at": "..."
    },
    "issues": [
      {
        "issue": { "..." : "..." },
        "board_id": "brd_abc",
        "position": 0,
        "has_position": true,
        "category": "open"
      }
    ]
  }
}
```

Board issues are resolved by executing the board's TDQ query first, then applying position overlays for custom ordering.

### `POST /v1/boards`

Create a new board. The `query` must be valid TDQ syntax.

```bash
curl -X POST http://localhost:54321/v1/boards \
  -H "Content-Type: application/json" \
  -d '{"name": "Sprint 12", "query": "sprint:sprint-12"}'
```

### `PATCH /v1/boards/{id}`

Update a board's name and/or query.

```bash
curl -X PATCH http://localhost:54321/v1/boards/brd_abc \
  -H "Content-Type: application/json" \
  -d '{"name": "Sprint 13", "query": "sprint:sprint-13"}'
```

### `DELETE /v1/boards/{id}`

Delete a board.

```bash
curl -X DELETE http://localhost:54321/v1/boards/brd_abc
```

### `POST /v1/boards/{id}/issues`

Set an explicit position for an issue within a board.

```bash
curl -X POST http://localhost:54321/v1/boards/brd_abc/issues \
  -H "Content-Type: application/json" \
  -d '{"issue_id": "td-abc123", "position": 0}'
```

### `DELETE /v1/boards/{id}/issues/{issue_id}`

Remove an issue's explicit position overlay from a board.

```bash
curl -X DELETE http://localhost:54321/v1/boards/brd_abc/issues/td-abc123
```

---

## Sessions

### `GET /v1/sessions`

List all sessions with the current server session highlighted.

```bash
curl http://localhost:54321/v1/sessions
```

```json
{
  "ok": true,
  "data": {
    "sessions": [
      {
        "id": "ses_a1b2c3",
        "name": "td-serve-web",
        "branch": "default",
        "agent_type": "web",
        "started_at": "2026-02-27T03:00:00Z",
        "last_activity": "2026-02-27T04:10:00Z"
      }
    ],
    "current_session_id": "ses_a1b2c3"
  }
}
```

---

## Stats

### `GET /v1/stats`

Project-wide statistics.

```bash
curl http://localhost:54321/v1/stats
```

```json
{
  "ok": true,
  "data": {
    "total": 142,
    "by_status": { "open": 34, "in_progress": 5, "blocked": 3, "in_review": 2, "closed": 98 },
    "by_type": { "bug": 12, "feature": 45, "task": 60, "epic": 8, "chore": 17 },
    "by_priority": { "P0": 2, "P1": 8, "P2": 55, "P3": 40, "P4": 37 },
    "created_today": 3,
    "created_this_week": 11,
    "total_points": 287,
    "completion_rate": 0.69,
    "total_logs": 534,
    "total_handoffs": 89
  }
}
```

---

## Real-Time Events (SSE)

### `GET /v1/events`

Server-Sent Events stream for real-time change notifications.

```bash
curl -N http://localhost:54321/v1/events
```

### Event Types

**`refresh`** -- emitted when data changes (after writes or when the poll detects a new change token):

```text
id: 1824
event: refresh
data: {"change_token":"1824","timestamp":"2026-02-27T04:20:07Z"}
```

**`ping`** -- emitted every 30 seconds as a keepalive:

```text
id: 1824
event: ping
data: {"change_token":"1824"}
```

### Reconnect Behavior

The server supports the `Last-Event-ID` header. When a client reconnects with a stale event ID, the server sends an immediate `refresh` event so the client can re-fetch current data.

Recommended client reconnect strategy: exponential backoff starting at 1 second, capped at 10 seconds.

### Usage Pattern

1. Connect to `GET /v1/events`.
2. On `refresh` events, re-fetch data from `GET /v1/monitor` or the relevant endpoint.
3. Use `change_token` from the health or monitor endpoints to track whether your local state is current.
