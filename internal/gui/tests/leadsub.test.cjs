const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const assets = path.resolve(__dirname, "../assets");

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(`${engine}: leadsub renders, selects groups, reads back writes and keeps failures visible`, async (t) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium", args: ["--mute-audio"] }));
    t.after(() => browser.close());
    for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
      const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
      const page = await context.newPage();
      page.setDefaultTimeout(6000);
      const errors = [], logs = [], writes = [];
      let cfg = { agent: "claude-desktop", leadEffort: "high", subagentEffort: "high" };
      const groups = [
        { id: "lead", name: "Lead route", ref: "group/lead", ready: true, members: ["fixture/lead:high"], routing: "order", affinity: "session", firstToken: 17, rules: [{ member: "fixture/lead", minTokens: 10 }] },
        { id: "workers", name: "Worker route", ref: "group/workers", ready: true, members: ["fixture/worker:max"], routing: "order" },
      ];
      const fields = [
        { key: "opus", value: "group/lead[1m]", options: [] },
        { key: "subagent_model", value: "group/workers", options: [] },
        { key: "subagent_effort", value: "high", options: ["low", "medium", "high", "max"].map((value) => ({ value })) },
      ];
      let reject = false;
      page.on("pageerror", (e) => errors.push(e.message));
      page.on("console", (m) => { if (m.type() === "error") logs.push(m.text()); });
      await page.route("**/*", async (route) => {
        const url = new URL(route.request().url());
        const json = (data) => route.fulfill({ json: data });
        if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs={lang:${JSON.stringify(lang)},theme:"light"};` });
        if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window={};" });
        if (url.pathname === "/api/state") return json({ agents: [{ id: "claude-desktop", name: "Claude Desktop", fields }], profiles: [], settings: { lang } });
        if (url.pathname === "/api/groups") return json({ groups });
        if (url.pathname === "/api/plugins/builtin") {
          if (route.request().method() === "POST") { cfg = route.request().postDataJSON().config; writes.push("config"); }
          return json([{ id: "leadsub", on: true, config: cfg }]);
        }
        if (url.pathname === "/api/set") {
          if (reject) return route.fulfill({ status: 400, json: { error: "fixture write rejected" } });
          const body = route.request().postDataJSON();
          fields.find((f) => f.key === body.Field).value = body.Value;
          writes.push(body);
          return json({});
        }
        if (url.pathname === "/api/groups/save") {
          const body = route.request().postDataJSON();
          Object.assign(groups.find((g) => g.id === body.id), body);
          writes.push(body);
          return json({ groups });
        }
        if (url.pathname.startsWith("/api/")) return json({});
        if (url.host !== "magpie.test") return route.fulfill({ status: 404, body: "" });
        const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
        try { return route.fulfill({ body: await fs.readFile(file), contentType: { ".html": "text/html", ".js": "text/javascript", ".css": "text/css" }[path.extname(file)] }); }
        catch { return route.fulfill({ status: 404, body: "" }); }
      });
      await page.goto("http://magpie.test/?view=leadsub");
      const lead = page.locator('.ls-card[data-side="lead"]');
      const sub = page.locator('.ls-card[data-side="subagent"]');
      await lead.locator(".ls-chip").waitFor();
      assert.match(await lead.innerText(), /fixture\/lead:high/);
      assert.equal(await page.locator("#view-leadsub .slot-error").count(), 0);
      await lead.locator("button").nth(1).click();
      await page.getByRole("menuitemradio", { name: "max", exact: true }).click();
      await page.waitForFunction(() => document.querySelector('.ls-card[data-side="lead"] .ls-chip')?.textContent.endsWith(":max"));
      assert.equal(groups[0].affinity, "session");
      assert.equal(groups[0].firstToken, 17);
      assert.equal(groups[0].rules.length, 1);
      await sub.locator("button").first().click();
      await page.getByRole("menuitemradio", { name: "Lead route", exact: true }).click();
      await page.waitForFunction(() => document.querySelector('.ls-card[data-side="subagent"] .ls-chip')?.textContent === "fixture/lead:max");
      assert.equal(cfg.subagentModel, "group/lead");
      assert.equal(cfg.manageGroups, false);
      const n = writes.length;
      reject = true;
      await sub.locator("button").first().click();
      await page.getByRole("menuitemradio", { name: "Worker route", exact: true }).click();
      await page.waitForFunction(() => document.body.innerText.includes("fixture write rejected"));
      assert.equal(writes.length, n, "failed agent write must not persist plugin config");
      assert.equal(cfg.subagentModel, "group/lead");
      await page.reload();
      await sub.locator(".ls-chip").waitFor();
      assert.match(await sub.innerText(), /fixture\/lead:max/);
      await page.setViewportSize({ width: 390, height: 844 });
      assert.equal(await page.locator("#view-leadsub").evaluate((e) => e.scrollWidth <= e.clientWidth + 1), true, `${lang}: fits narrow window`);
      assert.deepEqual(errors, []);
      assert.equal(logs.some((m) => m.includes("said twice") || m.includes("leadsub:")), false, logs.join("\n"));
      await context.close();
    }
  });
}
