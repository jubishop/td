import { api, markdown } from "./api.js";
import {
  esc,
  icon,
  statusBadge,
  priorityBadge,
  relative,
  timestamp,
  issueLink,
  sessionName,
  options,
  types,
  priorities,
  editableFields,
  changedFields,
  normalized,
  formValues,
  toast,
} from "./ui.js";

export function createPanels(state, refresh) {
  const drawer = document.querySelector("#drawer");
  const modal = document.querySelector("#modal");
  let request = 0;
  let modalSubmit;

  function dirty() {
    const form = drawer.querySelector("#issue-form");
    return (
      Boolean(
        form &&
        Object.keys(changedFields(state.editor.original, formValues(form)))
          .length,
      ) ||
      Boolean(drawer.querySelector('[name="comment"]')?.value.trim()) ||
      Boolean(drawer.querySelector('[name="depends_on"]')?.value.trim())
    );
  }
  function mayLeave() {
    return !dirty() || confirm("Discard your unsaved draft?");
  }
  function close() {
    if (!mayLeave()) return false;
    request++;
    state.detail = null;
    state.editor = null;
    drawer.hidden = true;
    drawer.innerHTML = "";
    document.body.classList.remove("drawer-open");
    return true;
  }
  function showDrawer(html) {
    drawer.innerHTML = html;
    drawer.hidden = false;
    document.body.classList.add("drawer-open");
  }
  function header(title, buttons = "") {
    return `<header class="drawer-header"><span class="mono muted">${esc(title)}</span><div>${buttons}<button class="icon-button" data-action="close-task" aria-label="Close task details">×</button></div></header>`;
  }
  function showModal(title, contents, onSubmit, submitLabel = "Save") {
    modalSubmit = onSubmit;
    modal.innerHTML = `<form id="modal-form"><header class="modal-header"><h2 id="modal-title">${esc(title)}</h2><button type="button" class="icon-button" data-action="close-modal" aria-label="Close dialog">×</button></header><div class="modal-body">${contents}<p id="modal-error" class="form-error" role="alert" hidden></p></div><footer class="modal-footer"><button type="button" class="quiet" data-action="close-modal">Cancel</button>${onSubmit ? `<button class="primary" type="submit">${esc(submitLabel)}</button>` : ""}</footer></form>`;
    if (!modal.open) modal.showModal();
  }

  async function open(id, { quiet = false } = {}) {
    if (quiet && state.detail?.issue.id !== id) return false;
    if (!quiet && !mayLeave()) return false;
    const serial = ++request;
    if (!quiet) {
      state.editor = null;
      state.detail = null;
      showDrawer(
        header(id) +
          '<div class="detail-body"><p class="muted">Loading task…</p></div>',
      );
    }
    const data = await api(`/issues/${encodeURIComponent(id)}`);
    if (serial !== request) return false;
    if (quiet && (state.editor || dirty())) {
      const warning = drawer.querySelector("#draft-warning");
      if (warning && data.issue.revision !== state.editor?.original.revision)
        warning.hidden = false;
      return true;
    }
    const [description, acceptance] = await Promise.all([
      markdown(data.issue.description),
      markdown(data.issue.acceptance),
    ]);
    if (serial !== request) return false;
    if (quiet && (state.editor || dirty())) return true;
    const oldScroll = drawer.querySelector(".detail-body")?.scrollTop || 0;
    state.detail = data;
    renderDetail(data, description, acceptance);
    if (quiet) drawer.querySelector(".detail-body").scrollTop = oldScroll;
    else
      drawer
        .querySelector('[data-action="close-task"]')
        .focus({ preventScroll: true });
    return true;
  }

  function renderDetail(data, description, acceptance) {
    const issue = data.issue;
    const actions = (
      {
        open: [
          ["start", "Start work"],
          ["review", "Submit for review"],
          ["block", "Block"],
        ],
        in_progress: [
          ["review", "Submit for review"],
          ["block", "Block"],
        ],
        blocked: [["unblock", "Unblock"]],
        in_review: [
          ["approve", "Approve"],
          ["reject", "Reject"],
        ],
        closed: [["reopen", "Reopen"]],
      }[issue.status] || []
    ).filter(
      ([action]) =>
        !issue.available_transitions ||
        issue.available_transitions.includes(action),
    );
    const handoff = data.latest_handoff;
    const children = data.children || [];
    showDrawer(
      header(
        issue.id,
        '<button class="quiet" data-action="edit-task">Edit</button>',
      ) +
        `<div class="detail-body">
      <div class="detail-badges">${statusBadge(issue.status)}${priorityBadge(issue.priority)}<span class="label">${esc(issue.type)}</span></div>
      <h2 class="task-heading">${esc(issue.title)}</h2>
      <div class="detail-actions">${actions.map(([action, label]) => `<button class="${action === "approve" ? "primary" : action === "reject" ? "danger-outline" : "secondary"}" data-action="transition" data-transition="${action}">${label}</button>`).join("")}</div>
      <dl class="task-properties"><dt>Implementer</dt><dd>${esc(sessionName(state.sessions, issue.implementer_session))}</dd><dt>Parent</dt><dd>${issue.parent_id ? issueLink(issue.parent_id) : "—"}</dd><dt>Labels</dt><dd>${issue.labels.length ? issue.labels.map((l) => `<span class="label">${esc(l)}</span>`).join(" ") : "—"}</dd><dt>Updated</dt><dd title="${esc(timestamp(issue.updated_at))}">${relative(issue.updated_at)}</dd>${issue.due_date ? `<dt>Due</dt><dd>${esc(issue.due_date)}</dd>` : ""}${issue.defer_until ? `<dt>Deferred until</dt><dd>${esc(issue.defer_until)}</dd>` : ""}${issue.sprint ? `<dt>Sprint</dt><dd>${esc(issue.sprint)}</dd>` : ""}${issue.points ? `<dt>Points</dt><dd>${issue.points}</dd>` : ""}${issue.minor ? "<dt>Review policy</dt><dd>Minor task</dd>" : ""}</dl>
      <section class="detail-section"><h3>Description</h3><div class="markdown">${description}</div></section>
      <section class="detail-section"><h3>Acceptance criteria</h3><div class="markdown">${acceptance}</div></section>
      ${issue.active_review ? `<section class="detail-section"><h3>Recorded approval</h3><p>${esc(issue.active_review.reviewed_by || sessionName(state.sessions, issue.active_review.reviewer_session))}</p><p class="preserve-lines">${esc(issue.active_review.summary)}</p></section>` : ""}
      ${children.length ? `<section class="detail-section"><h3>Child tasks <span class="count">${children.length}</span></h3>${children.map((c) => `<div class="relation-row">${statusBadge(c.status)}${issueLink(c.id, c.title)}</div>`).join("")}</section>` : ""}
      <section class="detail-section handoff"><h3>Latest handoff ${handoff ? `<span class="muted">${relative(handoff.timestamp)}</span>` : ""}</h3>${
        handoff
          ? `<p class="muted">${esc(sessionName(state.sessions, handoff.session_id))}</p>${[
              ["done", "Done"],
              ["remaining", "Remaining"],
              ["decisions", "Decisions"],
              ["uncertain", "Uncertain"],
            ]
              .map(
                ([key, label]) =>
                  `<h4>${label}</h4>${handoff[key]?.length ? `<ul>${handoff[key].map((item) => `<li>${esc(item)}</li>`).join("")}</ul>` : '<p class="muted">None recorded.</p>'}`,
              )
              .join("")}`
          : '<p class="muted">No handoff recorded yet.</p>'
      }</section>
      <section class="detail-section"><h3>Dependencies</h3><p class="small muted">This task depends on</p>${data.dependencies.length ? data.dependencies.map((dep) => `<div class="relation-row">${issueLink(dep.depends_on_id, state.issues.find((i) => i.id === dep.depends_on_id)?.title || dep.depends_on_id)}<button class="icon-button" data-action="remove-dependency" data-id="${esc(dep.dep_id)}" aria-label="Remove dependency ${esc(dep.depends_on_id)}">×</button></div>`).join("") : '<p class="muted">No dependencies.</p>'}<form id="dependency-form" class="inline-form"><input name="depends_on" placeholder="Find a task or enter its ID" aria-label="Dependency task" list="dependency-options" data-issue-search="dependency-options" required autocomplete="off"><datalist id="dependency-options"></datalist><button class="secondary">Add</button></form>${data.blocked_by.length ? `<h4>Tasks that depend on this</h4>${data.blocked_by.map((dep) => `<div class="relation-row">${issueLink(dep.issue_id)}</div>`).join("")}` : ""}</section>
      <section class="detail-section"><h3>Comments <span class="count">${data.comments.length}</span></h3>${data.comments.map((c) => `<article class="comment"><div class="activity-byline"><strong>${esc(sessionName(state.sessions, c.session_id))}</strong><time>${relative(c.created_at)}</time></div><p class="preserve-lines">${esc(c.text)}</p></article>`).join("")}<form id="comment-form"><label for="comment-text" class="sr-only">Add a comment</label><textarea id="comment-text" name="comment" rows="3" placeholder="Add context or review feedback…" required></textarea><button class="secondary">Add comment</button></form></section>
      <section class="detail-section"><h3>Task log <span class="count">${data.logs.length}</span></h3>${data.logs.length ? data.logs.map((log) => `<article class="log-item"><div class="activity-byline"><span class="label">${esc(log.type)}</span><time title="${esc(timestamp(log.timestamp))}">${relative(log.timestamp)}</time></div><p class="preserve-lines">${esc(log.message)}</p><small class="muted">${esc(sessionName(state.sessions, log.session_id))}</small></article>`).join("") : '<p class="muted">No log entries yet.</p>'}</section>
      <div class="detail-admin">${issue.available_transitions?.includes("close") ? '<button class="quiet" data-action="transition" data-transition="close">Close without review…</button>' : ""}<button class="quiet danger-text" data-action="delete-task">Delete task…</button></div>
      <p id="detail-error" class="form-error" role="alert" hidden></p>
    </div>`,
    );
  }

  function editor(original, values = original, note = "") {
    request++;
    state.editor = { original };
    showDrawer(
      header(original.id || "NEW TASK") +
        `<form id="issue-form" class="editor-form"><div class="detail-body"><h2>${original.id ? "Edit task" : "Create a task"}</h2>${note ? `<p class="notice">${esc(note)}</p>` : ""}<p id="draft-warning" class="notice" hidden>This task has changed. Your draft is preserved. Saving will open a comparison.</p>
      <label>Title<input name="title" value="${esc(values.title)}" minlength="${state.project.title_min_length}" maxlength="${state.project.title_max_length}" required autofocus><span class="field-hint">${state.project.title_min_length}–${state.project.title_max_length} characters</span></label>
      <div class="form-grid"><label>Type<select name="type">${options(types, values.type)}</select></label><label>Priority<select name="priority">${options(priorities, values.priority)}</select></label></div>
      ${["description", "acceptance"].map((key) => `<div class="markdown-editor"><div class="editor-label"><label for="edit-${key}">${key === "description" ? "Description" : "Acceptance criteria"}</label><button type="button" class="quiet" data-action="preview" data-field="${key}">Preview</button></div><textarea id="edit-${key}" name="${key}" rows="${key === "description" ? 8 : 5}" placeholder="Write Markdown…">${esc(values[key])}</textarea><div id="preview-${key}" class="markdown markdown-preview" hidden></div></div>`).join("")}
      <label>Labels<span class="field-hint">Separate labels with commas</span><input name="labels" value="${esc((values.labels || []).join(", "))}" placeholder="frontend, release"></label>
      <label>Parent task / epic<input name="parent_id" value="${esc(values.parent_id)}" placeholder="Find a task or enter its ID" data-issue-search="parent-options" list="parent-options" autocomplete="off"><datalist id="parent-options"></datalist></label>
      <details class="advanced"><summary>More fields</summary><div class="form-grid"><label>Points<select name="points">${options(["0", "1", "2", "3", "5", "8", "13", "21"], String(values.points || 0))}</select></label><label>Sprint<input name="sprint" value="${esc(values.sprint)}"></label><label>Due date<input name="due_date" type="date" value="${esc(values.due_date)}"></label><label>Defer until<input name="defer_until" type="date" value="${esc(values.defer_until)}"></label></div><label class="check-label"><input name="minor" type="checkbox" ${values.minor ? "checked" : ""}>Minor task</label></details>
      <p id="editor-error" class="form-error" role="alert" hidden></p></div><footer class="editor-footer"><span class="muted small">Markdown supported</span><button class="primary" type="submit">${original.id ? "Save changes" : "Create task"}</button></footer></form>`,
    );
    drawer.querySelector('[name="title"]').focus();
  }

  function newTask() {
    if (!mayLeave()) return false;
    request++;
    state.detail = null;
    editor({
      title: "",
      description: "",
      acceptance: "",
      type: "task",
      priority: "P2",
      labels: [],
      parent_id: "",
      points: 0,
      sprint: "",
      minor: false,
      due_date: "",
      defer_until: "",
    });
    return true;
  }

  async function conflict(editing, draft) {
    const original = editing.original;
    const current = (await api(`/issues/${original.id}`)).issue;
    if (state.editor !== editing) return;
    const fields = editableFields.filter(
      (key) =>
        JSON.stringify(normalized(original[key])) !==
          JSON.stringify(normalized(current[key])) ||
        JSON.stringify(normalized(original[key])) !==
          JSON.stringify(normalized(draft[key])),
    );
    const display = (value) =>
      esc(
        Array.isArray(value)
          ? value.join(", ")
          : typeof value === "boolean"
            ? String(value)
            : value || "(empty)",
      );
    showModal(
      "This task changed",
      `<p>The saved version stays in place. Your draft has not been applied. Compare the changes and choose values to review before saving again.</p><p class="muted">Saved ${esc(timestamp(current.updated_at))} · ${statusBadge(current.status)}${original.status !== current.status ? ` (was ${esc(original.status.replaceAll("_", " "))})` : ""}</p><div class="conflicts">${fields
        .map((key) => {
          const savedChanged =
            JSON.stringify(normalized(original[key])) !==
            JSON.stringify(normalized(current[key]));
          return `<section class="conflict-field"><h3>${esc(key.replaceAll("_", " "))}</h3><div class="conflict-values"><div><h4>When opened</h4><pre>${display(original[key])}</pre></div><div><h4>Saved now</h4><pre>${display(current[key])}</pre></div><div><h4>Your draft</h4><pre>${display(draft[key])}</pre></div></div><label>Use<select name="${key}" aria-label="Resolution for ${key}"><option value="saved" ${savedChanged ? "selected" : ""}>Saved value</option><option value="draft" ${savedChanged ? "" : "selected"}>Draft value</option></select></label></section>`;
        })
        .join("")}</div>`,
      async (form) => {
        const resolved = { ...current };
        for (const key of fields)
          if (form.elements[key].value === "draft") resolved[key] = draft[key];
        state.detail.issue = current;
        editor(
          current,
          resolved,
          "The saved version is unchanged. Review these selected values, then save when ready.",
        );
      },
      "Review selected values",
    );
  }

  async function saveIssue(form) {
    const draft = formValues(form);
    const editing = state.editor;
    const original = editing.original;
    const changes = changedFields(original, draft);
    if (original.id && !Object.keys(changes).length) {
      state.editor = null;
      drawer.innerHTML = "";
      await open(original.id);
      toast("No changes to save");
      return;
    }
    try {
      const data = await api(
        original.id ? `/issues/${original.id}` : "/issues",
        {
          method: original.id ? "PATCH" : "POST",
          body: original.id ? changes : draft,
          revision: original.revision,
        },
      );
      if (state.editor === editing) {
        state.editor = null;
        drawer.innerHTML = "";
        history.replaceState(null, "", `#${state.view}?issue=${data.issue.id}`);
        await open(data.issue.id);
      }
      await refresh();
      toast(original.id ? "Task updated" : "Task created");
    } catch (error) {
      if (error.status === 409 && original.id) await conflict(editing, draft);
      else throw error;
    }
  }

  function transition(action) {
    const issue = state.detail.issue;
    const labels = {
      start: "Start work",
      review: "Submit for review",
      block: "Block task",
      unblock: "Unblock task",
      approve: issue.active_review ? "Close reviewed task" : "Approve task",
      reject: "Reject task",
      reopen: "Reopen task",
      close: "Close without review",
    };
    showModal(
      labels[action],
      `<p><strong>${esc(issue.title)}</strong></p><p class="muted">${action === "close" ? "Use this for duplicates, cancellations, or administrative cleanup." : action === "approve" ? (issue.active_review ? "Close this task using its recorded approval." : "Approval closes this task and records who reviewed it.") : action === "reject" ? "Rejection returns the task to Open for rework." : "This action is recorded in the task log."}</p>${action === "approve" && !issue.active_review ? '<label>Review attribution<select name="review_method"><option value="independent">I reviewed someone else’s work</option><option value="self">I reviewed my own work</option><option value="attributed">Another reviewer performed the review</option></select></label><label>Other reviewer’s name<input name="reviewed_by" placeholder="Only when another reviewer performed the review"></label>' : ""}<label>${["approve", "reject", "close", "block"].includes(action) ? "Reason" : "Note (optional)"}<textarea name="reason" rows="4" ${["approve", "reject", "close", "block"].includes(action) ? "required" : ""}></textarea></label>`,
      async (form) => {
        const body = { reason: form.elements.reason.value };
        const method = form.elements.review_method?.value;
        if (method === "self") body.self_review = true;
        if (method === "attributed") {
          body.reviewed_by = form.elements.reviewed_by.value.trim();
          if (!body.reviewed_by)
            throw new Error(
              "Enter the name of the reviewer who performed the review.",
            );
        }
        await api(`/issues/${issue.id}/${action}`, {
          method: "POST",
          body,
          revision: issue.revision,
        });
        await open(issue.id, { quiet: true });
        await refresh();
        toast(labels[action]);
      },
      labels[action],
    );
  }

  function boardForm(board) {
    showModal(
      board ? "Edit board" : "Create a saved board",
      `<label>Name<input name="name" value="${esc(board?.name)}" required></label><label>TDQ query<span class="field-hint">Leave empty to include all tasks. Example: priority &lt;= P1</span><textarea name="query" rows="4" placeholder="type = feature AND status != closed">${esc(board?.query)}</textarea></label>${board ? '<button type="button" class="quiet danger-text" data-action="delete-board">Delete board…</button>' : ""}`,
      async (form) => {
        const data = await api(board ? `/boards/${board.id}` : "/boards", {
          method: board ? "PATCH" : "POST",
          body: {
            name: form.elements.name.value,
            query: form.elements.query.value,
          },
        });
        state.boardID = data.board.id;
        state.view = "board";
        await refresh();
        location.hash = "board";
        toast(board ? "Board updated" : "Board created");
      },
    );
  }

  async function handleAction(action, target) {
    if (action === "edit-task") {
      if (mayLeave()) editor(state.detail.issue);
    } else if (action === "preview") {
      const field = target.dataset.field;
      const preview = drawer.querySelector(`#preview-${field}`);
      const textarea = drawer.querySelector(`[name="${field}"]`);
      if (preview.hidden) {
        preview.innerHTML = await markdown(textarea.value);
        preview.hidden = false;
        textarea.hidden = true;
        target.textContent = "Write";
      } else {
        preview.hidden = true;
        textarea.hidden = false;
        target.textContent = "Preview";
        textarea.focus();
      }
    } else if (action === "transition") transition(target.dataset.transition);
    else if (action === "new-board") boardForm();
    else if (action === "edit-board")
      boardForm(state.boards.find((b) => b.id === state.boardID));
    else if (action === "delete-board") {
      const board = state.boards.find((b) => b.id === state.boardID);
      showModal(
        "Delete board",
        `<p>Delete <strong>${esc(board.name)}</strong>? The tasks remain in the project.</p>`,
        async () => {
          await api(`/boards/${board.id}`, { method: "DELETE" });
          state.boardID = "";
          await refresh();
          toast("Board deleted");
        },
        "Delete board",
      );
    } else if (action === "delete-task") {
      const issue = state.detail.issue;
      showModal(
        "Delete task",
        `<p>Delete <strong>${esc(issue.title)}</strong>? This uses td’s soft delete and is recorded in the action log.</p>`,
        async () => {
          await api(`/issues/${issue.id}`, {
            method: "DELETE",
            revision: issue.revision,
          });
          state.editor = null;
          close();
          location.hash = state.view;
          await refresh();
          toast("Task deleted");
        },
        "Delete task",
      );
    } else if (action === "remove-dependency") {
      await api(
        `/issues/${state.detail.issue.id}/dependencies/${encodeURIComponent(target.dataset.id)}`,
        { method: "DELETE" },
      );
      await open(state.detail.issue.id, { quiet: true });
      await refresh();
      toast("Dependency removed");
    } else if (action === "close-modal") modal.close();
    else if (action === "shortcuts")
      showModal(
        "Keyboard shortcuts",
        '<dl class="shortcuts"><dt>N</dt><dd>New task</dd><dt>/</dt><dd>Focus search</dd><dt>1 / 2 / 3 / 4</dt><dd>Board / List / Reviews / Activity</dd><dt>J / K</dt><dd>Next / previous task</dd><dt>Enter</dt><dd>Open focused task</dd><dt>Esc</dt><dd>Close panel or dialog</dd><dt>?</dt><dd>Show shortcuts</dd></dl>',
      );
  }

  async function submit(form) {
    if (form.id === "issue-form") return saveIssue(form);
    if (form.id === "modal-form") {
      if (modalSubmit) {
        await modalSubmit(form);
        modal.close();
      }
      return;
    }
    const id = state.detail?.issue.id;
    if (form.id === "comment-form") {
      await api(`/issues/${id}/comments`, {
        method: "POST",
        body: { text: form.elements.comment.value },
      });
      form.reset();
      await open(id, { quiet: true });
      await refresh();
      toast("Comment added");
    } else if (form.id === "dependency-form") {
      await api(`/issues/${id}/dependencies`, {
        method: "POST",
        body: { depends_on: form.elements.depends_on.value.trim() },
      });
      form.reset();
      await open(id, { quiet: true });
      await refresh();
      toast("Dependency added");
    }
  }

  return { open, close, dirty, mayLeave, newTask, handleAction, submit };
}
