import { rankTokens } from "@dc/filter";
import * as projectSort from "@dc/project-sort";

const RECENT = 5;

// A project counts its repository as a second name, so a hit there ranks
// like one in the name and ahead of a hit in the branch or the path alone.
export function rankProject(row, query) {
  const name = row.dataset.projectName || "";
  const repo = row.querySelector(".editor-palette-repo")?.textContent || "";
  return rankTokens([name, repo], row.dataset.projectSearch || name, query);
}

function group(label, rows, members) {
  const node = document.createElement("div");
  node.className = "editor-palette-group";
  if (label) {
    const head = document.createElement("div");
    head.className = "editor-palette-group-head";
    head.textContent = label;
    node.appendChild(head);
  }
  rows.forEach((row) => {
    const clone = row.cloneNode(true);
    if (members && members.has(row)) clone.classList.add("editor-palette-member");
    node.appendChild(clone);
  });
  return node;
}

// paletteGroups lays the server's project rows out as a palette lists them,
// clones in groups. Without a query the last used projects stand as a
// shortlist over the full list, which keeps the shared sort with its
// worktrees folded behind their main; a query collapses both into one list
// ranked over name, repository and branch together.
export function paletteGroups(rows, query) {
  if (query) {
    const hits = rows.map((row) => ({ row, rank: rankProject(row, query) })).filter((hit) => hit.rank >= 0);
    hits.sort((a, b) => a.rank - b.rank);
    return hits.length ? [group("", hits.map((hit) => hit.row))] : [];
  }
  const recent = rows
    .filter((row) => Number(row.dataset.projectUsed) > 0)
    .sort((a, b) => Number(b.dataset.projectUsed) - Number(a.dataset.projectUsed))
    .slice(0, RECENT);
  // A worktree that stands behind its main in the full list is drawn as its
  // member; in the shortlist and under a query every row stands on its own.
  const byName = new Map(rows.map((row) => [row.dataset.projectName, row]));
  const members = new Set(rows.filter((row) => projectSort.mainOf(row, byName)));
  const groups = [];
  if (recent.length) groups.push(group("Recent", recent));
  groups.push(group(recent.length ? "All projects" : "", rows, members));
  return groups;
}
