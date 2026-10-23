const { assert, sleep } = require("./lib");

// Not a runner of its own: song-player.js runs these checks with its own
// helpers. A sheet the page's scriptune cannot parse rejects the play and the
// player stops at once, so a title still playing a moment after its length
// shows proves its sheet parses in the browser. A click answers before its
// song is composed: the title turns current with the stop icon and a length
// of 0:00, and a frame of that is drawn before the length follows.
const SHELVES = [
  { genre: "Space Ambient", icon: "ti-planet", art: "planet", color: "purple" },
  { genre: "Classic Rock", icon: "ti-guitar-pick", art: "guitar pick", color: "red" },
];

async function watchAnswer(page, key) {
  await page.evaluate((k) => {
    const root = document.querySelector("dc-song-player");
    const read = () => ({
      key: root.querySelector("[data-song][aria-current]")?.dataset.song,
      icon: root.querySelector("[data-song-toggle] .ti").className,
      length: root.querySelector("[data-song-length]").textContent,
    });
    window.dcAnswer = null;
    const watch = new MutationObserver(() => {
      if (root.querySelector("[data-song][aria-current]")?.dataset.song !== k) return;
      watch.disconnect();
      window.dcAnswer = read();
      requestAnimationFrame(() => { window.dcAnswer.frame = read(); });
    });
    watch.observe(root, { subtree: true, attributes: true });
  }, key);
}

module.exports.genreChecks = async function genreChecks({ page, run, ready, openPanel, setFolds, state, waitPlaying, toggle }) {
  for (const { genre, icon, art, color } of SHELVES) {
    await run(`every ${genre} song answers its click before it is composed and plays through the page's scriptune under the genre's ${art} in ${color}`, async () => {
      await ready(page);
      await openPanel(page);
      await setFolds(page, [genre]);
      const keys = await page.$$eval(`dc-song-player [data-song-shelf="${genre}"] [data-song]`, (items) => items.map((i) => i.dataset.song));
      const lengths = [];
      for (const key of keys) {
        await watchAnswer(page, key);
        await page.click(`dc-song-player [data-song="${key}"]`);
        await page.waitForFunction((k) => document.querySelector("dc-song-player [data-song][aria-current]")?.dataset.song === k
          && document.querySelector("dc-song-player [data-song-length]").textContent !== "0:00", key, { timeout: 15000 });
        await sleep(300);
        const now = await state(page);
        const length = await page.textContent("dc-song-player [data-song-length]");
        const answer = await page.evaluate(() => window.dcAnswer);
        assert([answer, answer?.frame].every((seen) => seen?.key === key && /ti-player-stop/.test(seen.icon) && seen.length === "0:00"), `${key} answered its click only with its song: ${JSON.stringify(answer)}`);
        assert(now.key === key && now.genre === genre && now.mode === genre && /ti-player-stop/.test(now.icon), `${key} did not play: ${JSON.stringify(now)}`);
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
