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
const localRef = "refs/heads/release";
const remoteRef = "refs/remotes/origin/release";
const orphanRef = "refs/heads/unrelated";
const localHead = "d".repeat(40);
const remoteHead = "e".repeat(40);
const orphanHead = "f".repeat(40);
const author = "A & B";
const date = Date.UTC(2026, 0, 15, 12) / 1000;
const longSubject = "Earlier commit " + "x".repeat(4080) + "…";
const longAuthor = "界".repeat(1023) + "…";
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
  async function context(width, language, terminalFixture = false) {
    const ctx = await browser.newContext({viewport: {width, height: 800}});
    ctx.on("page", page => page.on("pageerror", error => errors.push(error.message)));
    await ctx.addInitScript(() => {
      window.EventSource = class { addEventListener() {} close() {} };
    });
    const queries = [];
    let childDiscovered = true;
    let remoteBranchError = "";
    const terminalRequests = [];
    const terminalMessages = [];
    const sessions = new Map();
    const sockets = new Map();
    let allowTerminalLAN = false;
    let nextTerminal = 1;
    if (terminalFixture) await ctx.routeWebSocket(/\/workspace-terminal\/[^/]+\/ws/, ws => {
      const url = new URL(ws.url());
      const id = url.pathname.split("/").at(-2);
      if (!allowTerminalLAN || !sockets.has(id)) {
        void ws.close({code: 1008});
        return;
      }
      sockets.get(id).add(ws);
      ws.onClose(() => sockets.get(id)?.delete(ws));
      ws.send(Buffer.from("PTY fixture for " + id + "\r\n"));
      ws.onMessage(data => {
        terminalMessages.push({id, data});
        if (Buffer.isBuffer(data)) ws.send(Buffer.from("echo=" + data.toString() + "\r\n"));
      });
    });
    await ctx.route("**/api/v1/**", async route => {
      const url = new URL(route.request().url());
      const path = url.pathname;
      const answer = (json, status = 200) => route.fulfill({json, status});
      if (path === "/api/v1/auth/status") return answer({authenticated: true, csrf_token: "fixture", username: "Owner"});
      if (path === "/api/v1/settings/general") return answer({general: {language}});
      if (terminalFixture && path === "/api/v1/settings/security") {
        return answer({security: {access_scope: "lan", validate_origin: true, workspace_terminal_lan: allowTerminalLAN}});
      }
      if (terminalFixture && path === "/api/v1/settings/security/workspace-terminal") {
        allowTerminalLAN = route.request().postDataJSON().workspace_terminal_lan;
        if (!allowTerminalLAN) for (const list of sockets.values()) {
          for (const ws of list) void ws.close({code: 1008});
        }
        return answer({security: {access_scope: "lan", validate_origin: true, workspace_terminal_lan: allowTerminalLAN}});
      }
      if (path.endsWith("/workspace-targets")) return answer({workspaces: terminalFixture
        ? [{id: "", name: "Local checkout"}, {id: "ssh-workspace", name: "Builder"}] : []});
      if (path.includes("/workspace-terminal")) {
        if (!terminalFixture || !allowTerminalLAN) return answer({locked: true});
        const workspace = url.searchParams.get("workspace") || "";
        const method = route.request().method();
        const list = sessions.get(workspace) || [];
        terminalRequests.push({method, workspace, path});
        if (method === "GET") return answer({sessions: list,
          cwd: workspace ? "/srv/workspace" : "/resolved/worktree", ssh: Boolean(workspace)});
        if (method === "POST") {
          assert.deepEqual(Object.keys(route.request().postDataJSON()).sort(), ["cols", "initial", "rows"]);
          const info = {id: "pty-" + nextTerminal++, number: list.length + 1};
          sockets.set(info.id, new Set());
          list.push(info);
          sessions.set(workspace, list);
          return answer(info, 201);
        }
        const id = path.split("/").at(-1);
        for (const ws of sockets.get(id) || []) void ws.close({code: 1000, reason: "terminal ended"});
        sockets.delete(id);
        sessions.set(workspace, list.filter(item => item.id !== id));
        return answer({ok: true});
      }
      if (path === "/api/v1/projects") return answer({projects: [{id: "p", name: "Project", project_type: "folder"}]});
      if (path === "/api/v1/workspaces") return answer({workspaces: [{id: "w", project_id: "p", name: "Viewer fixture", root_path: "/project"}]});
      if (path === "/api/v1/tasks") return answer({tasks: [task], counts: {status: {active: 1}, project: {p: 1}, device: {local: 1}}});
      if (path === "/api/v1/tasks/viewer-task") return answer({task, turns: [], agents: [{agent_id: "main", title: "Main", role: "main", backend: "stub", status: "idle", wire_model: "stub"}], hardware: []});
      if (path.endsWith("/conversation")) return answer({conversation: {items: []}});
      if (path === "/api/v1/assistant") return answer({assistant: {enabled: false}});
      const match = path.match(/^\/api\/v1\/tasks\/([^/]+)\/(?:workspace|workspace-target\/ssh-workspace)\/([^/]+)$/);
      if (!match) return answer({});
      const [, id, view] = match;
      queries.push({view, query: url.searchParams, method: route.request().method()});
      if (id === "missing") return answer({error: "workspace_viewer_unavailable"}, 404);
      const isGit = id !== "plain";
      const repo = url.searchParams.get("repo");
      const child = repo === childRepository;
      const unavailable = repo === "modules/missing";
      const branch = url.searchParams.get("branch");
      const remote = branch === remoteRef;
      const orphan = branch === orphanRef;
      const branchHead = orphan ? orphanHead : remote ? remoteHead : localHead;
      const branchName = orphan ? "unrelated" : remote ? "origin/release" : "release";
      const version = branch ? "v1-" + branch : "v1";
      if (view === "state" && url.searchParams.has("availability")) return answer({available: true});
      if (view === "state" && repo === "modules/pending") {
        return answer({error: "workspace_viewer_repository_discovery_incomplete"}, 503);
      }
      if ((child && !childDiscovered) || (view === "state" && repo && !repositories.some(item => item.id === repo))) {
        return answer({error: "workspace_viewer_invalid_repository"}, 400);
      }
      if (remote && remoteBranchError) {
        return answer({error: remoteBranchError}, remoteBranchError === "workspace_viewer_invalid_branch" ? 400 : 503);
      }
      if (view === "state" && url.searchParams.has("summary")) return answer({isGit, version});
      if (view === "state") return answer({
        isGit: isGit && !unavailable, name: isGit ? "Viewer fixture" : "Plain folder", taskTitle: task.title, taskCode: task.code,
        repositories: isGit ? repositories.filter(item => childDiscovered || item.id !== childRepository) : undefined,
        repositoryUnavailable: unavailable, repositoriesTruncated: isGit && !child, repositoryDeadlineReached: isGit && !child,
        version, branch: branch ? branchName : child ? "feature" : longBranch, head: branch ? branchHead : head,
        checkedOutBranch: child ? "feature" : longBranch,
        branchOptions: child ? [{ref: "refs/heads/feature", name: "feature"}] : [
          {ref: "refs/heads/" + longBranch, name: longBranch}, {ref: localRef, name: "release"},
          {ref: orphanRef, name: "unrelated"}, {ref: remoteRef, name: "origin/release"},
        ],
        localBranchesTruncated: isGit && !child && !remote, remoteBranchesTruncated: isGit && !child && !!branch,
        base: child ? "HEAD" : url.searchParams.get("base") || longBase, mergeBase: child || orphan ? "" : older,
        noCommonAncestor: orphan,
        baseAtHead: child, baseFallback: child && url.searchParams.has("base"),
        ahead: child || orphan ? 0 : 1, behind: 0, branches: child ? ["feature"] : ["main", "release", longBase, longBranch], baseChanges: orphan ? [] : changes,
        uncommitted: branch ? [] : [{...changes[0], path: child ? childPath : changes[0].path, status: ".M"}],
        commits: unavailable ? [] : branch ? [{id: branchHead, short: branchHead.slice(0, 7), subject: branchName + " commit", author, date, files: changes}]
          : child ? [{id: head, short: "aaaaaaa", subject: "Child commit", files: [{...changes[0], path: childPath}]}]
          : [{id: head, short: "aaaaaaa", subject: "Latest commit", author, date, files: changes},
          {id: older, short: "bbbbbbb", subject: longSubject, author: longAuthor, date, files: [{path: longPath, added: 1, deleted: 0}]},
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
        if (branch) return answer({text: patch.replace("+new", "+" + branchName + " change")});
        return answer({text: url.searchParams.has("ignore_whitespace") ? "" : child ? patch.replace("+new", "+child change") : patch});
      }
      const file = url.searchParams.get("path");
      return answer(file === ".env" ? {text: "", placeholder: "masked"}
        : {text: child && file === childPath ? "Child source" : file === "README.md" ? "<script>literal only</script>\n" + "long".repeat(300) : "Working notes"});
    });
    return {ctx, queries, terminalRequests, terminalMessages, forgetChild: () => { childDiscovered = false; }, restoreChild: () => { childDiscovered = true; },
      failRemoteBranch: error => { remoteBranchError = error; }};
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
      const branch = meta.querySelector("[data-wv-branch]");
      const branchNotices = document.getElementById(branch.getAttribute("aria-describedby"));
      const notices = branchNotices?.getBoundingClientRect();
      return {
        sameRow: boxes.every(box => Math.abs(center(box) - center(boxes[0])) < 1),
        noOverlap: boxes.every((box, i) => box.right <= innerWidth && (i === 0 || box.left >= boxes[i - 1].right)) &&
          label.getBoundingClientRect().right <= baseSelect.getBoundingClientRect().left,
        noWrap: getComputedStyle(label).whiteSpace === "nowrap",
        baseEllipsis: getComputedStyle(baseSelect).textOverflow === "ellipsis",
        branchEllipsis: getComputedStyle(branch).textOverflow === "ellipsis",
        noticesNearby: notices && notices.top >= branch.getBoundingClientRect().bottom && notices.right <= innerWidth,
      };
    });
    assert.deepEqual(layout, {sameRow: true, noOverlap: true, noWrap: true, baseEllipsis: true, branchEllipsis: true, noticesNearby: true}, `viewer header at ${width}px`);
    assert.equal(await page.locator("[data-wv-branch]").getAttribute("title"), longBranch);
  }
  const branchSelect = viewer.getByRole("combobox", {name: "Branch"});
  const branchNotices = viewer.locator("header .wv-branch-notices");
  await branchNotices.getByText("Local branch list truncated.", {exact: true}).waitFor();
  assert.equal(await branchNotices.getByText("The remote branch list reached its scan limit and may be incomplete.", {exact: true}).count(), 0);
  assert.equal(await viewer.locator(".wv-navigator").getByText("This list is truncated.", {exact: true}).count(), 0);
  assert.equal(await branchSelect.inputValue(), "");
  assert.equal(await branchSelect.locator("option:checked").textContent(), longBranch);
  assert.equal(await branchSelect.locator('optgroup[label="Local branches"]').locator(`option[value="${localRef}"]`).count(), 1);
  assert.equal(await branchSelect.locator('optgroup[label="Remote branches"]').locator(`option[value="${remoteRef}"]`).textContent(), "origin/release");
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
  const latestRow = viewer.locator(`[data-wv-toggle-commit="${head}"]`);
  await latestRow.hover();
  assert.equal(await latestRow.locator("code").textContent(), "aaaaaaa");
  const commitInfo = await latestRow.getAttribute("title");
  assert.match(commitInfo, new RegExp(`Latest commit\\nFull hash ${head}\\nAuthor A & B\\nCommitted .*2026`));
  assert.equal(await latestRow.getAttribute("aria-label"), commitInfo);
  const copy = viewer.locator(`[data-wv-copy-commit="${head}"]`);
  await copy.focus();
  await viewer.keyboard.press("Shift+Tab");
  assert.equal(await latestRow.evaluate(row => row.matches(":focus-visible")), true);
  assert.equal(await commitRows.first().locator(".wv-commit-info").innerText(), commitInfo);
  const longRow = viewer.locator(`[data-wv-toggle-commit="${older}"]`);
  assert.ok((await longRow.getAttribute("title")).startsWith(longSubject));
  assert.ok((await longRow.getAttribute("title")).includes(`\nAuthor ${longAuthor}\n`));
  assert.equal(await longRow.locator(".wv-row-name > span:last-child").evaluate(el => el.scrollWidth > el.clientWidth), true);
  await desktop.ctx.grantPermissions(["clipboard-read", "clipboard-write"], {origin});
  await copy.click();
  assert.equal(await viewer.evaluate(() => navigator.clipboard.readText()), head);
  await viewer.locator('[data-wv-scope="commit"][data-wv-commit="' + head + '"]').click();
  await viewer.locator(".wv-hunk").first().waitFor();
  assert.equal(await viewer.locator(".wv-hunk").count(), 2);
  async function checkToolbar(page, width, names, constrained = false) {
    await page.setViewportSize({width, height: 800});
    const toolbar = page.locator(".wv-toolbar");
    await page.locator(".wv-hunk").first().waitFor();
    if (constrained) await toolbar.evaluate(el => el.style.maxWidth = "280px");
    const layout = await toolbar.evaluate(el => {
      const boxes = [...el.children].map(control => control.getBoundingClientRect());
      const center = ({top, height}) => top + height / 2;
      return {
        sameRow: boxes.length === 4 && boxes.every(box => Math.abs(center(box) - center(boxes[0])) < 1),
        noOverlap: boxes.every((box, index) => !index || boxes[index - 1].right <= box.left),
        scrollable: getComputedStyle(el).overflowX === "auto" && el.scrollWidth > el.clientWidth,
        pageFits: document.documentElement.scrollWidth <= innerWidth,
      };
    });
    assert.ok(layout.sameRow && layout.noOverlap && layout.pageFits, JSON.stringify({width, layout}));
    if (constrained) assert.equal(layout.scrollable, true);
    const controls = toolbar.locator("[aria-label]");
    assert.deepEqual(await controls.evaluateAll(nodes => nodes.map(node => node.getAttribute("aria-label"))), names);
    assert.deepEqual(await controls.evaluateAll(nodes => nodes.map(node => node.title)), names);
    await controls.nth(2).focus();
    await page.keyboard.press("Tab");
    const focus = await controls.nth(3).evaluate(button => ({
      active: document.activeElement?.getAttribute("aria-label"),
      visible: button.matches(":focus-visible"),
      outline: getComputedStyle(button).outlineStyle,
      fullyVisible: button.getBoundingClientRect().right <= button.closest(".wv-toolbar").getBoundingClientRect().right + 1,
    }));
    assert.deepEqual(focus, {active: names[3], visible: true, outline: "solid", fullyVisible: true}, `toolbar focus at ${width}px`);
    if (constrained) await toolbar.evaluate(el => el.style.removeProperty("max-width"));
  }
  const enToolbar = ["Ignore whitespace", "Wrap lines", "Previous hunk", "Next hunk"];
  for (const width of [1280, 1024, 390]) await checkToolbar(viewer, width, enToolbar);
  await checkToolbar(viewer, 390, enToolbar, true);
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

  await viewer.goto(origin + "/workspace-viewer?task=" + task.id);
  await branchSelect.selectOption(localRef);
  await viewer.waitForURL(url => url.searchParams.get("branch") === localRef);
  await commitGroup.click();
  await viewer.locator(`[data-wv-toggle-commit="${localHead}"]`).waitFor();
  await branchNotices.getByText("Local branch list truncated.", {exact: true}).waitFor();
  await branchNotices.getByText("The remote branch list reached its scan limit and may be incomplete.", {exact: true}).waitFor();
  await viewer.locator('[data-wv-action="baseChanges"]').click();
  await viewer.locator('[data-wv-scope="base"]').click();
  await viewer.getByText("+release change", {exact: true}).waitFor();
  await viewer.getByRole("combobox", {name: "Branch"}).selectOption(remoteRef);
  await viewer.locator(`[data-wv-toggle-commit="${remoteHead}"]`).waitFor();
  assert.equal(await branchNotices.getByText("Local branch list truncated.", {exact: true}).count(), 0);
  await branchNotices.getByText("The remote branch list reached its scan limit and may be incomplete.", {exact: true}).waitFor();
  assert.equal(await viewer.locator(`[data-wv-toggle-commit="${localHead}"]`).count(), 0);
  assert.equal(await viewer.locator('[data-wv-action="uncommitted"]').count(), 0);
  assert.equal(await viewer.locator(".wv-tab").count(), 0);
  await viewer.locator(`[data-wv-scope="commit"][data-wv-commit="${remoteHead}"]`).click();
  const remoteDiff = viewer.getByText("+origin/release change", {exact: true});
  await remoteDiff.waitFor();
  await viewer.reload();
  await remoteDiff.waitFor();
  assert.equal(await branchSelect.inputValue(), remoteRef);
  assert.equal(new URL(viewer.url()).searchParams.get("commit"), remoteHead);
  assert.ok(desktop.queries.some(({view, query}) => view === "diff" && query.get("branch") === localRef && query.get("scope") === "base"));
  assert.ok(desktop.queries.some(({view, query}) => view === "diff" && query.get("branch") === remoteRef && query.get("commit") === remoteHead));

  const refresh = viewer.locator('[data-wv-action="refresh"]');
  desktop.failRemoteBranch("workspace_viewer_git_failed");
  await refresh.click();
  await viewer.locator(".wv-notice").getByText("Unable to read Git information for this workspace.").waitFor();
  assert.equal(await remoteDiff.count(), 0);
  assert.equal(await branchSelect.inputValue(), remoteRef);
  desktop.failRemoteBranch("");
  await refresh.click();
  await remoteDiff.waitFor();
  desktop.failRemoteBranch("workspace_viewer_invalid_branch");
  await viewer.waitForResponse(response => response.status() === 400 && new URL(response.url()).searchParams.get("summary") === "1");
  const missingBranch = viewer.locator(".wv-notice").getByText("The selected branch is missing or unavailable.");
  await missingBranch.waitFor();
  assert.equal(await remoteDiff.count(), 0);
  assert.equal(await branchSelect.inputValue(), remoteRef);
  await viewer.reload();
  await missingBranch.waitFor();
  assert.equal(new URL(viewer.url()).searchParams.get("branch"), remoteRef);
  await branchSelect.selectOption("");
  await viewer.locator('[data-wv-action="uncommitted"]').waitFor();
  assert.equal(new URL(viewer.url()).searchParams.has("branch"), false);

  await branchSelect.selectOption(orphanRef);
  await viewer.locator(".wv-navigator").getByText("This branch and the base have no common ancestor. The diff against base is unavailable.").waitFor();
  assert.match(await commitGroup.textContent(), /Recent commits/);
  assert.equal(await viewer.locator(".wv-counts, [data-wv-action=baseChanges]").count(), 0);
  await commitGroup.click();
  await viewer.locator(`[data-wv-toggle-commit="${orphanHead}"]`).waitFor();
  await viewer.getByRole("button", {name: "Working tree files", exact: true}).click();
  await viewer.locator('[data-wv-scope="file"][data-wv-path="README.md"]').click();
  await viewer.locator(".wv-code").getByText("<script>literal only</script>", {exact: false}).waitFor();
  await viewer.locator(".wv-tab .wv-badge").getByText("Working tree", {exact: true}).waitFor();
  await viewer.locator(".wv-content-head .wv-badge").getByText("Working tree", {exact: true}).waitFor();
  const fileQuery = desktop.queries.findLast(({view, query}) => view === "file" && query.get("path") === "README.md");
  assert.ok(fileQuery && !fileQuery.query.has("branch"));
  assert.equal(await branchSelect.inputValue(), orphanRef);
  assert.ok(desktop.queries.every(({method}) => method === "GET"));
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
  await checkHeader(narrow, 390);
  const narrowBranch = narrow.getByRole("combobox", {name: "分支"});
  const narrowNotices = narrow.locator("header .wv-branch-notices");
  await narrowNotices.getByText("本地分支列表已截断。", {exact: true}).waitFor();
  assert.equal(await narrowBranch.locator("option:checked").textContent(), longBranch);
  await narrowBranch.selectOption(remoteRef);
  await narrowNotices.getByText("远程分支列表达到读取上限，可能不完整。", {exact: true}).waitFor();
  await narrow.locator('[data-wv-action="commits"]').click();
  await narrow.locator(`[data-wv-scope="commit"][data-wv-commit="${remoteHead}"]`).click();
  await checkToolbar(narrow, 390, ["忽略空白", "自动换行", "上一处差异", "下一处差异"]);
  const narrowCommit = narrow.locator(`[data-wv-toggle-commit="${remoteHead}"]`);
  assert.equal(await narrowCommit.locator("code").textContent(), "eeeeeee");
  assert.match(await narrowCommit.getAttribute("title"), new RegExp(`完整哈希 ${remoteHead}\\n作者 A & B\\n提交时间 .*2026`));
  await narrowBranch.selectOption(orphanRef);
  await narrow.locator(".wv-navigator").getByText("该分支与基线无共同祖先，无法计算基线差异。").waitFor();
  assert.equal(await narrow.locator(".wv-counts, [data-wv-action=baseChanges]").count(), 0);
  await narrow.getByRole("button", {name: "工作区文件", exact: true}).click();
  await narrow.locator('[data-wv-scope="file"][data-wv-path="README.md"]').click();
  await narrow.locator(".wv-content-head .wv-badge").getByText("工作区", {exact: true}).waitFor();
  await narrow.locator(".wv-tab .wv-badge").getByText("工作区", {exact: true}).waitFor();
  assert.equal(await narrow.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  await narrow.goto(origin + "/workspace-viewer?task=missing");
  await narrow.getByText("该任务的工作区或工作树不可用。").waitFor();
  await mobile.ctx.close();
  const terminalLocales = {
    "en-US": {
      title: "Workspace terminal", lock: /LAN terminal locked/, allow: /Allow workspace terminals over LAN/,
      save: "Save terminal setting", saved: "Workspace terminal setting saved.", refresh: "Refresh",
      new: "New terminal on this workspace host", split: "Split terminal side by side", input: "Terminal input",
      maximize: "Maximize terminal", restore: "Restore terminal height", expand: "Expand terminal",
      switcher: "Switch workspace", ssh: /Terminal 1 SSH/, close: "Close Terminal 2", first: "Terminal 1", second: "Terminal 2",
    },
    "zh-CN": {
      title: "工作区终端", lock: /局域网终端已锁定/, allow: /允许通过局域网访问终端/,
      save: "保存终端设置", saved: "工作区终端设置已保存。", refresh: "刷新",
      new: "在当前工作区主机新建终端", split: "分屏（左右两栏）", input: "终端输入",
      maximize: "最大化终端", restore: "恢复终端高度", expand: "展开终端",
      switcher: "切换工作区", ssh: /终端 1 SSH/, close: "关闭 终端 2", first: "终端 1", second: "终端 2",
    },
  };
  for (const [width, locale] of [[1280, "en-US"], [1024, "en-US"], [390, "en-US"], [1024, "zh-CN"]]) {
    const labels = terminalLocales[locale];
    const terminal = await context(width, locale, true);
    const page = await terminal.ctx.newPage();
    await page.goto(origin + "/workspace-viewer?task=" + task.id);
    const lock = page.getByRole("link", {name: labels.lock});
    await lock.waitFor();
    const drawer = page.locator(".wv-terminal:visible");
    assert.equal(Math.round((await drawer.boundingBox()).height), 37);
    assert.equal(await drawer.getByRole("tab").count(), 0);
    assert.equal(terminal.terminalRequests.length, 0);
    const openingSettings = page.waitForEvent("popup");
    await lock.click();
    const settings = await openingSettings;
    await settings.waitForURL("**/#other");
    const title = settings.locator("section:has(> #workspace-terminal-settings-form) > .panel-head");
    await title.getByText(labels.title, {exact: true}).waitFor();
    const toggle = settings.getByRole("checkbox", {name: labels.allow});
    assert.equal(await toggle.isChecked(), false);
    await toggle.check();
    await settings.getByRole("button", {name: labels.save}).click();
    await settings.getByText(labels.saved).waitFor();
    await settings.reload();
    await settings.locator('[name="workspace_terminal_lan"]:checked').waitFor();
    await page.bringToFront();
    await page.getByRole("button", {name: labels.refresh, exact: true}).click();
    const left = drawer.locator('[data-pane="0"]');
    const right = drawer.locator('[data-pane="1"]');
    await left.getByText(/PTY fixture for pty-1/).waitFor();
    assert.equal(await left.locator(".wv-term-cwd").textContent(), "/resolved/worktree");
    await drawer.getByRole("button", {name: labels.new}).click();
    await left.getByText(/PTY fixture for pty-2/).waitFor();
    assert.equal(await drawer.getByRole("tab").count(), 2);
    await drawer.getByRole("button", {name: labels.split}).click();
    await right.getByText(/PTY fixture for pty-1/).waitFor();
    assert.equal(await drawer.locator(".wv-term-tab.in-other").count(), 1);
    const [leftRect, rightRect] = await Promise.all([left.boundingBox(), right.boundingBox()]);
    assert.ok(Math.abs(leftRect.width - rightRect.width) <= 2 && leftRect.x + leftRect.width <= rightRect.x + 1);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await left.getByRole("textbox", {name: labels.input}).focus();
    await page.keyboard.press("X");
    await left.getByText("echo=X", {exact: true}).waitFor();
    assert.ok(terminal.terminalMessages.some(({data}) => typeof data === "string" && JSON.parse(data).cols > 1));

    const resizer = drawer.locator(".wv-term-resizer");
    const initialHeight = (await drawer.boundingBox()).height;
    const drag = await resizer.boundingBox();
    await page.mouse.move(drag.x + drag.width / 2, drag.y + 4);
    await page.mouse.down();
    await page.mouse.move(drag.x + drag.width / 2, drag.y - 66, {steps: 4});
    await page.mouse.up();
    assert.ok((await drawer.boundingBox()).height > initialHeight + 50);
    await resizer.dblclick();
    assert.equal((await drawer.boundingBox()).height, initialHeight);
    await drawer.getByRole("button", {name: labels.maximize}).click();
    assert.ok((await drawer.boundingBox()).height > initialHeight * 1.5);
    await drawer.getByRole("button", {name: labels.restore}).click();
    assert.equal((await drawer.boundingBox()).height, initialHeight);

    await page.keyboard.press("Control+Backquote");
    await left.waitFor({state: "hidden"});
    assert.equal(Math.round((await drawer.boundingBox()).height), 37);
    assert.equal(await drawer.getByRole("tab").count(), 2);
    await drawer.getByRole("button", {name: labels.expand}).click();
    await left.getByText("echo=X", {exact: true}).waitFor();

    const switcher = page.getByRole("combobox", {name: labels.switcher});
    await switcher.selectOption("ssh-workspace");
    await drawer.getByRole("tab", {name: labels.ssh}).waitFor();
    assert.equal(await drawer.getByRole("tab").count(), 1);
    await left.getByText(/PTY fixture for pty-3/).waitFor();
    assert.equal(await left.locator(".wv-term-cwd").textContent(), "/srv/workspace");
    assert.ok(terminal.terminalRequests.some(({method, workspace}) => method === "POST" && workspace === "ssh-workspace"));
    await switcher.selectOption("");
    await right.getByText(/PTY fixture for pty-1/).waitFor();
    await left.getByText("echo=X", {exact: true}).waitFor();
    assert.equal(terminal.terminalRequests.filter(({method}) => method === "POST").length, 3);

    await drawer.getByRole("button", {name: labels.close}).click();
    await drawer.getByRole("tab", {name: labels.second, exact: true}).waitFor({state: "hidden"});
    assert.ok(terminal.terminalRequests.some(({method, path}) => method === "DELETE" && path.endsWith("/pty-2")));
    await page.reload();
    await drawer.getByRole("tab", {name: labels.first, exact: true}).waitFor();
    await left.getByText(/PTY fixture for pty-1/).waitFor();
    assert.equal(terminal.terminalRequests.filter(({method}) => method === "POST").length, 3);

    await toggle.uncheck();
    await settings.getByRole("button", {name: labels.save}).click();
    await lock.waitFor();
    await terminal.ctx.close();
    console.log(`Workspace terminal and LAN settings verified at ${width}px (${locale})`);
  }
  assert.deepEqual(errors, []);
  console.log("Workspace viewer verified at 1280px, 1024px and 390px in real Chromium");
} finally {
  await browser?.close();
  await new Promise(resolve => server.close(resolve));
}
