import * as store from "@dc/store";

// indentPref maps a stored "indent" setting to a style/size descriptor.
export function indentPref(value) {
  if (value === "2spaces") return { style: "space", size: 2 };
  if (value === "4spaces") return { style: "space", size: 4 };
  return { style: "tab" };
}

// Editor settings are stored per-device in localStorage (font size, wrap,
// indentation and which diff view to use depend on the device/screen, so they
// should not follow the user across machines). The stored indentation is the
// fallback used when the open file's .editorconfig does not dictate one.
const EDITOR_SETTINGS_KEY = "dc-editor-settings";

export function loadEditorSettings() {
  const def = { tab_size: 4, indent: "tab", line_wrap: false, font_size: 14, diff_view: "auto", diff_collapse: true, search_regex: false, search_case: false };
  let stored = {};
  try {
    stored = store.getJSON(EDITOR_SETTINGS_KEY, {}) || {};
  } catch {
    stored = {};
  }
  const s = { ...def, ...stored };
  if (![2, 4, 8].includes(s.tab_size)) s.tab_size = def.tab_size;
  if (!["tab", "2spaces", "4spaces"].includes(s.indent)) s.indent = def.indent;
  if (typeof s.line_wrap !== "boolean") s.line_wrap = def.line_wrap;
  if (!(s.font_size >= 10 && s.font_size <= 24)) s.font_size = def.font_size;
  if (!["auto", "side", "inline"].includes(s.diff_view)) s.diff_view = def.diff_view;
  if (typeof s.diff_collapse !== "boolean") s.diff_collapse = def.diff_collapse;
  if (typeof s.search_regex !== "boolean") s.search_regex = def.search_regex;
  if (typeof s.search_case !== "boolean") s.search_case = def.search_case;
  return s;
}

export function saveEditorSettings(settings) {
  try {
    store.setJSON(EDITOR_SETTINGS_KEY, settings);
  } catch (err) {
    console.warn("failed to save editor settings", err);
  }
}

// setupSettingsUI initializes the editor-settings dropdown controls from the
// stored values, then on change applies the value live and persists it. onChange
// carries the key on to settings the editor itself does not own, how a
// comparison looks for one, which belongs to the tab and not to the buffer.
export function setupSettingsUI(root, editor, settings, onChange) {
  const numeric = (key) => key === "tab_size" || key === "font_size";
  root.querySelectorAll("[data-editor-setting]").forEach((el) => {
    const key = el.dataset.editorSetting;
    if (el.type === "checkbox") el.checked = !!settings[key];
    else el.value = String(settings[key]);
    el.addEventListener("change", () => {
      const value = el.type === "checkbox" ? el.checked : numeric(key) ? parseInt(el.value, 10) : el.value;
      settings[key] = value;
      editor.applySetting(key, value);
      saveEditorSettings(settings);
      onChange?.(key, value);
    });
  });
}

// setupIndentControl wires the Indentation dropdown. It shows the effective
// indentation (.editorconfig over the stored preference over the default). When
// .editorconfig dictates the value the control is read-only with a hint;
// otherwise editing it updates the stored preference. Returns a sync function
// the caller invokes after each file open.
export function setupIndentControl(root, editor, settings) {
  const select = root.querySelector("[data-editor-indent]");
  if (!select) return () => {};
  const display = root.querySelector("[data-editor-indent-display]");
  const hint = root.querySelector("[data-editor-indent-hint]");
  const valueOf = (ind) => (ind.style === "space" ? `${ind.size}spaces` : "tab");
  const labelOf = (ind) => (ind.style === "space" ? `${ind.size} spaces` : "Tab");
  function sync() {
    const ind = editor.getIndent();
    const val = valueOf(ind);
    if (ind.style === "space" && !select.querySelector(`option[value="${val}"]`)) {
      const opt = document.createElement("option");
      opt.value = val;
      opt.textContent = `${ind.size} spaces`;
      select.appendChild(opt);
    }
    select.value = val;
    // From .editorconfig: show a disabled text input with the value; otherwise
    // the editable dropdown.
    const fromConfig = !!ind.fromConfig;
    select.hidden = fromConfig;
    if (display) {
      display.hidden = !fromConfig;
      display.value = labelOf(ind);
    }
    if (hint) hint.hidden = !fromConfig;
  }
  select.addEventListener("change", () => {
    settings.indent = select.value;
    editor.applySetting("indent", select.value);
    saveEditorSettings(settings);
    sync();
  });
  sync();
  return sync;
}
