// Pictures wait in IndexedDB, too large for the trays' storage. They go with
// the trays that hold them, kept.js decides on start which those are.

const DB = "dc-extension-pictures";
const STORE = "pictures";

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

export async function keepPicture(key, blob) {
  const bytes = await blob.arrayBuffer();
  await inStore("readwrite", (store) => store.put({ type: "image/png", bytes }, key));
}

export async function readPicture(key) {
  const kept = await inStore("readonly", (store) => store.get(key)).catch(() => null);
  return kept && kept.bytes instanceof ArrayBuffer && kept.type === "image/png" ? new Blob([kept.bytes], { type: "image/png" }) : null;
}

export function dropPictures(keys) {
  if (!keys.length) return Promise.resolve();
  return inStore("readwrite", (store) => {
    let last = null;
    for (const key of keys) last = store.delete(key);
    return last;
  }).catch(() => {});
}

export const dropAllPictures = () => inStore("readwrite", (store) => store.clear()).catch(() => {});

export function keepPictures(keys) {
  const keep = new Set(keys);
  return inStore("readwrite", (store) => {
    const listed = store.getAllKeys();
    listed.onsuccess = () => {
      for (const key of listed.result) if (!keep.has(key)) store.delete(key);
    };
    return listed;
  }).catch(() => {});
}
