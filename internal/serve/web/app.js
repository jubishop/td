import { api, allIssues } from "./api.js";
import { esc, dropAction, toast } from "./ui.js";
import { renderWorkspace } from "./workspace.js";
import { createPanels } from "./details.js";

const state = {
  view: "board",
  boardID: "",
  boards: [],
  issues: [],
  boardIssues: [],
  sessions: [],
  reviewIssues: [],
  monitor: null,
  detail: null,
  editor: null,
  filters: {
    search: "",
    search_mode: "text",
    type: "",
    priority: "",
    include_closed: false,
  },
};
const panels = createPanels(state, refresh);
let projectKey = "";
let refreshID = 0;
let refreshController;
let filterTimer;
let dragID;
let busy = false;
let queuedRefresh = false;

function setting(key, value) {
  try {
    if (value === undefined) return localStorage.getItem(key);
    localStorage.setItem(key, value);
  } catch {
    /* Storage may be disabled by the browser. */
  }
}
function saveSettings() {
  if (projectKey)
    setting(
      projectKey,
      JSON.stringify({
        boardID: state.boardID,
        view: state.view,
        filters: state.filters,
      }),
    );
}
function setTheme(theme) {
  document.documentElement.dataset.theme = theme;
  document.querySelector("#theme-button").textContent = `Theme: ${theme}`;
  setting("td.browser.theme", theme);
}
setTheme(setting("td.browser.theme") || "system");

function showError(error, form) {
  if (error.name === "AbortError") return;
  const local =
    form?.querySelector(".form-error") ||
    document.querySelector("#drawer:not([hidden]) #detail-error");
  const node = local || document.querySelector("#error-banner");
  node.textContent =
    error.message ||
    "Could not connect to td. Check the terminal and try again.";
  node.hidden = false;
  if (local) local.scrollIntoView({ block: "nearest" });
}
function connection(connected) {
  const node = document.querySelector("#connection");
  node.classList.toggle("offline", !connected);
  node.innerHTML = `<span class="connection-dot"></span>${connected ? "Live · local connection" : "Disconnected · reconnecting"}`;
}
function filterParams(filters) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filters))
    if (value !== "" && value !== false) params.set(key, String(value));
  return params;
}

async function refresh() {
  if (dragID || busy) {
    queuedRefresh = true;
    return;
  }
  const generation = ++refreshID;
  refreshController?.abort();
  refreshController = new AbortController();
  const signal = refreshController.signal;
  try {
    const [{ boards }, { monitor }, { sessions }, reviews] = await Promise.all([
      api("/boards", { signal }),
      api("/monitor", { signal }),
      api("/sessions", { signal }),
      allIssues(new URLSearchParams({ status: "in_review" }), signal),
    ]);
    if (generation !== refreshID) return;
    if (!boards.some((b) => b.id === state.boardID))
      state.boardID = (boards.find((b) => b.is_builtin) || boards[0])?.id || "";
    let issues = [];
    let boardIssues = [];
    const params = filterParams(state.filters);
    if (state.view === "board" && state.boardID) {
      const data = await api(
        `/boards/${state.boardID}?include_closed=${state.filters.include_closed}`,
        { signal },
      );
      boardIssues = data.issues;
      issues = boardIssues.map((row) => row.issue);
      if (state.filters.search) {
        const matches = new Set(
          (await allIssues(params, signal)).map((i) => i.id),
        );
        const search = state.filters.search.toLowerCase();
        issues = issues.filter(
          (i) =>
            matches.has(i.id) ||
            (state.filters.search_mode === "text" &&
              i.labels.join(" ").toLowerCase().includes(search)),
        );
      }
      if (state.filters.type)
        issues = issues.filter((i) => i.type === state.filters.type);
      if (state.filters.priority)
        issues = issues.filter((i) => i.priority === state.filters.priority);
    } else if (state.view !== "activity") {
      if (state.view === "reviews") params.set("status", "in_review");
      issues = await allIssues(params, signal);
    }
    if (generation !== refreshID) return;
    Object.assign(state, {
      boards,
      monitor,
      sessions,
      reviewIssues: reviews,
      issues,
      boardIssues,
    });
    document.querySelector("#error-banner").hidden = true;
    renderWorkspace(state);
    saveSettings();
    if (state.detail) {
      try {
        await panels.open(state.detail.issue.id, { quiet: true });
      } catch (error) {
        showError(error);
      }
    }
  } catch (error) {
    if (generation === refreshID) showError(error);
  }
}

async function route() {
  const [view, query] = location.hash.slice(1).split("?");
  if (["board", "list", "reviews", "activity"].includes(view))
    state.view = view;
  const id = new URLSearchParams(query).get("issue");
  let routeError;
  if (id && state.detail?.issue.id !== id) {
    try {
      const opened = await panels.open(id);
      if (!opened) return;
    } catch (error) {
      panels.close();
      history.replaceState(null, "", `#${state.view}`);
      routeError = error;
    }
  } else if (!id && state.detail && !state.editor) panels.close();
  await refresh();
  if (routeError) toast(routeError.message);
}

async function action(name, target) {
  if (name === "view" || name === "board") {
    if (!panels.close()) return;
    if (name === "board") state.boardID = target.dataset.id;
    state.view = name === "board" ? "board" : target.dataset.view;
    history.replaceState(null, "", `#${state.view}`);
    await refresh();
  } else if (name === "task") {
    const opened = await panels.open(target.dataset.id);
    if (opened)
      history.pushState(null, "", `#${state.view}?issue=${target.dataset.id}`);
  } else if (name === "close-task") {
    if (panels.close()) {
      history.replaceState(null, "", `#${state.view}`);
      document.querySelector("#content").focus();
    }
  } else if (name === "new-task") {
    if (panels.newTask()) history.replaceState(null, "", `#${state.view}`);
  } else if (name === "refresh") await refresh();
  else if (name === "theme") {
    const themes = ["system", "light", "dark"];
    setTheme(
      themes[
        (themes.indexOf(document.documentElement.dataset.theme) + 1) %
          themes.length
      ],
    );
  } else await panels.handleAction(name, target);
}

document.addEventListener("click", async (event) => {
  const target = event.target.closest("[data-action]");
  if (!target || target.disabled) return;
  event.preventDefault();
  target.disabled = true;
  try {
    await action(target.dataset.action, target);
  } catch (error) {
    showError(error);
  } finally {
    target.disabled = false;
  }
});

document.addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.target;
  if (form.id === "filters") {
    readFilters();
    return;
  }
  const button =
    form.querySelector('[type="submit"]') || form.querySelector("button");
  if (button?.disabled) return;
  if (button) button.disabled = true;
  form.inert = true;
  form.querySelectorAll(".form-error").forEach((node) => {
    node.hidden = true;
  });
  try {
    await panels.submit(form);
  } catch (error) {
    showError(error, form);
  } finally {
    form.inert = false;
    if (button) button.disabled = false;
  }
});

function readFilters() {
  const form = document.querySelector("#filters");
  state.filters = {
    ...Object.fromEntries(new FormData(form)),
    include_closed: form.elements.include_closed.checked,
  };
  refresh();
}
document.querySelector("#filters").addEventListener("change", readFilters);
document.querySelector("#search").addEventListener("input", () => {
  clearTimeout(filterTimer);
  filterTimer = setTimeout(readFilters, 300);
});

let lookupTimer;
document.addEventListener("input", (event) => {
  const input = event.target;
  if (!input.dataset.issueSearch) return;
  clearTimeout(lookupTimer);
  const text = input.value;
  lookupTimer = setTimeout(async () => {
    try {
      const params = new URLSearchParams({
        search: text,
        search_mode: "text",
        include_closed: "true",
        limit: "20",
      });
      const { issues } = await api(`/issues?${params}`);
      if (!input.isConnected || input.value !== text) return;
      document.getElementById(input.dataset.issueSearch).innerHTML = issues
        .filter((i) => i.id !== state.detail?.issue.id)
        .map((i) => `<option value="${esc(i.id)}">${esc(i.title)}</option>`)
        .join("");
    } catch (error) {
      showError(error);
    }
  }, 200);
});

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !document.querySelector("#modal").open) {
    event.preventDefault();
    action("close-task", {});
    return;
  }
  if (
    event.ctrlKey ||
    event.metaKey ||
    event.altKey ||
    event.target.closest('input, textarea, select, [contenteditable="true"]') ||
    document.querySelector("#modal").open
  )
    return;
  const key = event.key.toLowerCase();
  if (key === "/") {
    event.preventDefault();
    document.querySelector("#search").focus();
  } else if (key === "n") action("new-task", {}).catch(showError);
  else if (key === "?") panels.handleAction("shortcuts").catch(showError);
  else if ("1234".includes(key) && key.length === 1)
    action("view", {
      dataset: {
        view: ["board", "list", "reviews", "activity"][Number(key) - 1],
      },
    }).catch(showError);
  else if (key === "j" || key === "k") {
    event.preventDefault();
    const buttons = [
      ...document.querySelectorAll('#content [data-action="task"]'),
    ];
    const index = buttons.indexOf(document.activeElement);
    buttons[
      Math.max(0, Math.min(buttons.length - 1, index + (key === "j" ? 1 : -1)))
    ]?.focus();
  }
});
window.addEventListener("beforeunload", (event) => {
  if (panels.dirty()) event.preventDefault();
});
window.addEventListener("hashchange", () => route().catch(showError));

document.addEventListener("dragstart", (event) => {
  const card = event.target.closest(".task-card");
  if (!card || busy) return;
  dragID = card.dataset.id;
  event.dataTransfer.setData("text/plain", dragID);
  event.dataTransfer.effectAllowed = "move";
  card.classList.add("dragging");
});
function clearDrop() {
  document
    .querySelectorAll(".drop-before, .drop-after, .drop-target")
    .forEach((node) =>
      node.classList.remove("drop-before", "drop-after", "drop-target"),
    );
}
document.addEventListener("dragover", (event) => {
  const column = event.target.closest(".column");
  if (!dragID || !column) return;
  event.preventDefault();
  event.dataTransfer.dropEffect = "move";
  clearDrop();
  const card = event.target.closest(".task-card");
  if (card)
    card.classList.add(
      event.clientY < card.getBoundingClientRect().top + card.offsetHeight / 2
        ? "drop-before"
        : "drop-after",
    );
  else column.classList.add("drop-target");
});
document.addEventListener("drop", async (event) => {
  const column = event.target.closest(".column");
  if (!column || !dragID) return;
  event.preventDefault();
  const id = dragID;
  const boardID = state.boardID;
  const issue = state.issues.find((i) => i.id === id);
  const card = event.target.closest(".task-card");
  const after = card?.classList.contains("drop-after");
  const targetID = card?.dataset.id;
  dragID = null;
  busy = true;
  clearDrop();
  let moveError;
  try {
    if (!issue || targetID === id) return;
    const toStatus = column.dataset.status;
    if (issue.status !== toStatus) {
      const transition = dropAction(issue.status, toStatus);
      if (!transition) {
        toast(
          "Use the task panel for this action. Approve and Reject are explicit review decisions.",
        );
        return;
      }
      await api(`/issues/${id}/${transition}`, {
        method: "POST",
        body: {},
        revision: issue.revision,
      });
    }
    const ordered = state.boardIssues
      .map((row) => row.issue.id)
      .filter((item) => item !== id);
    let beforeID = targetID;
    if (after && targetID)
      beforeID = ordered[ordered.indexOf(targetID) + 1] || "";
    if (!targetID) {
      const sameColumn = state.issues.filter(
        (i) => i.status === toStatus && i.id !== id,
      );
      const last = sameColumn.at(-1)?.id;
      beforeID = last ? ordered[ordered.indexOf(last) + 1] || "" : "";
    }
    await api(`/boards/${boardID}/move`, {
      method: "POST",
      body: { issue_id: id, before_id: beforeID || "" },
    });
    toast("Board updated");
  } catch (error) {
    moveError = error;
  } finally {
    busy = false;
    queuedRefresh = false;
    await refresh();
    if (moveError) showError(moveError);
  }
});
document.addEventListener("dragend", () => {
  dragID = null;
  clearDrop();
  document
    .querySelectorAll(".dragging")
    .forEach((node) => node.classList.remove("dragging"));
  if (queuedRefresh && !busy) {
    queuedRefresh = false;
    refresh();
  }
});

async function start() {
  const project = await api("/browser");
  state.project = project;
  projectKey = `td.browser:${project.path}`;
  document.title = `${project.name} · td`;
  document.querySelector("#project-name").textContent = project.name;
  document.querySelector("#project-name").title = project.path;
  document.querySelector("#project-initial").textContent =
    project.name[0].toUpperCase();
  document.querySelector("#breadcrumb-project").textContent = project.name;
  try {
    const saved = JSON.parse(setting(projectKey) || "{}");
    state.boardID = saved.boardID || "";
    if (["board", "list", "reviews", "activity"].includes(saved.view))
      state.view = saved.view;
    Object.assign(state.filters, saved.filters || {});
  } catch {
    /* Invalid local preferences do not block the workspace. */
  }
  const form = document.querySelector("#filters");
  for (const [key, value] of Object.entries(state.filters)) {
    if (key === "include_closed") form.elements[key].checked = value;
    else if (form.elements[key]) form.elements[key].value = value;
  }
  await route();
  const stream = new EventSource("/v1/events");
  let eventToken;
  let eventTimer;
  stream.onopen = () => {
    connection(true);
    refresh();
  };
  stream.onerror = () => connection(false);
  const onEvent = (event) => {
    const payload = JSON.parse(event.data);
    if (payload.change_token === eventToken) return;
    eventToken = payload.change_token;
    clearTimeout(eventTimer);
    eventTimer = setTimeout(refresh, 150);
  };
  stream.addEventListener("refresh", onEvent);
  stream.addEventListener("ping", onEvent);
}

start().catch(showError);
