// The channel settings source picker must follow the live knowledge source:
// legacy roots only while the vault is off, vault libraries once it is on.
// Mixing them would let a channel grant keep pointing at the old library.
import assert from "node:assert/strict";
import test from "node:test";
import {resolve} from "node:path";
import {pathToFileURL} from "node:url";

const build = resolve(import.meta.dirname, "..", "build", "channels.js");
const channels = await import(pathToFileURL(build).href);

const legacyContext = {
  models: [],
  accounts: [],
  projects: [
    {id: "project-1", name: "Project One", project_type: "folder"},
    {id: "channel-project", name: "Channel Host", project_type: "channel"},
  ],
  workspaces: [],
  knowledgeIndexes: [
    {id: "knowledge_root_project_project-1", project_id: "project-1"},
    {id: "knowledge_root_project_container-1", project_id: "container-1"},
  ],
  libraries: [{id: "library-1", container_project_id: "container-1", name: "Imported"}],
  knowledgeVaultEnabled: false,
  vaultLibraries: [{id: "project:p1", kind: "project", name: "Demo", project_id: "p1", count: 2}],
};

const vaultContext = {
  ...legacyContext,
  knowledgeVaultEnabled: true,
  vaultLibraries: [
    {id: "global", kind: "global", name: "全局知识", count: 4},
    {id: "project:p1", kind: "project", name: "Demo", project_id: "p1", count: 2},
    {id: "archive:old", kind: "archive", name: "Old", project_id: "old", count: 1},
  ],
};

test("vault off keeps the legacy project and library sources", () => {
  const sources = channels.channelKnowledgeSources(legacyContext);
  assert.deepEqual(sources.map(item => item.root), ["knowledge_root_project_project-1", "knowledge_root_project_container-1"]);
  assert.ok(sources.every(item => item.kind === "legacy" && item.selectable));
  assert.ok(sources.every(item => !item.root.startsWith("project:")), "legacy mode must not surface vault library ids");
});

test("vault on uses vault libraries and never legacy roots", () => {
  const sources = channels.channelKnowledgeSources(vaultContext);
  assert.deepEqual(sources.map(item => item.root), ["global", "project:p1"]);
  assert.ok(sources.every(item => item.kind === "vault" && item.selectable === false));
  assert.ok(sources.every(item => !item.root.startsWith("knowledge_root_")), "vault mode leaked a legacy root");
});

test("vault on with no readable libraries returns an empty list instead of legacy fallback", () => {
  const sources = channels.channelKnowledgeSources({...vaultContext, vaultLibraries: []});
  assert.deepEqual(sources, []);
});