export const statuses = {
  open: "Open",
  in_progress: "In progress",
  in_review: "In review",
  blocked: "Blocked",
  closed: "Closed",
};
export const types = ["task", "bug", "feature", "epic", "chore"];
export const priorities = ["P0", "P1", "P2", "P3", "P4"];
export const editableFields = [
  "title",
  "description",
  "acceptance",
  "type",
  "priority",
  "labels",
  "parent_id",
  "points",
  "sprint",
  "minor",
  "due_date",
  "defer_until",
];

export function esc(value = "") {
  return String(value ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
}
export function icon(name) {
  const paths = {
    board:
      '<rect x="3" y="4" width="5" height="16" rx="1"/><rect x="10" y="4" width="5" height="10" rx="1"/><rect x="17" y="4" width="4" height="13" rx="1"/>',
    list: '<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>',
    reviews:
      '<path d="m8 12 3 3 5-6"/><rect x="4" y="3" width="16" height="18" rx="3"/>',
    activity: '<path d="M2 12h5l3-8 4 16 3-8h5"/>',
    task: '<circle cx="12" cy="12" r="8"/>',
    bug: '<path d="M8 6h8v10a4 4 0 0 1-8 0ZM4 10h4m8 0h4M4 16h4m8 0h4M9 3l2 3m4-3-2 3"/>',
    epic: '<path d="m13 2-8 12h6l-1 8 9-13h-7Z"/>',
    feature: '<path d="m12 3 3 6 6 3-6 3-3 6-3-6-6-3 6-3Z"/>',
    chore: '<path d="m14 4 6 6-10 10-6-6ZM12 6l6 6"/>',
  };
  return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] || paths.task}</svg>`;
}
export function statusBadge(status) {
  return `<span class="status-badge status-${esc(status)}"><span class="status-dot"></span>${esc(statuses[status] || status)}</span>`;
}
export function priorityBadge(priority) {
  return `<span class="priority priority-${esc(priority)}" title="Priority ${esc(priority)}">${esc(priority)}</span>`;
}
export function relative(value) {
  if (!value) return "—";
  const seconds = Math.max(0, (Date.now() - new Date(value).getTime()) / 1000);
  if (seconds < 60) return "just now";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}
export function timestamp(value) {
  return value ? new Date(value).toLocaleString() : "—";
}
export function options(values, selected) {
  return values
    .map(
      (value) =>
        `<option value="${esc(value)}" ${value === selected ? "selected" : ""}>${esc(value)}</option>`,
    )
    .join("");
}
export function issueLink(id, title) {
  return `<button class="text-link" data-action="task" data-id="${esc(id)}">${esc(title || id)}</button>`;
}
export function sessionName(sessions, id) {
  const session = sessions.find((s) => s.id === id);
  return session?.name || session?.agent_type || id || "Unassigned";
}
export function formValues(form) {
  const values = Object.fromEntries(new FormData(form));
  values.labels = (values.labels || "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  values.points = Number(values.points || 0);
  values.minor = form.elements.minor.checked;
  return values;
}
export function normalized(value) {
  return value ?? "";
}
export function changedFields(original, draft) {
  return Object.fromEntries(
    editableFields
      .filter(
        (key) =>
          JSON.stringify(normalized(original[key])) !==
          JSON.stringify(normalized(draft[key])),
      )
      .map((key) => [key, draft[key]]),
  );
}

// Review outcomes are deliberately excluded from drag-and-drop.
export function dropAction(from, to) {
  return {
    "open:in_progress": "start",
    "open:in_review": "review",
    "open:blocked": "block",
    "in_progress:in_review": "review",
    "in_progress:blocked": "block",
    "blocked:open": "unblock",
    "closed:open": "reopen",
  }[`${from}:${to}`];
}

let toastTimer;
export function toast(message) {
  const node = document.querySelector("#toast");
  node.textContent = message;
  node.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    node.hidden = true;
  }, 5500);
}
