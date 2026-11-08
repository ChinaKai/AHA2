// Optional real-browser verification that the live inbox switch is gone.
// Live inbox delivery is always on, so no Backend may render the old row, the
// checkbox, or the "experimental" copy, even when the server response still
// carries the historical stream_input field.
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
  console.log("live inbox switch removal skipped: no Edge/Chromium binary found");
  process.exit(0);
}

const {renderAgentConfigDialog} = await import(pathToFileURL(resolve(root, "dist", "task_agents.js")));
const css = await readFile(resolve(root, "dist", "styles.css"), "utf8");
const profile = await mkdtemp(resolve(tmpdir(), "aha2-live-inbox-browser-"));

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

async function renderedDOM(backendName, index) {
  const html = `<!doctype html><html><head><meta charset="utf-8"><style>${css}</style></head><body>
    <div id="host"></div>
    <script>
      document.getElementById("host").innerHTML = ${JSON.stringify(dialog(backendName))};
    </script>
  </body></html>`;
  const file = resolve(profile, `dialog-${index}.html`);
  await writeFile(file, html);
  const {stdout} = await run(browser, [
    "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
    `--user-data-dir=${resolve(profile, `profile-${index}`)}`,
    "--allow-file-access-from-files", "--dump-dom", pathToFileURL(file).href,
  ], {timeout: 60000, maxBuffer: 20 * 1024 * 1024});
  return stdout;
}

try {
  for (const [index, backendName] of ["claude", "codex", "dsh"].entries()) {
    const dom = await renderedDOM(backendName, index);
    assert.doesNotMatch(dom, /agent-config-stream-input-row/, `${backendName} still renders the live inbox switch row`);
    assert.doesNotMatch(dom, /name="stream_input"/, `${backendName} still renders the live inbox checkbox`);
    assert.doesNotMatch(dom, /实验性/, `${backendName} still renders experimental live inbox copy`);
  }
  console.log("live inbox switch removal verified in a real browser");
} finally {
  await rm(profile, {recursive: true, force: true});
}
