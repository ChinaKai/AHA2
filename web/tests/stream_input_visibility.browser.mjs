// Optional real-browser verification for the Claude-only stream-json switch.
// The row uses HTML `hidden`, but the global `label`/`.security-setting-toggle`
// display:grid rules override the UA [hidden] rule. This drives the built
// renderer and the full production stylesheet through Edge/Chromium and reads
// the computed display, which is the only way to prove the row is really gone
// for Codex/DSH and visible for Claude.
import assert from "node:assert/strict";
import {execFile} from "node:child_process";
import {existsSync} from "node:fs";
import {mkdtemp, readFile, rm, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";
import {promisify} from "node:util";

const root = resolve(import.meta.dirname, "..");
const run = promisify(execFile);

function browserBinary() {
  const candidates = [
    process.env.AHA2_BROWSER_PATH,
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
    "C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe",
    "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
    "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
  ].filter(Boolean);
  return candidates.find(candidate => existsSync(candidate));
}

const browser = browserBinary();
if (!browser) {
  console.log("stream input visibility skipped: no Edge/Chromium binary found");
  process.exit(0);
}

const {renderAgentConfigDialog} = await import(pathToFileURL(resolve(root, "dist", "task_agents.js")));
const css = await readFile(resolve(root, "dist", "styles.css"), "utf8");
const profile = await mkdtemp(resolve(tmpdir(), "aha2-stream-input-browser-"));

function dialog(backendName) {
  return renderAgentConfigDialog({
    task: {
      id: "task-1", project_id: "project-1", workspace_id: "workspace-1", title: "Task",
      original_request: "", current_goal: "", status: "active", isolation: "inplace",
      collaboration_mode: "auto", max_agents: 3, knowledge_policy: "inherit", skill_ids: [],
      total_tokens: 0, created_at: "", updated_at: "",
    },
    agents: [{
      agent_id: "main", role: "main", title: "Main", backend: backendName,
      model_source: "env", model_id: "model-1", reasoning_effort: "high",
      filesystem: "workspace-write", approval: "never", proxy_enabled: false,
      fast_mode: false, stream_input: true, runtime_config_valid: true, unread_count: 0,
    }],
    turns: [], memory: {task_id: "task-1", current_goal: ""},
  }, "main", [], [], []);
}

async function computedDisplay(backendName, index) {
  const html = `<!doctype html><html><head><meta charset="utf-8"><style>${css}</style></head><body>
    <div id="host"></div>
    <script>
      document.getElementById("host").innerHTML = ${JSON.stringify(dialog(backendName))};
      const row = document.getElementById("agent-config-stream-input-row");
      const style = getComputedStyle(row);
      document.body.setAttribute("data-display", style.display);
      document.body.setAttribute("data-visibility", style.visibility);
    </script>
  </body></html>`;
  const file = resolve(profile, `dialog-${index}.html`);
  await writeFile(file, html);
  const {stdout} = await run(browser, [
    "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
    `--user-data-dir=${resolve(profile, `profile-${index}`)}`,
    "--allow-file-access-from-files", "--dump-dom", pathToFileURL(file).href,
  ], {timeout: 60000, maxBuffer: 20 * 1024 * 1024});
  const display = /data-display="([^"]*)"/.exec(stdout)?.[1];
  const visibility = /data-visibility="([^"]*)"/.exec(stdout)?.[1];
  assert.ok(display, `the browser did not report a computed display for ${backendName}`);
  return {display, visibility};
}

try {
  const claude = await computedDisplay("claude", 0);
  assert.notEqual(claude.display, "none", "the stream-json switch is hidden for Claude");
  assert.notEqual(claude.visibility, "hidden", "the stream-json switch is invisible for Claude");

  for (const [index, backendName] of ["codex", "dsh"].entries()) {
    const hidden = await computedDisplay(backendName, index + 1);
    assert.equal(hidden.display, "none", `the stream-json switch is still displayed for ${backendName} (display=${hidden.display})`);
  }
  console.log("stream input visibility verified in a real browser");
} finally {
  await rm(profile, {recursive: true, force: true});
}
