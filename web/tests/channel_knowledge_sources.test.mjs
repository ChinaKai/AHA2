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
    {id: "system-project", name: "Pet", project_type: "system"},
  ],
  workspaces: [],
  knowledgeIndexes: [
    {id: "knowledge_root_project_project-1", project_id: "project-1"},
    {id: "knowledge_root_project_container-1", project_id: "container-1"},
    {id: "knowledge_root_project_system-project", project_id: "system-project"},
  ],
  libraries: [{id: "library-1", container_project_id: "container-1", name: "Imported"}],
  knowledgeVaultEnabled: false,
  vaultLibraries: [{id: "project:p1", kind: "project", name: "Demo", project_id: "p1", count: 2}],
};

const vaultContext = {
  ...legacyContext,
  knowledgeVaultEnabled: true,
  vaultLibraries: [
    {id: "global", kind: "global", name: "全局知识", root_id: "knowledge_root_global", count: 4},
    {id: "project:p1", kind: "project", name: "Demo", project_id: "p1", root_id: "knowledge_root_project_pv", count: 2},
    {id: "archive:old", kind: "archive", name: "Old", project_id: "old", root_id: "knowledge_root_archive_old", count: 1},
  ],
};

test("vault off keeps the legacy project and library sources", () => {
  const sources = channels.channelKnowledgeSources(legacyContext);
  assert.deepEqual(sources.map(item => item.root), ["knowledge_root_project_project-1", "knowledge_root_project_container-1"]);
  assert.ok(sources.every(item => item.kind === "legacy" && item.selectable));
  assert.ok(sources.every(item => !item.root.startsWith("project:")), "legacy mode must not surface vault library ids");
  // The pet's system project has an index root in the fixture but must never
  // be granted as a channel knowledge source, same as channel hosts.
  assert.ok(sources.every(item => item.root !== "knowledge_root_project_system-project"), "system project must not be offered as a legacy knowledge source");
});

test("vault on lists project roots by index entry id and never legacy roots", () => {
  const sources = channels.channelKnowledgeSources(vaultContext);
  // The picker key ("project:p1") is not a knowledge entry id; grants must use
  // the root_id. Global and archive libraries are not channel-grantable.
  assert.deepEqual(sources.map(item => item.root), ["knowledge_root_project_pv"]);
  assert.ok(sources.every(item => item.kind === "vault" && item.selectable));
  assert.ok(sources.every(item => !item.root.startsWith("knowledge_root_project_project")));
  assert.ok(sources.every(item => !item.root.startsWith("project:")), "vault mode leaked a picker key as a grant id");
});

test("vault on with no readable libraries returns an empty list instead of legacy fallback", () => {
  const sources = channels.channelKnowledgeSources({...vaultContext, vaultLibraries: []});
  assert.deepEqual(sources, []);
});

test("vault project libraries without a root id are not offered as grants", () => {
  const sources = channels.channelKnowledgeSources({...vaultContext, vaultLibraries: [{id: "project:p2", kind: "project", name: "No root", project_id: "p2", count: 1}]});
  assert.deepEqual(sources, []);
});

test("group knowledge payload carries the global choice and project spaces", () => {
  const spaces = [{space_project_id: "p1", access_mode: "read", scope_mode: "all"}];
  // One atomic payload; instance defaults are cleared server-side in the same
  // transaction, so there is no second client call that can fail open.
  assert.deepEqual(channels.channelGroupKnowledgeSelectionPayload(spaces, true), {
    include_global: true, bindings: spaces,
  });
  assert.deepEqual(channels.channelGroupKnowledgeSelectionPayload([], false), {
    include_global: false, bindings: [],
  });
  // Global alone is a valid group selection and must not carry project spaces.
  assert.deepEqual(channels.channelGroupKnowledgeSelectionPayload([], true), {
    include_global: true, bindings: [],
  });
});

test("the group picker renders the global checkbox from the stored selection", () => {
  const html = channels.channelVaultKnowledgeSection(vaultContext, [], true);
  assert.match(html, /name="include_global" checked/);
  assert.match(html, /全局知识（跨项目共享）/);
  const unchecked = channels.channelVaultKnowledgeSection(vaultContext, [], false);
  assert.match(unchecked, /name="include_global" >/);
  assert.doesNotMatch(unchecked, /name="include_global" checked/);
  // The legacy all/selected switch has no effect in the vault save path, so
  // the vault picker must not offer it.
  assert.doesNotMatch(unchecked, /name="knowledge_scope_mode"/);
});
