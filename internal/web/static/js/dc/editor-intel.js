import { csrfHeaders } from "@dc/http";

const PERMANENT_LSP_STATUSES = new Set(["disabled", "no-language", "not-installed"]);

const AI_BACKOFF_MS = 60000;

export function parseConfig(raw) {
  try {
    const config = JSON.parse(raw || "{}");
    if (config && (config.mode === "lsp" || config.mode === "lsp-ai")) {
      return {
        mode: config.mode,
        autoAi: config.autoAi !== false,
        debounceMs: Number(config.debounceMs) >= 100 ? Number(config.debounceMs) : 300,
        exts: Array.isArray(config.exts) ? config.exts.filter((e) => typeof e === "string") : [],
      };
    }
  } catch {
    return null;
  }
  return null;
}

function extOf(path) {
  const name = path.split("/").pop();
  const i = name.lastIndexOf(".");
  return i > 0 ? name.slice(i + 1).toLowerCase() : "";
}

export function createClient(base, config) {
  if (!config) return null;
  const id = crypto.randomUUID ? crypto.randomUUID() : `c${Date.now()}${Math.random().toString(36).slice(2)}`;
  const lspExts = new Set(config.exts);
  const lspStatusByExt = new Map();
  const aiStatusByPath = new Map();
  let aiOff = config.mode !== "lsp-ai";
  let aiBackoffUntil = 0;

  async function complete(payload, signal) {
    const res = await fetch(`${base}/completion`, {
      method: "POST",
      headers: csrfHeaders({ "Content-Type": "application/json", Accept: "application/json" }),
      body: JSON.stringify({ client: id, ...payload }),
      signal,
    });
    if (!res.ok) throw new Error(`Completion failed (${res.status}).`);
    return res.json();
  }

  function closeDocument(path) {
    fetch(`${base}/completion/close`, {
      method: "POST",
      headers: csrfHeaders({ "Content-Type": "application/json", Accept: "application/json" }),
      body: JSON.stringify({ client: id, path }),
      keepalive: true,
    }).catch(() => {});
  }

  return {
    id,
    mode: config.mode,
    autoAi: config.autoAi,
    debounceMs: config.debounceMs,
    complete,
    closeDocument,

    hasLanguage(path) {
      return lspExts.has(extOf(path));
    },
    lspUsable(path) {
      return lspExts.has(extOf(path)) && !PERMANENT_LSP_STATUSES.has(lspStatusByExt.get(extOf(path)));
    },
    noteLsp(path, status) {
      lspStatusByExt.set(extOf(path), status);
    },
    lspStatus(path) {
      return lspStatusByExt.get(extOf(path)) || "";
    },

    aiUsable(path) {
      if (aiOff || Date.now() < aiBackoffUntil) return false;
      return aiStatusByPath.get(path) !== "withheld";
    },
    noteAi(path, status) {
      aiStatusByPath.set(path, status);
      if (status === "disabled") aiOff = true;
      if (status === "unavailable") aiBackoffUntil = Date.now() + AI_BACKOFF_MS;
    },
    aiStatus(path) {
      if (aiOff) return "disabled";
      return aiStatusByPath.get(path) || "";
    },
  };
}
