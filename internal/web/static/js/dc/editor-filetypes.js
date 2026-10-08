// Languages are dynamic-imported by full URL. The jsDelivr dist files keep their
// @codemirror/@lezer imports bare, so they resolve through the page import map to
// the single shared instances (otherwise instanceof checks break).
export const langUrl = (pkg) => `https://cdn.jsdelivr.net/npm/@codemirror/${pkg}/dist/index.js`;

// Tabler carries a glyph for the common formats, everything else keeps the
// plain file icon. Names win over extensions (Dockerfile, LICENSE, dotfiles).
const FILE_ICONS = {
  js: "ti-file-type-js", mjs: "ti-file-type-js", cjs: "ti-file-type-js",
  jsx: "ti-file-type-jsx", ts: "ti-file-type-ts", tsx: "ti-file-type-tsx",
  mts: "ti-file-type-ts", cts: "ti-file-type-ts",
  css: "ti-file-type-css", scss: "ti-file-type-css", less: "ti-file-type-css",
  html: "ti-file-type-html", htm: "ti-file-type-html", vue: "ti-file-type-vue",
  php: "ti-file-type-php", xml: "ti-file-type-xml", svg: "ti-file-type-svg",
  sql: "ti-file-type-sql", rs: "ti-file-type-rs", csv: "ti-file-type-csv",
  txt: "ti-file-type-txt", log: "ti-file-type-txt", pdf: "ti-file-type-pdf",
  doc: "ti-file-type-doc", docx: "ti-file-type-doc",
  zip: "ti-file-zip", tar: "ti-file-zip", gz: "ti-file-zip", tgz: "ti-file-zip",
  png: "ti-file-type-png", jpg: "ti-file-type-jpg", jpeg: "ti-file-type-jpg",
  bmp: "ti-file-type-bmp", gif: "ti-photo", webp: "ti-photo", ico: "ti-photo",
  json: "ti-json", md: "ti-markdown", markdown: "ti-markdown",
  go: "ti-brand-golang", py: "ti-brand-python",
  yml: "ti-file-settings", yaml: "ti-file-settings", toml: "ti-file-settings",
  ini: "ti-file-settings", conf: "ti-file-settings", cfg: "ti-file-settings",
  env: "ti-key", lock: "ti-lock", db: "ti-database", sqlite: "ti-database",
  sh: "ti-terminal-2", bash: "ti-terminal-2", zsh: "ti-terminal-2",
  fish: "ti-terminal-2", bashrc: "ti-terminal-2", profile: "ti-terminal-2",
  c: "ti-file-code", h: "ti-file-code", cpp: "ti-file-code", cc: "ti-file-code",
  hpp: "ti-file-code", java: "ti-file-code", rb: "ti-file-code",
};

const NAME_ICONS = {
  dockerfile: "ti-brand-docker",
  "docker-compose.yml": "ti-brand-docker",
  "docker-compose.yaml": "ti-brand-docker",
  makefile: "ti-file-code",
  license: "ti-license",
  ".gitignore": "ti-brand-git",
  ".gitattributes": "ti-brand-git",
  ".gitmodules": "ti-brand-git",
  ".env": "ti-key",
};

export function fileIcon(name) {
  const lower = (name || "").toLowerCase();
  if (NAME_ICONS[lower]) return NAME_ICONS[lower];
  if (lower.startsWith(".env.")) return "ti-key";
  const ext = lower.includes(".") ? lower.split(".").pop() : "";
  return FILE_ICONS[ext] || "ti-file";
}

// Shell, Dockerfile and TOML have no lezer grammar; the legacy stream modes are
// the official route and ship as standalone ESM files.
export const modeUrl = (mode) => `https://cdn.jsdelivr.net/npm/@codemirror/legacy-modes@6.5.1/mode/${mode}.js`;

export const STREAM_LANGS = {
  sh: ["shell", "shell"],
  bash: ["shell", "shell"],
  zsh: ["shell", "shell"],
  ksh: ["shell", "shell"],
  fish: ["shell", "shell"],
  bashrc: ["shell", "shell"],
  profile: ["shell", "shell"],
  toml: ["toml", "toml"],
  dockerfile: ["dockerfile", "dockerFile"], // the module exports it camel cased
};

// Files the shell modes own by name, they carry no extension.
export const STREAM_NAMES = {
  ".bashrc": "sh",
  ".bash_profile": "sh",
  ".bash_aliases": "sh",
  ".profile": "sh",
  ".zshrc": "sh",
  ".zprofile": "sh",
  ".envrc": "sh",
  dockerfile: "dockerfile",
};

export const LANGS = {
  js: ["lang-javascript@6.2.2", "javascript", { jsx: true }],
  jsx: ["lang-javascript@6.2.2", "javascript", { jsx: true }],
  mjs: ["lang-javascript@6.2.2", "javascript", {}],
  cjs: ["lang-javascript@6.2.2", "javascript", {}],
  ts: ["lang-javascript@6.2.2", "javascript", { typescript: true }],
  mts: ["lang-javascript@6.2.2", "javascript", { typescript: true }],
  cts: ["lang-javascript@6.2.2", "javascript", { typescript: true }],
  tsx: ["lang-javascript@6.2.2", "javascript", { typescript: true, jsx: true }],
  go: ["lang-go@6.0.0", "go", null],
  html: ["lang-html@6.4.9", "html", null],
  htm: ["lang-html@6.4.9", "html", null],
  vue: ["lang-html@6.4.9", "html", null],
  gohtml: ["lang-html@6.4.9", "html", null],
  tmpl: ["lang-html@6.4.9", "html", null],
  gotmpl: ["lang-html@6.4.9", "html", null],
  twig: ["lang-jinja@6.0.1", "jinja", null],
  css: ["lang-css@6.2.1", "css", null],
  scss: ["lang-css@6.2.1", "css", null],
  less: ["lang-css@6.2.1", "css", null],
  json: ["lang-json@6.0.1", "json", null],
  md: ["lang-markdown@6.2.5", "markdown", null],
  markdown: ["lang-markdown@6.2.5", "markdown", null],
  py: ["lang-python@6.1.6", "python", null],
  php: ["lang-php@6.0.1", "php", null],
  yaml: ["lang-yaml@6.1.1", "yaml", null],
  yml: ["lang-yaml@6.1.1", "yaml", null],
  xml: ["lang-xml@6.1.0", "xml", null],
  svg: ["lang-xml@6.1.0", "xml", null],
  sql: ["lang-sql@6.7.0", "sql", null],
  rs: ["lang-rust@6.0.1", "rust", null],
  c: ["lang-cpp@6.0.2", "cpp", null],
  h: ["lang-cpp@6.0.2", "cpp", null],
  cpp: ["lang-cpp@6.0.2", "cpp", null],
  cc: ["lang-cpp@6.0.2", "cpp", null],
  hpp: ["lang-cpp@6.0.2", "cpp", null],
  java: ["lang-java@6.0.1", "java", null],
};

export const COLOR_FUNCTIONS = new Set([
  "rgb", "rgba", "hsl", "hsla", "hwb", "lab", "lch", "oklab", "oklch", "color", "color-mix",
]);
