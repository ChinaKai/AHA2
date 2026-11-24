// Real Chromium, built modules and the production CSP. The API fixture lets
// the test drive task navigation without a production workspace or account.
import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {createServer} from "node:http";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const tools = resolve(root, ".tools/web-validation");
const playwright = process.env.AHA_PLAYWRIGHT_MODULE || resolve(tools, "node_modules/playwright/index.mjs");
const {chromium} = await import(pathToFileURL(playwright));
const executablePath = process.env.AHA_BROWSER_EXECUTABLE;
const launch = executablePath ? {executablePath, args: ["--no-sandbox"]} : await (async () => {
  const {default: binary} = await import(pathToFileURL(resolve(tools, "node_modules/@sparticuz/chromium/build/index.js")));
  return {executablePath: await binary.executablePath(), args: binary.args};
})();
const csp = (await readFile(resolve(root, "internal/httpapi/server.go"), "utf8")).match(/Set\("Content-Security-Policy", "([^"]+)"\)/)[1];
const mime = {".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml"};
const server = createServer(async (request, response) => {
  const pathname = new URL(request.url, "http://localhost").pathname;
  const name = ["/", "/workspace-viewer"].includes(pathname) ? "index.html" : pathname.slice(1);
  if (!/^[a-z0-9_/-]+\.[a-z]+$/.test(name)) return response.writeHead(404).end();
  try {
    const body = await readFile(resolve(root, "web/build", name));
    response.writeHead(200, {"Content-Security-Policy": csp, "Content-Type": mime[name.slice(name.lastIndexOf("."))] || "application/octet-stream"});
    response.end(body);
  } catch {
    response.writeHead(404).end();
  }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const task = {id: "viewer-task", code: "task-42", title: "Review the changes", status: "active", isolation: "worktree",
  project_id: "p", workspace_id: "w", task_workspace_path: "/resolved/worktree"};
const head = "a".repeat(40);
const older = "b".repeat(40);
const changes = [{path: "README.md", added: 2, deleted: 1}];
const patch = "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1,2 +1,2 @@\n-old\n+new\n@@ -20 +20 @@\n-before\n+after\n";

let browser;
const errors = [];
try {
  browser = await chromium.launch({...launch, headless: true});
  async function context(width, language) {
    const ctx = await browser.newContext({viewport: {width, height: 800}});
    ctx.on("page", page => page.on("pageerror", error => errors.push(error.message)));
    await ctx.addInitScript(() => {
      window.EventSource = class { addEventListener() {} close() {} };
    });
    const queries = [];
    await ctx.route("**/api/v1/**", async route => {
      const url = new URL(route.request().url());
      const path = url.pathname;
      const answer = (json, status = 200) => route.fulfill({json, status});
      if (path === "/api/v1/auth/status") return answer({authenticated: true, csrf_token: "fixture", username: "Owner"});
      if (path === "/api/v1/settings/general") return answer({general: {language}});
      if (path === "/api/v1/projects") return answer({projects: [{id: "p", name: "Project", project_type: "folder"}]});
      if (path === "/api/v1/workspaces") return answer({workspaces: [{id: "w", project_id: "p", name: "Viewer fixture", root_path: "/project"}]});
      if (path === "/api/v1/tasks") return answer({tasks: [task], counts: {status: {active: 1}, project: {p: 1}, device: {local: 1}}});
      if (path === "/api/v1/tasks/viewer-task") return answer({task, turns: [], agents: [{agent_id: "main", title: "Main", role: "main", backend: "stub", status: "idle", wire_model: "stub"}], hardware: []});
      if (path.endsWith("/conversation")) return answer({conversation: {items: []}});
      if (path === "/api/v1/assistant") return answer({assistant: {enabled: false}});
      const match = path.match(/^\/api\/v1\/tasks\/([^/]+)\/workspace\/([^/]+)$/);
      if (!match) return answer({});
      const [, id, view] = match;
      queries.push({view, query: url.searchParams, method: route.request().method()});
      if (id === "missing") return answer({error: "workspace_viewer_unavailable"}, 404);
      const isGit = id !== "plain";
      if (view === "state" && url.searchParams.has("availability")) return answer({available: true});
      if (view === "state" && url.searchParams.has("summary")) return answer({isGit, version: "v1"});
      if (view === "state") return answer({
        isGit, name: isGit ? "Viewer fixture" : "Plain folder", taskTitle: task.title, taskCode: task.code,
        version: "v1", branch: "task", head, base: url.searchParams.get("base") || "main", mergeBase: older,
        ahead: 1, behind: 0, branches: ["main", "release", "task"], baseChanges: changes,
        uncommitted: [{...changes[0], status: ".M"}],
        commits: [{id: head, short: "aaaaaaa", subject: "Latest commit", files: changes},
          {id: older, short: "bbbbbbb", subject: "Earlier commit", files: [{path: "docs/notes.md", added: 1, deleted: 0}]}],
      });
      if (view === "tree") {
        const directory = url.searchParams.get("path");
        const entry = (path, kind, masked = false) => ({path, kind, masked, size: 12, mtime: "2026-01-01T00:00:00Z"});
        return answer({entries: directory === "docs" ? [entry("docs/notes.md", "file")]
          : [entry("docs", "directory"), entry("README.md", "file"), entry(".env", "file", true)]});
      }
      if (view === "diff") {
        return answer({text: url.searchParams.has("ignore_whitespace") ? "" : patch});
      }
      const file = url.searchParams.get("path");
      return answer(file === ".env" ? {text: "", placeholder: "masked"}
        : {text: file === "README.md" ? "<script>literal only</script>\n" + "long".repeat(300) : "Working notes"});
    });
    return {ctx, queries};
  }

  const desktop = await context(1280, "en-US");
  const page = await desktop.ctx.newPage();
  await page.goto(origin + "/#tasks");
  await page.locator(".task-card [data-task-open]").click();
  const link = page.getByRole("link", {name: "Open Workspace"});
  await link.waitFor();
  const popup = page.waitForEvent("popup");
  await link.click();
  const viewer = await popup;
  await viewer.getByRole("heading", {name: "Viewer fixture"}).waitFor();
  assert.equal(new URL(viewer.url()).pathname, "/workspace-viewer");
  assert.equal(new URL(viewer.url()).searchParams.get("task"), task.id);
  assert.equal(await viewer.getByRole("button", {name: /Latest commit/}).getAttribute("aria-expanded"), "true");
  await viewer.locator('[data-wv-scope="commit"][data-wv-commit="' + head + '"]').click();
  await viewer.locator(".wv-hunk").first().waitFor();
  assert.equal(await viewer.locator(".wv-hunk").count(), 2);
  await viewer.getByRole("button", {name: /Next hunk/}).click();
  await viewer.getByRole("button", {name: /Next hunk/}).click();
  assert.match(await viewer.locator(".wv-current-hunk").textContent(), /-20/);
  await viewer.getByRole("button", {name: /Previous hunk/}).click();
  assert.match(await viewer.locator(".wv-current-hunk").textContent(), /-1,2/);
  await viewer.getByLabel("Wrap lines").check();
  assert.equal(await viewer.locator(".wv-wrap").count(), 1);
  await viewer.getByLabel("Ignore whitespace").check();
  await viewer.getByText("No differences with the current options.").waitFor();
  await viewer.getByLabel("Ignore whitespace").uncheck();
  await viewer.locator(".wv-hunk").first().waitFor();
  await viewer.locator("[data-wv-base]").selectOption("release");
  await viewer.waitForURL(url => url.searchParams.get("base") === "release");
  await viewer.getByRole("button", {name: /Earlier commit/}).click();
  await viewer.locator('[data-wv-scope="commit"][data-wv-commit="' + older + '"]').waitFor();
  await viewer.locator('[data-wv-scope="uncommitted"]').click();
  await viewer.locator(".wv-content-head .wv-badge").getByText("Uncommitted changes").waitFor();
  assert.equal(await viewer.locator(".wv-tab").count(), 1, "different scope reuses the tab for this path");

  await viewer.getByRole("button", {name: "Files", exact: true}).click();
  await viewer.getByRole("button", {name: "docs", exact: true}).click();
  await viewer.getByRole("button", {name: "notes.md", exact: true}).click();
  await viewer.getByText("Working notes").waitFor();
  assert.equal(await viewer.locator(".wv-tab").count(), 2);
  await viewer.getByRole("button", {name: "Close notes.md"}).click();
  await viewer.locator(".wv-content-head").getByText("README.md").waitFor();
  await viewer.locator('[data-wv-scope="file"][data-wv-path="README.md"]').click();
  await viewer.getByText("<script>literal only</script>", {exact: false}).waitFor();
  await viewer.getByRole("button", {name: "notes.md", exact: true}).click();
  await viewer.locator('[data-wv-select="1"]').click({button: "middle"});
  await viewer.locator(".wv-content-head").getByText("README.md").waitFor();
  assert.equal(await viewer.locator(".wv-tab").count(), 1);
  await viewer.locator('[data-wv-scope="file"][data-wv-path=".env"]').click();
  await viewer.getByText("This file’s contents are hidden.").waitFor();
  assert.equal(await viewer.locator(".wv-tab").count(), 2);
  await viewer.reload();
  await viewer.getByText("This file’s contents are hidden.").waitFor();
  assert.equal(await viewer.locator(".wv-tab").count(), 1, "reload restores just the active file");
  await viewer.waitForResponse(response => new URL(response.url()).searchParams.get("summary") === "1");
  assert.ok(desktop.queries.every(({query, method}) => !query.has("root") && method === "GET"));
  await viewer.getByRole("button", {name: "Close all"}).click();
  await viewer.getByText("Select a file or a change to preview it.").waitFor();
  assert.equal(new URL(viewer.url()).searchParams.has("file"), false);
  assert.equal(await viewer.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  await desktop.ctx.close();

  const mobile = await context(390, "zh-CN");
  const narrow = await mobile.ctx.newPage();
  await narrow.goto(origin + "/#tasks");
  await narrow.locator(".task-card [data-task-open]").click();
  await narrow.getByRole("link", {name: "打开工作区"}).waitFor();
  assert.equal(await narrow.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  await narrow.goto(origin + "/workspace-viewer?task=plain&tab=changes&file=README.md&scope=commit");
  await narrow.getByText("非 Git 工作区").waitFor();
  await narrow.getByText("<script>literal only</script>", {exact: false}).waitFor();
  assert.equal(await narrow.getByRole("button", {name: "变更", exact: true}).count(), 0);
  assert.equal(await narrow.getByLabel("忽略空白").count(), 0);
  assert.equal(await narrow.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  assert.ok(mobile.queries.every(({view}) => view !== "diff"));
  await narrow.goto(origin + "/workspace-viewer?task=missing");
  await narrow.getByText("该任务的工作区或工作树不可用。").waitFor();
  await mobile.ctx.close();
  assert.deepEqual(errors, []);
  console.log("Workspace viewer verified at 1280px and 390px in real Chromium");
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
