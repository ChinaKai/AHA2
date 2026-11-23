import assert from "node:assert/strict";
import {readFile} from "node:fs/promises";
import {createServer} from "node:http";
import {resolve} from "node:path";
import {setTimeout as delay} from "node:timers/promises";
import {pathToFileURL} from "node:url";

const root = resolve(import.meta.dirname, "../..");
const {chromium} = await import(pathToFileURL(process.env.AHA_PLAYWRIGHT_MODULE || resolve(root, ".tools/web-validation/node_modules/playwright/index.mjs")));
const mock = {dismissedAt: null, model: false, cli: false, workspace: false, task: false, taskRequests: 0, testOK: false};
const workspace = {id: "workspace_starter_test", project_id: "project_starter", name: "Starter Workspace", root_path: "/aha/data/starter-workspaces/workspace_starter_test"};
const model = {id: "model-test", name: "Provider Test", backend: "codex", updated_at: "2026-01-01T00:00:00Z"};
const task = {id: "task_starter_test", project_id: workspace.project_id, workspace_id: workspace.id, title: "Explore my workspace", status: "draft", isolation: "inplace", collaboration_mode: "single", max_agents: 1};

const server = createServer(async (request, response) => {
  const {pathname} = new URL(request.url, "http://localhost");
  const json = (body, status = 200) => {
    response.writeHead(status, {"Content-Type": "application/json"});
    response.end(JSON.stringify(body));
  };
  const body = async () => {
    let content = "";
    for await (const chunk of request) content += chunk;
    return JSON.parse(content);
  };
  if (pathname === "/api/v1/auth/status") {
    const authenticated = request.headers.cookie?.includes("onboarding-test=1") || false;
    return json({authenticated, registration_open: !authenticated, username: "owner", csrf_token: authenticated ? "csrf" : ""});
  }
  if (pathname === "/api/v1/auth/register") {
    response.setHeader("Set-Cookie", "onboarding-test=1; Path=/; HttpOnly; SameSite=Lax");
    return json({username: "owner", csrf_token: "csrf"}, 201);
  }
  if (pathname === "/api/v1/settings/general") return json({general: {language: "en-US"}});
  if (pathname === "/api/v1/system") return json({system: {}});
  if (pathname === "/api/v1/assistant") return json({assistant: {character: "none", pet_enabled: false}, models: []});
  if (pathname === "/api/v1/projects") return json({projects: mock.workspace ? [{id: workspace.project_id, name: workspace.name, project_type: "folder"}] : []});
  if (pathname === "/api/v1/workspaces") return json({workspaces: mock.workspace ? [workspace] : []});
  if (pathname === "/api/v1/tasks") return json({tasks: mock.task ? [task] : []});
  if (pathname === "/api/v1/onboarding") {
    return json({
      show_automatically: !mock.dismissedAt && !mock.task, onboarding_dismissed_at: mock.dismissedAt,
      backend_cli: {codex: mock.cli, claude: false, dsh: false}, models: mock.model ? [model] : [],
      workspaces: mock.workspace ? [workspace] : [],
      task_id: mock.task ? task.id : undefined, task_workspace_id: mock.task ? workspace.id : undefined,
    });
  }
  if (pathname === "/api/v1/onboarding/dismiss") {
    assert.equal(request.headers["x-csrf-token"], "csrf");
    mock.dismissedAt ||= new Date().toISOString();
    return json({ok: true});
  }
  if (pathname === "/api/v1/onboarding/workspace") {
    assert.equal(request.headers["x-csrf-token"], "csrf");
    const payload = await body();
    if (payload.root_path) return json({error: "onboarding_directory_unavailable"}, 400);
    mock.workspace = true;
    return json({workspace});
  }
  if (pathname === "/api/v1/models/model-test/health-check") {
    return json({health: {status: mock.testOK ? "ok" : "unavailable"}});
  }
  if (pathname === "/api/v1/onboarding/task") {
    const payload = await body();
    assert.equal(payload.workspace_id, workspace.id);
    assert.equal(payload.model_id, model.id);
    assert.equal(payload.title, "Explore my workspace");
    assert.match(payload.request, /README/);
    mock.taskRequests++;
    await delay(350);
    mock.task = true;
    return json({task});
  }
  if (pathname.startsWith("/api/v1/events")) {
    response.writeHead(204);
    return response.end();
  }
  if (pathname.startsWith("/api/v1/")) return json({});
  try {
    const file = pathname === "/" ? "index.html" : pathname.slice(1);
    const type = file.endsWith(".js") ? "text/javascript" : file.endsWith(".css") ? "text/css" : file.endsWith(".svg") ? "image/svg+xml" : "text/html";
    response.writeHead(200, {"Content-Type": type});
    response.end(await readFile(resolve(root, "web/build", file)));
  } catch {
    response.end();
  }
});
await new Promise(done => server.listen(0, "127.0.0.1", done));
const url = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch({executablePath: process.env.AHA_BROWSER_EXECUTABLE, args: ["--no-sandbox"]});

try {
  const context = await browser.newContext({locale: "en-US", viewport: {width: 390, height: 800}});
  const page = await context.newPage();
  const pageErrors = [];
  page.on("pageerror", error => pageErrors.push(error.message));
  await page.goto(url);
  await page.locator("#auth-form").waitFor();
  assert.equal(await page.locator(".onboarding-card").count(), 0);
  await page.locator('[name="setup_token"]').fill("test-setup-token");
  await page.locator('[name="password"]').fill("test-owner-password");
  await page.locator('[name="password"]').press("Enter");

  const card = page.locator(".onboarding-card");
  await card.waitFor();
  const live = card.locator(".onboarding-bubble");
  assert.equal(await live.getAttribute("aria-live"), "polite");
  assert.match(await live.textContent(), /before adding a model/);
  assert.doesNotMatch(await card.innerText(), /\p{Script=Han}/u);
  assert.equal(await card.locator("#onboarding-create-task").isDisabled(), true);
  assert.equal(await card.evaluate(element => element.scrollWidth <= element.clientWidth), true);
  await card.locator("#onboarding-model-settings").press("Enter");
  await page.getByRole("button", {name: "Return to setup"}).press("Enter");
  await card.waitFor();

  await card.locator("#onboarding-existing").press("Space");
  await card.locator("#onboarding-folder").fill("/unavailable");
  await card.locator("#onboarding-folder").press("Enter");
  await card.getByText(/The folder is unavailable/).waitFor();
  assert.equal(await card.locator("#onboarding-folder").inputValue(), "/unavailable");
  await card.locator("#onboarding-managed").press("Space");
  await card.locator("#onboarding-create-workspace").press("Enter");
  await card.locator("#onboarding-workspace-choice").waitFor();
  assert.equal(await card.locator("#onboarding-create-task").isDisabled(), true);

  const second = await context.newPage();
  await second.goto(url);
  await second.locator(".onboarding-card").waitFor();
  await card.locator("#onboarding-dismiss").press("Enter");
  await card.waitFor({state: "hidden"});
  await second.locator(".onboarding-card").waitFor({state: "hidden"});
  await second.reload();
  await second.locator(".command-palette-fab").waitFor();
  assert.equal(await second.locator(".onboarding-card").count(), 0);

  await page.keyboard.press("Control+k");
  await page.locator("#command-palette-input").fill("open setup checklist");
  await page.locator("#command-palette-input").press("Enter");
  await card.waitFor();
  mock.model = true;
  await card.locator("#onboarding-recheck").press("Enter");
  await card.locator("#onboarding-task-reason").getByText(/Install Codex on the AHA2 server/).waitFor();
  await card.locator("#onboarding-model-test").press("Enter");
  await card.getByText(/model endpoint is unreachable/).waitFor();
  assert.equal(await card.locator("#onboarding-create-task").isDisabled(), true);
  mock.cli = true;
  mock.testOK = true;
  await card.locator("#onboarding-recheck").press("Enter");
  await card.getByText("Codex · Found", {exact: true}).waitFor();
  await card.locator("#onboarding-model-test").press("Enter");
  await card.getByText("Model test passed.", {exact: true}).waitFor();
  assert.equal(await card.locator("#onboarding-create-task").isEnabled(), true);
  assert.match(await card.innerText(), /\.aha2-context\/.*\.git\/info\/exclude/s);
  await card.locator("#onboarding-create-task").evaluate(button => {
    button.click();
    button.click();
  });
  await card.getByText(/Creating the task and verifying/).waitFor();
  await card.getByRole("button", {name: "Open read-only task"}).waitFor();
  assert.equal(mock.taskRequests, 1);
  assert.equal(await second.locator(".onboarding-card").count(), 0);
  assert.doesNotMatch(await card.innerText(), /\p{Script=Han}/u);
  assert.deepEqual(pageErrors, []);
  console.log("Onboarding browser flow passed (en-US, narrow viewport, keyboard, two tabs, recoverable failures)");
} finally {
  await browser.close();
  await new Promise(done => server.close(done));
}
