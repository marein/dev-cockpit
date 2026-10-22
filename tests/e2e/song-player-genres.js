const { assert, sleep } = require("./lib");

// Not a runner of its own: song-player.js runs these checks with its own
// helpers. A sheet the page's scriptune cannot parse rejects the play and the
// player stops at once, so a title still playing a moment after its click
// proves its sheet parses in the browser.
const SHELVES = [
  { genre: "Space Ambient", icon: "ti-planet", art: "planet", color: "purple" },
  { genre: "Classic Rock", icon: "ti-guitar-pick", art: "guitar pick", color: "red" },
];

module.exports.genreChecks = async function genreChecks({ page, run, ready, openPanel, setFolds, state, waitPlaying, toggle }) {
  for (const { genre, icon, art, color } of SHELVES) {
    await run(`every ${genre} song plays through the page's scriptune under the genre's ${art} in ${color}`, async () => {
      await ready(page);
      await openPanel(page);
      await setFolds(page, [genre]);
      const keys = await page.$$eval(`dc-song-player [data-song-shelf="${genre}"] [data-song]`, (items) => items.map((i) => i.dataset.song));
      const lengths = [];
      for (const key of keys) {
        await page.click(`dc-song-player [data-song="${key}"]`);
        await page.waitForFunction((k) => document.querySelector("dc-song-player [data-song][aria-current]")?.dataset.song === k, key, { timeout: 15000 });
        await sleep(300);
        const now = await state(page);
        const length = await page.textContent("dc-song-player [data-song-length]");
        assert(now.key === key && now.genre === genre && now.mode === genre && /ti-player-stop/.test(now.icon), `${key} did not play: ${JSON.stringify(now)}`);
        assert(length !== "0:00", `${key} reads a length of ${length}`);
        lengths.push(`${now.now} ${length}`);
      }
      const shown = await page.evaluate((tone) => {
        const root = document.querySelector("dc-song-player");
        const glyph = root.querySelector("[data-song-art] .ti");
        const rgb = getComputedStyle(document.documentElement).getPropertyValue(`--tblr-${tone}-rgb`).split(",").map((part) => part.trim());
        return { icon: glyph.className, glyph: getComputedStyle(glyph, "::before").content, color: getComputedStyle(root.querySelector("[data-song-art]")).color, want: `rgb(${rgb.join(", ")})` };
      }, color);
      assert(shown.icon.split(" ").includes(icon) && shown.glyph !== "none" && shown.glyph !== "" && shown.color === shown.want, `the art ${JSON.stringify(shown)}`);
      await page.click(toggle);
      await waitPlaying(page, false);
      await setFolds(page, []);
      return lengths.join(", ");
    });
  }
};
