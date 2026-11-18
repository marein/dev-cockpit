import { parentDir } from "@dc/editor-format";

// The counted folder tree under a change list, pure data: what every folder
// row needs is counted once per list here and moved by the clicks from there
// on, see changeList. Answering it by filtering the change list instead costs
// one pass per folder row and another per click, which is what made a working
// copy with ten thousand changes a panel nobody could open.

// dirOf is the folder a row is grouped under: the parent, with the trailing
// slash of a directory entry out of the way first.
export function dirOf(entry) {
  return parentDir(entry.path.endsWith("/") ? entry.path.slice(0, -1) : entry.path);
}

function emptyNode(dir, parent) {
  return { dir, parent, dirs: new Map(), files: [], count: 0, selectable: 0, picked: 0 };
}

export function emptyIndex() {
  const root = emptyNode("", null);
  return { shown: [], byPath: new Map(), nodes: new Map([["", root]]), root, total: 0, pickedAll: 0 };
}

// buildIndex counts what the rows need. byPath and the two totals are about
// every change there is, because a pick outside the filter is still a pick and
// must not be pruned or forgotten; the folder tree holds only what the filter
// shows, which is what makes every folder row, every count and every checkbox
// under it speak about the same set without a second path through the code.
export function buildIndex(sorted, { matches, selectable, picked }) {
  const index = emptyIndex();
  const { root, nodes, byPath } = index;
  for (const entry of sorted) {
    byPath.set(entry.path, entry);
    const canPick = selectable(entry);
    const isPicked = canPick && picked.has(entry.path);
    if (isPicked) index.pickedAll += 1;
    if (!matches(entry)) continue;
    index.shown.push(entry);
    let node = root;
    const dir = dirOf(entry);
    if (dir) {
      let prefix = "";
      for (const segment of dir.split("/")) {
        prefix = prefix ? `${prefix}/${segment}` : segment;
        let child = node.dirs.get(segment);
        if (!child) {
          child = emptyNode(prefix, node);
          node.dirs.set(segment, child);
          nodes.set(prefix, child);
        }
        node = child;
      }
    }
    node.files.push(entry);
    for (let n = node; n; n = n.parent) {
      n.count += 1;
      if (canPick) n.selectable += 1;
      if (isPicked) n.picked += 1;
    }
  }
  index.total = sorted.length;
  return index;
}

// recountPicks reads the picks back into the folder counts. The clicks move
// them one by one; this is for the moments a whole set arrives at once, the
// all box, a draft from another device and a spent commit.
export function recountPicks(index, sorted, { selectable, picked }) {
  for (const node of index.nodes.values()) node.picked = 0;
  for (const entry of index.shown) {
    if (!selectable(entry) || !picked.has(entry.path)) continue;
    for (let n = index.nodes.get(dirOf(entry)); n; n = n.parent) n.picked += 1;
  }
  let all = 0;
  for (const entry of sorted) {
    if (selectable(entry) && picked.has(entry.path)) all += 1;
  }
  index.pickedAll = all;
}

// rowSpecs is the list the panel would render in full: one entry per row,
// folders first and files after them on every level in the grouped view, the
// sorted changes in the flat one. Building it is cheap, building its rows is
// not, which is what the panel's cap is about.
//
// Grouped by folder, the way an IDE's directory tree reads: folders first and
// files after them, on every level. A folder becomes a row of its own as soon
// as it holds more than one thing; a chain of folders that only hands down to
// a single subfolder and has no files of its own merges into one row with the
// joined path as its label, keyed by the deepest folder of the chain. A folded
// folder keeps its row and leaves out everything below it.
export function rowSpecs(index, grouped, folded) {
  const list = [];
  if (!grouped) {
    for (const entry of index.shown) list.push({ entry, nested: false, depth: 0 });
    return list;
  }
  const emit = (node, depth) => {
    for (const segment of [...node.dirs.keys()].sort((a, b) => a.localeCompare(b))) {
      let label = segment;
      let child = node.dirs.get(segment);
      while (child.files.length === 0 && child.dirs.size === 1) {
        const [next] = child.dirs.keys();
        label = `${label}/${next}`;
        child = child.dirs.get(next);
      }
      const isFolded = folded.has(child.dir);
      list.push({ node: child, label, depth, folded: isFolded });
      if (!isFolded) emit(child, depth + 1);
    }
    for (const entry of node.files) list.push({ entry, nested: depth > 0, depth });
  };
  emit(index.root, 0);
  return list;
}
