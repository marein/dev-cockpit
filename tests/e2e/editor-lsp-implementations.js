const L = require("./lib");
const { assert, sleep, BASE } = L;

// The implementations of a Go, PHP or TypeScript interface and the
// subclasses of a PHP, TypeScript, JavaScript or Python class in the usages
// list: on its declaration
// they stand under their own title above the usages, in the palette and in
// the phone's sheet, a line in one group only, a class's heritage on the
// lines below its name included, a group a filter empties without its
// title. A struct, an interface nothing implements, a class nothing extends
// and a type off its declaration show no group, and neither the object
// literal typed with an interface nor the class itself poses as one. A PHP
// interface lists the classes, the enum and the interface naming it in their
// clauses, one through an import alias in another namespace, a PHP class
// its subclasses, an anonymous one among them, the clauses no usages; an
// enum, a trait and a usage off the declaration show no group. A Python
// class lists the classes naming it among their bases, beside a metaclass,
// over lines, through an import alias and behind its module, the base lists
// no usages, a call naming it in a base list one; a class nothing extends
// and a usage off the declaration show no group. The types below another
// type below stand under their own title below the direct ones, in
// TypeScript as tsgo names them, in PHP and Python walked down, a chain
// longer than the walk goes and a server too slow for it ending in a note
// that the search stopped at its limit. Ctrl+Alt+U lists the same as
// Ctrl+click. The own action that listed them alone is gone: Ctrl+F12 does
// nothing, the editor's menu and the pill carry no entry, its route answers
// 404, and neither the shortcuts dialog nor /docs names it or Shift+F12.
// Routes: POST /projects/:name/editor/lsp/{definition,references}
// (internal/editorintelligence, implementations off gopls' type hierarchy,
// tsgo's implementation request and the clauses among intelephense's and the
// base lists among pyright's references, walked down for the indirect ones).
// Instance: the one editor-lsp.js runs on, the fake first on PATH as "gopls",
// "tsgo", "intelephense" and "pyright-langserver" (tests/e2e/fake-lsp.py),
// answering shape.go as gopls v0.23.0 did, Shape's subtypes being Solid, Square
// and wrapped, the other types having none, and shape.ts and animal.js as tsgo
// 7.0.0-dev did, and shape.php and circle.php as intelephense 1.18.5 did, its
// references naming a type in every clause and an alias apart, and none at a
// position in a file it has not opened, and shape.py, circle.py and chain.py as
// pyright 1.1.414 did, its references naming a class in every base list and an
// alias apart, each taking 1.5s while the project holds a .fake-lsp-slow-walk
// file.

const SHAPE_GO = `package lib

type Shape interface{ Area() float64 }

type Square struct{ Side float64 }

func (s Square) Area() float64 { return s.Side * s.Side }

type Solid interface {
	Shape
	Volume() float64
}

type wrapped struct{ Shape }

func area(s Shape) float64 { return s.Area() }
`;

const SHAPE_TS = `export interface Shape {
  area(): number;
}

export interface Solid extends Shape {
  volume(): number;
}

export class Square implements Shape {
  area(): number { return 1; }
}

export class Tile extends Square {}

export class Wide
  extends Square
  implements Shape {
  area(): number { return 2; }
}

export const Anon = class implements Shape {
  area(): number { return 3; }
};

export const unit: Shape = { area: () => 0 };

export function total(shapes: Shape[]): number {
  return shapes.length;
}

export class Plain {}
`;

const ANIMAL_JS = `export class Animal {
  speak() { return ""; }
}

export class Dog extends Animal {
  speak() { return "woof"; }
}

export const Cat = class extends Animal {};
`;

const SHAPE_PHP = `<?php
namespace App;

/**
 * Shapes.
 */
interface Shape {}

interface Solid extends
    Shape {}

abstract class Base implements Shape {}

final class Square extends Base implements Shape {}

enum Suit: string implements Shape {}

function make(Shape $s): Shape {
    return new class extends Base {};
}

trait Painted {}
`;

const CIRCLE_PHP = `<?php
namespace App\\Round;

use App\\Shape as Form;
use App\\Base;

class Circle extends Base implements Form {}

$c = new Circle();
`;

const SHAPE_PY = `from abc import ABCMeta
from typing import Generic, TypeVar

T = TypeVar("T")


class Shape:
    pass


class Box(Generic[T]):
    pass


class Square(Shape, metaclass=ABCMeta):
    pass


class Framed(
    Box[int],
    Shape,
):
    pass


class Wide(Square):
    pass


def make(kind):
    return kind


class Built(make(Shape)):
    pass


def area(s: Shape) -> Shape:
    return s
`;

const CIRCLE_PY = `import shape
from shape import (
    Shape as Form,
)


class Circle(Form):
    pass


class Ring(shape.Shape):
    pass


c = Circle()
`;

// A chain of classes each extending the one before it, longer than the walk
// to the indirect subclasses goes.
const CHAIN_PY = ["class Link0:\n    pass\n", ...Array.from({ length: 54 }, (_, i) => `\n\nclass Link${i + 1}(Link${i}):\n    pass\n`)].join("");

L.runFeature("EDITOR-LSP-IMPLEMENTATIONS", async ({ engine, page, run, mobilePage }) => {
  const tag = `impl-${engine}-${Date.now().toString(36)}`;
  const project = `zzlspi-${tag}`;
  const base = `${BASE}/projects/${project}/editor`;

  const post = (path, body) => page.evaluate(async ({ url, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    const res = await fetch(url, {
      method: "POST",
      headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams(body).toString(),
    });
    if (!res.ok) throw new Error(`${url} -> ${res.status}`);
  }, { url: `${base}${path}`, body });

  const editorAt = (p = page) => p.evaluate(() => ({
    path: document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path || "",
    pos: document.querySelector("[data-editor-pos]")?.textContent || "",
  }));

  // What the list shows, top to bottom: a group's title as "# title", a row
  // as its title, each with the top of its box, the hidden ones left out.
  const shown = (p, selector) => p.evaluate((sel) => {
    const visible = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
    return [...document.querySelectorAll(sel)].filter(visible).map((el) => ({
      text: el.matches("[data-editor-usage-group]") ? `# ${el.textContent}` : el.title,
      top: el.getBoundingClientRect().top,
    }));
  }, selector);
  const PALETTE = "[data-editor-quickopen-list] [data-editor-usage-group], [data-editor-quickopen-list] .editor-quickopen-item";
  const SHEET = "[data-editor-sheet-body] [data-editor-usage-group], [data-editor-sheet-body] .editor-sheet-open";
  const palette = async () => {
    const entries = await shown(page, PALETTE);
    const state = await page.evaluate(() => ({
      placeholder: document.querySelector("[data-editor-quickopen-input]").placeholder,
      count: document.querySelector("[data-editor-quickopen-foot-left]").textContent,
      active: document.querySelector("[data-editor-quickopen-list] .editor-quickopen-item.active")?.title || "",
    }));
    return { ...state, entries };
  };
  const listed = (entries) => entries.map((e) => e.text).join(",");
  const topDown = (entries) => entries.every((e, i) => i === 0 || e.top > entries[i - 1].top);
  const GROUPED = "# Implementations,shape.go:5,shape.go:9,shape.go:14,# Usages,shape.go:3,shape.go:10,shape.go:16";
  const TS_IMPLEMENTED = "# Implementations,shape.ts:5,shape.ts:9,shape.ts:15,shape.ts:21,# Indirect implementations,shape.ts:13,# Usages,shape.ts:1,shape.ts:25,shape.ts:27";
  const TS_EXTENDED = "# Subclasses,shape.ts:13,shape.ts:15,# Usages,shape.ts:9";

  const waitPalette = (title) => page.waitForFunction((t) => !document.querySelector("[data-editor-quickopen]").hidden
    && document.querySelector("[data-editor-quickopen-input]").placeholder === t, title, { timeout: 20000 });
  const closePalette = async () => {
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
  };
  const usagesAt = async (lineNeedle, word) => {
    const pt = await L.wordPoint(page, lineNeedle, word);
    assert(pt, `word ${word} on the surface`);
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.press("Control+Alt+u");
  };

  const saveLSPSettings = async () => {
    await Promise.all([
      page.waitForResponse((r) => r.url().includes("/settings/editor/lsp") && r.request().method() === "POST", { timeout: 8000 }),
      page.click('#settings-editor-lsp button[type="submit"]'),
    ]);
    await page.waitForFunction(() => document.body.textContent.includes("Settings saved."), null, { timeout: 8000 });
  };
  const servers = [["go", "gopls"], ["php", "intelephense"], ["typescript", "tsgo"], ["python", "pyright"]];

  await run("setup: every language pinned to Docker, the fake answering", async () => {
    await page.goto(`${BASE}/settings/editor/lsp`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    const picked = await page.$$eval("#settings-editor-lsp select", (els) => els.map((sel) => `${sel.name}=${sel.value}`).join(","));
    const fresh = picked === servers.map(([id]) => `server_${id}=auto`).join(",");
    const pinned = picked === servers.map(([id, server]) => `server_${id}=${server}-docker`).join(",");
    assert(fresh || pinned, `Automatic or the Docker pin, got ${picked} (the runner needs the editor-lsp.js instance)`);
    if (fresh) {
      for (const [id, server] of servers) await page.selectOption(`select[name="server_${id}"]`, `${server}-docker`);
      await saveLSPSettings();
    }
  });

  const openFile = async (p, path, needle) => {
    await p.click(`.editor-item[data-path="${path}"]`);
    await p.waitForFunction((n) => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes(n)), needle, { timeout: 10000 });
  };

  await run("setup: a Go, TypeScript, PHP and Python project with interfaces, classes and their implementations", async () => {
    await L.createProject(page, project);
    await L.setEditorFiles(page, { autosave: false });
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    for (const [path, content] of [["go.mod", "module example.com/lib\n"], ["shape.go", SHAPE_GO], ["shape.ts", SHAPE_TS], ["animal.js", ANIMAL_JS], ["shape.php", SHAPE_PHP], ["circle.php", CIRCLE_PHP], ["shape.py", SHAPE_PY], ["circle.py", CIRCLE_PY], ["chain.py", CHAIN_PY]]) {
      await post("/create", { path });
      await post("/file", { path, content });
    }
    await page.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.click('.editor-item[data-path="shape.go"]');
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes("type wrapped")), null, { timeout: 10000 });
  });

  await run("ctrl+click on an interface's declaration lists its implementations above the usages", async () => {
    const pt = await L.wordPoint(page, "type Shape interface", "Shape");
    await page.keyboard.down("Control");
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.up("Control");
    await waitPalette('3 implementations and 3 usages of "Shape"');
    const state = await palette();
    // wrapped embeds Shape on the line of its own name: that line stands
    // among the implementations alone.
    assert(listed(state.entries) === GROUPED, `the groups, got ${listed(state.entries)}`);
    assert(topDown(state.entries), `each entry stands below the one before: ${JSON.stringify(state.entries)}`);
    assert(state.count === "3 implementations, 3 usages", `the count, got "${state.count}"`);
    assert(state.active === "shape.go:5", `the first implementation is marked, got "${state.active}"`);
  });

  await run("the keys walk the rows past the group titles, enter opens the usage", async () => {
    for (let i = 0; i < 3; i++) await page.keyboard.press("ArrowDown");
    const active = (await palette()).active;
    assert(active === "shape.go:3", `three steps down lands on the first usage, got "${active}"`);
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    const at = await editorAt();
    assert(at.path === "shape.go" && at.pos === "3:6", `the usage opens, got ${JSON.stringify(at)}`);
  });

  await run("ctrl+alt+u lists the same groups, a filter that empties a group takes its title along", async () => {
    await usagesAt("type Shape interface", "Shape");
    await waitPalette('3 implementations and 3 usages of "Shape"');
    await page.fill("[data-editor-quickopen-input]", "func");
    let state = await palette();
    assert(listed(state.entries) === "# Usages,shape.go:16", `usages alone, got ${listed(state.entries)}`);
    assert(state.count === "1 usage", `the count names the group left, got "${state.count}"`);
    await page.fill("[data-editor-quickopen-input]", "struct");
    state = await palette();
    assert(listed(state.entries) === "# Implementations,shape.go:5,shape.go:14", `implementations alone, got ${listed(state.entries)}`);
    assert(state.count === "2 implementations", `the count names the group left, got "${state.count}"`);
    await page.fill("[data-editor-quickopen-input]", "");
    state = await palette();
    assert(listed(state.entries) === GROUPED, `an emptied box shows both groups again, got ${listed(state.entries)}`);
  });

  await run("an implementation row opens the type's declaration", async () => {
    await page.click('[data-editor-quickopen-list] .editor-quickopen-item[title="shape.go:9"]');
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    const at = await editorAt();
    assert(at.path === "shape.go" && at.pos === "9:6", `Solid's declaration opens, got ${JSON.stringify(at)}`);
  });

  await run("a struct's usages carry no implementations group", async () => {
    await usagesAt("type Square struct", "Square");
    await waitPalette('2 usages of "Square"');
    const state = await palette();
    assert(listed(state.entries) === "shape.go:5,shape.go:7", `the usages alone, no group title, got ${listed(state.entries)}`);
    assert(state.count === "2 usages", `the count, got "${state.count}"`);
    await closePalette();
  });

  await run("an interface nothing implements shows no empty group", async () => {
    await usagesAt("type Solid interface", "Solid");
    await waitPalette('1 usage of "Solid"');
    const state = await palette();
    assert(listed(state.entries) === "shape.go:9", `the declaration alone, no group title, got ${listed(state.entries)}`);
    await closePalette();
  });

  await run("the usages of an interface off its declaration carry no group", async () => {
    await usagesAt("func area", "Shape");
    await waitPalette('4 usages of "Shape"');
    const state = await palette();
    assert(listed(state.entries) === "shape.go:3,shape.go:10,shape.go:14,shape.go:16", `the usages alone, got ${listed(state.entries)}`);
    await closePalette();
  });

  await run("ctrl+click on a TS interface's declaration lists its classes and interfaces above the usages", async () => {
    await openFile(page, "shape.ts", "export class Plain");
    const pt = await L.wordPoint(page, "export interface Shape", "Shape");
    await page.keyboard.down("Control");
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.up("Control");
    await waitPalette('4 implementations, 1 indirect implementation and 3 usages of "Shape"');
    const state = await palette();
    // Wide with Shape two lines below its name, which stays its row alone,
    // and Anon by its class expression; Tile through Square apart below
    // them; the object literal typed with Shape stays a usage on line 25.
    assert(listed(state.entries) === TS_IMPLEMENTED, `the groups, got ${listed(state.entries)}`);
    assert(topDown(state.entries), `each entry stands below the one before: ${JSON.stringify(state.entries)}`);
    assert(state.count === "4 implementations, 1 indirect implementation, 3 usages", `the count, got "${state.count}"`);
    await page.click('[data-editor-quickopen-list] .editor-quickopen-item[title="shape.ts:21"]');
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    const at = await editorAt();
    assert(at.path === "shape.ts" && at.pos === "21:21", `the class expression opens, got ${JSON.stringify(at)}`);
  });

  await run("a TS class lists the classes extending it as subclasses, not itself, ctrl+alt+u alike", async () => {
    await usagesAt("export class Square", "Square");
    await waitPalette('2 subclasses and 1 usage of "Square"');
    let state = await palette();
    assert(listed(state.entries) === TS_EXTENDED, `the groups, got ${listed(state.entries)}`);
    assert(state.count === "2 subclasses, 1 usage", `the count, got "${state.count}"`);
    assert(state.active === "shape.ts:13", `the first subclass is marked, got "${state.active}"`);
    await page.fill("[data-editor-quickopen-input]", "Tile");
    state = await palette();
    assert(listed(state.entries) === "# Subclasses,shape.ts:13" && state.count === "1 subclass", `subclasses alone, got ${listed(state.entries)} "${state.count}"`);
    await page.fill("[data-editor-quickopen-input]", "implements");
    state = await palette();
    assert(listed(state.entries) === "# Usages,shape.ts:9" && state.count === "1 usage", `usages alone, got ${listed(state.entries)} "${state.count}"`);
    await closePalette();
  });

  await run("a TS interface nothing implements, a class nothing extends and a type off its declaration show no group", async () => {
    for (const [needle, word, title, rows] of [
      ["export interface Solid", "Solid", '1 usage of "Solid"', "shape.ts:5"],
      ["export class Plain", "Plain", '1 usage of "Plain"', "shape.ts:31"],
      ["shapes: Shape", "Shape", '7 usages of "Shape"', "shape.ts:1,shape.ts:5,shape.ts:9,shape.ts:17,shape.ts:21,shape.ts:25,shape.ts:27"],
    ]) {
      await usagesAt(needle, word);
      await waitPalette(title);
      const state = await palette();
      assert(listed(state.entries) === rows, `${word}: the usages alone, got ${listed(state.entries)}`);
      await closePalette();
    }
  });

  await run("a JavaScript class lists its subclasses, a class expression among them", async () => {
    await openFile(page, "animal.js", "export const Cat");
    await usagesAt("export class Animal", "Animal");
    await waitPalette('2 subclasses and 1 usage of "Animal"');
    const state = await palette();
    assert(listed(state.entries) === "# Subclasses,animal.js:5,animal.js:9,# Usages,animal.js:1", `the groups, got ${listed(state.entries)}`);
    await page.click('[data-editor-quickopen-list] .editor-quickopen-item[title="animal.js:9"]');
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    const at = await editorAt();
    assert(at.path === "animal.js" && at.pos === "9:20", `Cat's class expression opens, got ${JSON.stringify(at)}`);
  });

  const PHP_IMPLEMENTED = "# Implementations,shape.php:9,shape.php:12,shape.php:14,shape.php:16,circle.php:7,# Indirect implementations,shape.php:19";
  const PHP_EXTENDED = "# Subclasses,shape.php:14,shape.php:19,circle.php:7";
  const rowText = (title) => page.evaluate((t) => document.querySelector(`[data-editor-quickopen-list] .editor-quickopen-item[title="${t}"]`)?.textContent || "", title);

  await run("ctrl+click on a PHP interface's declaration lists its classes, enum and interfaces above the usages", async () => {
    await openFile(page, "shape.php", "trait Painted");
    const pt = await L.wordPoint(page, "interface Shape {}", "Shape");
    await page.keyboard.down("Control");
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.up("Control");
    await waitPalette('5 implementations, 1 indirect implementation and 4 usages of "Shape"');
    const state = await palette();
    // Solid names Shape a line below its name, Circle through the alias
    // Form, the anonymous class extending Base stands apart below them;
    // their clauses are no usages, the import and the hints are.
    assert(listed(state.entries) === `${PHP_IMPLEMENTED},# Usages,shape.php:7,shape.php:18,shape.php:18,circle.php:4`, `the groups, got ${listed(state.entries)}`);
    assert(topDown(state.entries), `each entry stands below the one before: ${JSON.stringify(state.entries)}`);
    assert(state.count === "5 implementations, 1 indirect implementation, 4 usages", `the count, got "${state.count}"`);
    const solid = await rowText("shape.php:9");
    const suit = await rowText("shape.php:16");
    assert(solid.includes("interface Solid extends") && suit.includes("enum Suit"), `the rows show an interface and an enum as such, got "${solid}" "${suit}"`);
    await page.click('[data-editor-quickopen-list] .editor-quickopen-item[title="circle.php:7"]');
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden
      && document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "circle.php", null, { timeout: 5000 });
    const at = await editorAt();
    assert(at.path === "circle.php" && at.pos === "7:7", `Circle's declaration opens, got ${JSON.stringify(at)}`);
  });

  await run("a PHP abstract class lists its subclasses, an anonymous one and one in another namespace among them, ctrl+alt+u alike", async () => {
    await openFile(page, "shape.php", "trait Painted");
    await usagesAt("abstract class Base", "Base");
    await waitPalette('3 subclasses and 2 usages of "Base"');
    const state = await palette();
    assert(listed(state.entries) === `${PHP_EXTENDED},# Usages,shape.php:12,circle.php:5`, `the groups, got ${listed(state.entries)}`);
    assert((await rowText("shape.php:19")).includes("new class extends Base"), "the anonymous class's row shows it");
    await page.click('[data-editor-quickopen-list] .editor-quickopen-item[title="shape.php:19"]');
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    const at = await editorAt();
    assert(at.path === "shape.php" && at.pos === "19:16", `the anonymous class opens, got ${JSON.stringify(at)}`);
  });

  await run("a PHP enum, a trait and an interface off its declaration show no group", async () => {
    for (const [needle, word, title, rows] of [
      ["enum Suit", "Suit", '1 usage of "Suit"', "shape.php:16"],
      ["trait Painted", "Painted", '1 usage of "Painted"', "shape.php:22"],
      ["function make(Shape", "Shape", '8 usages of "Shape"', "shape.php:7,shape.php:10,shape.php:12,shape.php:14,shape.php:16,shape.php:18,shape.php:18,circle.php:4"],
    ]) {
      await usagesAt(needle, word);
      await waitPalette(title);
      const state = await palette();
      assert(listed(state.entries) === rows, `${word}: the usages alone, got ${listed(state.entries)}`);
      await closePalette();
    }
  });

  const PY_EXTENDED = "# Subclasses,shape.py:15,shape.py:19,circle.py:7,circle.py:11,# Indirect subclasses,shape.py:26";

  await run("ctrl+click on a Python class's declaration lists its subclasses above the usages", async () => {
    await openFile(page, "shape.py", "def area");
    const pt = await L.wordPoint(page, "class Shape:", "Shape");
    await page.keyboard.down("Control");
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.up("Control");
    await waitPalette('4 subclasses, 1 indirect subclass and 5 usages of "Shape"');
    const state = await palette();
    // Square beside its metaclass, Framed naming Shape two lines below its
    // name behind a generic base, Circle through the alias Form and Ring
    // behind its module; Wide through Square apart below them; their base
    // lists are no usages, the import, the hints and Built's call are.
    assert(listed(state.entries) === `${PY_EXTENDED},# Usages,shape.py:7,shape.py:34,shape.py:38,shape.py:38,circle.py:3`, `the groups, got ${listed(state.entries)}`);
    assert(topDown(state.entries), `each entry stands below the one before: ${JSON.stringify(state.entries)}`);
    assert(state.count === "4 subclasses, 1 indirect subclass, 5 usages", `the count, got "${state.count}"`);
    assert(state.active === "shape.py:15", `the first subclass is marked, got "${state.active}"`);
    assert((await rowText("shape.py:19")).includes("class Framed("), "Framed's row shows its declaration");
    await page.click('[data-editor-quickopen-list] .editor-quickopen-item[title="circle.py:11"]');
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden
      && document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "circle.py", null, { timeout: 5000 });
    const at = await editorAt();
    assert(at.path === "circle.py" && at.pos === "11:7", `Ring's declaration opens, got ${JSON.stringify(at)}`);
  });

  await run("a Python class nothing extends and a usage off the declaration show no group", async () => {
    await openFile(page, "shape.py", "def area");
    await usagesAt("class Square(", "Square");
    await waitPalette('1 subclass and 1 usage of "Square"');
    let state = await palette();
    assert(listed(state.entries) === "# Subclasses,shape.py:26,# Usages,shape.py:15", `Wide below Square, its base list no usage, got ${listed(state.entries)}`);
    await closePalette();
    for (const [needle, word, title, rows] of [
      ["class Built(", "Built", '1 usage of "Built"', "shape.py:34"],
      ["def area", "Shape", '8 usages of "Shape"', "shape.py:7,shape.py:15,shape.py:21,shape.py:34,shape.py:38,shape.py:38,circle.py:3,circle.py:11"],
    ]) {
      await usagesAt(needle, word);
      await waitPalette(title);
      state = await palette();
      assert(listed(state.entries) === rows, `${word}: the usages alone, got ${listed(state.entries)}`);
      await closePalette();
    }
  });

  await run("ctrl+alt+u on a Python class lists the indirect subclass apart, it keeps its title under a filter and its row opens the class", async () => {
    await openFile(page, "shape.py", "def area");
    await usagesAt("class Shape:", "Shape");
    await waitPalette('4 subclasses, 1 indirect subclass and 5 usages of "Shape"');
    await page.fill("[data-editor-quickopen-input]", "Wide");
    const state = await palette();
    assert(listed(state.entries) === "# Indirect subclasses,shape.py:26" && state.count === "1 indirect subclass", `the indirect group alone, got ${listed(state.entries)} "${state.count}"`);
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    const at = await editorAt();
    assert(at.path === "shape.py" && at.pos === "26:7", `Wide's declaration opens, got ${JSON.stringify(at)}`);
  });

  const CUT = "The search for indirect subclasses stopped at its limit.";
  const foot = () => page.evaluate(() => document.querySelector("[data-editor-quickopen-foot-right]").textContent);

  await run("a chain longer than the walk lists its first 50 indirect subclasses and says the search stopped", async () => {
    await openFile(page, "chain.py", "class Link0:");
    await usagesAt("class Link0:", "Link0");
    await waitPalette('1 subclass, 50 indirect subclasses and 1 usage of "Link0"');
    const state = await palette();
    const rows = state.entries.map((e) => e.text);
    assert(rows.slice(0, 3).join(",") === "# Subclasses,chain.py:5,# Indirect subclasses" && rows[3] === "chain.py:9" && rows[52] === "chain.py:205" && rows.slice(53).join(",") === "# Usages,chain.py:1", `the groups, got ${rows.join(",")}`);
    assert(state.count === "1 subclass, 50 indirect subclasses, 1 usage", `the count, got "${state.count}"`);
    assert(await foot() === CUT, `the note says the search stopped, got "${await foot()}"`);
    await page.fill("[data-editor-quickopen-input]", "Link9(");
    assert(await foot() === "", "a filtered list carries no note");
    await closePalette();
  });

  await run("a server too slow for the walk ends it in time and the list says the search stopped", async () => {
    await post("/create", { path: ".fake-lsp-slow-walk" });
    try {
      const started = Date.now();
      await usagesAt("class Link0:", "Link0");
      await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden
        && document.querySelector("[data-editor-quickopen-input]").placeholder.startsWith("1 subclass, "), null, { timeout: 20000 });
      const title = await page.evaluate(() => document.querySelector("[data-editor-quickopen-input]").placeholder);
      const took = Date.now() - started;
      const m = /^1 subclass, (\d+) indirect subclass(es)? and 1 usage of "Link0"$/.exec(title);
      assert(m && Number(m[1]) >= 1 && Number(m[1]) < 50, `the walk ended early, got "${title}"`);
      assert(took < 12000, `the walk ended within its time, took ${took} ms`);
      assert(await foot() === CUT, `the note says the search stopped, got "${await foot()}"`);
      await closePalette();
    } finally {
      await post("/delete", { path: ".fake-lsp-slow-walk" });
    }
  });

  await run("find implementations is gone: no menu entry, ctrl+f12 does nothing, its route answers 404", async () => {
    await openFile(page, "shape.ts", "export class Plain");
    const asked = [];
    const record = (req) => { if (req.url().includes("/editor/lsp/")) asked.push(req.url().replace(/^.*\/editor\/lsp\//, "")); };
    page.on("request", record);
    try {
      const pt = await L.wordPoint(page, "export class Square", "Square");
      await page.mouse.click(pt.x, pt.y);
      for (const key of ["Control+F12", "Meta+F12"]) await page.keyboard.press(key);
      await sleep(800);
    } finally {
      page.off("request", record);
    }
    const shown = await page.evaluate(() => document.querySelector("[data-editor-quickopen]").getClientRects().length > 0);
    assert(!shown && asked.length === 0, `Ctrl+F12 still acts: palette ${shown}, asked ${asked.join(",")}`);
    await page.click("[data-editor-menu]");
    await page.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
    const items = await page.evaluate(() => [...document.querySelectorAll("[data-editor-menu-list] .dropdown-item")].map((el) => el.textContent.trim()));
    await page.keyboard.press("Escape");
    assert(!items.some((t) => /implementation/i.test(t)) && !(await page.$("[data-editor-impl-item]")), `the menu still offers it: ${items.join(" | ")}`);
    const status = await page.evaluate(async (url) => {
      const res = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content },
        body: JSON.stringify({ client: "probe", path: "shape.ts", content: "export class Square {}\n", position: { line: 0, character: 14 } }),
      });
      return res.status;
    }, `${base}/lsp/implementations`);
    assert(status === 404, `the route answered ${status}`);
  });

  await run("the shortcuts dialog and /docs describe the group and the keys, the action nowhere", async () => {
    await page.click("[data-editor-menu]");
    await page.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
    await page.click('[data-editor-menu-list] [data-bs-target="#editor-shortcuts-modal"]');
    await L.modalShown(page, "editor-shortcuts-modal");
    const note = await page.evaluate(() => [...document.querySelectorAll("#editor-shortcuts-modal .dc-shortcuts-note")]
      .map((n) => n.textContent.trim()).find((n) => n.includes("subclasses")) || "");
    assert(note === "On a declaration, implementations and subclasses stand in their own groups above the usages.", `the dialog note reads "${note}"`);
    const dialog = await page.evaluate(() => {
      const row = [...document.querySelectorAll("#editor-shortcuts-modal .dc-shortcuts-row")].find((r) => r.firstElementChild.textContent === "Find usages");
      return { usages: row ? row.querySelector(".dc-shortcuts-keys").textContent.replace(/\s+/g, " ").trim() : "", text: document.querySelector("#editor-shortcuts-modal").textContent };
    });
    assert(dialog.usages === "Ctrl+Alt+U", `the dialog's usages row reads "${dialog.usages}"`);
    assert(!/Find implementations|Ctrl\+F12/.test(dialog.text), "the dialog still names the action");
    assert(!/Shift\+F12/.test(dialog.text), "the dialog still names Shift+F12");
    await page.click('#editor-shortcuts-modal [data-bs-dismiss="modal"]');
    await page.waitForFunction(() => !document.querySelector("#editor-shortcuts-modal").classList.contains("show"), null, { timeout: 5000 });
    await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
    const desc = await page.evaluate(() => [...document.querySelectorAll("#editor .list-group-item")]
      .find((el) => el.querySelector(".fw-medium")?.textContent === "Go to definition and usages")?.querySelector(".mt-1").textContent || "");
    assert(desc.includes("a small Look up action. Ctrl+Alt+U lists the usages at the cursor. On a declaration, the implementations of an interface and the subclasses of a class stand above the usages, the indirect ones below them. A target outside the project opens too,"), `the docs text: ${desc}`);
    const docs = await page.evaluate(() => {
      const el = [...document.querySelectorAll("#editor .list-group-item")].find((e) => e.querySelector(".fw-medium")?.textContent === "Go to definition and usages");
      return {
        keys: [...el.querySelectorAll(".dc-docs-item-head .text-nowrap")].map((alt) => [...alt.querySelectorAll("kbd")].map((k) => k.textContent).join("+")).join(" or "),
        text: document.body.textContent.replace(/\s+/g, " "),
      };
    });
    assert(docs.keys === "Ctrl+B or Ctrl+Alt+U", `the docs item's keys read "${docs.keys}"`);
    assert(!/Find implementations|Ctrl\+F12/.test(docs.text), "/docs still names the action");
    assert(!/Shift\+F12/.test(docs.text), "/docs still names Shift+F12");
  });

  // A tree row is tapped at its left end: its middle may lie where the word
  // tapped next in the file it opens stands, and two taps there read as a
  // double tap, which selects the word.
  const tapTreeItem = (mp, path) => mp.tap(`.editor-item[data-path="${path}"]`, { position: { x: 12, y: 8 } });

  await run("mobile: the sheet carries the implementations above the usages", async () => {
    const mp = await mobilePage();
    await mp.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(mp);
    await mp.waitForSelector("[data-editor-drawer-toggle]", { timeout: 10000 });
    await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 4000 }).catch(() => {});
    if (!(await mp.evaluate(() => !!document.querySelector(".editor.editor-drawer-open")))) {
      await mp.tap("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
    }
    await tapTreeItem(mp, "shape.go");
    await mp.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes("type wrapped")), null, { timeout: 10000 });
    const pt = await L.wordPoint(mp, "type Shape interface", "Shape");
    await mp.waitForFunction(({ x, y }) => !!document.elementFromPoint(x, y)?.closest(".cm-content"), pt, { timeout: 5000 });
    await mp.touchscreen.tap(pt.x, pt.y);
    await mp.waitForSelector("[data-editor-lsp-pill]", { state: "visible", timeout: 8000 });
    const labels = await mp.$$eval("[data-editor-lsp-pill] button", (els) => els.map((b) => b.textContent.trim()).join(","));
    assert(labels === "Look up", `the pill offers the lookup alone, got ${labels}`);
    await mp.tap("[data-pill-action]");
    await mp.waitForFunction(() => document.querySelector("[data-editor-sheet-title]")?.textContent === '3 implementations and 3 usages of "Shape"'
      && document.querySelector("[data-editor-sheet]").getClientRects().length > 0, null, { timeout: 20000 });
    const entries = await shown(mp, SHEET);
    assert(listed(entries) === GROUPED, `the sheet's groups, got ${listed(entries)}`);
    assert(topDown(entries), `each entry stands below the one before: ${JSON.stringify(entries)}`);
  });

  await run("mobile: the sheet's filter takes an emptied group's title along", async () => {
    const mp = await mobilePage();
    const input = mp.locator("[data-editor-sheet-body] input");
    await input.fill("func");
    assert(listed(await shown(mp, SHEET)) === "# Usages,shape.go:16", `usages alone, got ${listed(await shown(mp, SHEET))}`);
    await input.fill("struct");
    assert(listed(await shown(mp, SHEET)) === "# Implementations,shape.go:5,shape.go:14", `implementations alone, got ${listed(await shown(mp, SHEET))}`);
    await input.fill("");
    assert(listed(await shown(mp, SHEET)) === GROUPED, `both groups again, got ${listed(await shown(mp, SHEET))}`);
    await mp.locator('[data-editor-sheet-body] .editor-sheet-open[title="shape.go:14"]').click();
    await mp.waitForFunction(() => document.querySelector("[data-editor-sheet]").getClientRects().length === 0, null, { timeout: 10000 });
    const at = await editorAt(mp);
    assert(at.path === "shape.go" && at.pos.startsWith("14:"), `the row opens wrapped's declaration, got ${JSON.stringify(at)}`);
  });

  await run("mobile: the sheet carries a TS class's subclasses above the usages", async () => {
    const mp = await mobilePage();
    if (!(await mp.evaluate(() => !!document.querySelector(".editor.editor-drawer-open")))) {
      await mp.tap("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
    }
    await tapTreeItem(mp, "shape.ts");
    await mp.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes("export class Plain")), null, { timeout: 10000 });
    const pt = await L.wordPoint(mp, "export class Square", "Square");
    await mp.waitForFunction(({ x, y }) => !!document.elementFromPoint(x, y)?.closest(".cm-content"), pt, { timeout: 5000 });
    await mp.touchscreen.tap(pt.x, pt.y);
    await mp.waitForSelector("[data-editor-lsp-pill]", { state: "visible", timeout: 8000 });
    await mp.tap("[data-pill-action]");
    await mp.waitForFunction(() => document.querySelector("[data-editor-sheet-title]")?.textContent === '2 subclasses and 1 usage of "Square"'
      && document.querySelector("[data-editor-sheet]").getClientRects().length > 0, null, { timeout: 20000 });
    const entries = await shown(mp, SHEET);
    assert(listed(entries) === TS_EXTENDED, `the sheet's groups, got ${listed(entries)}`);
    assert(topDown(entries), `each entry stands below the one before: ${JSON.stringify(entries)}`);
    await mp.locator('[data-editor-sheet-body] .editor-sheet-open[title="shape.ts:13"]').click();
    await mp.waitForFunction(() => document.querySelector("[data-editor-sheet]").getClientRects().length === 0, null, { timeout: 10000 });
  });

  await run("mobile: a PHP interface's and a Python class's sheet carry the types below, the pill offers the lookup alone", async () => {
    const mp = await mobilePage();
    // Each needle stands in its PHP file alone, so neither the wait nor the
    // word's point reads the file open before.
    const tapWord = async (path, needle, word) => {
      if (!(await mp.evaluate(() => !!document.querySelector(".editor.editor-drawer-open")))) {
        await mp.tap("[data-editor-drawer-toggle]");
        await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
      }
      await tapTreeItem(mp, path);
      await mp.waitForFunction((n) => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes(n)), needle, { timeout: 10000 });
      const pt = await L.wordPoint(mp, needle, word);
      await mp.waitForFunction(({ x, y }) => !!document.elementFromPoint(x, y)?.closest(".cm-content"), pt, { timeout: 5000 });
      await mp.touchscreen.tap(pt.x, pt.y);
      await mp.waitForSelector("[data-editor-lsp-pill]", { state: "visible", timeout: 8000 });
    };
    await tapWord("shape.php", "interface Shape {}", "Shape");
    const labels = await mp.$$eval("[data-editor-lsp-pill] button", (els) => els.map((b) => b.textContent.trim()).join(","));
    assert(labels === "Look up", `the pill offers the lookup alone in PHP, got ${labels}`);
    await mp.tap("[data-pill-action]");
    await mp.waitForFunction(() => document.querySelector("[data-editor-sheet-title]")?.textContent === '5 implementations, 1 indirect implementation and 4 usages of "Shape"'
      && document.querySelector("[data-editor-sheet]").getClientRects().length > 0, null, { timeout: 20000 });
    let entries = await shown(mp, SHEET);
    assert(listed(entries) === `${PHP_IMPLEMENTED},# Usages,shape.php:7,shape.php:18,shape.php:18,circle.php:4`, `the sheet's groups, got ${listed(entries)}`);
    assert(topDown(entries), `each entry stands below the one before: ${JSON.stringify(entries)}`);
    await mp.locator('[data-editor-sheet-body] .editor-sheet-open[title="shape.php:12"]').click();
    await mp.waitForFunction(() => document.querySelector("[data-editor-sheet]").getClientRects().length === 0, null, { timeout: 10000 });

    await tapWord("shape.py", "class Shape:", "Shape");
    const pyLabels = await mp.$$eval("[data-editor-lsp-pill] button", (els) => els.map((b) => b.textContent.trim()).join(","));
    assert(pyLabels === "Look up", `the pill offers the lookup alone in Python, got ${pyLabels}`);
    await mp.tap("[data-pill-action]");
    await mp.waitForFunction(() => document.querySelector("[data-editor-sheet-title]")?.textContent === '4 subclasses, 1 indirect subclass and 5 usages of "Shape"'
      && document.querySelector("[data-editor-sheet]").getClientRects().length > 0, null, { timeout: 20000 });
    entries = await shown(mp, SHEET);
    assert(listed(entries) === `${PY_EXTENDED},# Usages,shape.py:7,shape.py:34,shape.py:38,shape.py:38,circle.py:3`, `the Python sheet's groups, got ${listed(entries)}`);
    assert(topDown(entries), `each entry stands below the one before: ${JSON.stringify(entries)}`);
    await mp.locator('[data-editor-sheet-body] .editor-sheet-open[title="shape.py:19"]').click();
    await mp.waitForFunction(() => document.querySelector("[data-editor-sheet]").getClientRects().length === 0, null, { timeout: 10000 });
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  });

  await run("cleanup: the runner's project", async () => {
    await L.deleteProject(page, project);
  });
});
