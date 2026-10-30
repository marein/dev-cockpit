import { csrfHeaders, getJSON, postJSON } from "@dc/http";

// The menu entry copying a class name the server already answered: the
// copy has to run inside the click, a page without a secure origin has no
// clipboard after an await.
export function classNameItem(name, copy) {
  const cut = name.lastIndexOf("\\") + 1;
  return {
    label: { head: `Copy ${name.slice(0, cut)}`, tail: name.slice(cut) },
    icon: "ti-copy",
    title: name,
    action: () => copy(name),
  };
}

export function createClient(base, spec, namesSpec) {
  const langs = new Map();
  for (const pair of (spec || "").split(",")) {
    const [ext, label] = pair.split(":");
    if (ext && label) langs.set(ext, label);
  }
  if (langs.size === 0) return null;
  const nameExts = new Set((namesSpec || "").split(",").filter(Boolean));
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
    names(path) {
      return nameExts.has(extOf(path));
    },
    definition: (payload, signal) => post("definition", payload, signal),
    references: (payload, signal) => post("references", payload, signal),
    className: (payload, signal) => post("class-name", payload, signal),
    // The project's types matching a query, over every language it holds.
    // The client id lets a newer query cancel this one on the server.
    types: (query, signal) => getJSON(`${base}/lsp/types?client=${encodeURIComponent(id)}&q=${encodeURIComponent(query)}`, { signal }),
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
