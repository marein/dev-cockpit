export function get(key, fallback = "") {
  try {
    const value = window.localStorage.getItem(key);
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
    const value = window.localStorage.getItem(key);
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
