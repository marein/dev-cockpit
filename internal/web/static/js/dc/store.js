// TODO(v2.0.0): drop legacyKeys and readItem's migration; every key is dc-*
// then. Old keys are only read as a fallback, migrated to the new name on
// first read and deleted.
const legacyKeys = {
  "dc-terminal-font-size": "session-terminal-font-size",
  "dc-update": "dcUpdate",
  "dc-editor-settings": "dev-cockpit.editor-settings",
};

// A guest of a shared project keeps what its pages remember apart from the
// owner's, in the owner's own browser as well, so looking at the link never
// moves the owner's editor or terminals.
const own = document.querySelector('meta[name="dc-base"]')?.getAttribute("content") ? (key) => "dc-guest:" + key : (key) => key;

function readItem(key) {
  let value = window.localStorage.getItem(own(key));
  if (value != null) {
    return value;
  }
  const legacy = legacyKeys[key];
  if (!legacy || own(key) !== key) {
    return null;
  }
  value = window.localStorage.getItem(legacy);
  if (value == null) {
    return null;
  }
  window.localStorage.removeItem(legacy);
  window.localStorage.setItem(key, value);
  return value;
}

export function get(key, fallback = "") {
  try {
    const value = readItem(key);
    return value == null ? fallback : value;
  } catch (error) {
    void error;
    return fallback;
  }
}

export function set(key, value) {
  try {
    window.localStorage.setItem(own(key), value);
  } catch (error) {
    void error;
  }
}

export function remove(key) {
  try {
    window.localStorage.removeItem(own(key));
  } catch (error) {
    void error;
  }
}

export function getJSON(key, fallback = null) {
  try {
    const value = readItem(key);
    if (value == null) {
      return fallback;
    }
    const parsed = JSON.parse(value);
    return parsed == null ? fallback : parsed;
  } catch (error) {
    void error;
    return fallback;
  }
}

export function setJSON(key, value) {
  try {
    window.localStorage.setItem(own(key), JSON.stringify(value));
  } catch (error) {
    void error;
  }
}
