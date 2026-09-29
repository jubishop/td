import {
  esc,
  icon,
  statusBadge,
  priorityBadge,
  relative,
  timestamp,
  issueLink,
  sessionName,
  statuses,
} from "./ui.js";

export function navigation(state) {
  const reviewCount = state.reviewIssues.length;
  document.querySelector("#navigation").innerHTML = [
    "board",
    "list",
    "reviews",
    "activity",
  ]
    .map(
      (view) =>
        `<button class="nav-item ${state.view === view ? "active" : ""}" data-action="view" data-view="${view}" ${state.view === view ? 'aria-current="page"' : ""}>${icon(view)}<span>${view[0].toUpperCase() + view.slice(1)}</span>${view === "reviews" && reviewCount ? `<span class="nav-count">${reviewCount}</span>` : ""}</button>`,
    )
    .join("");
  document.querySelector("#boards").innerHTML = state.boards
    .map(
      (board) =>
        `<button class="nav-item board-nav ${board.id === state.boardID && state.view === "board" ? "active" : ""}" data-action="board" data-id="${esc(board.id)}"><span class="board-symbol">▦</span><span class="truncate">${esc(board.name)}</span></button>`,
    )
    .join("");
  const board = state.boards.find((b) => b.id === state.boardID);
  const title =
    state.view === "board"
      ? board?.name || "Board"
      : { list: "All tasks", reviews: "Reviews", activity: "Activity" }[
          state.view
        ];
  document.querySelector("#view-title").textContent = title;
  document.querySelector("#breadcrumb-view").textContent = {
    board: "Board",
    list: "List",
    reviews: "Reviews",
    activity: "Activity",
  }[state.view];
  document.querySelector("#view-description").textContent = {
    board: "Move work forward, one task at a time.",
    list: "The details behind the work.",
    reviews: "Read the evidence. Decide what is ready.",
    activity: "Follow the work across your agent sessions.",
  }[state.view];
  const summary = state.monitor?.task_list;
  document.querySelector("#summary").innerHTML = summary
    ? [
        ["In progress", summary.in_progress.length, "in_progress"],
        ["To review", reviewCount, "in_review"],
        ["Blocked", summary.blocked.length, "blocked"],
      ]
        .map(
          ([label, count, status]) =>
            `<div class="summary-item"><strong class="status-${status}">${count}</strong><span>${label}</span></div>`,
        )
        .join("")
    : "";
  document.querySelector("#filters").hidden = state.view === "activity";
}

function empty(title, description, action = true) {
  return `<div class="empty-state"><span class="empty-icon">${icon("task")}</span><h2>${title}</h2><p>${description}</p>${action ? '<button class="primary" data-action="new-task">+ Create a task</button>' : ""}</div>`;
}

function card(issue, state) {
  const parent = state.issues.find((i) => i.id === issue.parent_id);
  return `<article class="task-card" draggable="true" data-id="${esc(issue.id)}" data-status="${esc(issue.status)}">
    <div class="card-top"><span class="mono muted">${esc(issue.id)}</span><span class="type-icon type-${esc(issue.type)}" title="${esc(issue.type)}">${icon(issue.type)}</span></div>
    <button class="card-title" data-action="task" data-id="${esc(issue.id)}">${esc(issue.title)}</button>
    ${issue.parent_id ? `<div class="card-parent" title="Parent task">${icon("epic")}${esc(parent?.title || issue.parent_id)}</div>` : ""}
    ${
      issue.labels.length
        ? `<div class="labels">${issue.labels
            .slice(0, 3)
            .map((l) => `<span class="label">${esc(l)}</span>`)
            .join(
              "",
            )}${issue.labels.length > 3 ? `<span class="muted">+${issue.labels.length - 3}</span>` : ""}</div>`
        : ""
    }
    <div class="card-bottom">${priorityBadge(issue.priority)}<span class="muted truncate">${issue.implementer_session ? esc(sessionName(state.sessions, issue.implementer_session)) : esc(issue.type)}${issue.due_date ? ` · due ${esc(issue.due_date)}` : ""}</span></div>
  </article>`;
}

function boardView(state) {
  const board = state.boards.find((b) => b.id === state.boardID);
  const tools = `<div class="board-tools"><span class="muted">${board?.query ? `<span class="mono">${esc(board.query)}</span>` : "All project tasks"} · ${state.issues.length} tasks</span><span>${board && !board.is_builtin ? '<button class="quiet" data-action="edit-board">Edit board</button>' : ""}<button class="quiet" data-action="new-board">+ Save a board</button></span></div>`;
  const columns = Object.entries(statuses).filter(
    ([key]) => key !== "closed" || state.filters.include_closed,
  );
  return (
    tools +
    `<div class="kanban">${columns
      .map(([key, label]) => {
        const issues = state.issues.filter((i) => i.status === key);
        return `<section class="column" data-status="${key}" aria-label="${label} tasks"><header class="column-header"><span class="status-${key}"><span class="status-dot"></span>${label}</span><span class="column-count">${issues.length}</span></header><div class="column-cards">${issues.map((i) => card(i, state)).join("")}<div class="column-end" data-status="${key}">${issues.length ? "Drop here to move to the end" : "No tasks here yet"}</div></div></section>`;
      })
      .join("")}</div>`
  );
}

function listView(state, reviews = false) {
  if (!state.issues.length)
    return empty(
      reviews ? "All caught up" : "No matching tasks",
      reviews
        ? "Tasks submitted for review will appear here."
        : "Try a different filter, or create your first task.",
      !reviews,
    );
  return `${reviews ? '<div class="review-intro"><span class="status-dot status-in_review"></span>Select a task to inspect its acceptance criteria, handoff, and activity before deciding.</div>' : ""}<table class="issue-table"><thead><tr><th>Task</th><th>Status</th><th>Priority</th><th>${reviews ? "Implementer" : "Labels"}</th><th>Updated</th></tr></thead><tbody>${state.issues.map((issue) => `<tr><td><div class="table-title"><span class="type-icon type-${esc(issue.type)}">${icon(issue.type)}</span><div><span class="mono muted">${esc(issue.id)}</span>${issueLink(issue.id, issue.title)}${issue.parent_id ? `<small class="muted">↳ ${esc(issue.parent_id)}</small>` : ""}</div></div></td><td>${statusBadge(issue.status)}</td><td>${priorityBadge(issue.priority)}</td><td>${reviews ? esc(sessionName(state.sessions, issue.implementer_session)) : `<div class="labels">${issue.labels.map((l) => `<span class="label">${esc(l)}</span>`).join("")}</div>`}</td><td class="muted nowrap" title="${esc(timestamp(issue.updated_at))}">${relative(issue.updated_at)}</td></tr>`).join("")}</tbody></table>`;
}

function activityMessage(item) {
  const message = item.message || item.action || item.type;
  return (
    {
      board_create: "Created a board",
      board_update: "Updated a board",
      board_delete: "Deleted a board",
      board_set_position: "Reordered a task",
      board_unposition: "Reset task order",
    }[message] || message
  );
}

function activityView(state) {
  const sessions = state.sessions
    .filter((s) => s.agent_type !== "web")
    .sort((a, b) => b.last_activity.localeCompare(a.last_activity));
  const events = state.monitor?.activity || [];
  return `<div class="activity-layout"><div class="activity-feed"><h2>Recent activity <span class="muted">Last 24 hours</span></h2>${events.length ? events.map((item) => `<article class="activity-item"><span class="event-mark">${icon(item.type === "handoff" ? "reviews" : "activity")}</span><div><div class="activity-byline"><strong>${esc(sessionName(state.sessions, item.session_id))}</strong><time title="${esc(timestamp(item.timestamp))}">${relative(item.timestamp)}</time></div><p>${esc(activityMessage(item))}</p>${item.issue_id?.startsWith("td-") ? issueLink(item.issue_id, item.issue_title || item.issue_id) : ""}${item.log_type ? `<span class="label">${esc(item.log_type)}</span>` : ""}</div></article>`).join("") : empty("A quiet workspace", "Progress logs, handoffs, and task changes will appear here.", false)}</div><aside class="sessions-panel"><h2>Agent sessions <span class="count">${sessions.length}</span></h2><p class="muted">Last recorded activity, not process health.</p>${sessions.length ? sessions.map((s) => `<article class="session-card"><div class="session-avatar">${esc((s.agent_type || s.name || "A")[0].toUpperCase())}</div><div><strong>${esc(s.name || s.agent_type || s.id)}</strong><span class="mono muted">${esc(s.id)}</span><span>${esc(s.branch || "No branch")}</span><span class="muted" title="${esc(timestamp(s.last_activity))}">Seen ${relative(s.last_activity)}</span></div></article>`).join("") : '<p class="muted">No agent sessions recorded.</p>'}</aside></div>`;
}

export function renderWorkspace(state) {
  navigation(state);
  const content = document.querySelector("#content");
  const focused = content.contains(document.activeElement)
    ? document.activeElement
    : null;
  const focusedID = focused?.dataset.id;
  content.innerHTML =
    state.view === "board"
      ? boardView(state)
      : state.view === "activity"
        ? activityView(state)
        : listView(state, state.view === "reviews");
  if (focusedID)
    content
      .querySelector(`[data-action="task"][data-id="${CSS.escape(focusedID)}"]`)
      ?.focus({ preventScroll: true });
  document.querySelector("#result-count").textContent =
    state.view === "activity"
      ? `${state.monitor?.activity?.length || 0} recent events`
      : `${state.issues.length} tasks · ${state.view === "board" ? "Drag to organize" : "Select a task to view details"}`;
}
