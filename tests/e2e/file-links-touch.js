// The touch checks of file-links.js at 360px, run by it on its own project,
// shell and files with its helpers: a tap on a printed path and on a copy view
// link opens the editor, a path the terminal wrapped over four rows keeps its
// links after a reload, and a tap on the rest of a path a program partly
// overwrote opens nothing.

module.exports = async function fileLinksTouch({ run, mobilePage, shell, waitLine, mirror, mark, cellOf, opens, stays, freshShellPage, overwriteTail, LINKED, FILE }) {
  const phone = async () => {
    const mp = await mobilePage();
    await mp.setViewportSize({ width: 360, height: 760 });
    return mp;
  };
  const printOnPhone = async (mp, lines, last) => {
    await freshShellPage(mp);
    await shell(mp, `clear; seq 12; ${lines}`);
    await waitLine(mp, last);
  };

  await run("touch: a tap on a printed path opens it at its line and column", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `printf '%s %s\\n' ${sh} 'tap ${LINKED}'`, m);
    await mp.evaluate(() => document.activeElement?.blur());
    const at = await cellOf(mp, m, LINKED);
    await opens(mp, () => mp.touchscreen.tap(at.x, at.y), { path: FILE, line: 3, col: 7 });
  });

  await run("touch: a path the terminal wrapped over four rows keeps the terminal and its links after a reload, at 360px", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `c=$(tput cols); d=deep/$(printf "%0$((3*c))d" 0); mkdir -p $d && touch $d/file.txt && printf '%s %s\\n%s\\n%s\\n%s\\n' ${sh}w $c $d/file.txt notes.txt ${sh}e`, `${m}e`);
    const cols = Number((await mirror(mp, "#terminal")).split("\n").find((line) => line.startsWith(`${m}w`)).split(" ")[1]);
    for (const [needle, down, path] of [["deep", 1, `deep/${"0".repeat(3 * cols)}/file.txt`], ["notes.txt", 5, "notes.txt"]]) {
      await freshShellPage(mp);
      await waitLine(mp, `${m}e`);
      await mp.evaluate(() => document.activeElement?.blur());
      const at = await cellOf(mp, `${m}w`, needle, { row: down });
      await opens(mp, () => mp.touchscreen.tap(at.x, at.y), { path });
    }
  });

  await run("touch: a tap on the rest of a path a program partly overwrote opens nothing", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, overwriteTail(m, sh), `${m}k`);
    await waitLine(mp, `${m} src/zz.txt`);
    await mp.evaluate(() => document.activeElement?.blur());
    const rest = await cellOf(mp, m, "src/");
    await mp.touchscreen.tap(rest.x, rest.y);
    await stays(mp, "a tap on the rest of an overwritten path");
    await mp.evaluate(() => document.activeElement?.blur());
    const fresh = await cellOf(mp, `${m}k`, "notes.txt");
    await opens(mp, () => mp.touchscreen.tap(fresh.x, fresh.y), { path: "notes.txt" });
  });

  await run("touch: a tap on a file link in the copy view opens the editor", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `printf '%s %s\\n' ${sh} 'copy ${LINKED}'`, m);
    await mp.locator("[data-terminal-copy]:visible").first().tap();
    const link = mp.locator(`terminal-copy a[data-file-link]:text-is("${LINKED}")`).last();
    await link.waitFor({ state: "visible", timeout: 10000 });
    await opens(mp, () => link.tap(), { path: FILE, line: 3, col: 7 });
  });
};
