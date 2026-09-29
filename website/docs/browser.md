# Browser workspace

`td browser` opens a local web interface for the current project. Use it to
manage the backlog and review agent work while agents continue to use td in
their own tools.

```bash
td browser
td browser --port 8080
td browser --no-open
td -w /path/to/project browser
```

The interface is included in the td binary. It needs no separate installation,
account, or JavaScript runtime. The server binds to `127.0.0.1` and stays attached
to your terminal. Press **Ctrl+C** to stop it. Closing a browser tab leaves the
server running. Only one `td serve` or `td browser` process can own a project's
port file; stop the existing process before starting the other command.

## Views

- **Board:** View tasks by status. Choose a saved board from the sidebar, or
  create one using a [TDQ query](query-language). Drag cards to reorder them
  or perform supported status changes.
- **List:** Search and filter project tasks in a compact table.
- **Reviews:** Open tasks submitted for review. Read their acceptance criteria,
  handoffs, comments, dependencies, and logs before using **Approve** or
  **Reject**. Approval and rejection are explicit actions, never drag gestures.
- **Activity:** Follow recent task changes, logs, and handoffs. Session timestamps
  show the last recorded activity; they do not indicate whether an agent process
  is currently running.

Select a task to open its side panel. Its URL can be bookmarked. Use **Edit** to
change its fields, including its parent task or epic. The panel also supports
comments and dependency editing. Descriptions and acceptance criteria use
Markdown, with a **Preview** button. **More fields** contains points, sprint,
due date, deferral, and the minor-task flag.

Text search is the default. Select **TDQ** to enter an explicit query. Type and
priority filters apply to task views. Enable **Closed** to include completed
tasks. Browser preferences are stored locally in the browser.

## Live updates and conflicts

Changes from the CLI, agents, and other browser tabs appear automatically.
The default change-check interval is two seconds; change it with
`--interval 1s`. A disconnected indicator appears if the server stops, and the
browser reconnects automatically when that address is available again.

Live updates preserve unsaved drafts. If a task changes after you open it,
saving the old draft returns a conflict. The saved version remains in place.
The comparison shows the original value, the current saved value, and your
draft for each changed field. Conflicting fields default to the saved value.
Choose the values you want, select **Review selected values**, then edit and
save explicitly. A further concurrent change triggers another comparison.

Writes use the existing HTTP API's shared web session and appear in td's action
history. **Close without review** is for administrative closures such as
duplicates or cancellations. See the [HTTP API](http-api/overview) for its
workflow and session model. Code review stays in your existing tools; links in
task descriptions can point to code or pull requests.

## Keyboard shortcuts

| Key | Action |
| --- | --- |
| `N` | Create a task |
| `/` | Focus search |
| `1`, `2`, `3`, `4` | Board, List, Reviews, Activity |
| `J`, `K` | Focus the next or previous task |
| `Enter` | Open the focused task |
| `Esc` | Close a panel or dialog |
| `?` | Show shortcuts |

Shortcuts are inactive while typing in a form. Use the sidebar theme control
to choose system, light, or dark mode. All status actions are also available as
buttons in the task panel.

## Development

The server embeds the files in `internal/serve/web/`. Build with `go build` as
usual; there is no frontend build step. Rebuild and restart the binary after
editing these files. `td serve` remains an API-only command, and `td monitor`
remains the terminal interface.
