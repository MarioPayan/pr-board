#!/usr/bin/env node
// Regenerates the README screenshots from `pr-board demo`.
//
//   node scripts/shots.mjs [outdir]     (default: docs/screenshots)
//
// It builds pr-board from this checkout, serves the demo on a free port,
// takes the screenshots with Playwright (fetched with npx, nothing is added
// to the repository) and stops the server. Needs Go, Node and a Playwright
// Chromium (`npx playwright install chromium` once).
import { spawn, spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import { mkdtempSync, rmSync } from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const PLAYWRIGHT = "playwright@1.61.0";
const self = fileURLToPath(import.meta.url);
const root = path.resolve(path.dirname(self), "..");
const out = path.resolve(process.argv[2] || path.join(root, "docs", "screenshots"));

// run again under npx, which puts Playwright's node_modules/.bin on PATH
if (!process.env.PR_BOARD_SHOTS) {
  const r = spawnSync("npx", ["--yes", "-p", PLAYWRIGHT, "node", self, out], {
    stdio: "inherit",
    env: { ...process.env, PR_BOARD_SHOTS: "1" },
  });
  process.exit(r.status ?? 1);
}
const bin = process.env.PATH.split(path.delimiter).find(p => /_npx[\\/][^\\/]+[\\/]node_modules[\\/]\.bin$/.test(p));
if (!bin) throw new Error("Playwright from npx not found on PATH");
const { chromium } = createRequire(path.join(bin, "..", "noop.js"))("playwright");

const freePort = () => new Promise((resolve, reject) => {
  const s = net.createServer();
  s.on("error", reject);
  s.listen(0, "127.0.0.1", () => { const { port } = s.address(); s.close(() => resolve(port)); });
});

const tmp = mkdtempSync(path.join(os.tmpdir(), "pr-board-shots-"));
const exe = path.join(tmp, "pr-board");
const build = spawnSync("go", ["build", "-o", exe, "./cmd/pr-board"], { cwd: root, stdio: "inherit" });
if (build.status !== 0) process.exit(build.status ?? 1);

const port = await freePort();
const base = `http://127.0.0.1:${port}/`;
const server = spawn(exe, ["demo", "--port", String(port)], { stdio: ["ignore", "ignore", "inherit"] });
let browser;
try {
  for (let i = 0; ; i++) {
    try { if ((await fetch(base + "api/prs")).ok) break; } catch {}
    if (i > 100) throw new Error("the demo server did not answer on " + base);
    await new Promise(r => setTimeout(r, 100));
  }
  browser = await chromium.launch();
  const shot = async (name, hash, scheme, fullPage, before) => {
    const ctx = await browser.newContext({ viewport: { width: 1360, height: 900 }, colorScheme: scheme });
    const page = await ctx.newPage();
    const errors = [];
    page.on("console", m => { if (m.type() === "error") errors.push(m.text()); });
    page.on("pageerror", e => errors.push(String(e)));
    await page.goto(base + hash);
    await page.waitForSelector("[data-key]", { state: "attached" });
    await page.evaluate(() => document.fonts.ready);
    await page.waitForTimeout(300);
    if (before) await before(page);
    const file = path.join(out, name);
    await page.screenshot({ path: file, fullPage });
    // every row of one kind has one height
    const heights = await page.$$eval("[data-key]", els => {
      const h = {};
      for (const e of els) {
        if (!e.offsetParent) continue;
        const kind = e.classList.contains("merged") ? "merged" : e.classList.contains("rv") ? "review" : "open";
        (h[kind] ||= new Set()).add(Math.round(e.getBoundingClientRect().height));
      }
      return Object.fromEntries(Object.entries(h).map(([k, v]) => [k, [...v].join(",")]));
    });
    console.log(file, "row heights", JSON.stringify(heights), errors.length ? "console errors: " + errors.join(" | ") : "no console errors");
    await ctx.close();
  };
  await shot("pr-board-light.png", "", "light", true);
  await shot("pr-board-dark.png", "", "dark", true);
  await shot("pr-board-repo.png", "#group=repo", "light", true);
  await shot("pr-board-filtered.png", "#repo=web,design-system&state=missing,approve", "light", false);
  await shot("pr-board-legend.png", "", "light", false, async p => { await p.click("#pb-legend-btn"); await p.waitForTimeout(300); });
} finally {
  if (browser) await browser.close();
  server.kill("SIGINT");
  rmSync(tmp, { recursive: true, force: true });
}
