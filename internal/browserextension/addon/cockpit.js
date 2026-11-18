import { api } from "./api.js";
import { API, BROWSER } from "./version.js";

// Only the extension's pages and background reach its IndexedDB, never a
// content script or a page.
const DB = "dc-extension";
const STORE = "setup";
const KEY = "cockpit";

function openDB() {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB, 1);
    request.onupgradeneeded = () => request.result.createObjectStore(STORE);
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

async function inStore(mode, act) {
  const db = await openDB();
  try {
    return await new Promise((resolve, reject) => {
      const tx = db.transaction(STORE, mode);
      const request = act(tx.objectStore(STORE));
      tx.oncomplete = () => resolve(request.result);
      tx.onerror = () => reject(tx.error);
      tx.onabort = () => reject(tx.error);
    });
  } finally {
    db.close();
  }
}

export async function readSetup() {
  const kept = await inStore("readonly", (store) => store.get(KEY));
  if (!kept || typeof kept.origin !== "string" || typeof kept.token !== "string" || !kept.token) return null;
  return { origin: kept.origin, token: kept.token };
}

export const saveSetup = (origin, token) => inStore("readwrite", (store) => store.put({ origin, token }, KEY));

export const dropSetup = () => inStore("readwrite", (store) => store.delete(KEY));

const SETUP_CHANNEL = "dc-extension-setup";

export function setupSaved() {
  const channel = new BroadcastChannel(SETUP_CHANNEL);
  channel.postMessage("saved");
  channel.close();
}

export function onSetupSaved(react) {
  new BroadcastChannel(SETUP_CHANNEL).addEventListener("message", react);
}

export function cockpitOrigin(typed) {
  const text = String(typed || "").trim();
  if (!text) return "";
  let url;
  try {
    url = new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(text) ? text : `https://${text}`);
  } catch {
    return "";
  }
  if ((url.protocol !== "http:" && url.protocol !== "https:") || url.username || url.password || !url.hostname) return "";
  return url.origin;
}

// A Firefox pattern with a port never matches and Safari documents none, so
// there it is the cockpit's host on every port.
export function pattern(origin) {
  if (BROWSER === "chrome") return `${origin}/*`;
  const url = new URL(origin);
  return `${url.protocol}//${url.hostname}/*`;
}

export class CockpitError extends Error {
  constructor(kind, message) {
    super(message);
    this.kind = kind;
  }
}

export const TOKEN_REFUSED = "The cockpit refuses the token, it is wrong or revoked. Make a new one under Settings › Browser and paste it into the setup.";

const CERTIFICATE = /CERT|SSL|SEC_ERROR|PKIX|SECURE/i;

// Only the request events carry the browser's own words, which tell an
// untrusted certificate from a server that does not answer.
function failure(origin, error) {
  if (error && CERTIFICATE.test(error)) return new CockpitError("certificate", `This browser does not trust the certificate of ${origin}. Open that address in a tab and trust it, then check again.`);
  if (error) return new CockpitError("unreachable", `Nothing answers at ${origin}. Check the address and that the cockpit runs.`);
  return new CockpitError("unreachable", `Nothing answers at ${origin}, or this browser does not trust its certificate.`);
}

function watchErrors(origin) {
  const events = api.webRequest?.onErrorOccurred;
  const seen = { error: "" };
  if (!events) return { seen, stop() {} };
  const listener = (details) => {
    if (details && typeof details.error === "string") seen.error = details.error;
  };
  try {
    events.addListener(listener, { urls: [pattern(origin)] });
  } catch {
    return { seen, stop() {} };
  }
  return { seen, stop: () => events.removeListener(listener) };
}

// No redirect is followed and no cookie goes along, so the token reaches the
// cockpit's origin alone.
export async function call(setup, method, path, form) {
  const url = new URL(path, setup.origin);
  if (url.origin !== setup.origin || !path.startsWith("/api/browser-extension/")) throw new CockpitError("address", "The extension calls only the cockpit's own address.");
  if (!(await api.permissions.contains({ origins: [pattern(setup.origin)] }))) {
    throw new CockpitError("permission", `The extension may not reach ${setup.origin} yet. Open the setup and allow it.`);
  }
  const watch = watchErrors(setup.origin);
  let response;
  try {
    response = await fetch(url, {
      method,
      headers: { Authorization: `Bearer ${setup.token}`, Accept: "application/json" },
      body: form instanceof FormData ? form : form ? new URLSearchParams(form) : undefined,
      redirect: "manual",
      credentials: "omit",
      cache: "no-store",
      referrerPolicy: "no-referrer",
    });
  } catch {
    await new Promise((resolve) => setTimeout(resolve, 150));
    throw failure(setup.origin, watch.seen.error);
  } finally {
    setTimeout(watch.stop, 300);
  }
  if (response.type === "opaqueredirect" || (response.status >= 300 && response.status < 400)) {
    throw new CockpitError("redirect", `${setup.origin} redirects to another address. Enter the address the cockpit's pages open on.`);
  }
  if (response.status === 401) throw new CockpitError("token", TOKEN_REFUSED);
  let data = null;
  try {
    data = await response.json();
  } catch {
    data = null;
  }
  if (!response.ok) {
    const said = typeof data?.error === "string" && data.error ? data.error.slice(0, 300) : `The cockpit answers with status ${response.status}.`;
    throw new CockpitError("refused", said);
  }
  if (!data || typeof data !== "object") throw new CockpitError("answer", `${setup.origin} answers, but not like a cockpit.`);
  return data;
}

export const VERSION = api.runtime.getManifest().version;

// Every cockpit answers this path with these fields, so an extension of any
// age tells whether it fits.
export const HANDSHAKE = "/api/browser-extension/version";

export const DOWNLOAD_PATH = `/settings/browser/extension/${BROWSER}.zip`;

const DOTTED = /^\d{1,5}(\.\d{1,5}){0,3}$/;

function older(mine, theirs) {
  const a = mine.split(".").map(Number);
  const b = theirs.split(".").map(Number);
  for (let i = 0; i < Math.max(a.length, b.length); i += 1) {
    if ((a[i] || 0) !== (b[i] || 0)) return (a[i] || 0) < (b[i] || 0);
  }
  return false;
}

export function fit(answer) {
  const speaks = Array.isArray(answer?.api) ? answer.api.filter(Number.isInteger) : [];
  const offered = typeof answer?.extension === "string" && DOTTED.test(answer.extension) ? answer.extension : "";
  const get = offered ? `version ${offered}` : "the cockpit's extension";
  if (!speaks.includes(API)) {
    if (speaks.some((one) => one > API)) return { blocks: true, text: `This extension is too old for the cockpit, install ${get}.` };
    return { blocks: true, text: `This extension is too new for the cockpit, update the cockpit or install ${get}.` };
  }
  if (offered && DOTTED.test(VERSION) && older(VERSION, offered)) return { blocks: false, text: `Version ${offered} is available.` };
  return { blocks: false, text: "" };
}
