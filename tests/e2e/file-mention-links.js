const fs = require("fs");
const path = require("path");
const L = require("./lib");
const { assert, BASE, sleep } = L;

// The file links of an assistant's chat, run by file-mention.js on its
// assistant and its projects. The workspace lies in no project, so only an
// absolute path names a file, and its project comes from the path: a message
// written with the @, sent as an ECHO_ANSWER prompt the fake claude streams
// back a line at a time, links in the user's bubble and in the answer once it
// is complete (the fragment of GET /assistants/:id/messages/:messageId, and
// the page itself after a reload). The answer carries a link, a download and
// an image out of the workspace's assistant-files, written there before the
// turn (STATE_DIR, default <AUX_DIR>/state, the instance's --state-dir), and
// those keep their targets. A click opens the editor through openFileLink
// without a full load, Ctrl a new tab.
// Gotcha: the streamed frames render without the workspace, so the image asks
// for /assistants/assistant-files/shot.png until the answer is complete, a
// 404 the report lists as non gating noise.

const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==";

function lines(n) {
  return Array.from({ length: n }, (_, i) => `line ${i + 1} of the file`).join("\n") + "\n";
}

function editorHref(project, file, line, col) {
  const q = new URLSearchParams();
  if (col) q.set("col", String(col));
  q.set("file", file);
  if (line) q.set("line", String(line));
  return `/projects/${encodeURIComponent(project)}/editor?${q}`;
}

function bubbleLinks(page, role) {
  return page.evaluate((r) => {
    const bubbles = [...document.querySelectorAll(`[data-assistant-message][data-role="${r}"]`)];
    const bubble = bubbles[bubbles.length - 1];
    if (!bubble) return null;
    const body = bubble.querySelector("[data-assistant-text]");
    const walker = document.createTreeWalker(body || bubble, NodeFilter.SHOW_TEXT);
    let outside = "";
    for (let node = walker.nextNode(); node; node = walker.nextNode()) if (!node.parentElement.closest("a")) outside += node.data;
    return {
      id: bubble.getAttribute("data-message-id") || "",
      state: bubble.getAttribute("data-state") || "",
      text: body?.textContent || "",
      outside,
      files: [...bubble.querySelectorAll("a[data-file-link]")].map((a) => ({
        text: a.textContent,
        href: a.getAttribute("href"),
        code: Boolean(a.closest("code")),
        pre: Boolean(a.closest("pre")),
        nested: Boolean(a.parentElement.closest("a")),
        shown: a.getClientRects().length > 0,
      })),
      anchors: [...bubble.querySelectorAll("a:not([data-file-link])")].map((a) => ({ text: a.textContent, href: a.getAttribute("href"), inner: a.querySelectorAll("a").length })),
      images: [...bubble.querySelectorAll("img")].map((img) => img.getAttribute("src")),
    };
  }, role);
}

async function waitBubble(page, role, test, what, timeout = 20000) {
  const deadline = Date.now() + timeout;
  let last = null;
  while (Date.now() < deadline) {
    last = await bubbleLinks(page, role);
    if (last && test(last)) return last;
    await sleep(50);
  }
  throw new Error(`${what}: ${JSON.stringify(last)}`);
}

module.exports = async function fileMentionLinks({ page, run, id, AUX, PROJECTS, SHOP, CHECKOUT, NOTES, BOX, openAssistant, reset, waitState }) {
  const STATE = process.env.STATE_DIR || path.join(AUX, "state");
  const abs = (project, rel) => path.join(PROJECTS, project, rel);
  const cart = abs(SHOP, "src/components/CartItem.tsx");
  const linked = [
    { text: cart, href: editorHref(SHOP, "src/components/CartItem.tsx") },
    { text: `${abs(SHOP, "src/api/orders.go")}:12`, href: editorHref(SHOP, "src/api/orders.go", 12), code: true },
    { text: `${abs(SHOP, "src/pages/cart.tsx")}:45-68`, href: editorHref(SHOP, "src/pages/cart.tsx", 45), code: true },
    { text: `${abs(CHECKOUT, "steps.txt")}:3:7`, href: editorHref(CHECKOUT, "steps.txt", 3, 7) },
    { text: `${abs(NOTES, "plan.txt")}:4`, href: editorHref(NOTES, "plan.txt", 4) },
    { text: abs(NOTES, "ideas/cart-redesign.md"), href: editorHref(NOTES, "ideas/cart-redesign.md"), code: true, pre: true },
  ];
  const plain = [
    "src/api/orders.go",
    "README.md",
    "/etc/hostname",
    abs(SHOP, "src/nope.go"),
    abs(SHOP, "src"),
    abs(NOTES, "leak.md"),
    `"${abs(SHOP, "docs/user guide.md")}"`,
  ];
  const carried = abs(SHOP, "README.md");
  const rest = [
    `and \`${linked[1].text}\`.`,
    `A range \`${linked[2].text}\`, the worktree ${linked[3].text} and the notes ${linked[4].text}.`,
    `Stay text: ${plain.join(", ")}.`,
    `Carried: [${carried}](https://example.com/x) and [the proof](assistant-files/proof.txt) and ![shot](assistant-files/shot.png)`,
    "",
    "```",
    `cat ${linked[5].text}`,
    "```",
  ].join("\n");
  const message = `ECHO_ANSWER\nLook at ${cart} ${rest}`;
  const workspace = path.join(STATE, "assistant", "instances", id, "workspace");
  let streamed = null;

  const checkPlain = (bubble, where) => {
    for (const word of plain) assert(bubble.outside.includes(word), `${where} links ${word} or lost it: ${bubble.outside}`);
  };

  await run("chat links: a message written with the @ links its absolute paths in the user's bubble, with lines, columns and ranges, per project", async () => {
    assert(fs.existsSync(workspace), `no workspace at ${workspace}, set STATE_DIR to the instance's --state-dir`);
    fs.mkdirSync(path.join(workspace, "assistant-files"), { recursive: true });
    fs.writeFileSync(path.join(workspace, "assistant-files", "proof.txt"), "proof\n");
    fs.writeFileSync(path.join(workspace, "assistant-files", "shot.png"), Buffer.from(PNG, "base64"));
    fs.writeFileSync(abs(CHECKOUT, "steps.txt"), lines(12));
    fs.writeFileSync(abs(NOTES, "plan.txt"), lines(12));
    fs.rmSync(abs(NOTES, "leak.md"), { force: true });
    fs.symlinkSync("/etc/hostname", abs(NOTES, "leak.md"));

    await openAssistant(page, id);
    await reset(page);
    await page.fill(BOX, "ECHO_ANSWER\nLook at ");
    await page.focus(BOX);
    await page.keyboard.press("End");
    await page.keyboard.type(`@${SHOP}/cartitem`);
    await waitState(page, (x) => x.rows[0] === "src/components/CartItem.tsx" && !x.busy, "CartItem never came first");
    await page.keyboard.press("Tab");
    await waitState(page, (x) => !x.shown && x.value === `ECHO_ANSWER\nLook at ${cart} `, "the mention did not land");
    await page.keyboard.insertText(rest);
    await waitState(page, (x) => !x.shown && x.value === message, "the message is not the one written");
    const watch = page.evaluate(() => new Promise((resolve) => {
      const seen = { first: "", files: -1, frames: 0 };
      const until = Date.now() + 30000;
      const look = () => {
        const bubble = [...document.querySelectorAll('[data-assistant-message][data-role="assistant"]')].pop();
        if (bubble?.getAttribute("data-state") === "streaming") {
          const text = bubble.querySelector("[data-assistant-text]")?.textContent || "";
          if (text.includes("Look at") && !seen.first) {
            seen.first = text;
            seen.files = bubble.querySelectorAll("a[data-file-link]").length;
          }
          seen.frames += 1;
        }
        if (bubble?.getAttribute("data-state") === "complete" || Date.now() > until) resolve(seen);
        else setTimeout(look, 20);
      };
      look();
    }));
    await page.keyboard.press("Enter");
    const user = await waitBubble(page, "user", (b) => b.id && b.files.length === linked.length + 1, "the user's bubble never got its links");
    const want = [linked[0], linked[1], linked[2], linked[3], linked[4], { text: carried, href: editorHref(SHOP, "README.md") }, { text: linked[5].text, href: linked[5].href }];
    assert(JSON.stringify(user.files.map((f) => [f.text, f.href])) === JSON.stringify(want.map((f) => [f.text, f.href])), `the user's bubble links ${JSON.stringify(user.files)}`);
    assert(user.files.every((f) => f.shown && !f.nested), `a link of the user's bubble is hidden or nested: ${JSON.stringify(user.files)}`);
    checkPlain(user, "the user's bubble");
    streamed = await watch;
    return `${user.files.length} links, the @ path first`;
  });

  await run("chat links: the answer streams, then links its paths in inline code and in a fenced block, and nothing else", async () => {
    assert(streamed, "the message never went out");
    assert(streamed.first && !streamed.first.includes(linked[5].text), `the answer never stood half streamed: ${JSON.stringify(streamed)}`);
    const answer = await waitBubble(page, "assistant", (b) => b.state === "complete" && b.files.length > 0, "the answer never completed with links");
    assert(JSON.stringify(answer.files.map((f) => [f.text, f.href, Boolean(f.code), Boolean(f.pre)])) === JSON.stringify(linked.map((f) => [f.text, f.href, Boolean(f.code), Boolean(f.pre)])), `the answer links ${JSON.stringify(answer.files)}`);
    assert(answer.files.every((f) => f.shown && !f.nested), `a link of the answer is hidden or nested: ${JSON.stringify(answer.files)}`);
    for (const f of linked) assert(answer.text.split(f.text).length === 2, `${f.text} stands ${answer.text.split(f.text).length - 1} times in the answer`);
    checkPlain(answer, "the answer");
    return `${answer.files.length} links, ${streamed.first.length} chars seen streaming with ${streamed.files} links`;
  });

  await run("chat links: links the answer already carries keep their targets, the workspace download and the image too", async () => {
    const answer = await bubbleLinks(page, "assistant");
    const external = answer.anchors.find((a) => a.href === "https://example.com/x");
    assert(external && external.text === carried && external.inner === 0, `the external link changed: ${JSON.stringify(answer.anchors)}`);
    const proof = answer.anchors.find((a) => a.text === "the proof");
    assert(proof && proof.href.startsWith(`/assistants/${id}/media/assistant-files/proof.txt`), `the download changed: ${JSON.stringify(answer.anchors)}`);
    assert(answer.images.some((src) => src.startsWith(`/assistants/${id}/media/assistant-files/shot.png`)), `the image changed: ${JSON.stringify(answer.images)}`);
    const served = await page.request.get(`${BASE}${proof.href.split("?")[0]}`);
    assert(served.status() === 200 && (await served.text()) === "proof\n", `the download serves ${served.status()}`);
    return "external, download and image untouched";
  });

  await run("chat links: a click opens the worktree's file in the editor at line and column without a full load, Ctrl opens a new tab", async () => {
    const popup = page.context().waitForEvent("page", { timeout: 10000 });
    await page.locator(`[data-assistant-message][data-role="assistant"] a[data-file-link]:text-is("${linked[4].text}")`).click({ modifiers: ["Control"] });
    const tab = await popup;
    await tab.waitForURL(/\/editor\?/, { timeout: 15000 });
    const opened = new URL(tab.url());
    await tab.close();
    assert(opened.pathname + opened.search === linked[4].href, `Ctrl opened ${opened.pathname}${opened.search}`);
    assert(new URL(page.url()).pathname === `/assistants/${id}`, `Ctrl moved the page to ${page.url()}`);

    await page.evaluate(() => { window.dcStayed = true; });
    await Promise.all([
      page.waitForURL(/\/editor\?/, { timeout: 15000 }),
      page.locator(`[data-assistant-message][data-role="assistant"] a[data-file-link]:text-is("${linked[3].text}")`).click(),
    ]);
    const here = new URL(page.url());
    assert(here.pathname + here.search === linked[3].href, `the click opened ${here.pathname}${here.search}`);
    await L.waitEditorCursor(page, { path: "steps.txt", line: 3, col: 7 });
    assert(await page.evaluate(() => window.dcStayed === true), "the click loaded the editor in full, not through openFileLink");
    return `${linked[3].href}, Ctrl ${opened.pathname}`;
  });

  await run("chat links: the page rendered anew carries the same links in both bubbles", async () => {
    await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const user = await bubbleLinks(page, "user");
    const answer = await bubbleLinks(page, "assistant");
    assert(user.files.length === linked.length + 1, `the user's bubble links ${JSON.stringify(user.files)} after a reload`);
    assert(JSON.stringify(answer.files.map((f) => f.href)) === JSON.stringify(linked.map((f) => f.href)), `the answer links ${JSON.stringify(answer.files)} after a reload`);
    return "same links";
  });
};
