export async function api(
  path,
  { method = "GET", body, revision, signal } = {},
) {
  const headers = { Accept: "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (revision) headers["If-Match"] = `"${revision}"`;
  const response = await fetch(`/v1${path}`, {
    method,
    headers,
    signal,
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const payload = await response.json();
  if (!response.ok || !payload.ok) {
    const fields = payload.error?.details?.fields
      ?.map((f) => f.message)
      .join("; ");
    const error = new Error(
      fields || payload.error?.message || `Request failed (${response.status})`,
    );
    error.status = response.status;
    throw error;
  }
  return payload.data;
}

export async function allIssues(params = new URLSearchParams(), signal) {
  const result = [];
  const query = new URLSearchParams(params);
  query.set("limit", "200");
  for (let offset = 0; ; offset += 200) {
    query.set("offset", String(offset));
    const page = await api(`/issues?${query}`, { signal });
    result.push(...page.issues);
    if (!page.has_more) return result;
  }
}

const markdownCache = new Map();
export async function markdown(text) {
  if (!text) return '<p class="muted">Not provided.</p>';
  if (markdownCache.has(text)) return markdownCache.get(text);
  const data = await api("/markdown", { method: "POST", body: { text } });
  if (markdownCache.size > 100) markdownCache.clear();
  markdownCache.set(text, data.html);
  return data.html;
}
