const { assert, sleep } = require("./lib");

// Not a runner of its own: song-player.js runs this check with its own
// helpers. A sheet the page's scriptune cannot parse rejects the play and the
// player stops at once, so a title still playing a moment after its click
// proves its sheet parses in the browser.
module.exports.spaceChecks = async function spaceChecks({ page, run, ready, openPanel, setFolds, state, waitPlaying, toggle }) {
  await run("every Space Ambient song plays through the page's scriptune under the genre's planet in purple", async () => {
    await ready(page);
    await openPanel(page);
    await setFolds(page, ["Space Ambient"]);
    const keys = await page.$$eval('dc-song-player [data-song-shelf="Space Ambient"] [data-song]', (items) => items.map((i) => i.dataset.song));
    const lengths = [];
    for (const key of keys) {
      await page.click(`dc-song-player [data-song="${key}"]`);
      await page.waitForFunction((k) => document.querySelector("dc-song-player [data-song][aria-current]")?.dataset.song === k, key, { timeout: 15000 });
      await sleep(300);
      const now = await state(page);
      const length = await page.textContent("dc-song-player [data-song-length]");
      assert(now.key === key && now.genre === "Space Ambient" && now.mode === "Space Ambient" && /ti-player-stop/.test(now.icon), `${key} did not play: ${JSON.stringify(now)}`);
      assert(length !== "0:00", `${key} reads a length of ${length}`);
      lengths.push(`${now.now} ${length}`);
    }
    const art = await page.evaluate(() => {
      const root = document.querySelector("dc-song-player");
      const icon = root.querySelector("[data-song-art] .ti");
      const purple = getComputedStyle(document.documentElement).getPropertyValue("--tblr-purple-rgb").split(",").map((part) => part.trim());
      return { icon: icon.className, glyph: getComputedStyle(icon, "::before").content, color: getComputedStyle(root.querySelector("[data-song-art]")).color, purple: `rgb(${purple.join(", ")})` };
    });
    assert(/ti-planet/.test(art.icon) && art.glyph !== "none" && art.glyph !== "" && art.color === art.purple, `the art ${JSON.stringify(art)}`);
    await page.click(toggle);
    await waitPlaying(page, false);
    await setFolds(page, []);
    return lengths.join(", ");
  });
};
