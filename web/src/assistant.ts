import {api} from "./api.js";
import {icon} from "./icons.js";
import type {AssistantHint, AssistantMessage, AssistantToolEvent, OwnerAssistantView, PetBrainAction, PetLayout, PetState, PetStatePatch} from "./types.js";

const PET_SIZE = 64;
const HINT_POLL_MS = 15000;
const ROAM_MIN_MS = 6000;
const ROAM_MAX_MS = 12000;
const ROAM_INITIAL_MS = 1800;
const DEFAULT_CHARACTER = "gugugaga";
const HINTS_ENABLED = false;
const PET_POSITION_KEY = "aha2.pet.position.v1";
const PET_QUIET_START = 23;
const PET_QUIET_END = 8;
const PET_HOURLY_LIMIT = 6;
const PET_BRAIN_RUNS_KEY = "aha2.pet.brain.runs.v1";
const PET_BRAIN_LAST_KEY = "aha2.pet.brain.last.v1";
const SPEECH_LINE_CHARS = 23;
const SPEECH_BUBBLE_HEIGHT = 60;

type MotionName = "idle" | "walk" | "run" | "jump" | "crouch" | "stand" | "turn" | "happy" | "angry" | "sad" | "joy";

const MOTION_LABELS: Array<[MotionName, string]> = [
  ["walk", "走"], ["jump", "跳"], ["happy", "喜"], ["angry", "怒"],
];

const MOTION_NAMES = new Set<MotionName>(["idle", "walk", "jump", "happy", "angry"]);

interface AssistantCharacter {
  id: string;
  name: string;
  nickname: string;
  description: string;
  kind: "svg" | "image" | "video";
  svg?: string;
  src?: string;
  headSrc?: string;
  moveSrc?: string;
}

const ASSISTANT_CHARACTERS: AssistantCharacter[] = [
  {id: "gugugaga", name: "咕咕嘎嘎", nickname: "咕咕嘎嘎", description: "3D 原版企鹅娘", kind: "image", src: "/assets/assistant/gugu-3d.png", headSrc: "/assets/assistant/gugu-head.png"},
  {id: "deepseek_fish", name: "DeepSeek 大肥鱼", nickname: "大肥鱼", description: "原版鲸鱼娘", kind: "image", src: "/assets/assistant/fish-3d.png", headSrc: "/assets/assistant/fish-head.png"},
];

let mounted = false;
let visible = true;
let layer: HTMLDivElement;
let pet: HTMLButtonElement;
let petArt: HTMLElement;
let miniAvatar: HTMLElement;
let characterButton: HTMLButtonElement;
let assistantName: HTMLElement;
let characterMini: HTMLElement;
let characterMenu: HTMLElement;
let characterOptions: HTMLElement;
let motionButton: HTMLButtonElement;
let motionMenu: HTMLElement;
let motionOptions: HTMLElement;
let bubble: HTMLDivElement;
let trailLayer: HTMLElement;
let bubbleTitle: HTMLElement;
let bubbleBody: HTMLElement;
let panel: HTMLElement;
let modelSelect: HTMLSelectElement;
let modelLabel: HTMLElement;
let view: OwnerAssistantView | null = null;
let hints: AssistantHint[] = [];
let activeHint: AssistantHint | null = null;
let sending = false;
let panelOpen = false;
let petX = 0;
let petY = 0;
let lastInteraction = Date.now();
let hintTimer = 0;
let roamTimer = 0;
let petMoving = false;
let moveRAF = 0;
let motionAnimation: Animation | null = null;
let petState: PetState | null = null;
let cachedCharacter = "";
let characterReady = false;
let applyingRemote = false;
let petSyncTimer = 0;
let pendingPetPatch: PetStatePatch = {};
function storedBrainLast(): number {
  try {
    const explicit = Number(localStorage.getItem(PET_BRAIN_LAST_KEY) || 0) || 0;
    if (explicit > 0) return explicit;
    const runs = brainRunsThisHour();
    return runs.length ? Math.max(...runs) : 0;
  } catch { return 0; }
}
let lastPetBrain = storedBrainLast();
let petBrainInFlight = false;
let speechTimer = 0;
let speechTypeTimer = 0;
let speechPageTimer = 0;
let bubbleEnabled = true;
let petFrozen = false;
let actionMoveUntil = 0;
const toolEventsByMessage = new Map<string, AssistantToolEvent[]>();
const recentActionKeys = new Map<string, number>();
const rawReplyByMessage = new Map<string, string>();
const noticeMessageIDs = new Set<string>();
const errorMessageIDs = new Set<string>();
let typingMessageID = "";
let messageTypeTimer = 0;
let settingsPanel: HTMLElement;
let settingsEnabled: HTMLInputElement;
let settingsBubble: HTMLInputElement;
let thinkingEl: HTMLElement;
let panelThinkingEl: HTMLElement;
let settingsIdleMinutes: HTMLInputElement;
let settingsHourlyLimit: HTMLInputElement;
let settingsQuietStart: HTMLInputElement;
let settingsQuietEnabled: HTMLInputElement;
let settingsQuietEnd: HTMLInputElement;
let settingsCharacters: HTMLElement;
let settingsChatModel: HTMLSelectElement;
let settingsBrainModel: HTMLSelectElement;
let settingsBrainStatus: HTMLElement;
let settingsError: HTMLElement;
let currentMotionName: MotionName = "idle";
let facing = 1;
let motionIdleTimer = 0;
let roamPausedUntil = 0;
let dragging = false;
let dragMoved = false;
let dragStartX = 0;
let dragStartY = 0;
let dragOriginX = 0;
let dragOriginY = 0;

export function mountAssistant(): void {
  if (mounted) {
    if (layer) layer.hidden = false;
    return;
  }
  if (!document.body) return;
  mounted = true;
  layer = document.createElement("div");
  layer.id = "assistant-layer";
  layer.className = "assistant-layer";
  layer.innerHTML = `
    <aside id="assistant-panel" class="assistant-panel" hidden aria-label="小A 助手">
      <header class="assistant-panel-head">
        <button class="assistant-identity" id="assistant-identity-button" type="button" aria-label="打开宠物设置">
          <span class="assistant-mini-avatar" id="assistant-mini-avatar" aria-hidden="true"></span>
          <span class="assistant-identity-copy"><strong id="assistant-name">小A</strong><small id="assistant-model-name">正在加载模型…</small><span id="assistant-panel-thinking" class="assistant-panel-thinking" hidden><i></i><i></i><i></i></span></span>
        </button>
        <div class="assistant-panel-actions">
          <button id="assistant-motion-button" class="assistant-character-button assistant-motion-button" type="button" aria-label="动作" title="动作">动</button>
          <button id="assistant-character-button" class="assistant-character-button" type="button" aria-label="更换形象" title="更换形象">
            <span id="assistant-character-mini" class="assistant-character-mini" aria-hidden="true"></span>
            <span class="assistant-character-badge" aria-hidden="true">✦</span>
          </button>
          <select id="assistant-model" aria-label="助手模型"></select>
          <button id="assistant-close" class="icon-button" type="button" aria-label="收起小A">×</button>
        </div>
      </header>
      <div id="assistant-character-menu" class="assistant-character-menu" hidden>
        <div class="assistant-character-menu-head"><strong>换个形象</strong><small>只改外观，不改能力</small></div>
        <div id="assistant-character-options" class="assistant-character-options"></div>
      </div>
      <div id="assistant-motion-menu" class="assistant-character-menu assistant-motion-menu" hidden>
        <div class="assistant-character-menu-head"><strong>动作</strong><small>模型也可调用</small></div>
        <div id="assistant-motion-options" class="assistant-motion-options"></div>
      </div>
    </aside>
    <div id="assistant-trail" class="assistant-trail" aria-hidden="true"></div>
    <div id="assistant-bubble" class="assistant-bubble" role="status" aria-live="polite" hidden>
      <strong id="assistant-bubble-title"></strong>
      <p id="assistant-bubble-body"></p>
      <div class="assistant-bubble-actions">
        <button type="button" data-assistant-hint-open>查看</button>
        <button type="button" data-assistant-hint-dismiss>知道了</button>
      </div>
    </div>
    <button id="assistant-pet" class="assistant-pet" type="button" aria-label="打开小A助手" aria-expanded="false">
      <span class="assistant-pet-shadow" aria-hidden="true"></span>
      <span id="assistant-pet-art" class="assistant-pet-art" aria-hidden="true"></span>
      <span class="assistant-pet-dot" aria-hidden="true"></span>
      <span id="assistant-thinking" class="assistant-thinking" aria-hidden="true"><b>?</b></span>
    </button>
    <div id="assistant-settings" class="assistant-settings" hidden>
      <div class="assistant-settings-card" role="dialog" aria-modal="true" aria-labelledby="assistant-settings-title">
        <header class="assistant-settings-head">
          <strong id="assistant-settings-title">宠物设置</strong>
          <button id="assistant-settings-close" class="icon-button" type="button" aria-label="关闭宠物设置">×</button>
        </header>
        <div class="assistant-settings-body">
          <label class="assistant-settings-switch"><input type="checkbox" id="pet-enabled"><span><b>启用宠物</b><small>关闭后隐藏宠物、停止自主思考和主动气泡</small></span></label>
          <label class="assistant-settings-switch"><input type="checkbox" id="pet-bubble-enabled"><span><b>显示说话气泡</b><small>关闭后宠物仍会思考，但不再显示文字</small></span></label>
          <section><h3>自主行为</h3>
            <div class="pet-budget-grid">
              <label class="assistant-settings-field">空闲触发（分钟）<input id="pet-idle-minutes" type="number" min="1" max="120"></label>
              <label class="assistant-settings-field">每小时上限（次，0=不限）<input id="pet-hourly-limit" type="number" min="0" max="60"></label>
              <label class="assistant-settings-switch"><input type="checkbox" id="pet-quiet-enabled"><span><b>启用安静时段</b><small>关闭后任何时间都可能自主互动</small></span></label>
              <label class="assistant-settings-field">免打扰开始（点）<input id="pet-quiet-start" type="number" min="0" max="23"></label>
              <label class="assistant-settings-field">免打扰结束（点）<input id="pet-quiet-end" type="number" min="0" max="23"></label>
            </div>
          </section>
          <section><h3>形象</h3><div id="pet-character-options" class="pet-character-options"></div></section>
          <section><h3>自主行为</h3><div id="pet-brain-status" class="assistant-settings-note">正在读取…</div><button type="button" id="pet-brain-trigger" class="assistant-settings-trigger">立即触发一次自主行为</button></section>
          <section><h3>模型</h3>
            <label class="assistant-settings-field">聊天模型<select id="pet-chat-model"></select></label>
            <label class="assistant-settings-field">自主思考模型<select id="pet-brain-model"></select></label>
          </section>
          <p id="pet-settings-error" class="assistant-settings-error" hidden></p>
        </div>
        <footer class="assistant-settings-foot">
          <button id="pet-settings-back" type="button">返回高级设置</button>
          <button id="pet-settings-cancel" type="button">取消</button>
          <button id="pet-settings-save" type="button" class="primary">保存</button>
        </footer>
      </div>
    </div>`;
  document.body.appendChild(layer);
  pet = layer.querySelector<HTMLButtonElement>("#assistant-pet")!;
  petArt = layer.querySelector<HTMLElement>("#assistant-pet-art")!;
  miniAvatar = layer.querySelector<HTMLElement>("#assistant-mini-avatar")!;
  characterButton = layer.querySelector<HTMLButtonElement>("#assistant-character-button")!;
  assistantName = layer.querySelector<HTMLElement>("#assistant-name")!;
  characterMini = layer.querySelector<HTMLElement>("#assistant-character-mini")!;
  characterMenu = layer.querySelector<HTMLElement>("#assistant-character-menu")!;
  characterOptions = layer.querySelector<HTMLElement>("#assistant-character-options")!;
  motionButton = layer.querySelector<HTMLButtonElement>("#assistant-motion-button")!;
  motionMenu = layer.querySelector<HTMLElement>("#assistant-motion-menu")!;
  motionOptions = layer.querySelector<HTMLElement>("#assistant-motion-options")!;
  settingsPanel = layer.querySelector<HTMLElement>("#assistant-settings")!;
  settingsEnabled = layer.querySelector<HTMLInputElement>("#pet-enabled")!;
  settingsBubble = layer.querySelector<HTMLInputElement>("#pet-bubble-enabled")!;
  thinkingEl = layer.querySelector<HTMLElement>("#assistant-thinking")!;
  panelThinkingEl = layer.querySelector<HTMLElement>("#assistant-panel-thinking")!;
  settingsIdleMinutes = layer.querySelector<HTMLInputElement>("#pet-idle-minutes")!;
  settingsHourlyLimit = layer.querySelector<HTMLInputElement>("#pet-hourly-limit")!;
  settingsQuietEnabled = layer.querySelector<HTMLInputElement>("#pet-quiet-enabled")!;
  settingsQuietStart = layer.querySelector<HTMLInputElement>("#pet-quiet-start")!;
  settingsQuietEnd = layer.querySelector<HTMLInputElement>("#pet-quiet-end")!;
  settingsCharacters = layer.querySelector<HTMLElement>("#pet-character-options")!;
  settingsChatModel = layer.querySelector<HTMLSelectElement>("#pet-chat-model")!;
  settingsBrainModel = layer.querySelector<HTMLSelectElement>("#pet-brain-model")!;
  settingsBrainStatus = layer.querySelector<HTMLElement>("#pet-brain-status")!;
  settingsError = layer.querySelector<HTMLElement>("#pet-settings-error")!;
  trailLayer = layer.querySelector<HTMLElement>("#assistant-trail")!;
  trailLayer.style.zIndex = "9";
  bubble = layer.querySelector<HTMLDivElement>("#assistant-bubble")!;
  pet.style.zIndex = "10";
  bubble.style.zIndex = "11";
  bubbleTitle = layer.querySelector<HTMLElement>("#assistant-bubble-title")!;
  bubbleBody = layer.querySelector<HTMLElement>("#assistant-bubble-body")!;
  panel = layer.querySelector<HTMLElement>("#assistant-panel")!;
  panel.style.zIndex = "4";
  modelSelect = layer.querySelector<HTMLSelectElement>("#assistant-model")!;
  modelLabel = layer.querySelector<HTMLElement>("#assistant-model-name")!;
  cachedCharacter = readCachedCharacter();
  characterReady = Boolean(cachedCharacter);
  if (!characterReady) {
    // Do not flash the default character while the saved appearance loads.
    pet.classList.add("character-loading");
    window.setTimeout(markCharacterReady, 2500);
  }
  renderCharacter();
  renderCharacterMenu();
  renderMotionMenu();
  bindEvents();
  try { bubbleEnabled = localStorage.getItem("aha2.pet.bubble.v1") !== "0"; } catch {}
  void refreshPetState();
  // Pet events arrive on the page's single global event stream; see handlePetStreamEvent.
  // Autonomy is scheduled by the server; the UI only exposes a manual kick.
  (window as unknown as {AHA2Assistant?: unknown}).AHA2Assistant = {
    play: (name: string, options?: {duration?: number}) => playMotion(name, options),
    list: () => [...MOTION_NAMES],
    moveTo: (x: number, y: number) => positionAt({x, y}),
    brain: (force?: boolean) => maybeRunPetBrain(Boolean(force)),
  };
  positionAtDock(false);
  restoreLocalPetPosition();
  void refreshAssistant();
  if (HINTS_ENABLED) {
    void refreshHints();
    hintTimer = window.setInterval(() => {
      if (!document.hidden) void refreshHints();
    }, HINT_POLL_MS);
  }
  scheduleRoam(ROAM_INITIAL_MS);
  window.addEventListener("resize", () => {
    if (activeHint) {
      const point = hintPoint(activeHint);
      positionAt(point || dockPoint());
    } else {
      positionAtDock(false);
    }
  });
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) void refreshHints();
  });
}

function setPetFrozen(frozen: boolean): void {
  if (petFrozen === frozen) return;
  petFrozen = frozen;
  if (frozen) {
    if (moveRAF) window.cancelAnimationFrame(moveRAF);
    moveRAF = 0;
    playMotion("idle");
    return;
  }
  scheduleRoam(1200);
}

function scheduleRoam(delay?: number): void {
  window.clearTimeout(roamTimer);
  const wait = delay ?? (ROAM_MIN_MS + Math.random() * (ROAM_MAX_MS - ROAM_MIN_MS));
  roamTimer = window.setTimeout(() => {
    if (visible && !document.hidden && !panelOpen && !activeHint && !sending && !dragging && !petFrozen && Date.now() >= roamPausedUntil) {
      if (Date.now() - lastInteraction >= 1500 && !window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
        positionAt(safePoint());
      }
    }
    scheduleRoam();
  }, wait);
}

export function setAssistantVisible(next: boolean): void {
  if (!mounted || !layer || visible === next) return;
  visible = next;
  layer.hidden = !next;
  if (!next) {
    panelOpen = false;
    panel.hidden = true;
    bubble.hidden = true;
    characterMenu.hidden = true;
    motionMenu.hidden = true;
    activeHint = null;
    return;
  }
  void refreshAssistant();
  void refreshHints();
  scheduleRoam(1200);
}

function bindEvents(): void {
  pet.addEventListener("pointerdown", event => {
    if (event.pointerType === "mouse" && event.button !== 0) return;
    stopPetMove();
    dragging = true;
    dragMoved = false;
    dragStartX = event.clientX;
    dragStartY = event.clientY;
    dragOriginX = petX;
    dragOriginY = petY;
    pet.classList.add("dragging");
    pet.setPointerCapture(event.pointerId);
    lastInteraction = Date.now();
    event.preventDefault();
  });
  pet.addEventListener("pointermove", event => {
    if (!dragging) return;
    const dx = event.clientX - dragStartX;
    const dy = event.clientY - dragStartY;
    if (Math.abs(dx) + Math.abs(dy) > 4) dragMoved = true;
    petX = Math.max(4, Math.min(window.innerWidth - PET_SIZE - 4, dragOriginX + dx));
    petY = Math.max(4, Math.min(window.innerHeight - PET_SIZE - 4, dragOriginY + dy));
    pet.style.transform = `translate3d(${petX}px, ${petY}px, 0)`;
    positionBubble();
    lastInteraction = Date.now();
  });
  const endDrag = (event: PointerEvent) => {
    if (!dragging) return;
    dragging = false;
    pet.classList.remove("dragging");
    if (pet.hasPointerCapture(event.pointerId)) pet.releasePointerCapture(event.pointerId);
    lastInteraction = Date.now();
    if (!dragMoved) openPetDestination();
    saveLocalPetPosition();
    scheduleRoam();
  };
  pet.addEventListener("pointerup", endDrag);
  pet.addEventListener("pointercancel", endDrag);
  window.addEventListener("aha:pet-task-thinking", () => notifyPetTaskThinking());
  window.addEventListener("aha:pet-task-reply", event => notifyPetTaskReply(String((event as CustomEvent<string>).detail || "")));
  window.addEventListener("aha:pet-task-idle", () => notifyPetTaskIdle());
  pet.addEventListener("keydown", event => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      openPetDestination();
    }
  });
  layer.querySelector<HTMLButtonElement>("#assistant-close")!.addEventListener("click", () => setPanelOpen(false));
  layer.querySelector<HTMLButtonElement>("[data-assistant-hint-dismiss]")!.addEventListener("click", () => {
    if (activeHint) void dismissHint(activeHint);
  });
  layer.querySelector<HTMLButtonElement>("[data-assistant-hint-open]")!.addEventListener("click", () => {
    if (!activeHint) return;
    if (activeHint.target_type === "task") {
      window.dispatchEvent(new CustomEvent("aha:assistant-open-task", {detail: activeHint.target_id}));
      hideBubble();
    } else {
      setPanelOpen(true);
    }
  });
  modelSelect.addEventListener("change", () => {
    const modelID = modelSelect.value;
    if (!modelID || !view) return;
    void api.updateOwnerAssistant({model_id: modelID}).then(result => {
      if (view) {
        view = {...view, assistant: result.assistant, models: result.models, model_name: result.model_name};
        renderModel();
      }
    }).catch(error => appendLocalError(error instanceof Error ? error.message : String(error)));
  });
  characterButton.addEventListener("click", event => {
    event.stopPropagation();
    void openPetSettings();
  });
  layer.querySelector<HTMLButtonElement>("#assistant-identity-button")!.addEventListener("click", event => {
    event.stopPropagation();
    void openPetSettings();
  });
  window.addEventListener("aha:pet-settings", () => { void openPetSettings(); });
  settingsPanel.querySelector("#assistant-settings-close")!.addEventListener("click", closePetSettings);
  settingsPanel.querySelector("#pet-settings-cancel")!.addEventListener("click", closePetSettings);
  settingsPanel.querySelector("#pet-settings-back")!.addEventListener("click", () => {
    closePetSettings();
    window.dispatchEvent(new CustomEvent("aha:open-advanced"));
  });
  settingsPanel.querySelector("#pet-settings-save")!.addEventListener("click", () => { void savePetSettings(); });
  settingsPanel.querySelector("#pet-brain-trigger")!.addEventListener("click", () => {
    const button = settingsPanel.querySelector<HTMLButtonElement>("#pet-brain-trigger");
    if (button) button.disabled = true;
    settingsBrainStatus.textContent = "已请求一次自主行为，等待 Task 执行…";
    void maybeRunPetBrain(true).finally(() => { if (button) button.disabled = false; });
  });
  settingsQuietEnabled.addEventListener("change", () => {
    settingsQuietStart.disabled = !settingsQuietEnabled.checked;
    settingsQuietEnd.disabled = !settingsQuietEnabled.checked;
  });
  settingsCharacters.addEventListener("click", event => {
    const option = (event.target as HTMLElement).closest<HTMLElement>("[data-pet-character]");
    if (!option) return;
    settingsCharacters.querySelectorAll("[data-pet-character]").forEach(item => item.classList.toggle("active", item === option));
  });
  settingsPanel.addEventListener("pointerdown", event => event.stopPropagation());
  characterOptions.addEventListener("click", event => {
    const option = (event.target as HTMLElement).closest<HTMLElement>("[data-assistant-character]");
    if (!option) return;
    void chooseCharacter(option.dataset.assistantCharacter || "");
  });
  motionButton.addEventListener("click", event => {
    event.stopPropagation();
    renderMotionMenu();
    motionMenu.hidden = !motionMenu.hidden;
    characterMenu.hidden = true;
  });
  motionOptions.addEventListener("click", event => {
    const option = (event.target as HTMLElement).closest<HTMLElement>("[data-assistant-motion]");
    if (!option) return;
    const selected = option.dataset.assistantMotion || "idle";
    if (selected === "walk") positionAt(safePoint());
    else playMotion(selected);
    motionMenu.hidden = true;
  });
  document.addEventListener("pointerdown", event => {
    const target = event.target as HTMLElement;
    if (!characterMenu.hidden && !target.closest("#assistant-character-menu") && !target.closest("#assistant-character-button")) {
      characterMenu.hidden = true;
    }
    if (!motionMenu.hidden && !target.closest("#assistant-motion-menu") && !target.closest("#assistant-motion-button")) {
      motionMenu.hidden = true;
    }
  }, true);
  document.addEventListener("keydown", event => {
    if (event.key !== "Escape") return;
    if (!motionMenu.hidden) {
      event.preventDefault();
      motionMenu.hidden = true;
      return;
    }
    if (!characterMenu.hidden) {
      event.preventDefault();
      characterMenu.hidden = true;
      return;
    }
    if (panelOpen) {
      event.preventDefault();
      setPanelOpen(false);
    }
  });
  document.addEventListener("pointerdown", () => { lastInteraction = Date.now(); }, true);
  document.addEventListener("keydown", () => { lastInteraction = Date.now(); }, true);
}

async function refreshAssistant(): Promise<void> {
  try {
    const result = await api.ownerAssistant();
    view = {assistant: result.assistant, messages: result.messages || [], models: result.models || [], model_name: result.model_name, brain_model_name: result.brain_model_name, tools: result.tools || []};
    rememberCharacter(view.assistant.character);
    renderCharacter();
    renderCharacterMenu();
    markCharacterReady();
    renderModel();
      if (view.models.length === 0 && view.messages.length === 0) {
      appendAssistantNotice("还没有可用于助手的 Provider 模型。请先到模型页配置一个 chat 或 anthropic 模型，我才能回答问题。");
    }
  } catch (error) {
    if (view) {
      appendLocalError("助手加载失败：" + (error instanceof Error ? error.message : String(error)));
    }
  }
}

async function refreshHints(): Promise<void> {
  if (!HINTS_ENABLED || !mounted || !visible || panelOpen) return;
  try {
    const result = await api.assistantHints();
    hints = result.hints || [];
    const next = hints[0] || null;
    if (!next) {
      if (activeHint) hideBubble();
      return;
    }
    if (activeHint && activeHint.kind === next.kind && activeHint.target_id === next.target_id
      && activeHint.title === next.title && activeHint.body === next.body) {
      return;
    }
    showHint(next);
  } catch {
    // Hints are optional; a network failure must never disturb the page.
  }
}

function showHint(hint: AssistantHint): void {
  if (!HINTS_ENABLED) return;
  activeHint = hint;
  bubbleTitle.textContent = hint.title;
  bubbleBody.textContent = hint.body;
  bubble.hidden = false;
  pet.classList.add("has-hint");
  const point = hintPoint(hint);
  if (point) positionAt(point);
  positionBubble();
}

function hideBubble(): void {
  activeHint = null;
  clearSpeechTimers();
  bubble.hidden = true;
  bubble.classList.remove("speech", "fade-out");
  const actions = bubble.querySelector<HTMLElement>(".assistant-bubble-actions");
  if (actions) actions.hidden = false;
  pet.classList.remove("has-hint");
  setPetFrozen(false);
  positionAtDock(true);
}

async function dismissHint(hint: AssistantHint): Promise<void> {
  hideBubble();
  try {
    await api.dismissAssistantHint(hint.kind, hint.target_id);
  } catch {
    // Dismiss is best-effort; the hint will return after the server cooldown.
  }
}

function saveLocalPetPosition(): void {
  try {
    localStorage.setItem(PET_POSITION_KEY, JSON.stringify({
      x: Math.max(0, Math.min(1, petX / window.innerWidth)),
      y: Math.max(0, Math.min(1, petY / window.innerHeight)),
    }));
  } catch {
    // Private mode or a full quota should not break the pet.
  }
}

function restoreLocalPetPosition(): void {
  try {
    const raw = localStorage.getItem(PET_POSITION_KEY);
    if (!raw) return;
    const point = JSON.parse(raw) as {x?: number; y?: number};
    if (typeof point.x !== "number" || typeof point.y !== "number") return;
    setPetPosition(point.x * window.innerWidth, point.y * window.innerHeight);
  } catch {
    // Ignore malformed local state and keep the dock position.
  }
}

function renderPetSettings(): void {
  if (!view) return;
  settingsEnabled.checked = Boolean(view.assistant.pet_enabled);
  settingsBubble.checked = bubbleEnabled;
  settingsCharacters.innerHTML = ASSISTANT_CHARACTERS.map(character => `<button type="button" class="pet-character-option ${character.id === view!.assistant.character ? "active" : ""}" data-pet-character="${escapeHTML(character.id)}">
    <span class="assistant-character-preview">${characterMarkup(character)}</span>
    <strong>${escapeHTML(character.nickname)}</strong>
  </button>`).join("");
  settingsChatModel.innerHTML = view.models.map(model => `<option value="${escapeHTML(model.id)}" ${model.id === view!.assistant.model_id ? "selected" : ""}>${escapeHTML([model.provider_name, model.display_name].filter(Boolean).join(" / ") || model.id)}</option>`).join("");
  settingsBrainModel.innerHTML = `<option value="">跟随聊天模型</option>` + view.models.map(model => `<option value="${escapeHTML(model.id)}" ${model.id === view!.assistant.brain_model_id ? "selected" : ""}>${escapeHTML(model.display_name || model.id)}</option>`).join("");
  settingsIdleMinutes.value = String(view.assistant.pet_idle_minutes || 5);
  settingsHourlyLimit.value = String(view.assistant.pet_hourly_limit ?? 6);
  settingsQuietEnabled.checked = view.assistant.pet_quiet_enabled !== false;
  settingsQuietStart.disabled = !settingsQuietEnabled.checked;
  settingsQuietEnd.disabled = !settingsQuietEnabled.checked;
  settingsQuietStart.value = String(view.assistant.pet_quiet_start ?? 23);
  settingsQuietEnd.value = String(view.assistant.pet_quiet_end ?? 8);
  const idleMs = Math.max(1, Number(view.assistant.pet_idle_minutes) || 5) * 60_000;
  const serverLast = Date.parse(view.pet_last_brain_at || "");
  const last = serverLast || lastPetBrain || storedBrainLast();
  const serverNext = Date.parse(view.pet_next_brain_at || "");
  const next = serverNext || last + idleMs;
  const nextText = next > Date.now() ? new Date(next).toLocaleTimeString([], {hour: "2-digit", minute: "2-digit"}) : "等待下一次检查";
  const budget = view.pet_brain_runs ?? brainRunsThisHour().length;
  settingsBrainStatus.textContent = `上次：${last ? new Date(last).toLocaleString() : "尚未运行"} · 下次预计：${nextText} · 本小时 ${budget}/${petHourlyLimit()}${inQuietHours() ? " · 当前安静时段" : ""}`;
  settingsError.hidden = true;
}

export async function showPetSettings(): Promise<void> {
  if (!mounted || !layer) return;
  if (!view) {
    await refreshAssistant();
  }
  if (!view) return;
  characterMenu.hidden = true;
  motionMenu.hidden = true;
  try { renderPetSettings(); } catch (error) { console.error("pet settings render failed", error); }
  let attempts = 0;
  const mountInline = () => {
    const host = document.querySelector<HTMLElement>("#pet-settings-host");
    if (host) {
      host.appendChild(settingsPanel);
      settingsPanel.hidden = false;
      settingsPanel.removeAttribute("hidden");
      settingsPanel.classList.add("assistant-settings-inline");
      Object.assign(settingsPanel.style, {position: "static", inset: "auto", zIndex: "auto", display: "block", background: "transparent", padding: "0"});
      return;
    }
    if (attempts++ < 30) window.requestAnimationFrame(mountInline);
  };
  mountInline();
}

function openPetSettings(): void {
  window.dispatchEvent(new CustomEvent("aha:open-pet-settings"));
}

function closePetSettings(): void {
  if (!settingsPanel) return;
  settingsPanel.hidden = true;
  settingsPanel.classList.remove("assistant-settings-inline");
  settingsPanel.removeAttribute("style");
  if (layer && settingsPanel.parentElement?.id === "pet-settings-host") layer.appendChild(settingsPanel);
}

function applyPetEnabled(enabled: boolean): void {
  if (!pet) return;
  if (!enabled) {
    visible = false;
    pet.hidden = true;
    panel.hidden = true;
    bubble.hidden = true;
    panelOpen = false;
    return;
  }
  pet.hidden = false;
  visible = true;
  layer.hidden = false;
  scheduleRoam(1200);
}

async function savePetSettings(): Promise<void> {
  if (!view) return;
  const character = settingsCharacters.querySelector<HTMLElement>(".active")?.dataset.petCharacter || view.assistant.character;
  const actions = ["walk", "jump", "happy", "angry"];
  const enabled = settingsEnabled.checked;
  bubbleEnabled = settingsBubble.checked;
  try { localStorage.setItem("aha2.pet.bubble.v1", bubbleEnabled ? "1" : "0"); } catch {}
  settingsError.hidden = true;
  try {
    const result = await api.updatePetSettings({
      enabled, character, chat_model_id: settingsChatModel.value,
      brain_model_id: settingsBrainModel.value, actions,
      idle_minutes: Number(settingsIdleMinutes.value) || 5,
      hourly_limit: Number(settingsHourlyLimit.value) || 0,
      quiet_enabled: settingsQuietEnabled.checked,
      quiet_start: Number(settingsQuietStart.value),
      quiet_end: Number(settingsQuietEnd.value),
    });
    view = {...view, assistant: result.assistant, models: result.models || view.models, model_name: result.model_name ?? view.model_name, brain_model_name: result.brain_model_name ?? view.brain_model_name};
    rememberCharacter(view.assistant.character);
    renderCharacter();
    markCharacterReady();
    renderModel();
    applyPetEnabled(result.assistant.pet_enabled);
    closePetSettings();
    window.dispatchEvent(new CustomEvent("aha:open-advanced"));
    characterButton.title = "宠物设置";
  } catch (error) {
    settingsError.textContent = error instanceof Error ? error.message : String(error);
    settingsError.hidden = false;
  }
}

function petDeviceID(): string {
  const key = "aha2.pet.device.v1";
  try {
    let id = localStorage.getItem(key) || "";
    if (!id) {
      id = "device_" + Math.random().toString(36).slice(2) + Date.now().toString(36);
      localStorage.setItem(key, id);
    }
    return id;
  } catch {
    return "device_default";
  }
}

function zonePoint(zone: string): {x: number; y: number} {
  const margin = 18;
  const width = window.innerWidth;
  const height = window.innerHeight;
  switch (zone) {
    case "left": return {x: margin, y: height * 0.55};
    case "right": return {x: width - PET_SIZE - margin, y: height * 0.55};
    case "top": return {x: width * 0.5 - PET_SIZE / 2, y: 72};
    case "bottom": return {x: width * 0.5 - PET_SIZE / 2, y: height - PET_SIZE - margin};
    default: return dockPoint();
  }
}

function inQuietHours(now = new Date()): boolean {
  if (view && view.assistant.pet_quiet_enabled === false) return false;
  const start = view?.assistant.pet_quiet_start ?? PET_QUIET_START;
  const end = view?.assistant.pet_quiet_end ?? PET_QUIET_END;
  if (start === end) return false;
  const hour = now.getHours();
  return start < end ? hour >= start && hour < end : hour >= start || hour < end;
}

function petIdleMinutes(): number {
  return view?.assistant.pet_idle_minutes || 5;
}

function petHourlyLimit(): number {
  return view?.assistant.pet_hourly_limit ?? PET_HOURLY_LIMIT;
}

function brainRunsThisHour(): number[] {
  try {
    const raw = localStorage.getItem(PET_BRAIN_RUNS_KEY);
    const values = raw ? JSON.parse(raw) as number[] : [];
    const cutoff = Date.now() - 60 * 60_000;
    return values.filter(value => typeof value === "number" && value >= cutoff);
  } catch {
    return [];
  }
}

function rememberBrainRun(): void {
  try {
    const values = brainRunsThisHour();
    values.push(Date.now());
    localStorage.setItem(PET_BRAIN_RUNS_KEY, JSON.stringify(values.slice(-20)));
    localStorage.setItem(PET_BRAIN_LAST_KEY, String(Date.now()));
  } catch {
    // Budget persistence is best-effort.
  }
}

async function maybeRunPetBrain(force = false): Promise<void> {
  if (!mounted || !visible || sending || petBrainInFlight) return;
  const now = Date.now();
  if (!force && now - lastPetBrain < petIdleMinutes() * 60_000) return;
  if (!force) {
    if (inQuietHours()) return;
    if (petHourlyLimit() > 0 && brainRunsThisHour().length >= petHourlyLimit()) return;
    rememberBrainRun();
  }
  petBrainInFlight = true;
  const thinkingStarted = Date.now();
  setPetFrozen(true);
  setThinking(true);
  try {
    const result = await api.petBrain({
      page: location.pathname + location.hash,
      idle_seconds: Math.floor((now - lastInteraction) / 1000),
      device_id: petDeviceID(),
      layout: currentPetLayout(),
    });
    lastPetBrain = Date.now();
    if (result.say) showPetSpeech(result.say);
    for (const action of (result.actions || []).slice(0, 2)) {
      applyPetBrainAction(action);
    }
  } catch (error) {
    // A failed thought should be invisible; try again after the cooldown.
  } finally {
    petBrainInFlight = false;
    const remaining = Math.max(0, 700 - (Date.now() - thinkingStarted));
    window.setTimeout(() => {
      setThinking(false);
      if (bubble.hidden) setPetFrozen(false);
    }, remaining);
  }
}

function applyPetControl(payload: {say?: string; actions?: PetBrainAction[]}): void {
  if (payload.say) showPetSpeech(payload.say);
  for (const action of (payload.actions || []).slice(0, 3)) applyPetBrainAction(action, true);
}

function applyPetBrainAction(action: PetBrainAction, forceMove = false): void {
  if (!action || !action.type) return;
  const key = JSON.stringify([action.type, action.name || "", action.zone || "", action.task || "", action.x ?? null, action.y ?? null]);
  const now = Date.now();
  if ((recentActionKeys.get(key) || 0) > now - 1500) return;
  recentActionKeys.set(key, now);
  switch (action.type) {
    case "play":
      if (action.name) {
        const duration = action.name === "happy" ? 1500 : action.name === "angry" ? 1000 : 3000;
        playMotion(action.name, {once: true, duration});
      }
      break;
    case "move_to":
      if (action.x != null && action.y != null) {
        const x = action.x * window.innerWidth - PET_SIZE / 2;
        const y = action.y * window.innerHeight - PET_SIZE / 2;
        positionAt({x, y}, forceMove, 3000);
      } else {
        positionAt(zonePoint(action.zone || "dock"), forceMove, 3000);
      }
      break;
    case "walk_path":
      if (action.points && action.points.length >= 2) walkPath(action.points, action.duration_ms || 6000);
      break;
    case "open_task":
      if (action.task) window.dispatchEvent(new CustomEvent("aha:assistant-open-task", {detail: action.task}));
      break;
    case "mood":
      if (action.name) pet.dataset.mood = action.name;
      break;
    case "sleep":
      pet.classList.add("pet-sleeping");
      break;
    case "wake":
      pet.classList.remove("pet-sleeping");
      break;
  }
}

export function notifyPetTaskThinking(): void {
  if (!pet) return;
  pet.classList.add("thinking");
  setThinking(true);
}

export function notifyPetTaskReply(text: string): void {
  if (!pet) return;
  pet.classList.remove("thinking");
  setThinking(false);
  const content = String(text || "").trim();
  if (content) showPetSpeech(content, {holdMs: 3000, fade: true});
}

export function notifyPetTaskIdle(): void {
  if (!pet) return;
  pet.classList.remove("thinking");
  setThinking(false);
}

function setThinking(active: boolean): void {
  if (!thinkingEl) return;
  thinkingEl.classList.toggle("active", active);
  thinkingEl.hidden = !active;
  if (panelThinkingEl) {
    panelThinkingEl.classList.toggle("active", active);
    panelThinkingEl.hidden = !active;
  }
}

function clearSpeechTimers(): void {
  window.clearTimeout(speechTimer);
  window.clearInterval(speechTypeTimer);
  window.clearTimeout(speechPageTimer);
}

function showPetSpeech(text: string, options: {holdMs?: number; fade?: boolean} = {}): void {
  if (!bubble || panelOpen || !bubbleEnabled || !text) return;
  clearSpeechTimers();
  setPetFrozen(true);
  activeHint = null;
  bubbleTitle.textContent = nickname();
  bubbleBody.textContent = "";
  bubble.classList.remove("fade-out");
  bubble.classList.add("speech");
  const actions = bubble.querySelector<HTMLElement>(".assistant-bubble-actions");
  if (actions) actions.hidden = true;
  bubble.hidden = false;
  pet.classList.add("pet-typing");
  pet.classList.remove("has-hint");
  positionBubble();
  const holdMs = options.holdMs ?? 3000;
  const normalized = text.replace(/\s+/g, " ").trim();
  const lines: string[] = [];
  for (let offset = 0; offset < normalized.length; offset += SPEECH_LINE_CHARS) {
    lines.push(normalized.slice(offset, offset + SPEECH_LINE_CHARS));
  }
  const content = lines.join("\n");
  const finish = () => {
    if (activeHint) return;
    pet.classList.remove("pet-typing");
    if (options.fade === false) {
      bubble.hidden = true;
      bubble.classList.remove("speech");
      if (actions) actions.hidden = false;
      setPetFrozen(false);
      return;
    }
    bubble.classList.add("fade-out");
    window.setTimeout(() => {
      if (activeHint) return;
      bubble.hidden = true;
      bubble.classList.remove("speech", "fade-out");
      if (actions) actions.hidden = false;
      setPetFrozen(false);
    }, 360);
  };
  {
    let index = 0;
    bubbleBody.textContent = "";
    speechTypeTimer = window.setInterval(() => {
      index += 1;
      bubbleBody.textContent = content.slice(0, index);
      bubbleBody.scrollTop = bubbleBody.scrollHeight;
      positionBubble();
      if (index < content.length) return;
      window.clearInterval(speechTypeTimer);
      speechTimer = window.setTimeout(finish, holdMs);
    }, 37);
  }
}

async function refreshPetState(): Promise<void> {
  try {
    const result = await api.petState();
    applyPetState(result.state);
  } catch {
    // Pet state is an enhancement; the local pet still works without it.
  }
}

// The global /api/v1/events stream carries every hub event, including pet_state
// and pet_action. Handling them here avoids a second long-lived SSE connection
// per page, which otherwise counts against the browser's per-origin limit.
export function handlePetStreamEvent(type: string, data: unknown): void {
  try {
    if (type === "pet_state") {
      const state = (data as {state?: PetState} | undefined)?.state;
      if (state) applyPetState(state);
      return;
    }
    if (type === "pet_action") {
      applyPetControl((data || {}) as {say?: string; actions?: PetBrainAction[]});
    }
  } catch {
    // Ignore malformed pushes and keep the last good state.
  }
}

function applyPetState(state: PetState): void {
  if (!state || !state.id) return;
  petState = state;
  applyingRemote = true;
  if (state.character) rememberCharacter(state.character);
  if (view && state.character && state.character !== view.assistant.character) {
    view = {...view, assistant: {...view.assistant, character: state.character}};
  }
  if (state.character && pet.dataset.character !== state.character) renderCharacter();
  markCharacterReady();
  pet.dataset.mood = state.mood || "neutral";
  pet.classList.toggle("pet-sleeping", Boolean(state.sleeping));
  pet.classList.toggle("pet-quiet", Boolean(state.quiet));
  window.setTimeout(() => { applyingRemote = false; }, 0);
}

function schedulePetSync(patch: PetStatePatch): void {
  if (applyingRemote || !petState) return;
  pendingPetPatch = {...pendingPetPatch, ...patch};
  window.clearTimeout(petSyncTimer);
  petSyncTimer = window.setTimeout(async () => {
    const next = {...pendingPetPatch, version: petState?.version || 0};
    pendingPetPatch = {};
    try {
      const result = await api.updatePetState(next);
      if (result.state) petState = result.state;
    } catch {
      void refreshPetState();
    }
  }, 240);
}

function renderModel(): void {
  if (!view) return;
  modelLabel.textContent = view.model_name ? "模型 · " + view.model_name : "未配置模型";
  const options = view.models.map(model => {
    const label = [model.provider_name, model.display_name].filter(Boolean).join(" / ");
    return `<option value="${escapeHTML(model.id)}" ${model.id === view!.assistant.model_id ? "selected" : ""}>${escapeHTML(label || model.id)}</option>`;
  }).join("");
  modelSelect.innerHTML = options || `<option value="">未配置模型</option>`;
  modelSelect.disabled = view.models.length <= 1;
}

function nickname(): string {
  return currentCharacter().nickname;
}

const PET_CHARACTER_KEY = "aha2.pet.character.v1";

function readCachedCharacter(): string {
  try {
    const id = localStorage.getItem(PET_CHARACTER_KEY) || "";
    return ASSISTANT_CHARACTERS.some(character => character.id === id) ? id : "";
  } catch {
    return "";
  }
}

function rememberCharacter(id: string): void {
  if (!id || id === cachedCharacter || !ASSISTANT_CHARACTERS.some(character => character.id === id)) return;
  cachedCharacter = id;
  try { localStorage.setItem(PET_CHARACTER_KEY, id); } catch {}
}

function markCharacterReady(): void {
  if (!mounted || characterReady || !pet) return;
  characterReady = true;
  pet.classList.remove("character-loading");
  renderCharacter();
}

function currentCharacter(): AssistantCharacter {
  const id = view?.assistant.character || petState?.character || cachedCharacter || DEFAULT_CHARACTER;
  return ASSISTANT_CHARACTERS.find(character => character.id === id) || ASSISTANT_CHARACTERS[0];
}

function motionElement(): HTMLElement | null {
  return petArt?.querySelector<HTMLElement>(".pet-motion") || null;
}

function motionBase(): string {
  return `scale(${facing}, 1)`;
}

function playMotion(name: string, options: {duration?: number; once?: boolean} = {}): boolean {
  const normalized = String(name || "").trim().toLowerCase() as MotionName;
  if (!MOTION_NAMES.has(normalized)) return false;
  if (!applyingRemote && petState) {
    const mood = normalized === "happy" || normalized === "jump" ? "happy" : normalized === "angry" ? "angry" : "";
    if (mood) schedulePetSync({mood});
  }
  const element = motionElement();
  if (!element) return false;
  window.clearTimeout(motionIdleTimer);
  if (motionAnimation) {
    motionAnimation.cancel();
    motionAnimation = null;
  }
  element.style.transform = motionBase();
  element.style.filter = "";
  element.dataset.motion = normalized;
  if (normalized === "idle" || normalized === "stand") roamPausedUntil = 0;
  currentMotionName = normalized;
  const base = motionBase();
  let keyframes: Keyframe[] = [];
  let duration = options.duration || 600;
  let iterations = 1;
  let easing = "ease-in-out";
  let fill: FillMode = "both";
  let autoIdle = true;
  switch (normalized) {
    case "idle":
      keyframes = [
        {transform: `translateY(0) rotate(-1deg) rotateY(-2deg) ${base}`},
        {transform: `translateY(-3px) rotate(1deg) rotateY(2deg) ${base}`},
        {transform: `translateY(0) rotate(-1deg) rotateY(-2deg) ${base}`},
      ];
      duration = options.duration || 4200; iterations = Infinity; break;
    case "walk":
      keyframes = [
        {transform: `translateY(0) rotate(-4deg) rotateY(6deg) ${base}`},
        {transform: `translateY(-6px) rotate(4deg) rotateY(-6deg) ${base}`},
        {transform: `translateY(0) rotate(-4deg) rotateY(6deg) ${base}`},
      ];
      duration = options.duration || 380; iterations = options.once ? 1 : Infinity; break;
    case "run":
      keyframes = [
        {transform: `translateY(0) rotate(-8deg) rotateY(10deg) scale(${facing}, .98)`},
        {transform: `translateY(-10px) rotate(8deg) rotateY(-10deg) scale(${facing}, 1.03)`},
        {transform: `translateY(0) rotate(-8deg) rotateY(10deg) scale(${facing}, .98)`},
      ];
      duration = options.duration || 240; iterations = options.once ? 1 : Infinity; break;
    case "jump":
      keyframes = [
        {transform: `translateY(0) scale(${facing}, 1)`, offset: 0},
        {transform: `translateY(2px) rotateX(-5deg) scale(${facing}, .9)`, offset: .2},
        {transform: `translateY(-28px) scale(${facing}, 1.06)`, offset: .48},
        {transform: `translateY(-10px) scale(${facing}, 1)`, offset: .72},
        {transform: `translateY(0) scale(${facing}, 1)`, offset: 1},
      ];
      duration = options.duration || 760; easing = "ease-out"; break;
    case "crouch":
      keyframes = [
        {transform: base},
        {transform: `translateY(10px) rotateX(5deg) scale(${facing}, .78)`},
      ];
      duration = options.duration || 280; autoIdle = false;
      roamPausedUntil = Date.now() + 120000;
      if (options.once) motionIdleTimer = window.setTimeout(() => playMotion("idle"), 2400);
      break;
    case "stand":
      keyframes = [
        {transform: `translateY(10px) rotateX(5deg) scale(${facing}, .78)`},
        {transform: base},
      ];
      duration = options.duration || 320; break;
    case "turn": {
      const next = -facing;
      keyframes = [
        {transform: `scale(${facing}, 1) rotateY(0deg)`, offset: 0},
        {transform: `scale(0.06, 1) rotateY(18deg)`, offset: 0.38},
        {transform: `scale(${next}, 1) rotateY(0deg)`, offset: 1},
      ];
      facing = next;
      duration = options.duration || 220; easing = "ease-out"; break;
    }
    case "happy":
      keyframes = [
        {transform: `translateY(0) rotate(0deg) ${base}`},
        {transform: `translateY(-10px) rotate(5deg) ${base}`},
        {transform: `translateY(0) rotate(-2deg) ${base}`},
        {transform: `translateY(-6px) rotate(3deg) ${base}`},
        {transform: `translateY(0) rotate(0deg) ${base}`},
      ];
      duration = options.duration || 900; iterations = 2; break;
    case "angry":
      keyframes = [
        {transform: `translateX(-4px) rotate(-2deg) ${base}`, filter: "drop-shadow(0 0 7px rgba(239,68,68,.65))"},
        {transform: `translateX(4px) rotate(2deg) ${base}`, filter: "drop-shadow(0 0 9px rgba(239,68,68,.8))"},
        {transform: `translateX(-4px) rotate(-2deg) ${base}`, filter: "drop-shadow(0 0 7px rgba(239,68,68,.65))"},
      ];
      duration = options.duration || 420; iterations = 3; easing = "linear"; break;
    case "sad":
      keyframes = [
        {transform: base},
        {transform: `translateY(7px) rotate(-4deg) scale(${facing}, .9)`, filter: "saturate(.65)"},
      ];
      duration = options.duration || 1200; autoIdle = false;
      roamPausedUntil = Date.now() + 3200;
      motionIdleTimer = window.setTimeout(() => playMotion("idle"), 3200);
      break;
    case "joy":
      keyframes = [
        {transform: `translateY(0) rotate(0deg) ${base}`},
        {transform: `translateY(-22px) rotate(180deg) scale(${facing}, 1.04)`},
        {transform: `translateY(0) rotate(360deg) ${base}`},
      ];
      duration = options.duration || 900; easing = "ease-out"; break;
  }
  if (!keyframes.length) return false;
  roamPausedUntil = Math.max(roamPausedUntil, Date.now() + duration * (iterations === Infinity ? 1 : iterations) + 300);
  const animation = element.animate(keyframes, {duration, iterations, easing, fill});
  motionAnimation = animation;
  if (iterations === Infinity || !autoIdle) {
    return true;
  }
  animation.onfinish = () => {
    if (currentMotionName === normalized) playMotion("idle");
  };
  return true;
}

function characterMarkup(character: AssistantCharacter): string {
  if (character.kind === "image" && character.src) {
    return `<img class="pet-img" src="${escapeHTML(character.src)}" alt="" draggable="false">`;
  }
  if (character.kind === "video" && character.src) {
    return `<video class="pet-video" src="${escapeHTML(character.src)}" muted playsinline autoplay loop preload="auto"></video>`;
  }
  return character.svg || "";
}

function petMarkup(character: AssistantCharacter): string {
  if (character.kind === "image" && character.src && character.headSrc) {
    return `<span class="pet-2_5d">
      <img class="pet-img pet-body-layer" src="${escapeHTML(character.src)}" alt="">
      <img class="pet-img pet-head-layer" src="${escapeHTML(character.headSrc)}" alt="">
    </span>`;
  }
  return characterMarkup(character);
}

function renderCharacter(): void {
  if (!petArt || !pet) return;
  const character = currentCharacter();
  pet.dataset.character = character.id;
  pet.classList.toggle("has-video", character.kind === "video");
  if (motionAnimation) {
    motionAnimation.cancel();
    motionAnimation = null;
  }
  facing = 1;
  currentMotionName = "idle";
  petArt.innerHTML = `<span class="pet-motion">${petMarkup(character)}</span>`;
  miniAvatar.innerHTML = characterMarkup(character);
  characterMini.innerHTML = characterMarkup(character);
  petMoving = false;
  playMotion("idle");
  if (assistantName) assistantName.textContent = character.nickname;
  panel.setAttribute("aria-label", `${character.nickname} 助手`);
  layer.querySelector<HTMLButtonElement>("#assistant-close")?.setAttribute("aria-label", `收起${character.nickname}`);
  pet.setAttribute("aria-label", `打开${character.nickname}助手`);
  characterButton.title = "宠物设置";
  characterButton.setAttribute("aria-label", "打开宠物设置");
}

function renderCharacterMenu(): void {
  if (!characterOptions) return;
  const selected = currentCharacter().id;
  characterOptions.innerHTML = ASSISTANT_CHARACTERS.map(character => `<button type="button" class="assistant-character-option ${character.id === selected ? "active" : ""}" data-assistant-character="${escapeHTML(character.id)}">
    <span class="assistant-character-preview">${characterMarkup(character)}</span>
    <span class="assistant-character-copy"><strong>${escapeHTML(character.name)}</strong><small>${escapeHTML(character.description)}</small></span>
  </button>`).join("");
}

function renderMotionMenu(): void {
  if (!motionOptions) return;
  motionOptions.innerHTML = MOTION_LABELS.map(([id, label]) => `<button type="button" class="assistant-motion-option" data-assistant-motion="${id}">${label}</button>`).join("");
}

async function chooseCharacter(characterID: string): Promise<void> {
  if (!characterID || !view) return;
  if (characterID === view.assistant.character) {
    characterMenu.hidden = true;
    return;
  }
  try {
    const result = await api.updateOwnerAssistant({character: characterID});
    view = {
      ...view, assistant: result.assistant,
      models: result.models || view.models, model_name: result.model_name ?? view.model_name,
    };
    rememberCharacter(characterID);
    renderCharacter();
    renderCharacterMenu();
    markCharacterReady();
    characterMenu.hidden = true;
    pet.classList.add("character-changed");
    window.setTimeout(() => pet.classList.remove("character-changed"), 720);
  } catch (error) {
    appendLocalError("换形象失败：" + (error instanceof Error ? error.message : String(error)));
  }
}

function assistantReplyText(content: string): string {
  return String(content || "").trim();
}

function appendLocalError(message: string): void {
  if (settingsError) { settingsError.textContent = message; settingsError.hidden = false; }
}

function appendAssistantNotice(message: string): void {
  if (!view) return;
  const item: AssistantMessage = {
    id: "local-notice-" + Date.now(), assistant_id: view.assistant.id,
    role: "assistant", content: message, created_at: new Date().toISOString(),
  };
  noticeMessageIDs.add(item.id);
  view = {...view, messages: [...view.messages, item]};
}

function keepPetClearOfPanel(): void {
  if (panelOpen) positionAtDock(false);
}

function openPetDestination(): void {
  const taskID = view?.assistant.pet_task_id;
  if (taskID) {
    window.dispatchEvent(new CustomEvent("aha:assistant-open-task", {detail: taskID}));
    setPanelOpen(false);
    return;
  }
  setPanelOpen(!panelOpen);
}

function setPanelOpen(open: boolean): void {
  panelOpen = open;
  panel.hidden = !open;
  characterMenu.hidden = true;
  motionMenu.hidden = true;
  pet.setAttribute("aria-expanded", String(open));
  pet.classList.toggle("panel-open", open);
  if (open) {
    hideBubble();
    if (bubble) bubble.hidden = true;
      // Keep the companion visible above the panel instead of letting the
    // opaque dialog cover it. Full-screen roaming resumes when it closes.
    positionAtDock(false);
    window.setTimeout(() => {
      if (panelOpen) positionAtDock(false);
      input.focus();
    }, 80);
  } else {
    positionAtDock(true);
    void refreshHints();
  }
}

function currentPetLayout(): PetLayout {
  const petRect = pet.getBoundingClientRect();
  const panelRect = panel && !panel.hidden ? panel.getBoundingClientRect() : null;
  const elements: Array<{name: string; x: number; y: number; w: number; h: number}> = [];
  const selectors: Array<[string, string]> = [
    [".mobile-header", "mobile_header"],
    [".sidebar", "sidebar"],
    [".bottom-nav", "bottom_nav"],
    [".assistant-panel", "chat_panel"],
    [".assistant-composer", "chat_composer"],
    ["#assistant-messages", "chat_messages"],
    ["#assistant-settings", "settings_panel"],
  ];
  for (const [selector, name] of selectors) {
    const element = document.querySelector<HTMLElement>(selector);
    if (!element || element.hidden) continue;
    const rect = element.getBoundingClientRect();
    if (rect.width < 1 || rect.height < 1) continue;
    elements.push({name, x: Math.round(rect.x), y: Math.round(rect.y), w: Math.round(rect.width), h: Math.round(rect.height)});
  }
  return {
    viewport_width: window.innerWidth,
    viewport_height: window.innerHeight,
    scroll_y: Math.round(window.scrollY),
    pet: {x: Math.round(petRect.x), y: Math.round(petRect.y), w: Math.round(petRect.width), h: Math.round(petRect.height)},
    panel: panelRect ? {x: Math.round(panelRect.x), y: Math.round(panelRect.y), w: Math.round(panelRect.width), h: Math.round(panelRect.height)} : undefined,
    elements,
  };
}

function dockPoint(): {x: number; y: number} {
  if (panelOpen && panel && !panel.hidden) {
    // Default: sit inside the chat panel, just under its header and clear of
    // the composer. The owner can drag the pet anywhere from there.
    const rect = panel.getBoundingClientRect();
    const x = Math.max(8, rect.right - PET_SIZE - 10);
    const y = Math.max(rect.top + 8, Math.min(rect.top + 58, rect.bottom - PET_SIZE - 76));
    return {x, y};
  }
  return {
    x: Math.max(12, window.innerWidth - PET_SIZE - 22),
    y: Math.max(12, window.innerHeight - PET_SIZE - 22),
  };
}

function hintPoint(hint: AssistantHint): {x: number; y: number} | null {
  if (hint.target_type !== "task") return null;
  const target = document.querySelector<HTMLElement>(`[data-task="${CSS.escape(hint.target_id)}"]`);
  if (!target) return null;
  const rect = target.getBoundingClientRect();
  if (rect.bottom < 40 || rect.top > window.innerHeight - 80) return null;
  const gap = 10;
  let x = rect.right + gap;
  if (x + PET_SIZE > window.innerWidth - 12) x = rect.left - PET_SIZE - gap;
  if (x < 12) x = 12;
  const y = Math.max(12, Math.min(window.innerHeight - PET_SIZE - 12, rect.top + 12));
  return {x, y};
}

function safePoint(): {x: number; y: number} {
  // Global bottom layer: roam the entire viewport, including behind panels.
  return {
    x: 8 + Math.random() * Math.max(1, window.innerWidth - PET_SIZE - 16),
    y: 8 + Math.random() * Math.max(1, window.innerHeight - PET_SIZE - 16),
  };
}

function blockedAt(x: number, y: number): boolean {
  const element = document.elementFromPoint(x, y);
  if (!element) return false;
  if (element.closest(".assistant-layer")) return false;
  return Boolean(element.closest("button,a,input,select,textarea,[contenteditable='true'],.sidebar,.bottom-nav,.mobile-header,dialog,.assistant-panel"));
}

function setPetMotion(moving: boolean): void {
  const character = currentCharacter();
  if (character.kind !== "video" || !character.src || petMoving === moving) return;
  petMoving = moving;
  const video = petArt.querySelector<HTMLVideoElement>("video.pet-video");
  if (!video) return;
  const src = moving && character.moveSrc ? character.moveSrc : character.src;
  if (video.getAttribute("src") === src) {
    void video.play().catch(() => undefined);
    return;
  }
  video.src = src;
  video.load();
  void video.play().catch(() => undefined);
}

function stopPetMove(): void {
  pet?.classList.remove("pet-moving");
  if (moveRAF) window.cancelAnimationFrame(moveRAF);
  moveRAF = 0;
  clearTrailGhosts();
  playMotion("idle");
}

function setPetPosition(x: number, y: number): void {
  petX = x;
  petY = y;
  pet.style.transform = `translate3d(${petX}px, ${petY}px, 0)`;
  positionBubble();
}

function clearTrailGhosts(): void {
  if (!trailLayer) return;
  for (const ghost of [...trailLayer.children]) ghost.remove();
}

function spawnTrailGhost(x: number, y: number, remainingMs: number): void {
  if (!trailLayer || !petArt) return;
  while (trailLayer.childElementCount >= 80) trailLayer.firstElementChild?.remove();
  const ghost = document.createElement("div");
  ghost.className = "assistant-pet-ghost";
  ghost.style.position = "absolute";
  ghost.style.top = "0";
  ghost.style.left = "0";
  ghost.style.width = `${PET_SIZE}px`;
  ghost.style.height = `${PET_SIZE}px`;
  ghost.style.opacity = ".35";
  ghost.style.pointerEvents = "none";
  ghost.style.transition = `opacity ${Math.max(80, remainingMs)}ms ease`;
  ghost.style.transform = `translate3d(${x}px, ${y}px, 0)`;
  ghost.appendChild(petArt.cloneNode(true));
  trailLayer.appendChild(ghost);
  window.requestAnimationFrame(() => {
    ghost.style.opacity = "0";
    ghost.classList.add("trail-fade");
  });
}

function walkPath(points: Array<{x: number; y: number} | [number, number]>, durationMs: number): void {
  if (points.length < 2) return;
  clearTrailGhosts();
  const duration = Math.max(1000, Math.min(30000, durationMs));
  const normalized = points.map(point => Array.isArray(point) ? {x: point[0], y: point[1]} : point);
  const coords = normalized.map(point => ({
    x: Math.max(8, Math.min(window.innerWidth - PET_SIZE - 8, point.x * window.innerWidth - PET_SIZE / 2)),
    y: Math.max(8, Math.min(window.innerHeight - PET_SIZE - 8, point.y * window.innerHeight - PET_SIZE / 2)),
  }));
  // Start from the current center, then follow the requested points.
  coords.unshift({x: petX, y: petY});
  const lengths: number[] = [0];
  let total = 0;
  for (let index = 1; index < coords.length; index++) {
    total += Math.hypot(coords[index].x - coords[index - 1].x, coords[index].y - coords[index - 1].y);
    lengths.push(total);
  }
  if (total < 1) return;
  stopPetMove();
  playMotion("walk");
  actionMoveUntil = Math.max(actionMoveUntil, Date.now() + duration + 500);
  roamPausedUntil = Math.max(roamPausedUntil, Date.now() + duration + 500);
  const started = performance.now();
  let lastTrailAt = 0;
  const frame = (now: number) => {
    const progress = Math.min(1, (now - started) / duration);
    const distance = progress * total;
    let index = 1;
    while (index < lengths.length - 1 && lengths[index] < distance) index++;
    const from = coords[index - 1];
    const to = coords[index];
    const segment = Math.max(1, lengths[index] - lengths[index - 1]);
    const local = Math.max(0, Math.min(1, (distance - lengths[index - 1]) / segment));
    setPetPosition(from.x + (to.x - from.x) * local, from.y + (to.y - from.y) * local);
    if (now - lastTrailAt > 95) {
      lastTrailAt = now;
      spawnTrailGhost(petX, petY, Math.max(0, started + duration - now));
    }
    if (progress < 1) {
      moveRAF = window.requestAnimationFrame(frame);
    } else {
      moveRAF = 0;
      clearTrailGhosts();
      playMotion("idle");
    }
  };
  moveRAF = window.requestAnimationFrame(frame);
}

// Move the pet along the page instead of teleporting it. The duration scales
// with distance and the walking class drives a visible gait on top.
function positionAt(point: {x: number; y: number}, force = false, fixedDuration = 0): void {
  if (petFrozen && !force) return;
  if (fixedDuration > 0) actionMoveUntil = Math.max(actionMoveUntil, Date.now() + fixedDuration + 500);
  const target = {
    x: Math.max(8, Math.min(window.innerWidth - PET_SIZE - 8, point.x)),
    y: Math.max(8, Math.min(window.innerHeight - PET_SIZE - 8, point.y)),
  };
  const distance = Math.hypot(target.x - petX, target.y - petY);
  if (distance < 4) {
    pet.classList.remove("pet-moving");
    setPetPosition(target.x, target.y);
    return;
  }
  stopPetMove();
  pet.classList.add("pet-moving");
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    setPetPosition(target.x, target.y);
    return;
  }
  const startX = petX;
  const startY = petY;
  if (Math.abs(target.x - startX) > 4) facing = target.x < startX ? -1 : 1;
  playMotion("walk");
  const duration = fixedDuration > 0 ? fixedDuration : Math.max(900, Math.min(3200, distance * 9));
  const start = performance.now();
  const steps = Math.max(4, Math.round(distance / 18));
  const frame = (now: number) => {
    const progress = Math.min(1, (now - start) / duration);
    const eased = progress < 0.5 ? 2 * progress * progress : 1 - Math.pow(-2 * progress + 2, 2) / 2;
    const x = startX + (target.x - startX) * eased;
    const y = startY + (target.y - startY) * eased;
    const bob = Math.sin(progress * Math.PI * steps) * (progress < 0.92 ? 2 : 0);
    setPetPosition(x, y + bob);
    if (progress < 1) {
      moveRAF = window.requestAnimationFrame(frame);
      return;
    }
    moveRAF = 0;
    pet.classList.remove("pet-moving");
    setPetPosition(target.x, target.y);
    playMotion("idle");
    saveLocalPetPosition();
  };
  moveRAF = window.requestAnimationFrame(frame);
}

function positionAtDock(animate: boolean): void {
  if (Date.now() < actionMoveUntil) return;
  if (!animate) {
    stopPetMove();
    const point = dockPoint();
    setPetPosition(point.x, point.y);
    return;
  }
  positionAt(dockPoint());
}

function positionBubble(): void {
  if (!bubble || bubble.hidden) return;
  const speech = bubble.classList.contains("speech");
  const width = speech ? Math.min(264, window.innerWidth - PET_SIZE - 24) : Math.min(280, window.innerWidth - 24);
  const rect = pet.getBoundingClientRect();
  const height = speech ? SPEECH_BUBBLE_HEIGHT : (bubble.offsetHeight || 0);
  let left: number;
  if (speech) {
    const onLeftHalf = rect.left + rect.width / 2 < window.innerWidth / 2;
    left = onLeftHalf ? rect.right + 10 : rect.left - width - 10;
    if (left + width > window.innerWidth - 8) left = rect.left - width - 10;
    if (left < 8) left = rect.right + 10;
    left = Math.max(8, Math.min(window.innerWidth - width - 8, left));
  } else {
    left = rect.left + rect.width / 2 - width / 2;
    left = Math.max(12, Math.min(window.innerWidth - width - 12, left));
  }
  let top = speech ? rect.top + (rect.height - height) / 2 : rect.top - height - 10;
  if (top < 8) top = rect.bottom + 10;
  if (top + height > window.innerHeight - 8) top = Math.max(8, window.innerHeight - height - 8);
  bubble.style.width = width + "px";
  bubble.style.left = left + "px";
  bubble.style.top = top + "px";
}

function escapeHTML(value: unknown): string {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

async function renderPetDiary(): Promise<void> {
  const list = document.querySelector<HTMLElement>("#pet-diary-list");
  if (!list) return;
  try {
    const result = await api.petDiary();
    const entries = result.entries || [];
    list.innerHTML = entries.length
      ? entries.slice(0, 7).map(entry => `<details class="pet-diary-entry"><summary>${escapeHTML(entry.date)}</summary><pre>${escapeHTML(entry.content)}</pre></details>`).join("")
      : `<p class="assistant-settings-note">还没有日记。宠物在 Task 里会写到 diary/ 目录。</p>`;
  } catch (error) {
    list.innerHTML = `<p class="assistant-settings-note">${escapeHTML(error instanceof Error ? error.message : String(error))}</p>`;
  }
}
