// TODO(v2.0.0): drop legacyKeys and readItem's migration; every key is dc-*
// then. Old keys are only read as a fallback, migrated to the new name on
// first read and deleted.
const legacyKeys = {
  "dc-terminal-font-size": "session-terminal-font-size",
  "dc-terminal-rows": "session-terminal-rows",
  "dc-update": "dcUpdate",
  "dc-editor-settings": "dev-cockpit.editor-settings",
};

function readItem(key) {
  let value = window.localStorage.getItem(key);
  if (value != null) {
    return value;
  }
  const legacy = legacyKeys[key];
  if (!legacy) {
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
    window.localStorage.setItem(key, value);
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
    window.localStorage.setItem(key, JSON.stringify(value));
  } catch (error) {
    void error;
  }
}
