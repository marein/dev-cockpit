const keymaps = {
  vim: [
    { label: ":w", seq: "\u001b:w\r", hint: "Save" },
    { label: ":wq", seq: "\u001b:wq\r", hint: "Save and quit" },
    { label: ":q!", seq: "\u001b:q!\r", hint: "Quit without saving" },
    { label: "u", seq: "\u001bu", hint: "Undo" },
    { label: "/", seq: "\u001b/", hint: "Search", typing: true },
  ],
  less: [
    { label: "q", seq: "q", hint: "Quit" },
    { label: "/", seq: "/", hint: "Search", typing: true },
    { label: "n", seq: "n", hint: "Next match" },
    { label: "Space", seq: " ", hint: "Page down" },
    { label: "b", seq: "b", hint: "Page up" },
    { label: "G", seq: "G", hint: "Jump to end" },
  ],
  htop: [
    { label: "F3", seq: "\u001bOR", hint: "Search", typing: true },
    { label: "F4", seq: "\u001bOS", hint: "Filter", typing: true },
    { label: "F6", seq: "\u001b[17~", hint: "Sort by" },
    { label: "F9", seq: "\u001b[20~", hint: "Kill" },
    { label: "q", seq: "q", hint: "Quit" },
  ],
  top: [
    { label: "M", seq: "M", hint: "Sort by memory" },
    { label: "P", seq: "P", hint: "Sort by CPU" },
    { label: "k", seq: "k", hint: "Kill", typing: true },
    { label: "q", seq: "q", hint: "Quit" },
  ],
  nano: [
    { label: "^O", seq: "\u000f", hint: "Save", typing: true },
    { label: "^X", seq: "\u0018", hint: "Exit", typing: true },
    { label: "^W", seq: "\u0017", hint: "Search", typing: true },
    { label: "^K", seq: "\u000b", hint: "Cut line" },
    { label: "^U", seq: "\u0015", hint: "Paste line" },
  ],
  emacs: [
    { label: "^X^S", seq: "\u0018\u0013", hint: "Save" },
    { label: "^X^C", seq: "\u0018\u0003", hint: "Exit" },
    { label: "^S", seq: "\u0013", hint: "Search", typing: true },
    { label: "^G", seq: "\u0007", hint: "Cancel" },
  ],
};

const aliases = {
  vi: "vim",
  view: "vim",
  nvim: "vim",
  vimdiff: "vim",
  more: "less",
  man: "less",
  pico: "nano",
};

export function contextKeys(command) {
  const name = String(command || "").toLowerCase();
  return keymaps[aliases[name] ?? name] ?? [];
}
