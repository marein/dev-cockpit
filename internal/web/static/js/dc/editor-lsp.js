import { csrfHeaders, getJSON, postJSON } from "@dc/http";

export function createClient(base, spec) {
  const langs = new Map();
  for (const pair of (spec || "").split(",")) {
    const [ext, label] = pair.split(":");
    if (ext && label) langs.set(ext, label);
  }
  if (langs.size === 0) return null;
  const id = crypto.randomUUID ? crypto.randomUUID() : `c${Date.now()}${Math.random().toString(36).slice(2)}`;
  const state = new Map();
  const permanent = new Set(["not-installed", "disabled"]);

  function extOf(path) {
    const name = path.split("/").pop();
    const i = name.lastIndexOf(".");
    return i > 0 ? name.slice(i + 1).toLowerCase() : "";
  }

  async function post(action, payload, signal) {
    for (let attempt = 0; ; attempt++) {
      let res;
      try {
        res = await fetch(`${base}/lsp/${action}`, {
          method: "POST",
          headers: csrfHeaders({ "Content-Type": "application/json", Accept: "application/json" }),
          body: JSON.stringify({ client: id, ...payload }),
          signal,
        });
      } catch (err) {
        if ((signal && signal.aborted) || attempt >= 2) throw err;
        await new Promise((resolve) => setTimeout(resolve, 250));
        continue;
      }
      if (!res.ok) throw new Error(`The lookup failed (${res.status}).`);
      return await res.json();
    }
  }

  return {
    usable(path) {
      const ext = extOf(path);
      return langs.has(ext) && !permanent.has(state.get(ext));
    },
    note(path, status) {
      state.set(extOf(path), status);
    },
    definition: (payload, signal) => post("definition", payload, signal),
    references: (payload, signal) => post("references", payload, signal),
    // The file behind a target outside the project, out of the source
    // directories the servers themselves work from. It answers text and
    // no version: nothing read here can be written back.
    source: (path, signal) => getJSON(`${base}/lsp/source?path=${encodeURIComponent(path)}`, { signal }),
    reindex() {
      return postJSON(`${base}/lsp/reindex`, {}).catch(() => {});
    },
    closeDocument(path) {
      fetch(`${base}/lsp/close`, {
        method: "POST",
        headers: csrfHeaders({ "Content-Type": "application/json", Accept: "application/json" }),
        body: JSON.stringify({ client: id, path }),
        keepalive: true,
      }).catch(() => {});
    },
  };
}
