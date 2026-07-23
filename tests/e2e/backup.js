const L = require("./lib");
const { assert, sleep, confirmSwal, BASE } = L;
const fs = require("fs");
const path = require("path");

// Backup (settings page /settings/backup): the export tab lists stored
// backups (download via GET /settings/backup/download?id=, delete with
// confirm), creation happens on /settings/backup/new: checkbox sections with
// dependency enforcement (dc-backup-sections, data-requires) and a
// mandatory password (framed AES-256-GCM, extension .dcbackup, the only
// accepted import shape), then a background job writes the archive under
// <state-dir>/backups and raises a notification
// (target "backup", link back to the page, page visit marks it read).
// Import: upload -> manifest inspect -> section selection -> apply, plus the
// overwrite review with merge, keep, restore.
// Gotchas: the runner MUST talk to a throwaway instance whose HOME is a
// scratch directory, an import of host sections would otherwise overwrite
// real files; the import checks select only the "settings" section and keep
// the restart checkbox off; restoring a cockpit file re-execs the instance,
// the check waits it out with a retry loop; submit clicks are form scoped
// (#backup-create), the layout carries hidden submit buttons; the runner
// creates a scratch project plus one scratch shell, the shell create writes
// the restore snapshot and makes the terminals section available for the
// dependency check.

L.runFeature("BACKUP", async ({ page, run }) => {
  const dlDir = fs.mkdtempSync("/tmp/dc-backup-e2e-");
  const project = `zzbk-${Date.now().toString(36)}`;
  const doneRows = () => page.locator('#settings-backup-export a[href^="/settings/backup/download"]');
  const createBackup = async (section, password = "s3cret") => {
    await page.goto(`${BASE}/settings/backup/new`, { waitUntil: "domcontentloaded" });
    await page.evaluate((sec) => {
      document.querySelectorAll('input[name="sections"]:not(:disabled)').forEach((cb) => {
        cb.checked = cb.value === sec;
      });
    }, section);
    await page.fill('#backup-create input[name="password"]', password);
    await page.click('#backup-create button[type="submit"]');
    await page.waitForURL(/settings\/backup(?!\/new)/, { timeout: 10000 });
  };
  const exportOnly = async (section, password = "s3cret") => {
    await page.goto(`${BASE}/settings/backup`, { waitUntil: "domcontentloaded" });
    const before = await doneRows().count();
    await createBackup(section, password);
    await page.waitForFunction((n) => {
      if (document.querySelector("#settings-backup-export .badge.bg-red-lt")) return true;
      return document.querySelectorAll('#settings-backup-export a[href^="/settings/backup/download"]').length > n;
    }, before, { timeout: 20000 });
    assert((await page.locator("#settings-backup-export .badge.bg-red-lt").count()) === 0, "backup job failed");
    assert((await doneRows().count()) > before, "backup never finished");
    const [download] = await Promise.all([
      page.waitForEvent("download", { timeout: 15000 }),
      doneRows().first().click(),
    ]);
    const file = path.join(dlDir, download.suggestedFilename());
    await download.saveAs(file);
    return file;
  };
  const uploadArchive = async (file, password = "s3cret") => {
    await page.goto(`${BASE}/settings/backup?tab=import`, { waitUntil: "domcontentloaded" });
    await page.setInputFiles('input[name="archive"]', file);
    await page.fill('form:has(input[name="archive"]) input[name="password"]', password);
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

  await L.createProject(page, project);
  const shellUrl = await L.createShell(page, project);
  await sleep(500);

  await run("export tab lists backups, create button leads to the form", async () => {
    await page.goto(`${BASE}/settings/backup`, { waitUntil: "domcontentloaded" });
    assert(await page.isVisible('a[href="/settings/backup"].active'), "nav entry missing");
    const rows = await page.locator("#settings-backup-export .list-group-item").count();
    const text = await page.textContent("#settings-backup-export");
    assert(rows > 0 || text.includes("No backups yet"), "neither backup rows nor the empty state render");
    const missingList = await L.waitUpgraded(page, ["dc-backup-list"]);
    assert(!missingList.length, `not upgraded: ${missingList}`);
    await page.click('#settings-backup-export a[href="/settings/backup/new"]');
    await page.waitForSelector("dc-backup-sections", { timeout: 8000 });
    const missing = await L.waitUpgraded(page, ["dc-backup-sections"]);
    assert(!missing.length, `not upgraded: ${missing}`);
    for (const label of ["Cockpit", "Coders", "Host"]) {
      assert(await page.locator("dc-backup-sections .form-label", { hasText: label }).count(), `group ${label} missing`);
    }
    const disabled = await page.locator('input[name="sections"]:disabled').count();
    assert(disabled > 0, "fresh instance should show unavailable sections as disabled");
  });

  await run("section dependencies select and deselect each other", async () => {
    await page.goto(`${BASE}/settings/backup/new`, { waitUntil: "domcontentloaded" });
    const box = (v) => page.locator(`input[name="sections"][value="${v}"]`);
    assert(!(await box("terminals").isDisabled()), "terminals should be available after the shell create");
    const terminalsRow = page.locator('.form-check:has(input[value="terminals"])');
    const rowText = await terminalsRow.textContent();
    assert(rowText.includes("Needs Projects") && rowText.includes("Claude sessions"), "visible dependency line missing");
    await box("projects").uncheck();
    assert(!(await box("terminals").isChecked()), "unchecking projects left terminals checked");
    await box("terminals").check();
    assert(await box("projects").isChecked(), "checking terminals did not pull projects in");
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

  await run("create without a selection flashes an error", async () => {
    await page.goto(`${BASE}/settings/backup/new`, { waitUntil: "domcontentloaded" });
    await page.evaluate(() => {
      document.querySelectorAll('input[name="sections"]').forEach((cb) => { cb.checked = false; });
    });
    await page.fill('#backup-create input[name="password"]', "s3cret");
    await page.click('#backup-create button[type="submit"]');
    await sleep(800);
    assert((await page.textContent("body")).includes("Select at least one section"), "no flash shown");
  });

  let archive = null;
  await run("backup runs in the background, list updates live, notification says ready", async () => {
    await setRestoreSetting(true);
    await page.goto(`${BASE}/settings/backup`, { waitUntil: "domcontentloaded" });
    await createBackup("settings");
    assert((await page.textContent("#settings-backup-export")).includes("created in the background"), "started flash missing");
    archive = await exportOnly("settings");
    assert(/dev-cockpit-backup_.*\.dcbackup$/.test(archive), `unexpected name ${archive}`);
    assert(fs.statSync(archive).size > 100, "archive suspiciously small");
    await page.goto(`${BASE}/settings/backup`, { waitUntil: "domcontentloaded" });
    const data = await page.evaluate(async () => (await fetch("/notifications", { headers: { Accept: "application/json" } })).json());
    const note = (data.notifications || []).find((n) => n.targetId === "backup");
    assert(note, "backup notification missing");
    assert(note.url === "/settings/backup", `unexpected notification ${JSON.stringify(note)}`);
    assert(/^Backup "dev-cockpit-backup_.*" ready\.$/.test(note.title || ""), `unexpected title ${note.title}`);
    assert(note.read === true, "visiting the backup page should have marked the notification read");
  });

  await run("delete removes a backup after a confirm, flash names it", async () => {
    await page.goto(`${BASE}/settings/backup`, { waitUntil: "domcontentloaded" });
    const before = await page.locator("#settings-backup-export .list-group-item").count();
    assert(before >= 2, `expected at least 2 backups, got ${before}`);
    const name = (await page.locator("#settings-backup-export .list-group-item code").first().textContent()).trim();
    await page.locator('#settings-backup-export form:has(input[value="backup-delete"]) button').first().click();
    await confirmSwal(page);
    await page.waitForFunction((n) => document.querySelectorAll("#settings-backup-export .list-group-item").length === n - 1, before, { timeout: 8000 });
    const flash = await page.textContent("#settings-backup-export");
    assert(flash.includes("deleted") && flash.includes(name), `flash misses the name: ${flash.trim().slice(0, 120)}`);
  });

  await run("import upload shows a progress bar via dc-backup-upload", async () => {
    await page.goto(`${BASE}/settings/backup?tab=import`, { waitUntil: "domcontentloaded" });
    const missing = await L.waitUpgraded(page, ["dc-backup-upload"]);
    assert(!missing.length, `not upgraded: ${missing}`);
    assert(await page.locator("dc-backup-upload [data-upload-progress]").count() === 1, "progress bar missing");
    assert(await page.locator("dc-backup-upload form[data-no-pe]").count() === 1, "upload form is not data-no-pe");
  });

  await run("import inspect adapts to the file, apply restores the setting", async () => {
    await setRestoreSetting(false);
    await uploadArchive(archive);
    await page.waitForSelector("#backup-apply", { timeout: 8000 });
    assert(!(await L.waitUpgraded(page, ["dc-backup-sections"])).length, "apply form dc-backup-sections not upgraded");
    const rows = await page.locator('#backup-apply input[name="sections"]').count();
    assert(rows === 1, `expected 1 adaptive section, got ${rows}`);
    const label = await page.textContent("#backup-apply .form-check-label");
    assert(label.includes("Settings") && label.includes("files"), `unexpected row ${label}`);
    await page.uncheck('#backup-apply input[name="restart"]');
    await page.click('#backup-apply button.btn-primary');
    await confirmSwal(page);
    await page.waitForURL(/settings\/backup(?!\?import)/, { timeout: 10000 });
    const flash = await page.textContent("#settings-backup-review");
    assert(flash.includes("Imported 1 sections") && flash.includes("resolve them below"), `flash says: ${flash.trim().slice(0, 160)}`);
    await page.goto(`${BASE}/settings/general`, { waitUntil: "domcontentloaded" });
    assert(await page.isChecked('#settings-terminal-restore input[name="restore"]'), "setting not restored");
  });

  await run("clashing file lands in review, merge view diffs, keep resolves", async () => {
    await page.goto(`${BASE}/settings/backup?tab=import`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector("#settings-backup-review", { timeout: 8000 });
    const rows = page.locator("#settings-backup-review .list-group-item");
    assert((await rows.count()) === 1, `expected 1 review row, got ${await rows.count()}`);
    assert((await page.locator('.navbar a[href="/settings/general"] .status-dot').count()) > 0, "main nav news dot missing");
    assert((await page.locator('a[href="/settings/backup"] .badge').last().textContent()).trim() === "1", "backup sub-nav badge missing the review count");
    assert((await rows.first().textContent()).includes("settings.json"), "settings.json row missing");
    await rows.first().locator('a:has-text("Merge")').click();
    await page.waitForSelector('textarea[name="content"]', { timeout: 8000 });
    const imported = await page.inputValue('#backup-merge textarea[name="content"]');
    assert(imported.includes('"terminal-restore": "on"'), `imported pane wrong: ${imported.slice(0, 80)}`);
    const previous = await page.inputValue("#backup-merge textarea[readonly]");
    assert(previous.includes('"terminal-restore": "off"'), `previous pane wrong: ${previous.slice(0, 80)}`);
    await page.click('form:has(input[value="keep"]) button');
    await page.waitForURL(/settings\/backup(?!\/merge)/, { timeout: 8000 });
    assert(!(await page.isVisible("#settings-backup-review")), "review entry not resolved by keep");
    const flash = await page.textContent("#settings-backup-import");
    assert(flash.includes("Kept the imported file. All files are resolved."), `resolve flash missing: ${flash.trim().slice(0, 120)}`);
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

  await run("an unencrypted tar.gz is rejected", async () => {
    const gzFile = path.join(dlDir, "plain.tar.gz");
    fs.writeFileSync(gzFile, require("zlib").gzipSync(Buffer.from("not a container")));
    await uploadArchive(gzFile);
    assert((await page.textContent("#settings-backup-import")).includes("not a Dev Cockpit backup"), "unencrypted gzip accepted");
  });

  await run("a foreign file is rejected", async () => {
    const foreign = path.join(dlDir, "foreign.tar.gz");
    fs.writeFileSync(foreign, "definitely not a backup");
    await uploadArchive(foreign);
    assert((await page.textContent("#settings-backup-import")).includes("not a Dev Cockpit backup"), "foreign file accepted");
  });

  await L.deleteShell(page, shellUrl).catch(() => {});
  await L.deleteProject(page, project).catch(() => {});
  fs.rmSync(dlDir, { recursive: true, force: true });
});
