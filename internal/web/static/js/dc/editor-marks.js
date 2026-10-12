// How a changed path is marked in the tree: the letter at the end of the row and
// the color both the name and that letter take. The colors are Tabler variables
// through its text utilities, so they follow the light and dark theme. rank
// decides what a folder shows when several kinds sit under it: the most
// pressing one wins.
export const GIT_MARKS = {
  conflict: { rank: 5, cls: "text-red", mark: "!", label: "Conflicted" },
  deleted: { rank: 4, cls: "text-red", mark: "D", label: "Deleted" },
  modified: { rank: 3, cls: "text-yellow", mark: "M", label: "Modified" },
  renamed: { rank: 2, cls: "text-azure", mark: "R", label: "Renamed" },
  added: { rank: 1, cls: "text-green", mark: "A", label: "Added" },
  untracked: { rank: 0, cls: "text-cyan", mark: "U", label: "Untracked" },
};
export const GIT_MARK_CLASSES = Object.values(GIT_MARKS).map((m) => m.cls);

// gitKind reduces the two porcelain codes of one entry to what the tree shows.
// The worktree code wins over the index one: what is on disk is what the
// person is looking at.
export function gitKind(entry) {
  const index = entry.index || ".";
  const worktree = entry.worktree || ".";
  if (index === "U" || worktree === "U" || (index === "A" && worktree === "A") || (index === "D" && worktree === "D")) {
    return "conflict";
  }
  if (worktree === "?") return "untracked";
  const code = worktree !== "." ? worktree : index;
  if (code === "D") return "deleted";
  if (code === "A") return "added";
  if (code === "R" || code === "C") return "renamed";
  return "modified";
}

// revdiffKind reads the letter of a revision comparison the same way, "?"
// being a file of the working copy git does not track.
export function revdiffKind(entry) {
  switch (entry.status) {
    case "A":
    case "C":
      return "added";
    case "D":
      return "deleted";
    case "R":
      return "renamed";
    case "?":
      return "untracked";
    default:
      return "modified";
  }
}
