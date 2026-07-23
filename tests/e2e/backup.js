const L = require("./lib");
const { assert, sleep, confirmSwal, BASE } = L;
const fs = require("fs");
const path = require("path");

// Backup (settings page /settings/backup): checkbox export of cockpit, coder
// and host state into one tar.gz (optional password -> framed AES-256-GCM,
// extension .dcbackup), adaptive import (upload -> manifest inspect -> section
// selection -> apply) plus optional server restart after the apply.
// Routes: GET/POST /settings/backup (forms inspect/apply/discard via hidden
// form field), POST /settings/backup/download (data-no-pe, gzip middleware
// skips /download paths).
// Gotchas: the runner MUST talk to a throwaway instance whose HOME is a
// scratch directory, an import of host sections would otherwise overwrite
// real files; the import checks here select only the "settings" section and
// keep the restart checkbox off, a restart would re-exec the instance.

L.runFeature("BACKUP", async ({ page, run }) => {
  const dlDir = fs.mkdtempSync("/tmp/dc-backup-e2e-");
  const exportOnly = async (section, password) => {
    await page.goto(`${BASE}/settings/backup`, { waitUntil: "domcontentloaded" });
    await page.evaluate((sec) => {
      document.querySelectorAll('#settings-backup-export input[name="sections"]:not(:disabled)').forEach((cb) => {
        cb.checked = cb.value === sec;
      });
    }, section);
    if (password) await page.fill('#settings-backup-export input[name="password"]', password);
    const [download] = await Promise.all([
      page.waitForEvent("download", { timeout: 15000 }),
      page.click('#settings-backup-export button[type="submit"]'),
    ]);
    const file = path.join(dlDir, download.suggestedFilename());
    await download.saveAs(file);
    return file;
  };
  const uploadArchive = async (file, password) => {
    await page.goto(`${BASE}/settings/backup?tab=import`, { waitUntil: "domcontentloaded" });
    await page.setInputFiles('input[name="archive"]', file);
    if (password) await page.fill('form:has(input[name="archive"]) input[name="password"]', password);
    await page.click('form:has(input[name="archive"]) button[type="submit"]');
    await sleep(800);
  };
  const setRestoreSetting = async (on) => {
    await page.goto(`${BASE}/settings/general`, { waitUntil: "domcontentloaded" });
    const cb = page.locator('#settings-terminal-restore input[name="restore"]');
    if (on) await cb.check(); else await cb.uncheck();
    await page.click('#settings-terminal-restore button[type="submit"]');
    await sleep(500);
  };

  await run("export page renders adaptive groups and nav entry", async () => {
    await page.goto(`${BASE}/settings/backup`, { waitUntil: "domcontentloaded" });
    assert(await page.isVisible('a[href="/settings/backup"].active'), "nav entry missing");
    const missing = await L.waitUpgraded(page, ["dc-backup-busy"]);
    assert(!missing.length, `not upgraded: ${missing}`);
    await page.waitForSelector("dc-backup-busy .spinner-border", { state: "attached", timeout: 8000 });
    const visible = await page.evaluate(() =>
      [...document.querySelectorAll("dc-backup-busy .spinner-border")]
        .filter((s) => getComputedStyle(s.parentElement).display !== "none").length);
    assert(visible === 0, `${visible} busy status lines visible without interaction`);
    for (const label of ["Cockpit", "Coders", "Host"]) {
      assert(await page.locator("#settings-backup-export .form-label", { hasText: label }).count(), `group ${label} missing`);
    }
    const disabled = await page.locator('#settings-backup-export input[name="sections"]:disabled').count();
    assert(disabled > 0, "fresh instance should show unavailable sections as disabled");
  });

  await run("tabs switch between export and import", async () => {
    await page.goto(`${BASE}/settings/backup`, { waitUntil: "domcontentloaded" });
    assert(await page.isVisible("#settings-backup-export"), "export pane not default");
    assert(!(await page.isVisible('input[name="archive"]')), "import pane leaking into export tab");
    await page.click('.nav-tabs a:has-text("Import")');
    await page.waitForSelector('input[name="archive"]', { timeout: 8000 });
    assert(!(await page.isVisible("#settings-backup-export")), "export pane leaking into import tab");
    await page.click('.nav-tabs a:has-text("Export")');
    await page.waitForSelector("#settings-backup-export", { timeout: 8000 });
  });

  await run("export without a selection flashes an error", async () => {
    await page.goto(`${BASE}/settings/backup`, { waitUntil: "domcontentloaded" });
    await page.evaluate(() => {
      document.querySelectorAll('#settings-backup-export input[name="sections"]').forEach((cb) => { cb.checked = false; });
    });
    await page.click('#settings-backup-export button[type="submit"]');
    await page.waitForURL(/settings\/backup/, { timeout: 8000 });
    assert((await page.textContent("#settings-backup-export")).includes("Select at least one section"), "no flash shown");
  });

  let archive = null;
  await run("export downloads a tar.gz of the selected sections", async () => {
    await setRestoreSetting(true);
    archive = await exportOnly("settings");
    assert(/dev-cockpit-backup_.*\.tar\.gz$/.test(archive), `unexpected name ${archive}`);
    assert(fs.statSync(archive).size > 100, "archive suspiciously small");
  });

  await run("export busy state clears once the download starts", async () => {
    await page.waitForFunction(() => {
      const btn = document.querySelector('#settings-backup-export button[type="submit"]');
      const status = document.querySelector("dc-backup-busy[download] .spinner-border");
      return btn && !btn.disabled && status && getComputedStyle(status.parentElement).display === "none";
    }, { timeout: 8000 });
  });

  await run("import inspect adapts to the file, apply restores the setting", async () => {
    await setRestoreSetting(false);
    await uploadArchive(archive);
    await page.waitForSelector("#backup-apply", { timeout: 8000 });
    const rows = await page.locator('#backup-apply input[name="sections"]').count();
    assert(rows === 1, `expected 1 adaptive section, got ${rows}`);
    const label = await page.textContent("#backup-apply .form-check-label");
    assert(label.includes("Settings") && label.includes("files"), `unexpected row ${label}`);
    await page.uncheck('#backup-apply input[name="restart"]');
    await page.click('#backup-apply button.btn-primary');
    await confirmSwal(page);
    await page.waitForURL(/settings\/backup(?!\?import)/, { timeout: 10000 });
    const flash = await page.textContent("#settings-backup-import");
    assert(flash.includes("Imported 1 sections"), `flash says: ${flash.trim().slice(0, 120)}`);
    await page.goto(`${BASE}/settings/general`, { waitUntil: "domcontentloaded" });
    assert(await page.isChecked('#settings-terminal-restore input[name="restore"]'), "setting not restored");
  });

  await run("clashing file lands in review, merge view diffs, keep resolves", async () => {
    await page.goto(`${BASE}/settings/backup?tab=import`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector("#settings-backup-review", { timeout: 8000 });
    const rows = page.locator("#settings-backup-review .list-group-item");
    assert((await rows.count()) === 1, `expected 1 review row, got ${await rows.count()}`);
    assert((await rows.first().textContent()).includes("settings.json"), "settings.json row missing");
    await rows.first().locator('a:has-text("Merge")').click();
    await page.waitForSelector('textarea[name="content"]', { timeout: 8000 });
    const diff = await page.textContent(".dc-diff");
    assert(diff.includes("terminal-restore"), "diff does not show the changed key");
    assert(await page.locator(".dc-diff-add").count() > 0, "no added lines marked");
    await page.click('form:has(input[value="keep"]) button');
    await page.waitForURL(/settings\/backup(?!\/merge)/, { timeout: 8000 });
    assert(!(await page.isVisible("#settings-backup-review")), "review entry not resolved by keep");
  });

  await run("restore previous puts the old file back", async () => {
    await setRestoreSetting(false);
    await uploadArchive(archive);
    await page.waitForSelector("#backup-apply", { timeout: 8000 });
    await page.uncheck('#backup-apply input[name="restart"]');
    await page.click("#backup-apply button.btn-primary");
    await confirmSwal(page);
    await page.waitForURL(/settings\/backup(?!\?import)/, { timeout: 10000 });
    const row = page.locator("#settings-backup-review .list-group-item").first();
    const restoreBtn = row.locator('form:has(input[value="review-restore"]) button');
    assert((await restoreBtn.textContent()).includes("and restart"), "cockpit file restore button lacks the restart hint");
    await restoreBtn.click();
    await confirmSwal(page);
    await sleep(3000);
    let up = false;
    for (let i = 0; i < 10 && !up; i += 1) {
      try {
        await page.goto(`${BASE}/settings/general`, { waitUntil: "domcontentloaded" });
        up = true;
      } catch {
        await sleep(1000);
      }
    }
    assert(up, "instance did not come back after the restore restart");
    assert(!(await page.isChecked('#settings-terminal-restore input[name="restore"]')), "old settings not restored");
  });

  await run("encrypted export, wrong password rejected, right one inspects, discard resets", async () => {
    const enc = await exportOnly("settings", "s3cret");
    assert(/\.dcbackup$/.test(enc), `unexpected name ${enc}`);
    await uploadArchive(enc, "wrong");
    assert((await page.textContent("#settings-backup-import")).includes("Wrong password"), "wrong password not rejected");
    await uploadArchive(enc, "s3cret");
    await page.waitForSelector("#backup-apply", { timeout: 8000 });
    await page.click('button[form="backup-discard"]');
    await page.waitForSelector('input[name="archive"]', { timeout: 8000 });
    assert((await page.textContent("#settings-backup-import")).includes("Import discarded"), "discard flash missing");
  });

  await run("a plain tar uploads too, the Safari auto gunzip case", async () => {
    const tarFile = path.join(dlDir, "unzipped.tar");
    fs.writeFileSync(tarFile, require("zlib").gunzipSync(fs.readFileSync(archive)));
    await uploadArchive(tarFile);
    await page.waitForSelector("#backup-apply", { timeout: 8000 });
    const rows = await page.locator('#backup-apply input[name="sections"]').count();
    assert(rows === 1, `expected 1 adaptive section, got ${rows}`);
    await page.click('button[form="backup-discard"]');
    await page.waitForSelector('input[name="archive"]', { timeout: 8000 });
  });

  await run("a foreign file is rejected", async () => {
    const foreign = path.join(dlDir, "foreign.tar.gz");
    fs.writeFileSync(foreign, "definitely not a backup");
    await uploadArchive(foreign);
    assert((await page.textContent("#settings-backup-import")).includes("not a dev-cockpit backup"), "foreign file accepted");
  });

  fs.rmSync(dlDir, { recursive: true, force: true });
});
