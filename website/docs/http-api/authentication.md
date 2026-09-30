---
sidebar_position: 3
---

# Authentication & CORS

By default, `td serve` runs in tokenless mode on localhost with no CORS restrictions. This is suitable for local development where the API consumer runs on the same machine.

## Bearer Token Authentication

Pass `--token` to require a bearer token on all requests:

```bash
td serve --token my-secret-token
```

Clients must include the token in the `Authorization` header:

```bash
curl -H "Authorization: Bearer my-secret-token" \
  http://localhost:54321/v1/issues
```

Requests without a valid token receive a `401 unauthorized` response:

```json
{
  "ok": false,
  "error": {
    "code": "unauthorized",
    "message": "missing authorization header"
  }
}
```

:::info
`GET /health` is always exempt from authentication, even when a token is configured. This allows discovery scripts to check server liveness without credentials.
:::

## CORS Configuration

Pass `--cors` to allow browser-based clients from a specific origin:

```bash
# Allow a specific origin
td serve --cors http://localhost:3000

# Allow any origin (development only)
td serve --cors "*"
```

When configured, the server sets these headers on matching requests:

| Header | Value |
|--------|-------|
| `Access-Control-Allow-Origin` | The requesting origin |
| `Access-Control-Allow-Methods` | `GET,POST,PATCH,PUT,DELETE,OPTIONS` |
| `Access-Control-Allow-Headers` | `Content-Type,Authorization,If-Match` |
| `Access-Control-Max-Age` | `3600` |

Preflight `OPTIONS` requests return `204 No Content` with the CORS headers.

When `--cors` is not set, no CORS headers are added. Requests without an `Origin` header are unaffected regardless of configuration.

## Combined Example

Running with both auth and CORS for a local React dev server:

```bash
td serve --port 8080 --token dev-token --cors http://localhost:3000
```

```javascript
// Frontend fetch example
const res = await fetch('http://localhost:8080/v1/issues', {
  headers: {
    'Authorization': 'Bearer dev-token',
    'Content-Type': 'application/json',
  },
});
const { ok, data } = await res.json();
```

## Same-Origin Proxy

For production setups, run `td serve` behind a reverse proxy on the same origin as your frontend. This avoids CORS entirely and keeps the token server-side:

```
Browser -> yourapp.localhost:3000/api/* -> td serve on localhost:54321
```

In this configuration, no `--cors` or `--token` flags are needed since the proxy handles authentication and the browser sees a same-origin request.
