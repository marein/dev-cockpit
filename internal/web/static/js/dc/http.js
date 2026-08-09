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
  const error = new Error(await errorText(response, fallback));
  // The status rides along, because "the server said no" and "the request never
  // arrived" are two different answers and a caller that acts on one of them
  // must not act on the other. A rejection without it came from the transport.
  error.status = response.status;
  throw error;
}

export function postForm(url, fields, { accept = "application/json" } = {}) {
  return fetch(url, {
    method: "POST",
    headers: csrfHeaders({ "Content-Type": "application/x-www-form-urlencoded", Accept: accept }),
    body: new URLSearchParams(fields).toString(),
  });
}

export async function landingURL(response) {
  if (response.redirected) {
    return response.url;
  }
  if (!/application\/json/i.test(response.headers.get("Content-Type") || "")) {
    return "";
  }
  const data = await response.json().catch(() => null);
  return data && typeof data.url === "string" ? data.url : "";
}

export function postJSON(url, body) {
  return fetch(url, {
    method: "POST",
    headers: csrfHeaders({ "Content-Type": "application/json", Accept: "application/json" }),
    body: JSON.stringify(body),
  });
}

// headers is for the one value a caller may not put in the URL: the access log
// writes the query out, so anything that names a running action of one browser
// travels as a header instead.
export function getJSON(url, { signal, headers } = {}) {
  return fetch(url, { headers: { Accept: "application/json", ...headers }, cache: "no-store", signal })
    .then((response) => ensureOk(response, "Request failed."))
    .then((response) => response.json());
}

export function getText(url, { signal } = {}) {
  return fetch(url, { headers: { Accept: "text/html" }, cache: "no-store", signal })
    .then((response) => ensureOk(response, "Request failed."))
    .then((response) => response.text());
}
