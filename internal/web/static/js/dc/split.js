// Creating a terminal straight into a split view, shared by the pane head's
// context menu and the strip's group tab menu. Everything a caller has to know
// is the target: the group, and the member whose column the new pane stacks
// into (empty opens a column of its own at the right edge, which is what the
// split wide entries ask for).
//
// The strip's + menu stays what it is on every page: it creates a standalone
// terminal, also on an open split page. A menu whose entries mean something
// else depending on the page is the thing to avoid here.

const navigate = (url) => {
  if (window.app?.navigate) Promise.resolve(window.app.navigate(url)).catch(() => {});
  else window.location.href = url;
};

// splitCreateItems are the two entries as @dc/contextmenu takes them.
export function splitCreateItems(target) {
  return [
    { label: "New shell here", icon: "ti-terminal-2", action: () => createIntoSplit("shell", target) },
    { label: "New coder here", icon: "ti-robot", action: () => createIntoSplit("coder", target) },
  ];
}

// createIntoSplit opens the session's create form prefilled: the target rides
// the query into hidden fields and back out through the POST like `return`
// does, so the create itself stays one request that starts the session and
// puts it into the split. The landing follows on its own, the create redirects
// to the session's page and a grouped session's page redirects to its split
// with the new pane focused.
export function createIntoSplit(kind, { group, column = "", project = "" }) {
  if (!group) return;
  const params = new URLSearchParams({ group, return: window.location.pathname + window.location.search });
  if (column) params.set("column", column);
  if (project) params.set("project", project);
  navigate((kind === "coder" ? "/coders/new" : "/shells/new") + "?" + params.toString());
}
