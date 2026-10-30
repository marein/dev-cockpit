const L = require("./lib");
const { assert, BASE } = L;

// Editor focus while a file loads: the editor takes the focus when a file
// stands, but a file that arrives while the project palette (Ctrl+Shift+P), the
// quick open (Ctrl+Shift+O, and its find and replace in files modes) or a sheet
// (git, comments, open files) stands must leave their field the keystrokes. A
// restored tab after a load, a jump from `?file=&line=` and a file link from
// the terminal panel (the `dc:open-file` event) are the late arrivals; before,
// they moved the focus into the buffer under the open overlay and the next keys
// landed in the file. Closing the palette, the quick open or a sheet hands the
// focus to the editor, by Escape, a sheet's close button or a click on the
// backdrop alike; a sheet that hands over to the palette, the quick open or one
// of its files leaves the focus where that one puts it, and with nothing open
// the restored file takes it as before.
// One rule decides the focus. Closing the palette, the quick open or a sheet
// gives it back to the element that held it when the overlay opened (the
// terminal, the commit message, an editor), or to the editor in front when
// that element is gone; an action of the overlay focuses its target instead.
// A tab click always focuses that tab's editor, the front tab of the other
// group included; Ctrl+Tab and Ctrl+Shift+Tab move the focus along only from
// an editor, the terminal and the commit message keep it. Closing a tab behind
// with its cross and a drag that reorders the strip leave it in the editor in
// front, a tab whose git diff is rebuilt side by side comes back with it in the
// working copy side, a viewer tab takes none, and on a phone neither a tab tap
// nor a closed sheet focuses the editor, so no keyboard rises. Ctrl+Shift+G in
// the editor opens the git sheet alone, without CodeMirror's search panel, and
// find previous stays on Shift+Enter in that panel.
// Gotchas:
//   - the file's read (GET .../editor/file?path=) is held by a route until
//     the overlay stands, so the late arrival is certain and not a race.
//   - find and replace in files bring back the stored search draft, so the
//     field is compared against what it held before the typing.
//   - a navigation that cuts a request off is a page error in WebKit, so the
//     typed query is answered first (the list stops saying Searching…) and
//     the draft the close writes is through before the next page loads
//     (draft writes are counted around fetch by an init script).
//   - a sheet docks right on a desktop, so its backdrop is the strip the
//     panel leaves free, checked with elementFromPoint before the click.
//   - the tab checks split the editor into two groups with the tab menu's
//     Split right and keep that layout to the end; the diff check builds the
//     repository through a shell (the app never writes git) and pins the
//     view to side by side in this device's editor settings. That shell is
//     the terminal the panel shows for the terminal checks after it, and the
//     change the diff leaves is what puts the commit view up.
//   - the terminal owns its keys, the editor's shortcuts are not taken from
//     inside it, so from there the overlays open with the mouse (the project
//     switcher, the menu's Go to file, the status line's git button), and the
//     click on the opener is why the place to return to is the last one that
//     took typing rather than the button that was pressed.

L.runFeature("EDITOR-FOCUS", async ({ engine, page, run, mobilePage }) => {
  const tag = `edf-${engine}-${Date.now().toString(36)}`;
  const project = `zzfocus-${tag}`;
  const editorURL = `${BASE}/projects/${encodeURIComponent(project)}/editor`;
  const press = (key) => () => page.keyboard.press(key);
  const overlays = [
    { name: "project palette", open: press("Control+Shift+P"), box: "[data-editor-palette]", field: "[data-editor-project-filter]" },
    { name: "quick open", open: press("Control+Shift+O"), box: "[data-editor-quickopen]", field: "[data-editor-quickopen-input]" },
    { name: "find in files", open: press("Control+Shift+F"), box: "[data-editor-quickopen]", field: "[data-editor-quickopen-input]" },
    { name: "replace in files", open: press("Control+Shift+H"), box: "[data-editor-quickopen]", field: "[data-editor-quickopen-input]" },
  ];
  const sheet = { box: "[data-editor-sheet]", field: "[data-editor-sheet-filter]" };
  const sheets = [
    {
      ...sheet,
      name: "git sheet",
      open: async () => {
        await page.waitForFunction(() => document.querySelector("dc-editor")?.dataset.gitRepo != null, null, { timeout: 12000 });
        await page.keyboard.press("Control+Shift+G");
      },
      close: "Escape",
    },
    { ...sheet, name: "comments sheet", open: press("Control+Shift+C"), close: "close button" },
  ];

  const write = (path, content) => page.evaluate(async ([p, path, content]) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    const res = await fetch(`/projects/${encodeURIComponent(p)}/editor/file`, {
      method: "POST",
      headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({ path, content }).toString(),
    });
    return res.status;
  }, [project, path, content]);
  const focusIn = (selector) => page.evaluate((s) => !!document.activeElement && document.activeElement.matches(s), selector);
  const buffer = () => page.evaluate(() => document.querySelector("[data-editor-surface] .cm-content")?.textContent || "");
  const fieldValue = (selector) => page.evaluate((s) => document.querySelector(s)?.value ?? null, selector);
  const holdFile = async () => {
    let release;
    const gate = new Promise((done) => { release = done; });
    await page.route("**/editor/file?**", async (route) => {
      await gate;
      await route.continue().catch(() => {});
    });
    return async () => {
      release();
      await page.unroute("**/editor/file?**");
    };
  };
  const typeInto = async (overlay) => {
    const before = await fieldValue(overlay.field);
    await page.keyboard.type("zq");
    const after = await fieldValue(overlay.field);
    assert(after === `${before}zq`, `${overlay.name}: the field holds ${JSON.stringify(after)} after typing into ${JSON.stringify(before)}`);
    assert(!(await buffer()).includes("zq"), `${overlay.name}: the keystrokes landed in the file`);
  };
  const closeSheet = async (overlay) => {
    if (overlay.close === "Escape") {
      await page.keyboard.press("Escape");
      await page.keyboard.press("Escape");
    } else if (overlay.close === "close button") {
      await page.click("[data-editor-sheet-close]");
    } else {
      const box = await page.locator(overlay.box).boundingBox();
      const point = { x: Math.round(box.x + 10), y: Math.round(box.y + box.height / 2) };
      assert(await page.evaluate(([b, x, y]) => document.elementFromPoint(x, y) === document.querySelector(b), [overlay.box, point.x, point.y]),
        `${overlay.name}: the click point is not on the backdrop`);
      await page.mouse.click(point.x, point.y);
    }
    await page.waitForFunction((box) => document.querySelector(box)?.hidden, overlay.box, { timeout: 4000 });
    await page.waitForFunction(() => document.activeElement?.matches(".cm-content"), null, { timeout: 4000 })
      .catch(async () => { throw new Error(`${overlay.name}: closing by ${overlay.close} left the focus on ${await page.evaluate(() => `${document.activeElement.tagName}.${document.activeElement.className}`)}`); });
  };
  const openFilesSheet = async () => {
    await page.click("[data-editor-menu]");
    await page.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
    await page.click("[data-editor-files-item]");
    await page.waitForSelector(`${sheet.box}:not([hidden])`, { timeout: 4000 });
  };
  const tabSel = (path) => `.editor-tab[data-path="${path}"]`;
  const focusIs = () => page.evaluate(() => {
    const a = document.activeElement;
    const g = a?.closest(".editor-group");
    if (!a?.matches(".cm-content") || !g) return `${a?.tagName}.${String(a?.className || "").split(" ")[0]}`;
    const side = a.closest(".cm-merge-b") ? " (working copy side)" : a.closest(".cm-merge-a") ? " (revision side)" : "";
    return `editor of group ${[...document.querySelectorAll(".editor-group")].indexOf(g) + 1} showing ${g.querySelector(".editor-tab.active")?.dataset.path}${side}`;
  });
  const expectFocus = async (what, want) => {
    const deadline = Date.now() + 4000;
    let seen = await focusIs();
    while (seen !== want && Date.now() < deadline) {
      await new Promise((done) => setTimeout(done, 100));
      seen = await focusIs();
    }
    assert(seen === want, `${what}: the focus is on ${seen}, not the ${want}`);
  };
  const shellInput = (shellUrl, raw) => page.evaluate(([href, cmd]) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    return fetch(`${href}/input`, { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": token }, body: JSON.stringify({ items: [{ raw: cmd }] }) }).then((r) => r.status);
  }, [new URL(shellUrl).pathname, raw]);
  const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
  let shellUrl = "";
  const draftsSettled = () => page.waitForFunction(() => !window.__draftWrites, null, { timeout: 8000 });
  const answered = () => page.waitForFunction(() => !/Searching…/.test(document.querySelector("[data-editor-quickopen-list]")?.textContent || ""), null, { timeout: 8000 });
  const openUnder = async (url, overlay) => {
    const release = await holdFile();
    try {
      await page.goto(url, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      await overlay.open();
      await page.waitForSelector(`${overlay.box}:not([hidden])`, { timeout: 4000 });
      assert(await focusIn(overlay.field), `${overlay.name}: the field did not take the focus on open`);
    } finally {
      await release();
    }
  };

  try {
    await page.addInitScript(() => {
      const send = window.fetch;
      window.__draftWrites = 0;
      window.fetch = async (resource, options = {}) => {
        const draft = String(resource).includes("/search-draft") && options.method === "POST";
        if (draft) window.__draftWrites += 1;
        try {
          return await send(resource, options);
        } finally {
          if (draft) window.__draftWrites -= 1;
        }
      };
    });
    await L.createProject(page, project);
    await page.goto(editorURL, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await L.waitUpgraded(page, ["dc-editor"]);
    assert(await write("a.txt", "alpha line\n") === 200, "writing a.txt failed");
    assert(await write("b.txt", "one\ntwo\nthree\nfour\n") === 200, "writing b.txt failed");
    assert(await write("c.txt", "c one\nc two\nc three\n") === 200, "writing c.txt failed");
    await page.click("[data-editor-refresh]");
    await page.click('.editor-file[data-path="a.txt"]');
    await page.waitForSelector('.editor-tab[data-path="a.txt"].active', { timeout: 8000 });

    await run("with nothing open the restored file takes the focus", async () => {
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await page.waitForFunction(() => /alpha line/.test(document.querySelector("[data-editor-surface] .cm-content")?.textContent || ""), null, { timeout: 12000 });
      await page.waitForFunction(() => document.activeElement?.matches(".cm-content"), null, { timeout: 6000 })
        .catch(() => { throw new Error("the restored file did not take the focus"); });
      return "focus in the buffer";
    });

    await run("a file that arrives under the palette or the quick open leaves their field the keystrokes, closing hands the focus back", async () => {
      const seen = [];
      for (const overlay of overlays) {
        await openUnder(editorURL, overlay);
        await page.waitForFunction(() => /alpha line/.test(document.querySelector("[data-editor-surface] .cm-content")?.textContent || ""), null, { timeout: 12000 });
        assert(await focusIn(overlay.field), `${overlay.name}: the late file took the focus`);
        await typeInto(overlay);
        await answered();
        await page.keyboard.press("Escape");
        await page.waitForFunction((box) => document.querySelector(box)?.hidden, overlay.box, { timeout: 4000 });
        assert(await focusIn(".cm-content"), `${overlay.name}: closing did not hand the focus to the editor`);
        await draftsSettled();
        seen.push(overlay.name);
      }
      return seen.join(", ");
    });

    await run("a jump that lands under the quick open moves the cursor and leaves the field the focus", async () => {
      const overlay = overlays[1];
      await openUnder(`${editorURL}?file=b.txt&line=3`, overlay);
      await page.waitForSelector('.editor-tab[data-path="b.txt"].active', { timeout: 12000 });
      await page.waitForFunction(() => /^3:/.test(document.querySelector("[data-editor-pos]")?.textContent || ""), null, { timeout: 6000 })
        .catch(() => { throw new Error("the jump did not reach line 3"); });
      assert(await focusIn(overlay.field), "the jump took the focus from the quick open");
      await page.keyboard.press("Escape");
      await page.waitForFunction((box) => document.querySelector(box)?.hidden, overlay.box, { timeout: 4000 });
      await draftsSettled();
      return "cursor on line 3, focus in the field";
    });

    await run("a file or a jump that arrives under a sheet leaves its filter the keystrokes, closing the sheet hands the focus to the editor", async () => {
      const seen = [];
      for (const overlay of sheets) {
        await openUnder(editorURL, overlay);
        await page.waitForSelector("[data-editor-tabs] .editor-tab.active", { timeout: 12000 });
        await page.waitForFunction(() => (document.querySelector("[data-editor-surface] .cm-content")?.textContent || "") !== "", null, { timeout: 6000 });
        assert(await focusIn(overlay.field), `${overlay.name}: the late file took the focus`);
        await typeInto(overlay);
        await closeSheet(overlay);
        seen.push(overlay.name);
      }
      const files = { ...sheet, name: "open files sheet", close: "backdrop" };
      await openFilesSheet();
      assert(await focusIn(files.field), "open files sheet: the filter did not take the focus on open");
      await page.evaluate((href) => document.dispatchEvent(new CustomEvent("dc:open-file", { cancelable: true, detail: { href } })),
        `${new URL(editorURL).pathname}?file=c.txt&line=2`);
      await page.waitForSelector('.editor-tab[data-path="c.txt"].active', { timeout: 12000 });
      await page.waitForFunction(() => /^2:/.test(document.querySelector("[data-editor-pos]")?.textContent || ""), null, { timeout: 6000 })
        .catch(() => { throw new Error("open files sheet: the terminal link's jump did not reach line 2"); });
      assert(await focusIn(files.field), "open files sheet: the late file took the focus");
      await typeInto(files);
      await closeSheet(files);
      seen.push(files.name);
      return seen.map((name, i) => `${name} (${[...sheets, files][i].close})`).join(", ");
    });

    await run("a sheet that hands over to the palette, the quick open or one of its files leaves the focus there", async () => {
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await page.waitForFunction(() => document.activeElement?.matches(".cm-content"), null, { timeout: 12000 });

      await sheets[0].open();
      await page.waitForSelector(`${sheet.box}:not([hidden])`, { timeout: 4000 });
      await page.keyboard.press("Control+Shift+P");
      await page.waitForSelector("[data-editor-palette]:not([hidden])", { timeout: 4000 });
      assert(await page.evaluate((b) => document.querySelector(b).hidden, sheet.box), "the git sheet stayed open under the palette");
      assert(await focusIn("[data-editor-project-filter]"), "the palette opened from the git sheet does not hold the focus");
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => document.querySelector("[data-editor-palette]")?.hidden, null, { timeout: 4000 });

      await sheets[1].open();
      await page.waitForSelector(`${sheet.box}:not([hidden])`, { timeout: 4000 });
      await page.keyboard.press("Control+Shift+O");
      await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 4000 });
      assert(await focusIn("[data-editor-quickopen-input]"), "the quick open opened from the comments sheet does not hold the focus");
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]")?.hidden, null, { timeout: 4000 });
      await draftsSettled();

      await openFilesSheet();
      const target = await page.evaluate(() => [...document.querySelectorAll(".editor-sheet-row")].map((row) => row.dataset.path).find((path) =>
        !document.querySelector(`[data-editor-tabs] .editor-tab.active[data-path="${path}"]`)));
      assert(target, "the open files sheet lists no file besides the active one");
      await page.click(`.editor-sheet-row[data-path="${target}"] .editor-sheet-open`);
      await page.waitForSelector(`.editor-tab[data-path="${target}"].active`, { timeout: 6000 });
      await page.waitForFunction(() => document.activeElement?.matches(".cm-content"), null, { timeout: 4000 })
        .catch(() => { throw new Error(`the row of ${target} did not leave the focus in its file`); });
      return `palette field, quick open field, ${target} in the editor`;
    });

    await run("a click on the palette's backdrop closes it and hands the focus to the editor like Escape", async () => {
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await page.waitForFunction(() => document.activeElement?.matches(".cm-content"), null, { timeout: 12000 });
      await page.keyboard.press("Control+Shift+P");
      await page.waitForSelector("[data-editor-palette]:not([hidden])", { timeout: 4000 });
      const box = await page.locator("[data-editor-palette]").boundingBox();
      const point = { x: Math.round(box.x + 10), y: Math.round(box.y + box.height - 10) };
      assert(await page.evaluate(([x, y]) => document.elementFromPoint(x, y) === document.querySelector("[data-editor-palette]"), [point.x, point.y]),
        "the click point is not on the backdrop");
      await page.mouse.click(point.x, point.y);
      await page.waitForFunction(() => document.querySelector("[data-editor-palette]")?.hidden, null, { timeout: 4000 });
      assert(await focusIn(".cm-content"), `the backdrop left the focus on ${await page.evaluate(() => document.activeElement.tagName)}`);
      return "palette closed, focus in the buffer";
    });

    await run("a tab click or Ctrl+Tab gives the editor of the tab in front the focus, across groups too", async () => {
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      for (const path of ["a.txt", "b.txt"]) {
        if (!(await page.$(tabSel(path)))) {
          await page.click(`.editor-file[data-path="${path}"]`);
          await page.waitForSelector(`${tabSel(path)}.active`, { timeout: 8000 });
        }
      }
      await page.click(tabSel("b.txt"), { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.locator(".dc-context-menu .dropdown-item", { has: page.locator(".dc-menu-label-head", { hasText: /^Split right$/ }) }).click();
      await page.waitForFunction(() => document.querySelectorAll(".editor-group").length === 2, null, { timeout: 8000 });
      await expectFocus("after Split right", "editor of group 2 showing b.txt");
      await page.click(tabSel("a.txt"));
      await expectFocus("a click on the front tab of the other group", "editor of group 1 showing a.txt");
      await page.click(tabSel("b.txt"));
      await expectFocus("a click back on the front tab of the first group", "editor of group 2 showing b.txt");
      await page.evaluate(() => document.activeElement?.blur());
      await page.click(tabSel("b.txt"));
      await expectFocus("a click on the front tab of the focused group", "editor of group 2 showing b.txt");
      await page.keyboard.press("Control+Tab");
      await expectFocus("Ctrl+Tab onto the front tab of the other group", "editor of group 1 showing a.txt");
      await page.keyboard.press("Control+Shift+Tab");
      await expectFocus("Ctrl+Shift+Tab back", "editor of group 2 showing b.txt");
      await page.click(tabSel("a.txt"));
      if (!(await page.$(tabSel("c.txt")))) {
        await page.click('.editor-file[data-path="c.txt"]');
        await page.waitForSelector(`${tabSel("c.txt")}.active`, { timeout: 8000 });
      }
      await page.click(tabSel("a.txt"));
      await page.keyboard.press("Control+Tab");
      await expectFocus("Ctrl+Tab onto a tab behind in the same group", "editor of group 1 showing c.txt");
      return "both groups, click and Ctrl+Tab both ways";
    });

    await run("closing a tab behind and dragging a tab in the strip leave the focus in the editor in front", async () => {
      await page.click(tabSel("a.txt"));
      await expectFocus("before the close", "editor of group 1 showing a.txt");
      await page.click(`${tabSel("c.txt")} .editor-tab-state`);
      await page.waitForSelector(tabSel("c.txt"), { state: "detached", timeout: 6000 });
      await expectFocus("closing the tab behind with its cross", "editor of group 1 showing a.txt");
      await page.click('.editor-file[data-path="c.txt"]');
      await page.waitForSelector(`${tabSel("c.txt")}.active`, { timeout: 8000 });
      const from = await page.locator(tabSel("c.txt")).boundingBox();
      const to = await page.locator(tabSel("a.txt")).boundingBox();
      await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
      await page.mouse.down();
      await page.mouse.move(from.x + from.width / 2 - 8, from.y + from.height / 2, { steps: 3 });
      await page.mouse.move(to.x + 4, to.y + to.height / 2, { steps: 12 });
      await page.mouse.up();
      await page.waitForFunction(() => [...document.querySelectorAll(".editor-group")[0].querySelectorAll(".editor-tab")].map((t) => t.dataset.path).join() === "c.txt,a.txt", null, { timeout: 6000 })
        .catch(() => { throw new Error("the drag did not put c.txt in front of a.txt"); });
      await expectFocus("a drag that reorders the strip", "editor of group 1 showing c.txt");
      return "close behind and reorder both end in the editor";
    });

    await run("a viewer tab takes no editor focus, and a text tab after it takes it again", async () => {
      await page.setInputFiles("[data-editor-upload-input]", [{ name: "pic.png", mimeType: "image/png", buffer: Buffer.from(PNG, "base64") }]);
      await page.waitForSelector(".editor-upload-list", { timeout: 6000 });
      await page.click(".swal2-confirm");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 6000 });
      await page.click('.editor-file[data-path="pic.png"]');
      await page.waitForSelector(`${tabSel("pic.png")}.active`, { timeout: 8000 });
      await page.click(tabSel("c.txt"));
      await expectFocus("before the viewer", "editor of group 1 showing c.txt");
      await page.click(tabSel("pic.png"));
      await page.waitForSelector("[data-editor-viewer] img", { timeout: 8000 });
      await page.waitForFunction(() => !document.activeElement?.closest(".cm-editor"), null, { timeout: 4000 })
        .catch(async () => { throw new Error(`the viewer left the focus in an editor: ${await focusIs()}`); });
      await page.click(tabSel("c.txt"));
      await expectFocus("back from the viewer", "editor of group 1 showing c.txt");
      return "no editor focus on the image, the text tab takes it back";
    });

    await run("a tab switch under an open quick open leaves the quick open its focus", async () => {
      await page.keyboard.press("Control+Shift+O");
      await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 4000 });
      const front = await page.evaluate(() => document.querySelector(".editor-group-focused .editor-tab.active")?.dataset.path);
      await page.keyboard.press("Control+Tab");
      await page.waitForFunction((was) => document.querySelector(".editor-group-focused .editor-tab.active")?.dataset.path !== was, front, { timeout: 4000 });
      assert(await focusIn("[data-editor-quickopen-input]"), `the tab switch took the focus from the quick open: ${await focusIs()}`);
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]")?.hidden, null, { timeout: 4000 });
      await draftsSettled();
      return "the field kept it";
    });

    await run("a tab with an open git diff comes back to the front with the focus in its working copy side", async () => {
      shellUrl = await L.createShell(page, project);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      assert(await shellInput(shellUrl, "git init -q && git add -A && git -c user.email=e2e@example.com -c user.name=e2e -c commit.gpgsign=false commit -qm init\r") === 200, "the shell refused the commit");
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      const deadline = Date.now() + 30000;
      let head = null;
      while (Date.now() < deadline) {
        head = await page.evaluate((p) => fetch(`/projects/${encodeURIComponent(p)}/editor/git/file?path=a.txt`, { headers: { Accept: "application/json" } })
          .then((r) => r.json()).then((d) => d.content ?? null).catch(() => null), project);
        if (typeof head === "string") break;
        await new Promise((done) => setTimeout(done, 500));
      }
      assert(typeof head === "string", "a.txt never reached the first commit");
      assert(await write("a.txt", "alpha line changed\n") === 200, "changing a.txt failed");
      await page.evaluate(() => {
        const settings = JSON.parse(localStorage.getItem("dc-editor-settings") || "{}");
        localStorage.setItem("dc-editor-settings", JSON.stringify({ ...settings, diff_view: "side" }));
      });
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel("a.txt"), { timeout: 12000 });
      await page.click(tabSel("a.txt"));
      await page.click(tabSel("a.txt"), { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.locator(".dc-context-menu .dropdown-item", { hasText: /git diff/ }).first().click();
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      await page.click(tabSel("c.txt"));
      await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
      await expectFocus("over to c.txt in the same group", "editor of group 1 showing c.txt");
      await page.click(tabSel("a.txt"));
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      await expectFocus("back to the diff by a click", "editor of group 1 showing a.txt (working copy side)");
      await page.keyboard.press("Control+Shift+Tab");
      await expectFocus("Ctrl+Shift+Tab away from the diff", "editor of group 1 showing c.txt");
      await page.keyboard.press("Control+Tab");
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      await expectFocus("back to the diff by Ctrl+Tab", "editor of group 1 showing a.txt (working copy side)");
      await page.click(tabSel("a.txt"), { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.locator(".dc-context-menu .dropdown-item", { hasText: /git diff/ }).first().click();
      await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
      return "click and Ctrl+Tab both land in the working copy side";
    });

    await run("Ctrl+Shift+G in the editor opens the git sheet alone, find previous stays in the search panel", async () => {
      const panel = () => page.evaluate(() => {
        const el = document.querySelector(".cm-panel.cm-search");
        return !!el && el.getBoundingClientRect().height > 0;
      });
      const line = () => page.evaluate(() => Number(/^(\d+):/.exec(document.querySelector("[data-editor-pos]")?.textContent || "")?.[1] || 0));
      await page.locator(".editor-group").nth(1).locator(".cm-content").click();
      await expectFocus("before the shortcut", "editor of group 2 showing b.txt");
      await page.keyboard.press("Control+Shift+G");
      await page.waitForSelector(`${sheet.box}:not([hidden])`, { timeout: 4000 });
      assert((await page.textContent("[data-editor-sheet-title]")).trim() === "Git", "the sheet that opened is not the git sheet");
      assert(!(await panel()), "Ctrl+Shift+G opened the editor's search panel behind the git sheet");
      await page.keyboard.press("Control+Shift+G");
      await page.waitForFunction((box) => document.querySelector(box)?.hidden, sheet.box, { timeout: 4000 });
      await expectFocus("Ctrl+Shift+G closing the sheet", "editor of group 2 showing b.txt");
      assert(!(await panel()), "closing the git sheet left a search panel");

      await page.keyboard.press("Control+Home");
      await page.keyboard.press("Control+f");
      await page.waitForFunction(() => document.activeElement?.matches(".cm-search input[name=search]"), null, { timeout: 4000 });
      await page.keyboard.type("o");
      await page.keyboard.press("Enter");
      await page.keyboard.press("Enter");
      const down = await line();
      await page.keyboard.press("Shift+Enter");
      const up = await line();
      assert(down === 2 && up === 1, `find previous no longer walks back from the panel: line ${down} then ${up}`);
      await page.keyboard.press("Enter");
      await page.keyboard.press("Control+Shift+G");
      await page.waitForSelector(`${sheet.box}:not([hidden])`, { timeout: 4000 });
      assert(await line() === 2, `Ctrl+Shift+G in the search field also moved the selection to line ${await line()}`);
      await page.keyboard.press("Escape");
      await page.waitForFunction((box) => document.querySelector(box)?.hidden, sheet.box, { timeout: 4000 });
      return `git sheet without a search panel, the panel walked ${down} then ${up}`;
    });

    const termPanel = "[data-editor-term-panel]";
    const inTerminal = () => page.evaluate((p) => !!document.activeElement?.closest(p) && document.activeElement.tagName === "TEXTAREA", termPanel);
    const focusTerminal = async () => {
      if (!(await page.$(`${termPanel}:not([hidden])`))) await page.keyboard.press("Control+j");
      await page.waitForSelector(`${termPanel}:not([hidden])`, { timeout: 6000 });
      await page.waitForSelector(`${termPanel} [data-term-tab]`, { timeout: 15000 });
      await page.waitForSelector(`${termPanel} .editor-term-pane.active terminal-attach[embedded] .xterm-screen canvas`, { timeout: 15000 });
      await page.click(`${termPanel} .editor-term-pane.active .xterm-screen`);
      await page.waitForFunction((p) => !!document.activeElement?.closest(p) && document.activeElement.tagName === "TEXTAREA", termPanel, { timeout: 4000 })
        .catch(async () => { throw new Error(`a click into the terminal left the focus on ${await focusIs()}`); });
    };
    const commitMessage = "[data-editor-commit-message]";
    const focusCommitMessage = async () => {
      if (!(await page.$("[data-editor-commit]:not([hidden])"))) {
        await page.waitForSelector("[data-editor-commit-toggle]:not([hidden])", { timeout: 15000 });
        await page.click("[data-editor-commit-toggle]");
        await page.waitForSelector("[data-editor-commit]:not([hidden])", { timeout: 10000 });
      }
      await page.click(commitMessage);
      assert(await focusIn(commitMessage), `a click into the commit message left the focus on ${await focusIs()}`);
    };
    const frontTab = () => page.evaluate(() => document.querySelector(".editor-group-focused .editor-tab.active")?.dataset.path || "");

    await run("closing the palette, the quick open or a sheet without acting gives the focus back to the terminal it came from", async () => {
      await focusTerminal();
      await page.click("[data-editor-project-switch]");
      await page.waitForSelector("[data-editor-palette]:not([hidden])", { timeout: 4000 });
      assert(await focusIn("[data-editor-project-filter]"), "the palette's field did not take the focus");
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => document.querySelector("[data-editor-palette]")?.hidden, null, { timeout: 4000 });
      assert(await inTerminal(), `Escape on the palette left the focus on ${await focusIs()}, not the terminal`);
      await page.click("[data-editor-menu]");
      await page.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
      await page.click("[data-editor-quick-open-item]");
      await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 4000 });
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]")?.hidden, null, { timeout: 4000 });
      await draftsSettled();
      assert(await inTerminal(), `Escape on the quick open left the focus on ${await focusIs()}, not the terminal`);
      await page.click("[data-editor-git-status]");
      await page.waitForSelector(`${sheet.box}:not([hidden])`, { timeout: 4000 });
      await page.keyboard.press("Escape");
      await page.waitForFunction((box) => document.querySelector(box)?.hidden, sheet.box, { timeout: 4000 });
      assert(await inTerminal(), `Escape on the git sheet left the focus on ${await focusIs()}, not the terminal`);
      return "palette, quick open and git sheet all closed back into the terminal";
    });

    await run("Ctrl+Tab keeps the focus where it stands outside an editor: the terminal keeps it, the commit message keeps it while the tab switches", async () => {
      await focusTerminal();
      await page.keyboard.press("Control+Tab");
      assert(await inTerminal(), `Ctrl+Tab took the focus out of the terminal to ${await focusIs()}`);
      await focusCommitMessage();
      const before = await frontTab();
      await page.keyboard.press("Control+Tab");
      await page.waitForFunction((was) => document.querySelector(".editor-group-focused .editor-tab.active")?.dataset.path !== was, before, { timeout: 4000 })
        .catch(() => { throw new Error("Ctrl+Tab from the commit message did not switch the tab"); });
      assert(await focusIn(commitMessage), `Ctrl+Tab took the focus out of the commit message to ${await focusIs()}`);
      return `the terminal kept it, the commit message kept it while ${before} gave way to ${await frontTab()}`;
    });

    await run("a sheet closed over the commit message gives it back, and a tab click then focuses that tab's editor", async () => {
      await focusCommitMessage();
      await page.keyboard.press("Control+Shift+G");
      await page.waitForSelector(`${sheet.box}:not([hidden])`, { timeout: 4000 });
      await page.click("[data-editor-sheet-close]");
      await page.waitForFunction((box) => document.querySelector(box)?.hidden, sheet.box, { timeout: 4000 });
      assert(await focusIn(commitMessage), `closing the git sheet left the focus on ${await focusIs()}, not the commit message`);
      await page.click(tabSel("b.txt"));
      await expectFocus("a tab click from the commit message", "editor of group 2 showing b.txt");
      await page.click("[data-editor-commit-close]");
      await page.waitForSelector("[data-editor-commit]", { state: "hidden", timeout: 6000 });
      return "commit message back after the sheet, the editor after the tab click";
    });

    await run("an element gone while the palette stood falls back to the editor in front", async () => {
      await page.click(tabSel("b.txt"));
      await page.evaluate(() => {
        const button = document.createElement("button");
        button.dataset.focusProbe = "";
        button.textContent = "probe";
        document.querySelector("dc-editor").appendChild(button);
        button.focus();
      });
      assert(await focusIn("[data-focus-probe]"), "the probe button did not take the focus");
      await page.keyboard.press("Control+Shift+P");
      await page.waitForSelector("[data-editor-palette]:not([hidden])", { timeout: 4000 });
      await page.evaluate(() => document.querySelector("[data-focus-probe]").remove());
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => document.querySelector("[data-editor-palette]")?.hidden, null, { timeout: 4000 });
      await expectFocus("Escape after the element went", "editor of group 2 showing b.txt");
      return "the front editor took it";
    });

    await run("on a phone neither a tab tap nor a closed sheet gives the editor the focus", async () => {
      const mp = await mobilePage();
      await mp.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(mp);
      await mp.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      for (const path of ["a.txt", "b.txt"]) {
        if (await mp.$(tabSel(path))) continue;
        if (!(await mp.$(".editor.editor-drawer-open"))) {
          await mp.tap("[data-editor-drawer-toggle]");
          await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
        }
        await mp.tap(`.editor-file[data-path="${path}"]`);
        await mp.waitForSelector(`${tabSel(path)}.active`, { state: "attached", timeout: 8000 });
        await mp.waitForSelector(".editor.editor-drawer-open", { state: "detached", timeout: 6000 });
      }
      const behind = await mp.evaluate(() => [...document.querySelectorAll("[data-editor-tabs] .editor-tab")]
        .find((t) => !t.classList.contains("active") && t.getBoundingClientRect().width > 0)?.dataset.path);
      assert(behind, "the phone's strip shows no tab behind the front one");
      await mp.tap(tabSel(behind));
      await mp.waitForSelector(`${tabSel(behind)}.active`, { state: "attached", timeout: 6000 });
      const active = await mp.evaluate(() => `${document.activeElement?.tagName}.${String(document.activeElement?.className || "").split(" ")[0]}`);
      assert(!/cm-content/.test(active), `the tap focused the editor, which raises the keyboard: ${active}`);
      await mp.tap("[data-editor-menu]");
      await mp.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
      await mp.tap("[data-editor-files-item]");
      await mp.waitForSelector(`${sheet.box}:not([hidden])`, { timeout: 4000 });
      await mp.tap("[data-editor-sheet-close]");
      await mp.waitForFunction((box) => document.querySelector(box)?.hidden, sheet.box, { timeout: 4000 });
      const closed = await mp.evaluate(() => `${document.activeElement?.tagName}.${String(document.activeElement?.className || "").split(" ")[0]}`);
      assert(!/cm-content/.test(closed), `closing the sheet focused the editor, which raises the keyboard: ${closed}`);
      return `${behind} in front, focus on ${active}, after the sheet on ${closed}`;
    });
  } finally {
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await page.unroute("**/editor/file?**").catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
