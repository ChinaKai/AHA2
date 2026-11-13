import {readFile, mkdir, writeFile, copyFile, rm, cp} from "node:fs/promises";
import {createHash} from "node:crypto";
import {createRequire, stripTypeScriptTypes} from "node:module";
import {resolve} from "node:path";

// Two outputs come out of one build:
//   web/build/  readable ES modules, one file per source module. Tests and the
//               optional browser checks read it, so identifiers and strings
//               stay intact; it is never embedded into the Go binary.
//   web/dist/   production assets: one minified ESM bundle, one minified
//               stylesheet, both with content-hashed names, plus assets and
//               vendor files. This is what Go embeds and serves.
const root = resolve(import.meta.dirname, "..");
const source = resolve(root, "web");
const modulesOutput = resolve(source, "build");
const output = resolve(source, "dist");

// esbuild is pinned in web/package.json. Resolve it from there so a plain
// `node scripts/build-web.mjs` works from the repository root.
let esbuild;
try {
  esbuild = createRequire(resolve(source, "package.json"))("esbuild");
} catch {
  throw new Error("esbuild is not installed; run `npm ci` in web/ before building");
}

const moduleNames = ["api", "i18n", "icons", "types", "pet_bubble", "pet_navigation", "pet_approvals", "pet_workbench", "runtime_picker", "task_agents", "task_composer", "task_filters", "hardware_terminal", "hardware_panel", "hardware_resources", "mail_cards", "mail_editor", "mail_resources", "mail_task", "resources", "desktop_panel", "task_tools", "markdown", "conversation_ui", "ui_helpers", "model_health", "prompt_admin", "proxy_settings", "data_retention", "sync_settings", "codex_accounts", "knowledge_workspace", "channels", "assistant", "backends", "main"];

async function writeAssets(target) {
  await mkdir(resolve(target, "vendor"), {recursive: true});
  await cp(resolve(source, "src", "assets", "assistant"), resolve(target, "assets", "assistant"), {recursive: true});
  await copyFile(resolve(root, "assets", "aha-logo.svg"), resolve(target, "assets", "aha-logo.svg"));
  await copyFile(resolve(root, "assets", "aha2-logo.png"), resolve(target, "assets", "aha2-logo.png"));
  await copyFile(resolve(root, "assets", "aha2.ico"), resolve(target, "favicon.ico"));
  for (const name of ["xterm.js", "xterm.css", "xterm.LICENSE"]) {
    await copyFile(resolve(source, "vendor", name), resolve(target, "vendor", name));
  }
}

async function buildModules() {
  await rm(modulesOutput, {recursive: true, force: true});
  await mkdir(modulesOutput, {recursive: true});
  await writeAssets(modulesOutput);

  for (const name of moduleNames) {
    const input = await readFile(resolve(source, "src", `${name}.ts`), "utf8");
    const transformed = stripTypeScriptTypes(input, {mode: "transform", sourceMap: false});
    const outputName = name === "main" ? "app" : name;
    await writeFile(resolve(modulesOutput, `${outputName}.js`), transformed);
  }
  await copyFile(resolve(source, "index.html"), resolve(modulesOutput, "index.html"));
  const styles = await readFile(resolve(source, "src", "styles.css"), "utf8");
  const mailStyles = await readFile(resolve(source, "src", "mail.css"), "utf8");
  await writeFile(resolve(modulesOutput, "styles.css"), `${styles}\n${mailStyles}`);

  // A single build version keeps every shared singleton module on the exact
  // same URL; two URLs would instantiate api.js twice and drop the CSRF token.
  const versionSource = await Promise.all([
    ...moduleNames.map(name => readFile(resolve(modulesOutput, `${name === "main" ? "app" : name}.js`))),
    readFile(resolve(modulesOutput, "styles.css")),
    readFile(resolve(modulesOutput, "vendor", "xterm.js")),
    readFile(resolve(modulesOutput, "vendor", "xterm.css")),
  ]);
  const version = createHash("sha256").update(Buffer.concat(versionSource)).digest("hex").slice(0, 12);

  // Rewrite each module's relative imports to the versioned URLs of the modules
  // that hold shared state. Missing a rewrite here duplicates a singleton at
  // runtime, so the list stays explicit per file.
  const importers = {
    "app.js": ["api", "i18n", "icons", "pet_approvals", "pet_workbench", "runtime_picker", "task_agents", "task_composer", "task_filters", "hardware_panel", "desktop_panel", "hardware_terminal", "task_tools", "markdown", "conversation_ui", "assistant", "backends", "ui_helpers", "model_health", "prompt_admin", "proxy_settings", "data_retention", "sync_settings", "codex_accounts", "hardware_resources", "knowledge_workspace", "channels", "resources", "mail_task", "mail_cards", "mail_resources"],
    "runtime_picker.js": ["backends"],
    "task_agents.js": ["icons", "runtime_picker"],
    "conversation_ui.js": ["icons", "mail_cards", "markdown", "task_agents"],
    "task_composer.js": ["icons", "i18n"],
    "task_tools.js": ["icons", "hardware_panel", "desktop_panel", "task_agents"],
    "hardware_panel.js": ["api", "icons", "hardware_terminal"],
    "hardware_resources.js": ["api", "icons"],
    "mail_cards.js": ["icons"],
    "mail_resources.js": ["api", "icons", "mail_cards"],
    "mail_task.js": ["api", "icons", "mail_cards"],
    "resources.js": ["icons", "hardware_resources", "mail_resources"],
    "desktop_panel.js": ["api", "icons"],
    "prompt_admin.js": ["api"],
    "proxy_settings.js": ["api", "icons"],
    "data_retention.js": ["i18n"],
    "sync_settings.js": ["api", "icons"],
    "codex_accounts.js": ["api", "icons"],
    "knowledge_workspace.js": ["api", "icons", "i18n", "markdown"],
    "channels.js": ["api", "icons"],
    "assistant.js": ["api", "pet_bubble", "pet_navigation"],
  };
  for (const [file, dependencies] of Object.entries(importers)) {
    const path = resolve(modulesOutput, file);
    let text = await readFile(path, "utf8");
    for (const dependency of dependencies) {
      text = text.replaceAll(`"./${dependency}.js"`, `"./${dependency}.js?v=${version}"`);
    }
    await writeFile(path, text);
  }

  const indexPath = resolve(modulesOutput, "index.html");
  const indexText = await readFile(indexPath, "utf8");
  const versionedIndex = indexText
    .replace('href="/vendor/xterm.css"', `href="/vendor/xterm.css?v=${version}"`)
    .replace('href="/styles.css"', `href="/styles.css?v=${version}"`)
    .replace('src="/vendor/xterm.js"', `src="/vendor/xterm.js?v=${version}"`)
    .replace('src="/app.js"', `src="/app.js?v=${version}"`);
  await writeFile(indexPath, versionedIndex);
  return version;
}

async function buildProduction() {
  await rm(output, {recursive: true, force: true});
  await mkdir(output, {recursive: true});
  await writeAssets(output);

  const js = await esbuild.build({
    entryPoints: [resolve(source, "src", "main.ts")],
    bundle: true,
    write: false,
    format: "esm",
    platform: "browser",
    target: "esnext",
    minify: true,
    charset: "utf8",
    legalComments: "none",
    logLevel: "silent",
  });
  const jsText = js.outputFiles[0].text;
  const jsHash = contentHash(jsText);
  await writeFile(resolve(output, `app.${jsHash}.js`), jsText);

  const styles = await readFile(resolve(source, "src", "styles.css"), "utf8");
  const mailStyles = await readFile(resolve(source, "src", "mail.css"), "utf8");
  const css = await esbuild.transform(`${styles}\n${mailStyles}`, {
    loader: "css",
    minify: true,
    charset: "utf8",
    legalComments: "none",
  });
  const cssHash = contentHash(css.code);
  await writeFile(resolve(output, `styles.${cssHash}.css`), css.code);

  const template = await readFile(resolve(source, "index.html"), "utf8");
  const appURL = `/app.${jsHash}.js?v=${jsHash}`;
  // The query keeps the existing contract used by hardware_panel.ts when it
  // builds /vendor/xterm.js URLs; the filename hash drives immutable caching.
  const index = template
    .replace(
      '<link rel="stylesheet" href="/styles.css">',
      `<link rel="stylesheet" href="/styles.${cssHash}.css">\n  <link rel="modulepreload" href="${appURL}">`,
    )
    .replace('<script type="module" src="/app.js"></script>', `<script type="module" src="${appURL}"></script>`);
  if (index === template) throw new Error("index.html template no longer matches the build entry points");
  await writeFile(resolve(output, "index.html"), index);
  return {jsHash, cssHash};
}

function contentHash(text) {
  return createHash("sha256").update(text).digest("hex").slice(0, 12);
}

await buildModules();
const production = await buildProduction();
console.log(`Built ${modulesOutput} and ${output}`);
console.log(`Production assets: app.${production.jsHash}.js styles.${production.cssHash}.css`);
