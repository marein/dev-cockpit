import { errorText } from "@dc/toast";

// The per-session CSRF token, rendered once into <meta name="csrf-token"> in the
// page head. The server accepts it via the X-CSRF-Token header or the form field;
// JS POSTs go through the header, so callers never thread the token themselves.
let cachedToken;

export function csrfToken() {
  if (cachedToken === undefined) {
    const meta = document.querySelector('meta[name="csrf-token"]');
    cachedToken = meta ? meta.getAttribute("content") || "" : "";
  }
  return cachedToken;
}

export function csrfHeaders(extra = {}) {
  const token = csrfToken();
  if (token) {
    extra["X-CSRF-Token"] = token;
  }
  return extra;
}

// Reject with the server's message (JSON {error} or text body) whenever a
// response is not ok. Always call this before reading res.json(): error
// responses may be plain text (e.g. a 401 "session expired") and would
// otherwise throw a raw SyntaxError.
export async function ensureOk(response, fallback) {
  if (response.ok) {
    return response;
  }
  throw new Error(await errorText(response, fallback));
}

export function postForm(url, fields, { accept = "application/json" } = {}) {
  return fetch(url, {
    method: "POST",
    headers: csrfHeaders({ "Content-Type": "application/x-www-form-urlencoded", Accept: accept }),
    body: new URLSearchParams(fields).toString(),
  });
}

export function postJSON(url, body) {
  return fetch(url, {
    method: "POST",
    headers: csrfHeaders({ "Content-Type": "application/json", Accept: "application/json" }),
    body: JSON.stringify(body),
  });
}

export function getJSON(url, { signal } = {}) {
  return fetch(url, { headers: { Accept: "application/json" }, cache: "no-store", signal })
    .then((response) => ensureOk(response, "Request failed."))
    .then((response) => response.json());
}

export function getText(url, { signal } = {}) {
  return fetch(url, { headers: { Accept: "text/html" }, cache: "no-store", signal })
    .then((response) => ensureOk(response, "Request failed."))
    .then((response) => response.text());
}
