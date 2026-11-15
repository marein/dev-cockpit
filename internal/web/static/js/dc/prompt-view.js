import { escapeHtml } from "@dc/dom";

const LINK = /https?:\/\/(?:[^\s&]|&amp;)+/gi;
const LINK_TAIL = /[.,:!?)\]]+$/;
const TONES = ["danger", "error", "success", "git", "docker", "script", "steer"];
const CHIPS = {
  project: { icon: "folder" },
  folder: { icon: "folder", mono: true },
  branch: { icon: "git-branch", mono: true },
  target: { icon: "git-branch", mono: true, after: "arrow-right" },
  place: { icon: "file-code", mono: true },
  stack: { icon: "stack-2" },
};
const ROWS = ["", "mono", "link"];
const KINDS = {
  delete: { icon: "trash", tone: "danger" },
  danger: { icon: "alert-triangle", tone: "danger" },
  info: { icon: "info-circle", tone: "" },
  error: { icon: "alert-circle", tone: "error" },
  success: { icon: "circle-check", tone: "success" },
};
const QUESTION = { icon: "help-circle", tone: "" };
const NAME = /^[a-z][a-z0-9-]*$/;
const ASKS = [
  { test: /passphrase/i, title: "Unlock your SSH key", by: "ssh", placeholder: "Passphrase", confirm: "Unlock" },
  { test: /\bPIN\b/, title: "Enter the PIN of your security key", by: "ssh", placeholder: "PIN" },
  { test: /continue connecting|fingerprint|authenticity/i, title: "Trust the host's key", by: "ssh" },
  { test: /'s password/i, title: "Enter the password of the host", by: "ssh", placeholder: "Password" },
  { test: /^username/i, title: "Enter your user name", by: "git", placeholder: "User name" },
  { test: /password|token/i, title: "Enter your password", by: "git", placeholder: "Password" },
];

const mark = (name) => (NAME.test(name || "") ? ` ${name}` : "");
const icon = (name) => `<i class="ti ti-${/^[a-z0-9-]+$/.test(name || "") ? name : "help"}" aria-hidden="true"></i>`;
const cut = (detail) => (detail.cut
  ? `<div class="dc-ask-note mt-1" data-approval-cut>${icon("scissors")}<div>Only the first 64 KiB are shown, the run takes the whole value.</div></div>`
  : "");

function link(href, text) {
  return `<a href="${href}" target="_blank" rel="noopener noreferrer">${text}${icon("arrow-up-right")}</a>`;
}

function markup(html) {
  const box = document.createElement("template");
  box.innerHTML = html;
  box.content.querySelectorAll("a").forEach((anchor) => {
    if (!/^https?:\/\//i.test(anchor.getAttribute("href") || "")) {
      anchor.replaceWith(...anchor.childNodes);
      return;
    }
    anchor.target = "_blank";
    anchor.rel = "noopener noreferrer";
    anchor.insertAdjacentHTML("beforeend", icon("arrow-up-right"));
  });
  return box.innerHTML;
}

function linked(text) {
  return text.replace(LINK, (url) => {
    const tail = url.match(LINK_TAIL)?.[0] || "";
    const href = url.slice(0, url.length - tail.length);
    return link(href, href) + tail;
  });
}

function rowValue(detail) {
  const text = escapeHtml(detail.value || "");
  if (detail.as === "mono") return `<span class="font-monospace" data-approval-value>${text}</span>`;
  if (detail.as === "link" && /^https?:\/\//i.test(detail.url || "")) {
    return `<span data-approval-value>${link(escapeHtml(detail.url), text)}</span>`;
  }
  return `<span data-approval-value>${linked(text)}</span>`;
}

function chip(detail, tagged = true) {
  const look = CHIPS[detail.as];
  const label = escapeHtml(detail.label || "");
  return (look.after ? icon(look.after) : "")
    + `<span class="dc-ask-chip" title="${label}"${tagged ? ` data-approval-detail="${label}"` : ""}>${icon(look.icon)}`
    + `<span><span${look.mono ? ' class="font-monospace"' : ""}${tagged ? " data-approval-value" : ""}>${escapeHtml(detail.value)}</span></span></span>`;
}

function section(detail, inner) {
  return `<div class="dc-ask-section" data-approval-detail="${escapeHtml(detail.label)}">${inner}</div>`;
}

function code(label, text, tag, note = "") {
  return `<div class="dc-ask-code"><div class="dc-ask-code-head">${escapeHtml(label)}${note}</div><pre class="dc-prompt-text" ${tag}>${text}</pre></div>`;
}

function item(detail) {
  const place = detail.place ? chip({ as: "project", label: "Project", value: detail.place }, false) : "";
  const progress = detail.progress
    ? '<div class="progress" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="0"><div class="progress-bar"></div></div>'
    : "";
  return `<div class="dc-ask-item" data-approval-detail="${escapeHtml(detail.label)}"${mark(detail.mark)}>${icon(detail.icon)}`
    + `<span class="dc-ask-label"><span class="dc-ask-name${detail.mono ? " font-monospace" : ""}" data-approval-value>${escapeHtml(detail.value)}</span>`
    + (detail.id ? `<span class="dc-ask-id font-monospace text-secondary">${escapeHtml(detail.id)}</span>` : "")
    + `</span>${place}${progress}</div>`;
}

function callout(detail, kind, name) {
  return `<div class="dc-ask-${kind}" data-approval-detail="${kind}"${mark(detail.mark)}${detail.hidden ? " hidden" : ""}>${icon(name)}<div data-approval-value>${linked(escapeHtml(detail.value))}</div></div>`;
}

function control(detail, own) {
  const value = escapeHtml(detail.value || "");
  if (detail.control === "select") {
    const options = (detail.options || []).map((one) => `<option value="${escapeHtml(one.value)}">${escapeHtml(one.text)}</option>`);
    return `<select class="form-select"${own}>${options.join("")}</select>`;
  }
  if (detail.control === "textarea") return `<textarea class="form-control" rows="${Number(detail.rows) || 4}"${own}>\n${value}</textarea>`;
  return `<input type="text" class="form-control" value="${value}"${own}>`;
}

function field(detail) {
  const id = NAME.test(detail.id || "") ? detail.id : "";
  const own = (id ? ` id="${id}"` : "")
    + mark(detail.mark)
    + (detail.label ? "" : ` aria-label="${escapeHtml(detail.aria || "")}"`)
    + (detail.placeholder ? ` placeholder="${escapeHtml(detail.placeholder)}"` : "")
    + (detail.focus ? " autofocus" : "");
  const label = detail.label ? `<label class="dc-ask-section-head d-block"${id ? ` for="${id}"` : ""}>${escapeHtml(detail.label)}</label>` : "";
  return `<div class="dc-ask-field">${label}${control(detail, own)}</div>`;
}

function block(detail) {
  switch (detail.as) {
    case "field":
      return field(detail);
    case "long":
      return section(detail, `<div class="dc-ask-section-head">${escapeHtml(detail.label)}${cut(detail)}</div>`
        + (detail.html === undefined
          ? `<div class="dc-ask-box dc-prompt-text" data-approval-value>${linked(escapeHtml(detail.value))}</div>`
          : `<div class="dc-ask-box dc-ask-md" data-approval-value>${markup(detail.html)}</div>`));
    case "code":
      return section(detail, code(detail.label, escapeHtml(detail.value), "data-approval-value", cut(detail)));
    case "quote":
      return `<div class="dc-ask-quote" data-approval-detail="${escapeHtml(detail.label)}">${cut(detail)}${detail.label ? `<b>${escapeHtml(detail.label)}</b> · ` : ""}`
        + `<span class="dc-prompt-text" data-approval-value>${linked(escapeHtml(detail.value))}</span></div>`;
    case "warning":
      return callout(detail, "warning", "alert-triangle");
    case "note":
      return callout(detail, "note", "info-circle");
    default:
      return "";
  }
}

function row(detail) {
  return `<div data-approval-detail="${escapeHtml(detail.label)}"><dt>${escapeHtml(detail.label)}</dt><dd>${cut(detail)}${rowValue(detail)}</dd></div>`;
}

function groupOf(detail) {
  if (detail.as === "item") return "item";
  return ROWS.includes(detail.as || "") ? "row" : "";
}

function details(list) {
  const chips = list.filter((d) => CHIPS[d.as]);
  const runs = [];
  for (const detail of list.filter((d) => !CHIPS[d.as])) {
    const last = runs.at(-1);
    if (last && last.group && last.group === groupOf(detail)) last.list.push(detail);
    else runs.push({ group: groupOf(detail), list: [detail] });
  }
  return (chips.length ? `<div class="dc-ask-chips">${chips.map((d) => chip(d)).join("")}</div>` : "")
    + runs.map(({ group, list: run }) => {
      if (group === "item") return `<div class="dc-ask-list">${run.map(item).join("")}</div>`;
      if (group === "row") return `<dl class="dc-ask-rows">${run.map(row).join("")}</dl>`;
      return block(run[0]);
    }).join("");
}

function command(question) {
  if (!question.command) return "";
  const text = `${question.dir ? `<span class="text-secondary">${escapeHtml(question.dir)}</span>\n` : ""}`
    + `<span class="dc-ask-ps">$</span> ${escapeHtml(question.command)}`;
  return `<div class="dc-ask-section">${code("command", text, "data-gitprompt-command")}</div>`;
}

function head(name, title, meta, what = " data-approval-what") {
  return `<div class="dc-ask-head"><div class="dc-ask-icon">${icon(name)}</div><div class="min-w-0">`
    + `<div class="dc-ask-title"${what}>${escapeHtml(title)}</div><div class="dc-ask-meta">${meta}</div></div></div>`;
}

function popup(tone, extra = "") {
  return `dc-ask${TONES.includes(tone) ? ` dc-ask-${tone}` : ""}${extra}`;
}

export const invalid = (message) => `${icon("alert-circle")}<span>${escapeHtml(message)}</span>`;
const button = (tone) => `btn ${tone === "danger" ? "btn-danger" : "btn-primary"}`;
const checkbox = (shown) => shown.querySelector(".swal2-checkbox input")?.classList.add("form-check-input");
const caretAtEnd = (shown) => {
  const focused = shown.querySelector("input[autofocus], textarea[autofocus]");
  if (focused && focused === document.activeElement) focused.setSelectionRange(focused.value.length, focused.value.length);
};

export function approvalView(question) {
  const meta = `<span class="dc-ask-by" data-approval-asker>${icon("sparkles")}<span><span class="dc-ask-who">${escapeHtml(question.assistant || "")}</span>`
    + `${question.assistantId ? ` <span class="font-monospace">${escapeHtml(question.assistantId)}</span>` : ""}</span></span>`
    + `<span class="dc-ask-sep">·</span><span class="dc-ask-by">${icon("clock")}<span>waits up to 30 min</span></span>`;
  return {
    html: head(question.icon, question.title || question.action, meta)
      + `<div class="dc-ask-body" data-approval-details>${details(question.details || [])}${command(question)}</div>`,
    width: "42rem",
    input: "checkbox",
    inputValue: 0,
    inputPlaceholder: "Don't ask again for this kind",
    showDenyButton: true,
    confirmButtonText: "Approve",
    denyButtonText: "Deny",
    buttonsStyling: false,
    customClass: {
      popup: popup(question.tone),
      confirmButton: button(question.tone),
      denyButton: "btn",
    },
    didOpen: checkbox,
  };
}

export function gitQuestionView(question) {
  const line = question.prompt || "An answer is needed to continue.";
  const ask = ASKS.find((a) => a.test.test(line)) || { title: "Git asks a question", by: "git" };
  const secret = /pass(word|phrase)|secret|token|pin\b/i.test(line);
  const meta = question.command
    ? ""
    : `<span>git ${escapeHtml(question.action)} in <span class="dc-ask-who">${escapeHtml(question.project)}</span></span>`;
  return {
    html: head("key", ask.title, meta)
      + `<div class="dc-ask-body"><div class="dc-ask-prompt font-monospace dc-prompt-text">${escapeHtml(line)}</div>${command(question)}`
      + `<div class="dc-ask-note">${icon("shield-lock")}<div>The cockpit hands it to ${ask.by} and never keeps it.</div></div></div>`,
    width: "34rem",
    input: secret ? "password" : "text",
    inputPlaceholder: ask.placeholder || "",
    inputAttributes: { autocomplete: "off", autocorrect: "off", autocapitalize: "off", spellcheck: "false" },
    showCancelButton: true,
    confirmButtonText: ask.confirm || "Send",
    cancelButtonText: "Cancel",
    buttonsStyling: false,
    customClass: {
      popup: popup("git", " dc-ask-plain"),
      input: "form-control",
      confirmButton: "btn btn-primary",
      cancelButton: "btn",
    },
  };
}

export function formView({ title, text, details: list, icon: name, tone, confirmText, cancelText = "Cancel", footer = false }) {
  const body = (text ? `<div class="dc-prompt-text">${escapeHtml(text)}</div>` : "") + details(list || []);
  return {
    html: head(name, title || "", "", "") + (body ? `<div class="dc-ask-body">${body}</div>` : ""),
    width: "34rem",
    showCancelButton: true,
    confirmButtonText: escapeHtml(confirmText),
    cancelButtonText: escapeHtml(cancelText),
    reverseButtons: true,
    buttonsStyling: false,
    customClass: {
      popup: popup(tone, footer ? " dc-ask-confirm" : " dc-ask-plain dc-ask-confirm"),
      confirmButton: button(tone),
      cancelButton: "btn",
    },
    native: { title, text },
    ...((list || []).some((detail) => detail.focus) && { didOpen: caretAtEnd }),
  };
}

export function confirmView({ title, text, warning, details: list, check, kind, confirmText = "Confirm", cancelText = "Cancel" }) {
  const look = KINDS[kind] || QUESTION;
  return {
    ...formView({
      title,
      text,
      details: [...(list || []), ...(warning ? [{ as: "warning", value: warning }] : [])],
      icon: look.icon,
      tone: look.tone,
      confirmText,
      cancelText,
      footer: Boolean(check),
    }),
    ...(check && {
      input: "checkbox",
      inputValue: check.checked ? 1 : 0,
      inputPlaceholder: escapeHtml(check.label),
      didOpen: (shown) => {
        checkbox(shown);
        if (check.mark) shown.querySelector(".swal2-checkbox input").setAttribute(check.mark, "");
        shown.querySelector(".swal2-confirm").focus();
      },
    }),
  };
}

function below(after, check) {
  const box = check
    ? `<label class="dc-ask-check"><input type="checkbox" class="form-check-input"${mark(check.mark)}${check.checked ? " checked" : ""}>`
      + `<span>${escapeHtml(check.label)}</span></label>`
    : "";
  const html = (after ? `<div class="dc-ask-body dc-ask-after">${details(after)}</div>` : "") + box;
  return (shown) => shown.querySelector(".swal2-actions").insertAdjacentHTML("beforebegin", html);
}

export function promptView({ title, text, details: list, after, check, icon: name = "pencil", tone, placeholder, value, required, confirmText }) {
  const view = formView({ title, text, details: list, icon: name, tone, confirmText, footer: Boolean(check) });
  return {
    ...view,
    input: "text",
    inputValue: value || "",
    inputPlaceholder: placeholder || "",
    inputAttributes: { "aria-label": title || "" },
    inputValidator: required ? (input) => (input && input.trim() ? undefined : invalid(required)) : undefined,
    customClass: { ...view.customClass, input: "form-control" },
    ...((after || check) && { didRender: below(after, check) }),
  };
}
