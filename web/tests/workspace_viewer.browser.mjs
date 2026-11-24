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
const longBranch = "task/" + "long-branch-".repeat(17) + "end";
const longBase = "release/" + "long-baseline-".repeat(16) + "end";
const childRepository = "modules/application";
const childPath = "README.md";
const longPath = "nested/" + "long-file-name-".repeat(24) + ".md";
const gitlinkCommit = "c".repeat(40);
const gitlink = {path: childRepository, added: 1, deleted: 1, gitlink: {old: older, new: head}};
const history = Array.from({length: 42}, (_, index) => ({
  id: index.toString(16).padStart(40, "0"), short: index.toString(16).padStart(7, "0"), subject: "Fixture " + (index + 1), files: [],
}));
const repositories = [
  {id: "", path: "", name: "AHA2", readable: true},
  {id: childRepository, path: childRepository, name: "application", readable: true},
  {id: "modules/missing", path: "modules/missing", name: "missing", readable: false},
];
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
    let childDiscovered = true;
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
      const repo = url.searchParams.get("repo");
      const child = repo === childRepository;
      const unavailable = repo === "modules/missing";
      if (view === "state" && url.searchParams.has("availability")) return answer({available: true});
      if (view === "state" && repo === "modules/pending") {
        return answer({error: "workspace_viewer_repository_discovery_incomplete"}, 503);
      }
      if ((child && !childDiscovered) || (view === "state" && repo && !repositories.some(item => item.id === repo))) {
        return answer({error: "workspace_viewer_invalid_repository"}, 400);
      }
      if (view === "state" && url.searchParams.has("summary")) return answer({isGit, version: "v1"});
      if (view === "state") return answer({
        isGit: isGit && !unavailable, name: isGit ? "Viewer fixture" : "Plain folder", taskTitle: task.title, taskCode: task.code,
        repositories: isGit ? repositories.filter(item => childDiscovered || item.id !== childRepository) : undefined,
        repositoryUnavailable: unavailable, repositoriesTruncated: isGit && !child, repositoryDeadlineReached: isGit && !child,
        version: "v1", branch: child ? "feature" : longBranch, head,
        base: child ? "HEAD" : url.searchParams.get("base") || longBase, mergeBase: child ? "" : older,
        baseAtHead: child, baseFallback: child && url.searchParams.has("base"),
        ahead: child ? 0 : 1, behind: 0, branches: child ? ["feature"] : ["main", "release", longBase, longBranch], baseChanges: changes,
        uncommitted: [{...changes[0], path: child ? childPath : changes[0].path, status: ".M"}],
        commits: unavailable ? [] : child ? [{id: head, short: "aaaaaaa", subject: "Child commit", files: [{...changes[0], path: childPath}]}]
          : [{id: head, short: "aaaaaaa", subject: "Latest commit", files: changes},
          {id: older, short: "bbbbbbb", subject: "Earlier commit", files: [{path: longPath, added: 1, deleted: 0}]},
          {id: gitlinkCommit, short: "ccccccc", subject: "Update application pointer", files: [gitlink]}, ...history],
      });
      if (view === "tree") {
        const directory = url.searchParams.get("path");
        const entry = (path, kind, masked = false) => ({path, kind, masked, size: 12, mtime: "2026-01-01T00:00:00Z"});
        return answer({entries: child ? [entry(childPath, "file")] : directory === "docs" ? [entry("docs/notes.md", "file")]
          : [entry("docs", "directory"), entry("README.md", "file"), entry(".env", "file", true)]});
      }
      if (view === "diff") {
        if (url.searchParams.get("path") === childRepository) {
          return answer({text: "@@ -1 +1 @@\n-Subproject commit " + older + "\n+Subproject commit " + head + "\n"});
        }
        return answer({text: url.searchParams.has("ignore_whitespace") ? "" : child ? patch.replace("+new", "+child change") : patch});
      }
      const file = url.searchParams.get("path");
      return answer(file === ".env" ? {text: "", placeholder: "masked"}
        : {text: child && file === childPath ? "Child source" : file === "README.md" ? "<script>literal only</script>\n" + "long".repeat(300) : "Working notes"});
    });
    return {ctx, queries, forgetChild: () => { childDiscovered = false; }, restoreChild: () => { childDiscovered = true; }};
  }

  const desktop = await context(1280, "en-US");
  const page = await desktop.ctx.newPage();
  await page.goto(origin + "/#tasks");
  await page.locator(".task-card [data-task-open]").click();
  const link = page.getByRole("link", {name: "Open Workspace"});
  await link.waitFor();
  assert.equal(await link.textContent(), "");
  assert.equal(await link.getAttribute("title"), "Open Workspace");
  assert.equal(await link.getAttribute("aria-label"), "Open Workspace");
  assert.deepEqual(await link.evaluate(anchor => {
    const {width, height} = anchor.getBoundingClientRect();
    const neighbor = anchor.nextElementSibling.getBoundingClientRect();
    return [width, height, neighbor.width, neighbor.height];
  }), [36, 36, 36, 36]);
  await link.focus();
  await page.keyboard.press("Shift+Tab");
  await page.keyboard.press("Tab");
  assert.equal(await link.evaluate(anchor => document.activeElement === anchor &&
    anchor.matches(":focus-visible") && getComputedStyle(anchor).outlineStyle === "solid"), true);
  const popup = page.waitForEvent("popup");
  await page.keyboard.press("Enter");
  const viewer = await popup;
  await viewer.getByRole("heading", {name: "Viewer fixture"}).waitFor();
  assert.equal(new URL(viewer.url()).pathname, "/workspace-viewer");
  assert.equal(new URL(viewer.url()).searchParams.get("task"), task.id);
  async function checkHeader(page, width) {
    await page.setViewportSize({width, height: 800});
    const layout = await page.locator(".wv-meta").evaluate(meta => {
      const controls = [...meta.children];
      const boxes = controls.map(control => control.getBoundingClientRect());
      const center = ({y, height}) => y + height / 2;
      const label = meta.querySelector("label > span");
      const baseSelect = meta.querySelector("[data-wv-base]");
      const branch = meta.querySelector(".wv-branch span");
      return {
        sameRow: boxes.every(box => Math.abs(center(box) - center(boxes[0])) < 1),
        noOverlap: boxes.every((box, i) => box.right <= innerWidth && (i === 0 || box.left >= boxes[i - 1].right)) &&
          label.getBoundingClientRect().right <= baseSelect.getBoundingClientRect().left,
        noWrap: getComputedStyle(label).whiteSpace === "nowrap",
        baseEllipsis: getComputedStyle(baseSelect).textOverflow === "ellipsis",
        branchClipped: branch.scrollWidth > branch.clientWidth,
      };
    });
    assert.deepEqual(layout, {sameRow: true, noOverlap: true, noWrap: true, baseEllipsis: true, branchClipped: true}, `viewer header at ${width}px`);
    assert.equal(await page.locator(".wv-branch").getAttribute("title"), longBranch);
  }
  const repositorySelect = viewer.getByRole("combobox", {name: "Repository"});
  assert.equal(await repositorySelect.locator('option[value=""]').textContent(), "AHA2");
  await viewer.getByText(/The submodule list may be incomplete/).waitFor();
  await viewer.getByText(/Reading registered submodules timed out/).waitFor();
  await checkHeader(viewer, 1280);
  await viewer.locator("[data-wv-base]").selectOption(longBase);
  await viewer.locator(`[data-wv-base][title="${longBase}"]`).waitFor();
  await checkHeader(viewer, 1024);
  const commitGroup = viewer.locator('[data-wv-action="commits"]');
  const commitRows = viewer.locator(".wv-commit");
  const loadMore = viewer.getByRole("button", {name: "Load more", exact: true});
  assert.equal(await commitGroup.getAttribute("aria-expanded"), "false");
  assert.equal(await commitRows.count(), 0);
  await commitGroup.click();
  assert.equal(await commitRows.count(), 20);
  await loadMore.click();
  assert.equal(await commitRows.count(), 40);
  await loadMore.click();
  assert.equal(await commitRows.count(), 45);
  assert.equal(await loadMore.count(), 0);
  await commitGroup.click();
  assert.equal(await commitRows.count(), 0);
  await commitGroup.click();
  assert.equal(await viewer.getByRole("button", {name: /Latest commit/}).getAttribute("aria-expanded"), "true");
  await viewer.locator('[data-wv-scope="commit"][data-wv-commit="' + head + '"]').click();
  await viewer.locator(".wv-hunk").first().waitFor();
  assert.equal(await viewer.locator(".wv-hunk").count(), 2);
  async function checkContentHead(page, width, expectClipped) {
    await page.setViewportSize({width, height: 800});
    const layout = await page.locator(".wv-content-head").evaluate(head => {
      const filename = head.querySelector("strong");
      const name = filename.getBoundingClientRect();
      const badge = head.querySelector(".wv-badge").getBoundingClientRect();
      const toolbar = head.nextElementSibling.querySelector("input").getBoundingClientRect();
      return {
        leftAligned: Math.abs(name.left - toolbar.left) < 1,
        rightAligned: Math.abs(head.getBoundingClientRect().right - parseFloat(getComputedStyle(head).paddingRight) - badge.right) < 1,
        noOverlap: name.right <= badge.left && badge.right <= innerWidth,
        clipped: filename.scrollWidth > filename.clientWidth && getComputedStyle(filename).textOverflow === "ellipsis",
      };
    });
    assert.deepEqual(layout, {leftAligned: true, rightAligned: true, noOverlap: true, clipped: expectClipped}, width + "px content header");
  }
  await checkContentHead(viewer, 1024, false);
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
  await viewer.locator('[data-wv-scope="commit"][data-wv-commit="' + older + '"]').click();
  await viewer.locator(".wv-hunk").first().waitFor();
  for (const width of [1280, 1024, 390]) await checkContentHead(viewer, width, true);
  await viewer.setViewportSize({width: 1024, height: 800});
  await viewer.getByRole("button", {name: /Update application pointer/}).click();
  await viewer.getByText("Submodule update bbbbbbbb → aaaaaaaa").click();
  await viewer.getByText("-Subproject commit " + older, {exact: true}).waitFor();
  await viewer.getByText("+Subproject commit " + head, {exact: true}).waitFor();
  await viewer.getByRole("button", {name: "Close application"}).click();
  await viewer.getByRole("button", {name: /Close long-file-name/}).click();
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
  await viewer.locator('[data-wv-path="README.md"]').click();
  await viewer.getByText("<script>literal only</script>", {exact: false}).waitFor();
  const beforeSwitch = desktop.queries.length;
  const rootSource = viewer.getByText("<script>literal only</script>", {exact: false});
  const childSource = viewer.getByText("Child source", {exact: true});
  const rootTab = viewer.getByRole("button", {name: childPath + " · AHA2", exact: true});
  const childTab = viewer.getByRole("button", {name: childPath + " · " + childRepository, exact: true});
  const fileRow = viewer.locator('[data-wv-scope="file"][data-wv-path="' + childPath + '"]');
  await repositorySelect.selectOption(childRepository);
  await fileRow.waitFor();
  await rootSource.waitFor();
  assert.equal(await viewer.locator(".wv-tab").count(), 1);
  assert.equal(new URL(viewer.url()).searchParams.get("file"), "README.md");
  assert.equal(new URL(viewer.url()).searchParams.get("fileRepo"), "");
  assert.equal(new URL(viewer.url()).searchParams.get("repo"), childRepository);
  assert.equal(await viewer.locator("[data-wv-base]").getAttribute("title"), "Base release is unavailable in this repository. Using HEAD.");
  assert.equal(await viewer.locator(".wv-counts").count(), 0);
  assert.ok(!desktop.queries.slice(beforeSwitch).some(({view}) => view === "file" || view === "diff"), "switching repositories leaves the open preview alone");
  await fileRow.click();
  await childSource.waitFor();
  assert.equal(await viewer.locator(".wv-tab").count(), 2);
  await rootTab.click();
  await rootSource.waitFor();
  await childTab.click();
  await childSource.waitFor();
  await repositorySelect.selectOption("");
  await fileRow.waitFor();
  await childSource.waitFor();
  assert.equal(new URL(viewer.url()).searchParams.get("fileRepo"), childRepository);
  await repositorySelect.selectOption(childRepository);
  await fileRow.waitFor();
  await rootTab.click();
  await rootSource.waitFor();
  await viewer.reload();
  await rootSource.waitFor();
  assert.equal(await repositorySelect.inputValue(), childRepository, "URL restores navigation repo separately from the active file");
  assert.equal(await viewer.locator(".wv-tab").count(), 1);
  const afterReload = desktop.queries.length;
  await fileRow.click();
  await childSource.waitFor();
  assert.ok(desktop.queries.slice(afterReload).some(({view, query}) => view === "file" && query.get("repo") === childRepository));

  await viewer.getByRole("button", {name: "Changes", exact: true}).click();
  await viewer.getByRole("button", {name: /Recent commits/}).waitFor();
  assert.equal(await commitGroup.getAttribute("aria-expanded"), "false");
  await commitGroup.click();
  await viewer.locator('[data-wv-scope="commit"][data-wv-path="README.md"]').click();
  const childDiff = viewer.getByText("+child change", {exact: true});
  await childDiff.waitFor();

  const invalid = viewer.locator(".wv-notice").filter({hasText: /The selected repository is missing or is no longer/});
  desktop.forgetChild();
  await viewer.waitForResponse(response => response.status() === 400 && new URL(response.url()).searchParams.get("summary") === "1");
  const fallback = viewer.locator(".wv-notice").filter({hasText: /Showing the workspace root/});
  await invalid.or(fallback).waitFor();
  assert.equal(await repositorySelect.inputValue(), childRepository, "an invalid repository on a later poll keeps the selected repository");
  assert.equal(await viewer.locator(".wv-tab").count(), 2);
  assert.equal(new URL(viewer.url()).searchParams.get("repo"), childRepository);
  assert.equal(await invalid.isVisible(), true);
  await childDiff.waitFor();
  await rootTab.click();
  await rootSource.waitFor();
  await childTab.click();
  await childDiff.waitFor();
  assert.equal(await viewer.locator(".wv-tab").count(), 2);
  desktop.restoreChild();
  await viewer.locator('[data-wv-action="refresh"]').click();
  await invalid.waitFor({state: "hidden"});

  await repositorySelect.selectOption("modules/missing");
  await viewer.getByText(/This repository has not been initialized or its Git data could not be read/).waitFor();
  await childDiff.waitFor();
  assert.equal(await viewer.locator(".wv-tab").count(), 2);
  await repositorySelect.selectOption("");
  await commitGroup.click();
  await viewer.getByRole("button", {name: /Latest commit/}).waitFor();
  assert.equal(new URL(viewer.url()).searchParams.has("repo"), false);
  assert.equal(await viewer.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);

  const beforeInvalidLink = desktop.queries.length;
  const staleLink = new URLSearchParams({task: task.id, repo: "modules/deleted", base: "child-base",
    file: childPath, scope: "commit", commit: head});
  await viewer.goto(origin + "/workspace-viewer?" + staleLink);
  const recovered = viewer.getByText("The selected repository is unavailable. Showing the workspace root.");
  await recovered.waitFor();
  assert.equal(await repositorySelect.inputValue(), "");
  await commitGroup.click();
  await viewer.getByRole("button", {name: /Latest commit/}).waitFor();
  const recoveredURL = new URL(viewer.url());
  assert.ok(["repo", "file", "scope", "commit"].every(key => !recoveredURL.searchParams.has(key)));
  assert.equal(await viewer.locator(".wv-tab").count(), 0);
  const recoveryQueries = desktop.queries.slice(beforeInvalidLink);
  assert.equal(recoveryQueries.filter(({query}) => query.get("repo") === "modules/deleted").length, 1);
  assert.ok(recoveryQueries.some(({view, query}) => view === "state" && !query.has("repo") && !query.has("base")));
  assert.ok(!recoveryQueries.some(({view}) => view === "file" || view === "diff"));
  await viewer.locator('[data-wv-action="refresh"]').click();
  await recovered.waitFor({state: "hidden"});
  await viewer.goto(origin + "/workspace-viewer?" + new URLSearchParams({task: task.id, repo: "modules/pending"}));
  await viewer.getByText(/The selected repository could not be verified/).waitFor();
  assert.equal(new URL(viewer.url()).searchParams.get("repo"), "modules/pending");
  await desktop.ctx.close();

  const mobile = await context(390, "zh-CN");
  const narrow = await mobile.ctx.newPage();
  await narrow.goto(origin + "/#tasks");
  await narrow.locator(".task-card [data-task-open]").click();
  const mobileLink = narrow.getByRole("link", {name: "打开工作区"});
  await mobileLink.waitFor();
  assert.equal(await mobileLink.textContent(), "");
  assert.equal(await narrow.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  await narrow.goto(origin + "/workspace-viewer?task=plain&tab=changes&file=README.md&scope=commit");
  await narrow.getByText("非 Git 工作区").waitFor();
  assert.equal(await narrow.getByRole("combobox", {name: "仓库"}).count(), 0);
  await narrow.getByText("<script>literal only</script>", {exact: false}).waitFor();
  assert.equal(await narrow.getByRole("button", {name: "变更", exact: true}).count(), 0);
  assert.equal(await narrow.getByLabel("忽略空白").count(), 0);
  assert.equal(await narrow.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  assert.ok(mobile.queries.every(({view}) => view !== "diff"));
  await narrow.goto(origin + `/workspace-viewer?task=${task.id}&base=${encodeURIComponent(longBase)}`);
  await narrow.getByLabel("基线", {exact: true}).waitFor();
  await narrow.getByText(/读取子模块登记超时/).waitFor();
  await checkHeader(narrow, 1024);
  await narrow.goto(origin + "/workspace-viewer?task=missing");
  await narrow.getByText("该任务的工作区或工作树不可用。").waitFor();
  await mobile.ctx.close();
  assert.deepEqual(errors, []);
  console.log("Workspace viewer verified at 1280px, 1024px and 390px in real Chromium");
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
