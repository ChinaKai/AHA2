import {api} from "./api.js";
import {icon} from "./icons.js";
import type {TaskDetail} from "./types.js";

type Notice = (type: "error" | "notice", message: string) => void;
type ActionKind = "invoke" | "set_value" | "toggle" | "select" | "expand" | "collapse";
type Mode = "foreground" | "background";
type InputKind = "click" | "double_click" | "drag" | "scroll" | "text" | "key" | "focus";
interface SharedWindow {
  id: string; title: string; process: string; desktop_id?: string; monitor_id?: string;
  desktop_name?: string; other_desktop?: boolean;
}
interface Monitor { id: string; name: string; x: number; y: number; width: number; height: number; primary: boolean }
interface VirtualDesktop { id: string; name: string; current: boolean }
interface Targets { windows: SharedWindow[]; monitors: Monitor[]; desktops: VirtualDesktop[]; desktop_switch_supported: boolean; desktop_reason?: string }
interface TargetSelection { kind: "window" | "desktop" | "new-desktop"; window_id?: string; desktop_id?: string; monitor_id?: string }
interface SharedElement {
  id: string; name: string; role: string; value?: string; actions: string[];
  x: number; y: number; width: number; height: number;
}
interface Session {
  id: string; task_id: string; window: SharedWindow; controller: string;
  revision: number; expires_at: string; claimed: boolean; mode?: Mode; switching?: boolean;
}
interface Status { supported: boolean; foreground_supported?: boolean; stream_supported?: boolean; targets_supported?: boolean; shared_control_supported?: boolean; background_desktop_supported?: boolean; reason?: string; session: Session | null }
interface Observation {
  id: string; session_id: string; revision: number; window: SharedWindow;
  elements: SharedElement[]; image: string; width: number; height: number; capture_error?: string; control_error?: string;
  input_actions?: string[]; surface?: string;
  mime?: string; preview_width?: number; preview_height?: number;
}
type VideoMode = "auto" | "quality" | "bandwidth";
interface FrameOptions { max_width: number; max_height: number; quality: number; interval_ms: number }
interface AdaptiveState { level: number; slow: number; fast: number }
interface StreamFrame { observation: Observation; capture_ms: number; interval_ms: number; bytes: Uint8Array }
interface PanelState {
  taskID: string; remote: boolean; status: Status | null; windows: SharedWindow[];
  windowID: string; selectedID: string; observation: Observation | null; observedAt: number;
  draft: string; dirty: boolean; error: string; sessionNote: string;
  loading: boolean; busy: string; denied: boolean;
  mode: Mode; windowsLoaded: boolean; inputDraft: string;
  actionError: string;
  videoMode: VideoMode; adaptive: AdaptiveState; transport: string; fps: number; cycleMS: number;
  zoom: number;
  targets: Targets | null; desktopID: string; monitorID: string; targetKind: TargetSelection["kind"];
  popover: "" | "targets" | "operations" | "keys";
}
interface Gesture { x: number; y: number; button: string; pointerID: number; observationID: string }
interface Binding {
  root: HTMLElement; state: PanelState; notify: Notice; timer: number;
  read: AbortController | null; write: AbortController | null; epoch: number;
  removeVisibility: () => void;
  chooserActive: boolean; clickTimer: number; click: Gesture | null; gesture: Gesture | null;
  refreshWindows: boolean;
  viewID: string; socket: WebSocket | null; socketKey: string; socketGeneration: number;
  socketReady: boolean; socketTimer: number; retries: number; retryAt: number; retryKey: string;
  decoding: boolean; decoder: HTMLImageElement | null; decodeTimer: number; blobURLs: Set<string>; imageURL: string;
  lastFrameAt: number; lastStatusAt: number; decodePending: number;
  pendingText: string | null;
  removeLayout: () => void;
  previewWidth: number; previewHeight: number;
  removePopover: () => void;
  listeners: Array<() => void>;
}

const frameProfiles: FrameOptions[] = [
  {max_width: 1920, max_height: 1080, quality: 80, interval_ms: 80},
  {max_width: 1280, max_height: 720, quality: 65, interval_ms: 100},
  {max_width: 960, max_height: 540, quality: 55, interval_ms: 160},
  {max_width: 640, max_height: 360, quality: 45, interval_ms: 250},
  {max_width: 320, max_height: 180, quality: 35, interval_ms: 500},
];

export function desktopPreviewSize(width: number, height: number, availableWidth: number, availableHeight: number, zoom = 1): {width: number; height: number} {
  if (![width, height, availableWidth, availableHeight, zoom].every(value => Number.isFinite(value) && value > 0)) return {width: 0, height: 0};
  const scale = Math.min(availableWidth / width, availableHeight / height) * Math.max(1, Math.min(3, zoom));
  return {width: width * scale, height: height * scale};
}

export function adaptDesktopVideo(state: AdaptiveState, cycleMS: number, bytes: number, decodeMS: number): AdaptiveState {
  const profile = frameProfiles[state.level] || frameProfiles[1];
  const slow = cycleMS > Math.max(350, profile.interval_ms * 2.5) || bytes > 300000 || decodeMS > 60;
  const fast = cycleMS < Math.max(240, profile.interval_ms * 1.6) && bytes < 140000 && decodeMS < 30;
  const next = {level: state.level, slow: slow ? state.slow + 1 : 0, fast: fast ? state.fast + 1 : 0};
  if (next.slow >= 2 && next.level < frameProfiles.length - 1) return {level: next.level + 1, slow: 0, fast: 0};
  if (next.fast >= 8 && next.level > 0) return {level: next.level - 1, slow: 0, fast: 0};
  return next;
}

export function desktopFrameOptions(mode: VideoMode, level = 1): FrameOptions {
  return {...frameProfiles[mode === "quality" ? 0 : mode === "bandwidth" ? 3 : Math.max(0, Math.min(4, level))]};
}

export function parseDesktopFrame(buffer: ArrayBuffer): StreamFrame {
  if (buffer.byteLength < 5 || buffer.byteLength > 4 + 65536 + 8 * 1024 * 1024) throw new Error("视频帧大小无效");
  const length = new DataView(buffer).getUint32(0, false);
  if (length < 2 || length > 65536 || 4 + length > buffer.byteLength) throw new Error("视频帧头无效");
  const metadata = JSON.parse(new TextDecoder("utf-8", {fatal: true}).decode(new Uint8Array(buffer, 4, length)));
  const observation = metadata.observation as Observation;
  const size = buffer.byteLength - 4 - length;
  if (metadata.type !== "frame" || !observation || !["image/jpeg", "image/png"].includes(observation.mime || "")
    || typeof observation.id !== "string" || !observation.id || observation.id.length > 128
    || typeof observation.session_id !== "string" || !Number.isSafeInteger(observation.revision)
    || !Number.isFinite(observation.width) || !Number.isFinite(observation.height)
    || observation.width < 0 || observation.height < 0 || observation.width > 32768 || observation.height > 32768
    || !Array.isArray(observation.elements) || observation.image
    || !Number.isFinite(metadata.capture_ms) || metadata.capture_ms < 0
    || !Number.isFinite(metadata.interval_ms) || metadata.interval_ms < 0 || size > 8 * 1024 * 1024
    || (size > 0 && (!Number.isInteger(observation.preview_width) || !Number.isInteger(observation.preview_height)
      || observation.preview_width! < 1 || observation.preview_height! < 1
      || observation.preview_width! > 1920 || observation.preview_height! > 1080))) throw new Error("视频帧格式无效");
  return {observation, capture_ms: metadata.capture_ms, interval_ms: metadata.interval_ms,
    bytes: new Uint8Array(buffer, 4 + length, size)};
}

function randomID(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, value => value.toString(16).padStart(2, "0")).join("");
}

const states = new Map<string, PanelState>();
let active: Binding | null = null;
const errorMessages: Record<string, string> = {
  desktop_windows_required: "当前宿主不是 Windows，无法共享本机软件窗口",
  desktop_interactive_required: "宿主需要已登录的 Windows 桌面会话",
  desktop_powershell_required: "宿主缺少 Windows PowerShell",
  desktop_native_unavailable: "宿主窗口接口当前不可用",
  desktop_native_failed: "目标软件操作失败，控制已暂停",
  desktop_focus_side_effect: "检测到输入焦点变化，已暂停此窗口的操作",
  desktop_focus_unverifiable: "无法确认输入焦点，已暂停操作",
  desktop_unsupported_action: "此控件没有支持的后台操作",
  desktop_unsupported_target: "无法访问此软件窗口",
  desktop_stale_target: "目标窗口已关闭或发生变化",
  desktop_background_control_changed: "窗口内的子控件已变化，请刷新画面后再操作",
  desktop_background_child_unverifiable: "无法核验窗口内嵌子进程的访问权限，暂不能读取画面",
  desktop_browser_accessibility_unavailable: "浏览器页面控件暂不可用，请等待页面就绪后刷新",
  desktop_browser_scope_changed: "浏览器控件已离开共享窗口，操作已停止",
  desktop_browser_page_unavailable: "浏览器尚未提供网页控件，当前不能后台操作此页面",
  desktop_window_gone: "目标窗口已关闭，请刷新窗口列表",
  desktop_window_in_use: "此窗口已由其他 Task 共享",
  desktop_session_exists: "请先停止当前窗口的共享",
  desktop_busy: "目标窗口正在处理上一项操作",
  desktop_stale_session: "控制状态已变化，请重新操作",
  desktop_stale_observation: "画面已更新，请重新选择目标控件",
  desktop_frame_superseded: "正在获取操作后的新画面",
  desktop_action_replayed: "此操作已提交，不会重复执行，请检查目标状态",
  desktop_action_limit: "本次共享的操作次数已达上限，请停止后重新共享",
  desktop_view_limit: "共享预览页面过多，请关闭部分页面后重试",
  desktop_view_in_use: "此预览连接尚未释放，正在等待重连",
  desktop_invalid_view: "预览标识无效，请重新打开面板",
  desktop_invalid_preview: "预览画质参数无效",
  desktop_not_shared: "窗口共享已结束，请重新共享",
  desktop_timeout: "目标软件响应超时，操作可能已经生效，请检查状态",
  desktop_cancelled: "操作已取消",
  desktop_password_element: "不能读取或修改密码控件",
  desktop_element_read_only: "此控件为只读",
  desktop_element_disabled: "此控件当前不可用",
  desktop_element_unavailable: "目标控件已变化，请刷新画面",
  desktop_element_limit: "窗口控件过多，当前无法完整读取",
  desktop_foreground_unsupported: "此宿主不支持前台控制",
  desktop_foreground_confirmation_required: "前台控制需要明确确认键鼠影响",
  desktop_shared_control: "共同控制会话无需交接控制权",
  desktop_foreground_in_use: "其他 Task 正在进行前台控制",
  desktop_focus_denied: "无法激活目标窗口，请在宿主确认窗口和权限后重试",
  desktop_geometry_changed: "目标窗口位置或大小已变化，请等待新画面后重试",
  desktop_surface_changed: "目标画面已变化，请等待新画面后重试",
  desktop_refresh_required: "窗口状态或尺寸已变化，请等待新画面后重试",
  desktop_foreground_denied: "目标窗口未能获得焦点，请在宿主确认后重试",
  desktop_caption_unavailable: "目标窗口没有可安全激活的可见标题栏，请在宿主选中它后重试",
  desktop_foreground_activation_unconfirmed: "已尝试激活目标，但未确认获得焦点，本次输入未继续",
  desktop_target_disabled: "目标被对话框或系统禁用，无法安全接管该窗口",
  desktop_target_state_unavailable: "无法确认目标窗口状态，当前不能安全操作",
  desktop_target_process_unavailable: "无法验证目标进程，当前不能安全操作",
  desktop_target_token_unavailable: "无法验证目标访问权限，当前不能安全操作",
  desktop_target_foreign_session: "目标不属于当前登录会话，不能共享或控制",
  desktop_target_foreign_user: "目标不属于当前用户，不能共享或控制",
  desktop_foreground_unavailable: "当前前台窗口暂不可用，请等待稳定画面后再操作",
  desktop_foreground_changed: "前台窗口已变化，输入未继续执行",
  desktop_secure_desktop: "宿主已锁定或处于安全桌面，无法控制",
  desktop_elevated_target: "目标软件权限较高，不能注入前台输入",
  desktop_input_busy: "宿主正在按键或拖动，请稍后重试",
  desktop_input_blocked: "宿主拒绝了输入，请检查目标窗口权限",
  desktop_input_partial: "部分输入可能已经生效，请检查目标状态后再操作",
  desktop_point_obscured: "目标位置被其他窗口遮挡，请恢复焦点后重试",
  desktop_creation_unverified: "未能确认新桌面已创建，未开始共享",
  desktop_creation_failed: "创建新桌面失败，未开始共享",
  desktop_identity_unavailable: "无法验证宿主桌面身份",
  desktop_desktop_changed: "宿主已切换到其他桌面，请先返回共享桌面",
  desktop_background_desktop_changed: "目标窗口所属桌面已变化，请重新选择共享目标",
  desktop_background_context_changed: "后台操作期间当前桌面发生变化，结果可能已生效，请检查后再操作",
  password_bounds_unavailable: "无法验证密码遮罩位置，已停止显示图像",
  desktop_changed: "宿主已切换到其他桌面，请先返回共享桌面",
  desktop_capture_unavailable: "目标画面暂不可用，可尝试恢复焦点",
  desktop_monitor_changed: "共享显示器的连接或布局已变化，请重新选择共享目标",
  desktop_monitor_required: "请选择要共享的显示器",
  desktop_monitor_unavailable: "所选显示器不可用，请刷新目标列表",
  desktop_foreground_outside_monitor: "当前前台窗口不在共享显示器内，本次输入未发送",
  desktop_enumeration_unavailable: "无法读取虚拟桌面列表，当前桌面仍可按宿主支持范围共享",
  desktop_state_inconsistent: "虚拟桌面状态不一致，无法安全切换，请刷新后重试",
  desktop_switch_unverified: "未能确认桌面切换结果，未开始新共享",
  desktop_switch_limit: "桌面切换超出安全步数限制，请在宿主切换后重试",
  window_minimized: "目标窗口已最小化，可恢复焦点",
  window_capture_blank: "目标窗口没有提供有效画面，可恢复焦点",
  window_capture_unavailable: "目标窗口画面暂不可用",
  window_capture_obscured: "目标窗口被遮挡，画面暂不可用",
  capture_size_limit: "共享画面尺寸超过当前限制",
  refresh_required: "窗口状态已变化，正在刷新画面",
};
const shortcuts = [
  {id: "start", label: "开始", keys: ["WIN"], icon: "menu"},
  {id: "run", label: "运行", keys: ["WIN", "R"], icon: "send"},
  {id: "enter", label: "Enter", keys: ["ENTER"]},
  {id: "escape", label: "Esc", keys: ["ESC"]},
  {id: "tab", label: "Tab", keys: ["TAB"]},
  {id: "backspace", label: "退格", keys: ["BACKSPACE"]},
  {id: "select-all", label: "全选", keys: ["CTRL", "A"]},
];
const actions: Record<ActionKind, {label: string; icon: string}> = {
  invoke: {label: "执行", icon: "send"},
  set_value: {label: "设置值", icon: "save"},
  toggle: {label: "切换", icon: "sync"},
  select: {label: "选中", icon: "tasks"},
  expand: {label: "展开", icon: "chevron-down"},
  collapse: {label: "折叠", icon: "chevron-up"},
};

function escapeHTML(value: unknown): string {
  return String(value ?? "").replaceAll("&", "&amp;").replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;").replaceAll('"', "&quot;").replaceAll("'", "&#039;");
}

function stateFor(detail: TaskDetail): PanelState {
  let state = states.get(detail.task.id);
  if (!state) {
    state = {
      taskID: detail.task.id, remote: Boolean(detail.task.read_only), status: null, windows: [],
      windowID: "new-desktop", selectedID: "", observation: null, observedAt: 0, draft: "", dirty: false,
      error: "", sessionNote: "", loading: false, busy: "", denied: false,
      mode: "foreground", windowsLoaded: false, inputDraft: "", actionError: "",
      videoMode: "auto", adaptive: {level: 1, slow: 0, fast: 0}, transport: "HTTP 预览", fps: 0, cycleMS: 0, zoom: 1,
      targets: null, desktopID: "", monitorID: "", targetKind: "new-desktop", popover: "",
    };
    states.set(state.taskID, state);
  }
  return state;
}

function current(binding: Binding): boolean {
  return active === binding && binding.root.isConnected;
}

function listen<K extends keyof (HTMLElementEventMap & DocumentEventMap)>(
  binding: Binding,
  target: EventTarget | null,
  type: K,
  handler: (event: (HTMLElementEventMap & DocumentEventMap)[K]) => void,
  options?: boolean | AddEventListenerOptions,
): void {
  if (!target) return;
  const listener: EventListener = event => {
    if (current(binding) && !binding.state.remote) handler(event as (HTMLElementEventMap & DocumentEventMap)[K]);
  };
  target.addEventListener(type, listener, options);
  binding.listeners.push(() => target.removeEventListener(type, listener, options));
}

function expired(session: Session | null | undefined): boolean {
  return Boolean(session && (!Number.isFinite(Date.parse(session.expires_at)) || Date.parse(session.expires_at) <= Date.now()));
}

function selected(state: PanelState): SharedElement | undefined {
  return state.observation?.elements.find(element => element.id === state.selectedID);
}

function supportedActions(element?: SharedElement): ActionKind[] {
  return Object.keys(actions).filter(kind => element?.actions?.includes(kind)) as ActionKind[];
}

function canAssist(state: PanelState): boolean {
  return !state.remote && !state.denied && state.status?.shared_control_supported === true && Boolean(state.status
    && (state.status.session?.mode === "foreground" ? state.status.foreground_supported : state.status.supported))
    && state.status?.session?.controller === "shared" && !expired(state.status?.session);
}

function actionable(state: PanelState): boolean {
  const session = state.status?.session;
  const observation = state.observation;
  return canAssist(state) && !state.busy && !session?.switching && Boolean(observation
    && !observation.control_error
    && observation.session_id === session?.id && observation.revision === session.revision
    && state.observedAt > 0 && Date.now() - state.observedAt < 25000);
}

function foreground(state: PanelState): boolean {
  return state.status?.session?.mode === "foreground";
}

function inputAllowed(state: PanelState, kind: InputKind): boolean {
  return foreground(state) && Boolean(state.observation?.input_actions?.includes(kind));
}

function windowSelectable(state: PanelState, window: SharedWindow): boolean {
  return !window.other_desktop || (state.mode === "background"
    ? state.status?.background_desktop_supported === true
    : state.targets?.desktop_switch_supported === true);
}

function windowDesktopName(state: PanelState, window: SharedWindow): string {
  return window.desktop_name || state.targets?.desktops.find(item => item.id === window.desktop_id)?.name
    || (window.other_desktop ? "其他桌面" : "");
}

function inputTextError(value: string): string {
  if (new TextEncoder().encode(value).length > 4096) return "文字超过单次发送上限（4096 字节）";
  if (/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/.test(value)) return "文字包含不支持的控制字符";
  return "";
}

export function renderDesktopHeaderControls(): string {
  return `<div class="desktop-header-controls" role="group" aria-label="共享控制操作">
    <button type="button" data-desktop-popover="targets" class="icon-button" aria-label="共享目标" title="共享目标" aria-expanded="false" aria-controls="desktop-targets-popover">${icon("browser")}</button>
    <button type="button" id="desktop-focus" class="icon-button" title="恢复焦点" aria-label="恢复焦点" disabled>${icon("monitor")}</button>
    <button type="button" data-desktop-popover="operations" class="icon-button" aria-label="操作与状态" title="操作与状态" aria-expanded="false" aria-controls="desktop-operations-popover">${icon("menu")}</button>
    <button type="button" id="desktop-stop" class="icon-button danger" title="停止共享" aria-label="停止共享" disabled>${icon("close")}</button>
  </div>`;
}

export function renderDesktopPanel(detail: TaskDetail, header = ""): string {
  const state = stateFor(detail);
  if (detail.task.read_only) {
    return `<section id="desktop-tool" class="desktop-tool" data-task-id="${escapeHTML(state.taskID)}" data-read-only="true">${header}<p class="desktop-empty" role="status">此 Task 为只读，无法共享或控制本机窗口。</p></section>`;
  }
  return `<section id="desktop-tool" class="desktop-tool" data-task-id="${escapeHTML(state.taskID)}" data-read-only="false">
    ${header || `<header class="task-tool-panel-head desktop-task-header"><h3>共享控制</h3>${renderDesktopHeaderControls()}</header>`}
    <section id="desktop-picture-controls" class="desktop-picture-bar" role="group" aria-label="画质与缩放">
      <div class="desktop-controller" role="group" aria-label="画面质量">
        <button type="button" data-desktop-video="auto" title="自动调整画质">自动</button>
        <button type="button" data-desktop-video="quality" title="优先清晰度">清晰</button>
        <button type="button" data-desktop-video="bandwidth" title="降低带宽占用">省流</button>
      </div>
      <span id="desktop-video-metrics" role="status"></span>
      <div class="desktop-view-controls">
        <button type="button" id="desktop-fit" class="icon-button" title="完整适配画面" aria-label="完整适配画面">${icon("monitor")}</button>
        <input type="range" id="desktop-zoom" min="100" max="300" step="25" value="${state.zoom * 100}" aria-label="画面放大比例">
        <output id="desktop-zoom-value">100%</output>
        <button type="button" id="desktop-fullscreen" class="icon-button" title="全屏" aria-label="全屏">${icon("expand")}</button>
      </div>
    </section>
    <div id="task-tool-panel-body" class="desktop-body">
      <p id="desktop-error" class="desktop-error desktop-alert" role="alert" hidden></p>
      <div class="desktop-observation">
        <div id="desktop-viewport" class="desktop-viewport"><div id="desktop-preview" class="desktop-preview" hidden>
          <img id="desktop-image" alt="共享窗口画面" aria-label="共享画面控制区" draggable="false" tabindex="-1">
          <div id="desktop-highlight" class="desktop-highlight" hidden></div>
        </div></div>
        <p id="desktop-capture-status" class="desktop-empty desktop-capture-notice" role="status">尚无窗口画面</p>
        <button type="button" id="desktop-empty-target" class="desktop-empty-target">${icon("browser")}选择共享目标</button>
      </div>
    </div>
    <section id="desktop-input" class="desktop-input-bar" role="group" aria-label="键盘和文字输入" hidden>
      <div class="desktop-input-toolbar" role="group" aria-label="快捷键">
        ${shortcuts.filter(item => ["enter", "escape"].includes(item.id)).map(item =>
          `<button type="button" data-desktop-key="${item.id}" title="${item.keys.join("+")}" hidden>${item.label}</button>`).join("")}
        <div id="desktop-keys-popover" class="desktop-key-options" role="group" aria-label="更多快捷键">
          ${shortcuts.filter(item => !["enter", "escape"].includes(item.id)).map(item =>
            `<button type="button" data-desktop-key="${item.id}" title="${item.keys.join("+")}" hidden>${item.icon ? icon(item.icon) : ""}<span>${item.label}</span></button>`).join("")}
        </div>
        <button type="button" id="desktop-more-keys" data-desktop-popover="keys" class="icon-button" title="更多快捷键" aria-label="更多快捷键" aria-expanded="false" aria-controls="desktop-keys-popover">${icon("menu")}</button>
      </div>
      <form id="desktop-text-form" class="desktop-text-form" hidden>
        <textarea id="desktop-text-input" name="text" rows="1" aria-label="输入文字" placeholder="输入文字" autocomplete="off" spellcheck="false">${escapeHTML(state.inputDraft)}</textarea>
        <button type="submit" class="icon-button" title="发送文字" aria-label="发送文字">${icon("send")}</button>
      </form>
    </section>
    <section id="desktop-targets-popover" class="desktop-popover" role="dialog" aria-label="共享目标" hidden>
      <div class="desktop-mode-row"><div class="desktop-controller" role="group" aria-label="控制模式">
        <button type="button" data-desktop-mode="foreground" aria-pressed="${state.mode === "foreground"}">前台控制</button>
        <button type="button" data-desktop-mode="background" aria-pressed="${state.mode === "background"}">后台控件</button>
      </div><span id="desktop-mode-status"></span></div>
      <div class="desktop-toolbar">
        <label class="desktop-window-label">共享目标<select id="desktop-window" aria-label="共享目标" disabled><option value="">正在加载目标...</option></select></label>
        <button type="button" id="desktop-refresh" class="icon-button" title="刷新目标与画面" aria-label="刷新目标与画面">${icon("refresh")}</button>
      </div>
      <label>显示器<select id="desktop-monitor" aria-label="显示器" disabled><option value="">主显示器</option></select></label>
      <p id="desktop-target-reason" class="desktop-empty" role="status"></p>
      <div class="desktop-target-actions">
        <button type="button" id="desktop-new-desktop">${icon("plus")}新建桌面</button>
        <button type="button" id="desktop-share" class="primary" disabled>${icon("link")}<span>共享所选目标</span></button>
      </div>
    </section>
    <section id="desktop-operations-popover" class="desktop-popover" role="dialog" aria-label="操作与状态" hidden>
      <div class="desktop-target"><div><strong id="desktop-title">尚未共享窗口</strong><small id="desktop-process"></small></div></div>
      <p id="desktop-status" class="desktop-status" role="status">正在读取共享状态...</p>
      <p id="desktop-input-status" class="desktop-empty" role="status"></p>
    <details class="desktop-inspector">
      <summary>窗口元素</summary>
      <header><h4>窗口元素</h4><small id="desktop-element-count">0</small></header>
      <div id="desktop-elements" class="desktop-elements" role="group" aria-label="窗口元素"></div>
      <div class="desktop-selection"><strong id="desktop-selected">未选择元素</strong><small id="desktop-value"></small></div>
      <div id="desktop-actions" class="desktop-actions"></div>
      <form id="desktop-value-form" class="desktop-value-form" hidden>
        <label>元素值<textarea id="desktop-value-input" name="value" rows="2" autocomplete="off" spellcheck="false">${escapeHTML(state.draft)}</textarea></label>
        <button type="submit" title="设置值" aria-label="设置值">${icon("save")}<span>设置值</span></button>
      </form>
    </details>
    </section>
  </section>`;
}

function statusText(state: PanelState): string {
  const session = state.status?.session;
  if (state.denied) return "未授权访问共享控制";
  if (!state.status) return state.error ? "共享状态不可用" : "正在读取共享状态...";
  if (!state.status.supported && !state.status.foreground_supported) return errorMessages[state.status.reason || ""] || state.status.reason || "当前宿主不支持共享控制";
  if (!state.status.shared_control_supported) return "此服务不支持共同控制，请更新服务后重新共享";
  if (expired(session)) return "共享授权已过期，请重新共享窗口";
  if (session?.switching) return "正在切换共享目标...";
  if (session && session.controller !== "shared") return "旧版独占授权：请停止后重新共享，以授权共同控制";
  if (!session) return state.sessionNote || (state.mode === "foreground" && !state.status.foreground_supported ? "此宿主不支持前台控制" : "尚未授权共享窗口");
  if (state.busy) return state.busy;
  return `共同控制 · ${session.claimed ? "Agent 已接入" : "Agent 可直接接入"} · 授权到期 ${new Date(session.expires_at).toLocaleTimeString()}`;
}

function updateDOM(binding: Binding): void {
  if (!current(binding) || binding.state.remote) return;
  const {root, state} = binding;
  const session = state.status?.session;
  root.classList.toggle("desktop-sharing", Boolean(session));
  const observation = state.observation;
  const element = selected(state);
  const allowed = supportedActions(element);
  const text = (selector: string, value: string) => {
    const node = root.querySelector<HTMLElement>(selector);
    if (node && node.textContent !== value) node.textContent = value;
  };
  const modeSupported = state.mode === "foreground" ? state.status?.foreground_supported === true : state.status?.supported === true;
  const canShare = modeSupported && state.status?.shared_control_supported === true
    && (!session || session.controller === "shared") && !state.denied && !state.busy && !session?.switching;
  const chooser = root.querySelector<HTMLSelectElement>("#desktop-window")!;
  const choiceValue = (kind: string, id: string) => JSON.stringify({kind, id});
  const options = '<option value="">选择已有桌面或窗口</option>'
    + (state.mode === "foreground" && state.targets?.desktops.length ? `<optgroup label="已有桌面">${state.targets.desktops.map(item =>
      `<option value="${escapeHTML(choiceValue("desktop", item.id))}" ${!state.targets!.desktop_switch_supported && !item.current ? "disabled" : ""}>${escapeHTML(item.name)}${item.current ? "（当前）" : ""}</option>`).join("")}</optgroup>` : "")
    + `<optgroup label="窗口">${state.windows.map(item => {
      const desktop = windowDesktopName(state, item);
      return `<option value="${escapeHTML(choiceValue("window", item.id))}" ${windowSelectable(state, item) ? "" : "disabled"}>${escapeHTML(item.process)} · ${escapeHTML(item.title || "无标题窗口")}${desktop ? ` · ${escapeHTML(desktop)}${item.other_desktop ? "（其他桌面）" : ""}` : ""}</option>`;
    }).join("")}</optgroup>`;
  // Native select menus close even on redundant disabled/value writes.
  // Disabled selects may receive pointerdown without a later focus/blur pair.
  if (chooser.disabled) binding.chooserActive = false;
  const chooserInteracting = !chooser.disabled && (binding.chooserActive || document.activeElement === chooser);
  if (!chooserInteracting) {
    if (chooser.dataset.options !== options) {
      chooser.innerHTML = options;
      chooser.dataset.options = options;
    }
    const value = state.targetKind === "desktop" ? choiceValue("desktop", state.desktopID)
      : state.targetKind === "window" && state.windowID ? choiceValue("window", state.windowID) : "";
    if (chooser.value !== value) chooser.value = value;
    if (chooser.disabled !== !canShare) chooser.disabled = !canShare;
  }
  root.querySelectorAll<HTMLButtonElement>("[data-desktop-mode]").forEach(button => {
    const pressed = button.dataset.desktopMode === state.mode;
    button.classList.toggle("active", pressed);
    button.setAttribute("aria-pressed", String(pressed));
    button.disabled = Boolean(session?.switching) || Boolean(state.busy);
  });
  text("#desktop-mode-status", (session?.mode || (session ? "background" : state.mode)) === "foreground" ? "前台 · 会影响宿主键鼠" : "后台 · 元素操作");
  root.querySelector<HTMLButtonElement>("#desktop-share")!.disabled = !canShare || state.targetKind === "new-desktop"
    || (state.targetKind === "window" && !state.windows.some(item => item.id === state.windowID && windowSelectable(state, item)))
    || (state.targetKind === "desktop" && (state.mode !== "foreground" || !state.targets?.desktops.some(item =>
      item.id === state.desktopID && (item.current || state.targets!.desktop_switch_supported))));
  text("#desktop-share span", session ? "切换到所选目标" : "共享所选目标");
  const monitor = root.querySelector<HTMLSelectElement>("#desktop-monitor")!;
  const monitorOptions = state.targets?.monitors.map(item => `<option value="${escapeHTML(item.id)}">${escapeHTML(item.name)} · ${item.width} × ${item.height}${item.primary ? "（主）" : ""}</option>`).join("") || '<option value="">主显示器</option>';
  if (document.activeElement !== monitor) {
    if (monitor.dataset.options !== monitorOptions) { monitor.innerHTML = monitorOptions; monitor.dataset.options = monitorOptions; }
    if (monitor.value !== state.monitorID) monitor.value = state.monitorID;
    monitor.disabled = !canShare || state.mode !== "foreground" || !state.targets?.monitors.length;
  }
  root.querySelector<HTMLButtonElement>("#desktop-new-desktop")!.disabled = !canShare || state.mode !== "foreground"
    || Boolean(state.targets && !state.targets.desktop_switch_supported);
  text("#desktop-target-reason", errorMessages[state.targets?.desktop_reason || ""] || state.targets?.desktop_reason || "");
  root.querySelector<HTMLElement>("#desktop-empty-target")!.hidden = Boolean(session);
  root.querySelector<HTMLButtonElement>("#desktop-refresh")!.disabled = Boolean(state.busy);
  // Revocation remains available even while a native operation is pending.
  root.querySelector<HTMLButtonElement>("#desktop-stop")!.disabled = !session || state.denied || state.busy === "正在停止共享...";
  text("#desktop-title", session?.window.title || (session ? "无标题窗口" : "尚未共享窗口"));
  text("#desktop-process", session?.window.process || "");
  text("#desktop-status", statusText(state));
  const headerTitle = root.querySelector<HTMLElement>(".desktop-task-header h3");
  if (headerTitle) headerTitle.title = session ? `${session.window.title} · ${statusText(state)}` : statusText(state);
  text("#desktop-error", state.error);
  root.querySelectorAll<HTMLButtonElement>("[data-desktop-video]").forEach(button => {
    const pressed = button.dataset.desktopVideo === state.videoMode;
    button.classList.toggle("active", pressed);
    button.setAttribute("aria-pressed", String(pressed));
  });
  root.querySelector<HTMLElement>("#desktop-picture-controls")!.title = `画质与缩放：${{auto: "自动", quality: "清晰", bandwidth: "省流"}[state.videoMode]}`;
  text("#desktop-video-metrics", `${state.transport} · ${observation?.preview_width || observation?.width || 0} × ${observation?.preview_height || observation?.height || 0} · ${state.fps.toFixed(1)} FPS · 帧周期 ${Math.round(state.cycleMS)} ms`);
  root.querySelector<HTMLElement>("#desktop-error")!.hidden = !state.error;
  const image = root.querySelector<HTMLImageElement>("#desktop-image")!;
  image.tabIndex = foreground(state) ? 0 : -1;
  image.classList.toggle("desktop-input-surface", foreground(state));
  image.setAttribute("aria-disabled", String(!actionable(state)));
  const mime = observation?.mime || "image/png";
  const hasImage = Boolean(observation && (binding.imageURL || (observation.image
    && ["image/png", "image/jpeg"].includes(mime) && /^[A-Za-z0-9+/=\r\n]+$/.test(observation.image)))
    && observation.width > 0 && observation.height > 0);
  const preview = root.querySelector<HTMLElement>("#desktop-preview")!;
  preview.hidden = !hasImage;
  if (hasImage && observation) {
    const src = binding.imageURL || `data:${mime};base64,${observation.image}`;
    if (image.getAttribute("src") !== src) image.src = src;
    image.width = observation.width;
    image.height = observation.height;
    preview.style.aspectRatio = `${observation.width} / ${observation.height}`;
  } else {
    image.removeAttribute("src");
  }
  const accessNotice = state.status && (!state.status.shared_control_supported || (session && session.controller !== "shared")) ? statusText(state) : "";
  const captureStatus = errorMessages[observation?.capture_error || ""] || observation?.capture_error || accessNotice
    || (hasImage ? "" : session ? (session.switching ? "正在切换共享目标..." : "窗口画面不可用")
      : state.sessionNote || (state.status && !state.status.supported && !state.status.foreground_supported ? statusText(state) : "尚无共享目标"));
  text("#desktop-capture-status", captureStatus);
  root.querySelector<HTMLElement>("#desktop-capture-status")!.hidden = !captureStatus;
  const highlight = root.querySelector<HTMLElement>("#desktop-highlight")!;
  highlight.hidden = foreground(state) || !hasImage || !element || element.width <= 0 || element.height <= 0;
  if (!highlight.hidden && element && observation) {
    const left = Math.max(0, Math.min(observation.width, element.x));
    const top = Math.max(0, Math.min(observation.height, element.y));
    const right = Math.max(left, Math.min(observation.width, element.x + element.width));
    const bottom = Math.max(top, Math.min(observation.height, element.y + element.height));
    highlight.style.left = `${left / observation.width * 100}%`;
    highlight.style.top = `${top / observation.height * 100}%`;
    highlight.style.width = `${(right - left) / observation.width * 100}%`;
    highlight.style.height = `${(bottom - top) / observation.height * 100}%`;
  }
  const elements = observation?.elements || [];
  text("#desktop-element-count", String(elements.length));
  const list = root.querySelector<HTMLElement>("#desktop-elements")!;
  const listHTML = elements.map(item => `<button type="button" class="desktop-element" data-desktop-element="${escapeHTML(item.id)}" aria-pressed="${item.id === state.selectedID}"><span>${escapeHTML(item.name || "未命名元素")}</span><small>${escapeHTML(item.role)}</small></button>`).join("")
    || '<p class="desktop-empty">暂无可访问元素</p>';
  if (list.dataset.content !== listHTML) {
    const focusedID = document.activeElement instanceof HTMLElement && list.contains(document.activeElement) ? document.activeElement.dataset.desktopElement : undefined;
    const scroll = list.scrollTop;
    list.innerHTML = listHTML;
    list.dataset.content = listHTML;
    list.scrollTop = scroll;
    if (focusedID) Array.from(list.querySelectorAll<HTMLButtonElement>("[data-desktop-element]"))
      .find(button => button.dataset.desktopElement === focusedID)?.focus({preventScroll: true});
  }
  text("#desktop-selected", element ? `${element.name || "未命名元素"} · ${element.role}` : "未选择元素");
  text("#desktop-value", element?.value || "");
  const toolbar = root.querySelector<HTMLElement>("#desktop-actions")!;
  const toolbarHTML = allowed.filter(kind => kind !== "set_value").map(kind =>
    `<button type="button" data-desktop-action="${kind}" title="${actions[kind].label}">${icon(actions[kind].icon)}<span>${actions[kind].label}</span></button>`).join("")
    || (element && !allowed.length ? '<span class="desktop-empty">此元素没有支持的操作</span>' : "");
  if (toolbar.dataset.content !== toolbarHTML) {
    toolbar.innerHTML = toolbarHTML;
    toolbar.dataset.content = toolbarHTML;
  }
  root.querySelectorAll<HTMLButtonElement>("[data-desktop-action], #desktop-value-form button")
    .forEach(button => { button.disabled = !actionable(state); });
  root.querySelector<HTMLElement>("#desktop-value-form")!.hidden = !allowed.includes("set_value");
  const input = root.querySelector<HTMLTextAreaElement>("#desktop-value-input")!;
  // Polling must not replace or disable a focused editor; actions still fail closed.
  input.readOnly = !canAssist(state);
  if (input.value !== state.draft) input.value = state.draft;
  // The inspector is how an element action is chosen, so it has to stay
  // reachable in background mode; the free-form input bar stays foreground-only
  // because coordinate gestures are not available there.
  root.querySelector<HTMLElement>(".desktop-inspector")!.hidden = !session;
  root.querySelector<HTMLElement>("#desktop-input")!.hidden = !foreground(state) || !session;
  const focus = root.querySelector<HTMLButtonElement>("#desktop-focus")!;
  focus.hidden = false;
  focus.disabled = !inputAllowed(state, "focus") || !actionable(state);
  focus.title = !session ? "恢复焦点（尚未共享）" : !inputAllowed(state, "focus") ? "当前目标不支持恢复焦点"
    : !canAssist(state) ? "当前未授权共同控制，不能恢复焦点" : state.busy ? "恢复焦点（操作进行中）"
      : !actionable(state) ? "恢复焦点（等待有效画面）" : "恢复并聚焦共享目标";
  root.querySelectorAll<HTMLButtonElement>("[data-desktop-key]").forEach(button => {
    button.hidden = !inputAllowed(state, "key");
    button.disabled = !actionable(state);
  });
  root.querySelector<HTMLButtonElement>("#desktop-more-keys")!.hidden = !inputAllowed(state, "key");
  const textForm = root.querySelector<HTMLFormElement>("#desktop-text-form")!;
  textForm.hidden = !inputAllowed(state, "text");
  const textError = inputTextError(state.inputDraft);
  root.querySelector<HTMLButtonElement>("#desktop-text-form button")!.disabled = !actionable(state) || !state.inputDraft || Boolean(textError);
  const textInput = root.querySelector<HTMLTextAreaElement>("#desktop-text-input")!;
  textInput.readOnly = !canAssist(state);
  textInput.setAttribute("aria-invalid", String(Boolean(textError)));
  textInput.title = textError || "输入文字";
  root.querySelector<HTMLButtonElement>("#desktop-text-form button")!.title = textError || "发送文字";
  if (textInput.value !== state.inputDraft) textInput.value = state.inputDraft;
  text("#desktop-input-status", textError || (!state.observation?.input_actions?.length ? "当前画面没有可用的前台操作" : ""));
  layoutPreview(binding);
  syncPopovers(binding);
}

function layoutPreview(binding: Binding): void {
  if (!current(binding)) return;
  const {root, state} = binding;
  const viewport = root.querySelector<HTMLElement>("#desktop-viewport");
  const preview = root.querySelector<HTMLElement>("#desktop-preview");
  const observation = state.observation;
  if (viewport && preview && observation && viewport.clientWidth > 0 && viewport.clientHeight > 0) {
    const size = desktopPreviewSize(observation.width, observation.height, viewport.clientWidth, viewport.clientHeight, state.zoom);
    if (size.width && size.height) {
      const width = `${size.width}px`, height = `${size.height}px`;
      if (Math.abs(binding.previewWidth - size.width) > .1 || Math.abs(binding.previewHeight - size.height) > .1) {
        if (binding.gesture || binding.click) {
          cancelGesture(binding);
          binding.epoch++;
          schedule(binding);
        }
        preview.style.width = width;
        preview.style.height = height;
        binding.previewWidth = size.width;
        binding.previewHeight = size.height;
      }
    }
  }
  const zoom = root.querySelector<HTMLInputElement>("#desktop-zoom");
  if (zoom && zoom.value !== String(state.zoom * 100)) zoom.value = String(state.zoom * 100);
  const output = root.querySelector<HTMLOutputElement>("#desktop-zoom-value");
  if (output) output.textContent = `${Math.round(state.zoom * 100)}%`;
  root.querySelector("#desktop-fit")?.setAttribute("aria-pressed", String(state.zoom === 1));
  const fullscreen = root.querySelector<HTMLButtonElement>("#desktop-fullscreen");
  if (fullscreen) {
    const title = document.fullscreenElement === root ? "退出全屏" : "全屏";
    fullscreen.disabled = typeof root.requestFullscreen !== "function";
    fullscreen.setAttribute("title", fullscreen.disabled ? "此浏览器不支持全屏" : title);
    fullscreen.setAttribute("aria-label", title);
    fullscreen.setAttribute("aria-pressed", String(document.fullscreenElement === root));
  }
}

function frameOptions(binding: Binding): FrameOptions {
  const options = desktopFrameOptions(binding.state.videoMode, binding.state.adaptive.level);
  const width = binding.root.querySelector<HTMLElement>("#desktop-preview")?.clientWidth;
  if (width) options.max_width = Math.max(320, Math.min(options.max_width, Math.round(width * Math.min(window.devicePixelRatio || 1, 2))));
  return options;
}

function clearImage(binding: Binding): void {
  for (const url of binding.blobURLs) URL.revokeObjectURL(url);
  binding.blobURLs.clear();
  binding.imageURL = "";
}

function closeStream(binding: Binding): void {
  binding.socketGeneration++;
  window.clearTimeout(binding.socketTimer);
  window.clearTimeout(binding.decodeTimer);
  if (binding.decoder) binding.decoder.src = "";
  binding.decoder = null;
  binding.decoding = false;
  binding.decodePending = 0;
  const socket = binding.socket;
  binding.socket = null;
  binding.socketReady = false;
  binding.socketKey = "";
  if (socket) {
    socket.onopen = socket.onmessage = socket.onclose = socket.onerror = null;
    socket.close();
    binding.state.observation = null;
    binding.state.observedAt = 0;
  }
  binding.lastFrameAt = 0;
  binding.state.fps = 0;
  binding.state.transport = "预览已暂停";
  cancelGesture(binding);
  clearImage(binding);
}

function setStatus(binding: Binding, status: Status): void {
  const old = binding.state.status?.session;
  if (old?.id !== status.session?.id) {
    binding.state.popover = "";
    binding.root.querySelector<HTMLDetailsElement>(".desktop-inspector")!.open = false;
    binding.state.zoom = 1;
    if (status.session) binding.state.mode = status.session.mode || "background";
  }
  if (old?.id !== status.session?.id || old?.revision !== status.session?.revision
    || old?.controller !== status.session?.controller || !status.stream_supported || expired(status.session)) {
    if (binding.socket) closeStream(binding);
    if (old?.id !== status.session?.id || old?.revision !== status.session?.revision) clearImage(binding);
  }
  applyStatus(binding.state, status);
}

function streamFailure(binding: Binding, message: string, terminal = false): void {
  if (!current(binding)) return;
  closeStream(binding);
  binding.lastStatusAt = 0;
  binding.retries++;
  binding.retryAt = Date.now() + [1000, 3000, 8000][Math.min(binding.retries - 1, 2)];
  binding.state.transport = terminal ? "视频流未授权" : "HTTP 预览（视频流中断）";
  if (terminal) {
    binding.retries = 3;
    binding.state.denied = true;
    binding.state.error = message;
  } else if (message) binding.state.error = binding.state.actionError || message;
  updateDOM(binding);
  if (!terminal && !binding.read && !binding.write) void poll(binding);
}

function recordFrame(binding: Binding, bytes: number, decodeMS: number, cycleMS: number): void {
  const state = binding.state;
  state.cycleMS = cycleMS;
  state.fps = cycleMS > 0 ? 1000 / cycleMS : 0;
  if (state.videoMode === "auto") state.adaptive = adaptDesktopVideo(state.adaptive, cycleMS, bytes, decodeMS);
}

function acceptObservation(binding: Binding, observation: Observation): void {
  const state = binding.state;
  state.observation = observation;
  state.observedAt = Date.now();
  state.error = state.actionError || (observation.control_error
    ? errorMessages[observation.control_error] || observation.control_error : "");
  if (!selected(state)) {
    state.selectedID = "";
    state.draft = "";
    state.dirty = false;
  } else if (!state.dirty) state.draft = selected(state)?.value || "";
}

async function receiveFrame(binding: Binding, socket: WebSocket, data: ArrayBuffer | Blob): Promise<void> {
  const generation = binding.socketGeneration;
  const valid = () => current(binding) && binding.socket === socket && generation === binding.socketGeneration && !document.hidden;
  binding.decodePending++;
  if (binding.decodePending > 2) {
    streamFailure(binding, "视频流超过帧缓冲限制");
    return;
  }
  let ownedURL = "";
  let ownsDecoder = false;
  try {
    if (data instanceof Blob && data.size > 4 + 65536 + 8 * 1024 * 1024) throw new Error("视频帧大小无效");
    const buffer = data instanceof Blob ? await data.arrayBuffer() : data;
    if (!valid()) return;
    const frame = parseDesktopFrame(buffer);
    const session = binding.state.status?.session;
    if (!binding.socketReady || !session || frame.observation.session_id !== session.id
      || frame.observation.revision !== session.revision || expired(session)) {
      streamFailure(binding, "视频会话已变化，正在刷新");
      return;
    }
    const ack = () => {
      if (valid() && socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({
        type: "ack", frame_id: frame.observation.id, ...frameOptions(binding),
      }));
    };
    // Frozen gestures and in-flight input keep the displayed frame; dropped frames
    // are acknowledged immediately, preventing a video backlog from blocking input.
    if (binding.gesture || binding.click || binding.write || binding.decoding) { ack(); return; }
    const start = performance.now();
    const arrived = Date.now();
    const cycle = binding.lastFrameAt ? arrived - binding.lastFrameAt : frame.capture_ms + frame.interval_ms;
    binding.lastFrameAt = arrived;
    const epoch = binding.epoch;
    if (frame.bytes.length) {
      binding.decoding = true;
      ownsDecoder = true;
      ownedURL = URL.createObjectURL(new Blob([frame.bytes as BlobPart], {type: frame.observation.mime}));
      binding.blobURLs.add(ownedURL);
      const decoder = new Image();
      binding.decoder = decoder;
      decoder.src = ownedURL;
      await Promise.race([
        decoder.decode(),
        new Promise<never>((_, reject) => {
          binding.decodeTimer = window.setTimeout(() => reject(new Error("视频帧解码超时")), 5000);
        }),
      ]);
      if (!valid()) return;
      if (decoder.naturalWidth !== frame.observation.preview_width || decoder.naturalHeight !== frame.observation.preview_height) {
        throw new Error("视频帧分辨率不匹配");
      }
    }
    if (!valid()) return;
    if (epoch !== binding.epoch || binding.gesture || binding.click || binding.write) { ack(); return; }
    const oldURL = binding.imageURL;
    binding.imageURL = ownedURL;
    ownedURL = "";
    acceptObservation(binding, frame.observation);
    recordFrame(binding, frame.bytes.length, performance.now() - start, cycle);
    binding.state.transport = "视频流";
    updateDOM(binding);
    if (oldURL) { URL.revokeObjectURL(oldURL); binding.blobURLs.delete(oldURL); }
    ack();
  } catch (error) {
    if (valid()) streamFailure(binding, error instanceof Error ? error.message : "视频帧不可用");
  } finally {
    if (ownedURL && binding.blobURLs.delete(ownedURL)) URL.revokeObjectURL(ownedURL);
    if (generation === binding.socketGeneration) {
      binding.decodePending--;
      if (ownsDecoder) {
        window.clearTimeout(binding.decodeTimer);
        binding.decoding = false;
        binding.decoder = null;
      }
    }
  }
}

function ensureStream(binding: Binding): boolean {
  const state = binding.state;
  const session = state.status?.session;
  if (!foreground(state) || !state.status?.stream_supported || !session || session.switching || state.denied || expired(session) || document.hidden
    || typeof WebSocket === "undefined") {
    if (binding.socket) closeStream(binding);
    return false;
  }
  const key = `${session.id}:${session.revision}`;
  if (binding.retryKey !== key) {
    binding.retries = 0;
    binding.retryAt = 0;
    binding.retryKey = key;
  }
  if (binding.socket && binding.socketKey === key) return true;
  if (binding.retries >= 3 || Date.now() < binding.retryAt) return false;
  if (binding.socket) closeStream(binding);
  try {
    const url = new URL(`/api/v1/tasks/${encodeURIComponent(state.taskID)}/desktop/stream`, location.href);
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    url.searchParams.set("session_id", session.id);
    const socket = new WebSocket(url);
    socket.binaryType = "arraybuffer";
    binding.socket = socket;
    binding.socketKey = key;
    binding.socketReady = false;
    state.transport = "视频流连接中";
    const valid = () => current(binding) && binding.socket === socket;
    const watchdog = () => {
      window.clearTimeout(binding.socketTimer);
      binding.socketTimer = window.setTimeout(() => {
        if (valid()) streamFailure(binding, "视频流超时，已切换 HTTP 预览");
      }, binding.socketReady ? 15000 : 5000);
    };
    watchdog();
    socket.onopen = () => {
      if (!valid()) return;
      socket.send(JSON.stringify({type: "start", csrf_token: api.csrfToken(), view_id: binding.viewID, ...frameOptions(binding)}));
    };
    socket.onmessage = event => {
      if (!valid()) return;
      watchdog();
      if (typeof event.data !== "string") {
        if (!(event.data instanceof ArrayBuffer) && !(event.data instanceof Blob)) { streamFailure(binding, "视频帧格式无效"); return; }
        void receiveFrame(binding, socket, event.data);
        return;
      }
      try {
        if (event.data.length > 65536) throw new Error("视频消息过大");
        const message = JSON.parse(event.data);
        if (message.type === "ready" || message.type === "status") {
          if (message.type === "ready" && message.protocol !== 1) throw new Error("视频协议版本不支持");
          if (!message.status || typeof message.status.supported !== "boolean") throw new Error("视频状态无效");
          const next = message.status as Status;
          if (next.session?.id !== session.id || next.session?.revision !== session.revision || expired(next.session)) {
            setStatus(binding, next);
            if (binding.socket === socket) closeStream(binding);
            updateDOM(binding);
            schedule(binding);
            return;
          }
          setStatus(binding, next);
          if (!valid()) return;
          binding.socketReady = true;
          state.transport = "视频流";
          updateDOM(binding);
        } else if (message.type === "error") {
          const code = String(message.error || "desktop_stream_error");
          streamFailure(binding, errorMessages[code] || code,
            /csrf|unauthorized|forbidden|auth_required|session_expired/.test(code));
        } else throw new Error("视频消息类型无效");
      } catch (error) { if (valid()) streamFailure(binding, error instanceof Error ? error.message : "视频消息无效"); }
    };
    socket.onerror = () => { if (valid()) streamFailure(binding, "视频流不可用，已切换 HTTP 预览"); };
    socket.onclose = () => { if (valid()) streamFailure(binding, "视频流已断开，已切换 HTTP 预览"); };
    return true;
  } catch {
    streamFailure(binding, "视频流连接失败，已切换 HTTP 预览");
    return false;
  }
}

function applyStatus(state: PanelState, status: Status): void {
  const previous = state.status?.session;
  const sameSharedSession = previous?.id === status.session?.id && previous?.controller === "shared"
    && status.session?.controller === "shared" && previous?.mode === status.session?.mode && !expired(status.session);
  if (previous && !status.session) state.sessionNote = expired(previous) ? "共享授权已过期，请重新共享窗口" : "共享已结束，请重新选择窗口";
  if (previous?.id !== status.session?.id || previous?.revision !== status.session?.revision || expired(status.session)) {
    state.observation = null;
    state.observedAt = 0;
    state.selectedID = "";
    state.draft = "";
    state.dirty = false;
    if (!sameSharedSession) {
      state.inputDraft = "";
      state.actionError = "";
    }
  }
  state.status = status;
  if (status.session && !state.windowID) state.windowID = status.session.window.id;
}

function fail(binding: Binding, error: unknown, announce = false): void {
  if (!current(binding)) return;
  const {state} = binding;
  const typed = error as Error & {status?: number; code?: string};
  state.error = error instanceof Error ? error.message : String(error);
  if (typed.code && errorMessages[typed.code]) state.error = errorMessages[typed.code];
  state.observation = null;
  binding.lastStatusAt = 0;
  const controlConflict = ["desktop_owner_control", "desktop_agent_control", "desktop_claim_required"].includes(typed.code || "");
  if (controlConflict) state.error = "控制权已变化，正在重新读取";
  if (typed.status === 401 || (typed.status === 403 && !controlConflict)) state.denied = true;
  if (state.denied && binding.socket) closeStream(binding);
  clearImage(binding);
  if (announce) {
    state.actionError = state.error;
    binding.notify("error", state.error);
  }
}

function schedule(binding: Binding): void {
  window.clearTimeout(binding.timer);
  if (!current(binding) || binding.state.remote || binding.state.denied || document.hidden) return;
  binding.timer = window.setTimeout(() => {
    if (!current(binding)) { if (active === binding) stopDesktopPanel(); return; }
    void poll(binding);
  }, foreground(binding.state) && !binding.socket ? Math.max(200, frameOptions(binding).interval_ms) : 2500);
}

async function request<T>(binding: Binding, suffix: string, signal: AbortSignal, payload?: object): Promise<T> {
  if (suffix === "/actions" && payload) payload = {...payload, view_id: binding.viewID, action_id: randomID()};
  return api.request<T>(`/api/v1/tasks/${encodeURIComponent(binding.state.taskID)}/desktop${suffix}`, {
    signal, ...(payload ? {method: "POST", body: JSON.stringify(payload)} : {}),
  });
}

async function poll(binding: Binding, windows = false): Promise<void> {
  if (windows) binding.refreshWindows = true;
  if (!current(binding) || binding.state.remote || (binding.state.denied && !windows) || binding.read || binding.write) return;
  if (document.hidden) { schedule(binding); return; }
  const controller = new AbortController();
  binding.read = controller;
  const epoch = binding.epoch;
  const valid = () => current(binding) && !controller.signal.aborted && binding.epoch === epoch;
  const {state} = binding;
  let readingWindows = false;
  state.loading = true;
  updateDOM(binding);
  try {
    if (!state.status || Date.now() - binding.lastStatusAt >= 2500 || windows || !foreground(state)) {
      const response = await request<{status: Status}>(binding, "", controller.signal);
      if (!valid()) return;
      binding.lastStatusAt = Date.now();
      setStatus(binding, response.status);
    }
    state.denied = false;
    state.error = state.actionError;
    if ((state.status?.supported || state.status?.foreground_supported) && (binding.refreshWindows || !state.windowsLoaded)) {
      binding.refreshWindows = false;
      state.windowsLoaded = true;
      readingWindows = true;
      const result = state.status?.targets_supported
        ? await request<{targets: Targets}>(binding, "/targets", controller.signal)
        : await request<{windows: SharedWindow[]}>(binding, "/windows", controller.signal);
      readingWindows = false;
      if (!valid()) return;
      if ("targets" in result) {
        state.targets = result.targets;
        state.windows = result.targets.windows || [];
        if (!state.monitorID) state.monitorID = result.targets.monitors.find(item => item.primary)?.id || result.targets.monitors[0]?.id || "";
      } else state.windows = result.windows || [];
      if (!binding.chooserActive && state.targetKind === "window" && !state.windows.some(item => item.id === state.windowID)) {
        state.windowID = "";
        state.targetKind = "new-desktop";
      }
    }
    const session = state.status?.session;
    const streaming = ensureStream(binding);
    if (!streaming && (state.status?.supported || state.status?.foreground_supported) && session && !session.switching && !expired(session) && !binding.gesture && !binding.click) {
      const options = frameOptions(binding);
      const query = new URLSearchParams({session_id: session.id, view_id: binding.viewID,
        max_width: String(options.max_width), max_height: String(options.max_height), quality: String(options.quality)});
      const started = performance.now();
      const result = await request<{observation: Observation}>(binding, `/observation?${query}`, controller.signal);
      if (!valid()) return;
      const observation = result.observation;
      if (observation.session_id !== session.id || observation.revision !== session.revision) {
        state.observation = null;
        state.error = state.actionError || "控制状态已变化，正在重新读取";
      } else {
        clearImage(binding);
        acceptObservation(binding, observation);
        const now = Date.now();
        recordFrame(binding, Math.round((observation.image?.length || 0) * .75), 0,
          binding.lastFrameAt ? now - binding.lastFrameAt : performance.now() - started);
        binding.lastFrameAt = now;
        state.transport = foreground(state) && state.status?.stream_supported ? "HTTP 预览（视频流不可用）" : "HTTP 预览";
      }
    } else if (!session || expired(session)) state.observation = null;
  } catch (error) {
    if (valid()) {
      const code = (error as {code?: string}).code;
      if (code === "desktop_busy" || code === "desktop_frame_superseded") {
        // Capture contention is not an input failure. Preserve the displayed
        // frame and its original expiry while the next poll obtains a new one.
        if (readingWindows) binding.refreshWindows = true;
        state.error = state.actionError;
      } else fail(binding, error);
    }
  } finally {
    if (binding.read === controller) binding.read = null;
    if (valid()) {
      state.loading = false;
      updateDOM(binding);
      schedule(binding);
    }
  }
}

async function mutate(binding: Binding, suffix: string, payload: object, busy: string, urgent = false): Promise<void> {
  if (!current(binding) || binding.state.remote || (binding.write && !urgent)) return;
  const {state} = binding;
  window.clearTimeout(binding.timer);
  binding.epoch++;
  cancelGesture(binding);
  binding.read?.abort();
  binding.read = null;
  binding.write?.abort();
  const controller = new AbortController();
  binding.write = controller;
  const valid = () => current(binding) && binding.write === controller && !controller.signal.aborted;
  state.loading = false;
  state.busy = busy;
  state.error = "";
  state.actionError = "";
  if (suffix === "/switch") {
    state.popover = "";
    state.selectedID = "";
    state.draft = "";
    state.inputDraft = "";
  }
  const sentText = suffix === "/actions" && (payload as {kind?: string}).kind === "text"
    ? String((payload as {value?: string}).value || "") : null;
  const draftSubmitted = sentText !== null && state.inputDraft === sentText;
  binding.pendingText = draftSubmitted ? sentText : null;
  if (draftSubmitted) state.inputDraft = "";
  if (suffix === "/actions") {
    // An element action in background still leaves the frame valid; a
    // coordinate action in foreground has moved the page and needs a new one.
    if (foreground(state)) state.observedAt = 0;
  } else {
    closeStream(binding);
    state.observation = null;
  }
  updateDOM(binding);
  try {
    const response = await request<{status: Status}>(binding, suffix, controller.signal, payload);
    if (!valid()) return;
    setStatus(binding, response.status);
    binding.lastStatusAt = Date.now();
    if (suffix === "/session") state.sessionNote = "";
    if (suffix === "/switch") {
      state.sessionNote = "";
      state.windowsLoaded = false;
    }
    if (suffix === "/stop") state.sessionNote = "共享已停止";
    if (suffix === "/actions") state.dirty = false;
  } catch (error) {
    if (valid()) {
      if (draftSubmitted) state.inputDraft = sentText + state.inputDraft;
      fail(binding, error, true);
      if (suffix === "/switch") {
        state.status = null;
        binding.lastStatusAt = 0;
      }
    }
  } finally {
    if (valid()) {
      binding.pendingText = null;
      binding.write = null;
      state.busy = "";
      updateDOM(binding);
      // Retain operation errors until the next scheduled status refresh.
      if (suffix === "/switch" && state.error) void poll(binding);
      else if (state.error) schedule(binding);
      else void poll(binding);
    }
  }
}

function chooseElement(binding: Binding, id: string): void {
  const {state} = binding;
  const element = state.observation?.elements.find(item => item.id === id);
  if (!element) return;
  binding.root.querySelector<HTMLDetailsElement>(".desktop-inspector")!.open = true;
  setPopover(binding, "operations");
  if (state.selectedID !== id) {
    state.selectedID = id;
    state.draft = element.value || "";
    state.dirty = false;
  }
  updateDOM(binding);
}

function performAction(binding: Binding, kind: string): void {
  const {state} = binding;
  const element = selected(state);
  // Element actions work in both modes. In background the adapter maps them to
  // the control's own accessibility action, which is exactly what keeps the
  // Owner's focus where it is. Free-form pointer gestures stay foreground-only:
  // the background adapter has no coordinate input.
  if (!actionable(state) || !element || !supportedActions(element).includes(kind as ActionKind)) return;
  const session = state.status!.session!;
  void mutate(binding, "/actions", {
    session_id: session.id, revision: session.revision, observation_id: state.observation!.id,
    kind, element_id: element.id, ...(kind === "set_value" ? {value: state.draft} : {}),
  }, "正在操作目标软件...");
}

function cancelGesture(binding: Binding): void {
  window.clearTimeout(binding.clickTimer);
  binding.clickTimer = 0;
  binding.click = null;
  binding.gesture = null;
}

function pauseObservation(binding: Binding): void {
  window.clearTimeout(binding.timer);
  binding.epoch++;
  binding.read?.abort();
  binding.read = null;
  binding.state.loading = false;
}

function performInput(binding: Binding, kind: InputKind, fields: Record<string, unknown> = {}, observationID?: string): void {
  const {state} = binding;
  if (!current(binding)) return;
  if (!inputAllowed(state, kind)) {
    state.error = "目标当前不支持这项前台操作";
    updateDOM(binding);
    schedule(binding);
    return;
  }
  if ((binding.click || binding.gesture) && !["click", "double_click", "drag"].includes(kind)) {
    state.error = "点击或拖动尚未完成，本次输入未发送";
    updateDOM(binding);
    return;
  }
  if (!actionable(state) || (observationID && state.observation?.id !== observationID)) {
    state.error = state.busy ? "上一项操作尚未完成，本次输入未发送" : "画面或控制状态已变化，本次输入未发送";
    updateDOM(binding);
    if (!binding.write) schedule(binding);
    return;
  }
  if (kind === "text") {
    const error = inputTextError(String(fields.value || ""));
    if (error) {
      state.error = error;
      updateDOM(binding);
      return;
    }
  }
  const session = state.status!.session!;
  void mutate(binding, "/actions", {
    ...fields, session_id: session.id, revision: session.revision, observation_id: state.observation!.id,
    kind, element_id: "$surface",
  }, "正在操作目标软件...");
}

function imagePoint(binding: Binding, event: MouseEvent): {x: number; y: number} | null {
  const observation = binding.state.observation;
  const image = binding.root.querySelector<HTMLImageElement>("#desktop-image");
  if (!observation || !image || (!observation.image && !binding.imageURL) || observation.width <= 0 || observation.height <= 0) return null;
  const bounds = image.getBoundingClientRect();
  if (bounds.width <= 0 || bounds.height <= 0) return null;
  return {
    x: Math.max(0, Math.min(observation.width - 1, (event.clientX - bounds.left) / bounds.width * observation.width)),
    y: Math.max(0, Math.min(observation.height - 1, (event.clientY - bounds.top) / bounds.height * observation.height)),
  };
}

function keyboardCommand(event: KeyboardEvent): {kind: "text" | "key"; fields: Record<string, unknown>} | null {
  if (event.isComposing || ["Control", "Alt", "Shift", "Meta", "Dead", "Unidentified"].includes(event.key)) return null;
  if (!event.ctrlKey && !event.altKey && !event.metaKey && [...event.key].length === 1) {
    return {kind: "text", fields: {value: event.key}};
  }
  const names: Record<string, string> = {
    Enter: "ENTER", Escape: "ESC", Tab: "TAB", Backspace: "BACKSPACE", Delete: "DELETE",
    ArrowLeft: "LEFT", ArrowRight: "RIGHT", ArrowUp: "UP", ArrowDown: "DOWN",
    Home: "HOME", End: "END", PageUp: "PAGEUP", PageDown: "PAGEDOWN", Insert: "INSERT", " ": "SPACE",
  };
  const key = names[event.key] || (/^[a-z0-9]$/i.test(event.key) || /^F(?:[1-9]|1[0-2])$/.test(event.key) ? event.key.toUpperCase() : "");
  if (!key) return null;
  const keys = [...(event.ctrlKey ? ["CTRL"] : []), ...(event.altKey ? ["ALT"] : []),
    ...(event.shiftKey ? ["SHIFT"] : []), ...(event.metaKey ? ["WIN"] : []), key];
  if (keys.length > 4 || (keys.includes("CTRL") && keys.includes("ALT") && key === "DELETE")
    || (keys.includes("CTRL") && keys.includes("WIN") && ["D", "LEFT", "RIGHT", "F4"].includes(key))
    || (keys.includes("WIN") && ["L", "U"].includes(key))) return null;
  return {kind: "key", fields: {keys}};
}

function bindInput(binding: Binding): void {
  const {root, state} = binding;
  const image = root.querySelector<HTMLImageElement>("#desktop-image")!;
  listen(binding, image, "pointerdown", event => {
    if (binding.gesture) return;
    if (!foreground(state) || !actionable(state) || !["click", "double_click", "drag"].some(kind => inputAllowed(state, kind as InputKind))) return;
    const point = imagePoint(binding, event);
    if (!point || ![0, 1, 2].includes(event.button)) return;
    pauseObservation(binding);
    image.focus({preventScroll: true});
    event.preventDefault();
    binding.gesture = {...point, button: ["left", "middle", "right"][event.button],
      pointerID: event.pointerId, observationID: state.observation!.id};
    image.setPointerCapture(event.pointerId);
  });
  listen(binding, image, "pointerup", event => {
    const gesture = binding.gesture;
    if (!gesture || gesture.pointerID !== event.pointerId) return;
    binding.gesture = null;
    schedule(binding);
    if (image.hasPointerCapture(event.pointerId)) image.releasePointerCapture(event.pointerId);
    const point = imagePoint(binding, event);
    if (!point) return;
    const moved = Math.hypot(point.x - gesture.x, point.y - gesture.y) > 5;
    if (moved) {
      cancelGesture(binding);
      performInput(binding, "drag", {x: gesture.x, y: gesture.y, end_x: point.x, end_y: point.y, button: gesture.button}, gesture.observationID);
      return;
    }
    if (binding.click && binding.click.button === gesture.button
      && binding.click.observationID === gesture.observationID
      && Math.hypot(binding.click.x - point.x, binding.click.y - point.y) < 8) {
      cancelGesture(binding);
      performInput(binding, "double_click", {...point, button: gesture.button}, gesture.observationID);
      return;
    }
    // One bounded click discriminator, never a queue of inputs to replay.
    if (binding.click) return;
    binding.click = gesture;
    binding.clickTimer = window.setTimeout(() => {
      binding.clickTimer = 0;
      binding.click = null;
      performInput(binding, "click", {...point, button: gesture.button}, gesture.observationID);
    }, inputAllowed(state, "double_click") ? 300 : 0);
  });
  listen(binding, image, "pointercancel", () => { cancelGesture(binding); schedule(binding); });
  listen(binding, image, "blur", () => {
    // A completed click may still be waiting for the double-click discriminator.
    // Moving into the text editor must not silently discard that target click.
    if (binding.gesture) cancelGesture(binding);
    schedule(binding);
  });
  listen(binding, image, "contextmenu", event => {
    if (foreground(state) && inputAllowed(state, "click")) event.preventDefault();
  });
  listen(binding, image, "wheel", event => {
    if (document.activeElement !== image || !inputAllowed(state, "scroll")) return;
    event.preventDefault();
    const point = imagePoint(binding, event);
    if (!point || !actionable(state) || (!event.deltaX && !event.deltaY)) return;
    const scale = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? image.clientHeight : 1;
    const clamp = (value: number) => Math.max(-2400, Math.min(2400, value * scale));
    performInput(binding, "scroll", {...point, delta_x: clamp(event.deltaX), delta_y: clamp(event.deltaY)});
  }, {passive: false});
  listen(binding, image, "keydown", event => {
    if (document.activeElement !== image) return;
    const command = keyboardCommand(event);
    if (!command || !inputAllowed(state, command.kind)) return;
    event.preventDefault();
    if (event.repeat) return;
    if (command.kind === "text" && canAssist(state)) {
      // Move printable typing into a real editor so fast input and IME are not
      // discarded while a native action or screenshot request is pending.
      state.inputDraft += String(command.fields.value || "");
      setPopover(binding, "");
      updateDOM(binding);
      const editor = root.querySelector<HTMLTextAreaElement>("#desktop-text-input")!;
      editor.focus();
      editor.setSelectionRange(editor.value.length, editor.value.length);
      return;
    }
    performInput(binding, command.kind, command.fields);
  });
  listen(binding, root.querySelector("#desktop-focus"), "click", () => performInput(binding, "focus"));
  root.querySelectorAll<HTMLButtonElement>("[data-desktop-key]").forEach(button => listen(binding, button, "click", () => {
    const shortcut = shortcuts.find(item => item.id === button.dataset.desktopKey);
    if (shortcut) performInput(binding, "key", {keys: shortcut.keys});
  }));
  listen(binding, root.querySelector("#desktop-text-input"), "input", event => {
    state.inputDraft = (event.target as HTMLTextAreaElement).value;
    updateDOM(binding);
  });
  listen(binding, root.querySelector("#desktop-text-form"), "submit", event => {
    event.preventDefault();
    if (state.inputDraft) performInput(binding, "text", {value: state.inputDraft});
  });
}

export function desktopPanelInteracting(): boolean {
  return Boolean(active && current(active) && (active.state.popover || document.fullscreenElement === active.root || active.chooserActive || active.gesture || active.click
    || (document.activeElement instanceof HTMLElement && active.root.contains(document.activeElement))));
}

function syncPopovers(binding: Binding): void {
  binding.root.querySelector("#desktop-keys-popover")?.classList.toggle("open", binding.state.popover === "keys");
  binding.root.querySelectorAll<HTMLElement>(".desktop-popover").forEach(popover => {
    popover.hidden = popover.id !== `desktop-${binding.state.popover}-popover`;
  });
  binding.root.querySelectorAll<HTMLButtonElement>("[data-desktop-popover]").forEach(button => {
    const expanded = button.dataset.desktopPopover === binding.state.popover;
    button.setAttribute("aria-expanded", String(expanded));
    button.classList.toggle("active", expanded);
  });
}

function setPopover(binding: Binding, name: PanelState["popover"], restoreFocus = false): void {
  if (!current(binding) || binding.state.remote) return;
  const previous = binding.state.popover;
  binding.state.popover = name;
  syncPopovers(binding);
  if (name) {
    const popover = binding.root.querySelector<HTMLElement>(`#desktop-${name}-popover`);
    Array.from(popover?.querySelectorAll<HTMLElement>("button:not([disabled]), select:not([disabled]), textarea:not([hidden])") || [])
      .find(element => element.getClientRects().length > 0)?.focus({preventScroll: true});
  } else if (restoreFocus && previous) {
    binding.root.querySelector<HTMLElement>(`[data-desktop-popover="${previous}"]`)?.focus({preventScroll: true});
  }
}

function bindPopovers(binding: Binding): void {
  const {root} = binding;
  root.querySelectorAll<HTMLButtonElement>("[data-desktop-popover]").forEach(button => listen(binding, button, "click", () => {
    const name = button.dataset.desktopPopover as PanelState["popover"];
    setPopover(binding, binding.state.popover === name ? "" : name);
  }));
  listen(binding, root.querySelector("#desktop-empty-target"), "click", () => setPopover(binding, "targets"));
  const outside = (event: Event) => {
    if (!current(binding) || !binding.state.popover || !(event.target instanceof Element)) return;
    if (event.target.closest(".desktop-popover, .desktop-key-options, [data-desktop-popover]")) return;
    setPopover(binding, "");
  };
  const keyboard = (event: KeyboardEvent) => {
    if (!current(binding) || !binding.state.popover) return;
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      setPopover(binding, "", true);
    }
    if (event.key === "Tab") {
      const popover = root.querySelector<HTMLElement>(`#desktop-${binding.state.popover}-popover`);
      const controls = Array.from(popover?.querySelectorAll<HTMLElement>("button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary") || [])
        .filter(element => element.getClientRects().length > 0);
      if (!controls.length) return;
      if (event.shiftKey && document.activeElement === controls[0]) {
        event.preventDefault(); controls.at(-1)!.focus();
      } else if (!event.shiftKey && document.activeElement === controls.at(-1)) {
        event.preventDefault(); controls[0].focus();
      }
    }
  };
  listen(binding, document, "pointerdown", outside, true);
  listen(binding, document, "keydown", keyboard, true);
  binding.removePopover = () => {
    binding.state.popover = "";
    syncPopovers(binding);
  };
}

function shareTarget(binding: Binding, createDesktop = false): void {
  if (!current(binding) || binding.state.remote) return;
  const {state} = binding;
  const supported = state.mode === "foreground" ? state.status?.foreground_supported : state.status?.supported;
  if (!supported || state.status?.shared_control_supported !== true || state.denied || state.busy || state.status?.session?.switching
    || (state.status?.session && state.status.session.controller !== "shared")) return;
  const target: TargetSelection = createDesktop ? {kind: "new-desktop"}
    : state.targetKind === "desktop" ? {kind: "desktop", desktop_id: state.desktopID}
      : {kind: "window", window_id: state.windowID};
  if (target.kind !== "window" && state.mode !== "foreground") return;
  const selectedWindow = target.kind === "window" ? state.windows.find(item => item.id === target.window_id) : undefined;
  if (target.kind === "window" && (!selectedWindow || !windowSelectable(state, selectedWindow))) return;
  if (target.kind === "desktop" && !state.targets?.desktops.some(item => item.id === target.desktop_id
    && (state.targets!.desktop_switch_supported || item.current))) return;
  if (target.kind === "new-desktop" && state.targets && !state.targets.desktop_switch_supported) return;
  if (target.kind !== "window" && state.monitorID) target.monitor_id = state.monitorID;
  const label = target.kind === "new-desktop" ? "新建并切换 Windows 桌面"
    : target.kind === "desktop" ? state.targets?.desktops.find(item => item.id === target.desktop_id)?.name
      : state.windows.find(item => item.id === target.window_id)?.title;
  const monitor = state.targets?.monitors.find(item => item.id === target.monitor_id);
  const switchNotice = selectedWindow?.other_desktop
    ? state.mode === "background"
      ? `\n将在窗口所属桌面「${windowDesktopName(state, selectedWindow)}」后台操作，不切换到该桌面。`
      : `\n将切换到窗口所属桌面：${windowDesktopName(state, selectedWindow)}。`
    : "";
  const effects = state.mode === "foreground"
    ? "会移动宿主鼠标、输入键盘、改变窗口焦点或切换桌面。不是虚拟机，也不提供输入隔离。"
    : "仅通过目标软件支持的后台控件操作改变软件状态，不切换桌面、不激活窗口。是否可显示画面或操作控件取决于目标应用。";
  const consent = window.confirm(`共同控制：${label || "共享目标"}${switchNotice}${monitor ? `\n显示器：${monitor.name}` : ""}\n\n授权当前任务的 Main Agent 和 Owner 共同查看并操作这个目标，无需交接控制权。\n${effects}\n\n${state.status?.session ? "更换共享目标后旧目标授权立即失效。" : ""}确认共同共享？`);
  if (!consent) return;
  const session = state.status?.session;
  void mutate(binding, session ? "/switch" : "/session", {
    ...(session ? {session_id: session.id, revision: session.revision} : {}),
    target, mode: state.mode, confirm_foreground: state.mode === "foreground", confirm_shared: true,
  }, session ? "正在切换共享目标..." : "正在共享目标...");
}

export function bindDesktopPanel(detail: TaskDetail, notify: Notice): void {
  const root = document.querySelector<HTMLElement>("#desktop-tool");
  if (active?.root === root && active.state.taskID === detail.task.id && active.state.remote === Boolean(detail.task.read_only)) return;
  stopDesktopPanel();
  if (!root || root.dataset.taskId !== detail.task.id) return;
  const state = stateFor(detail);
  state.remote = Boolean(detail.task.read_only);
  const binding: Binding = {root, state, notify, timer: 0, read: null, write: null, epoch: 0,
    removeVisibility: () => {}, chooserActive: false, clickTimer: 0, click: null, gesture: null, refreshWindows: false,
    viewID: randomID(), socket: null, socketKey: "", socketGeneration: 0, socketReady: false, socketTimer: 0,
    retries: 0, retryAt: 0, retryKey: "", decoding: false, decoder: null, decodeTimer: 0, blobURLs: new Set(),
    imageURL: "", lastFrameAt: 0, lastStatusAt: 0, decodePending: 0, pendingText: null, removeLayout: () => {},
    previewWidth: 0, previewHeight: 0, removePopover: () => {}, listeners: []};
  active = binding;
  root.querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLTextAreaElement>(
    ".desktop-picture-bar button, .desktop-picture-bar input, .desktop-input-bar button, .desktop-input-bar textarea",
  ).forEach(control => { control.disabled = state.remote; });
  if (state.remote) {
    root.querySelectorAll<HTMLButtonElement>(".desktop-header-controls button").forEach(button => { button.disabled = true; });
    return;
  }
  root.querySelectorAll<HTMLButtonElement>("[data-desktop-popover]").forEach(button => { button.disabled = false; });
  bindPopovers(binding);
  const layout = () => {
    if (state.popover === "keys" && !root.querySelector("#desktop-more-keys")?.getClientRects().length) setPopover(binding, "");
    layoutPreview(binding);
  };
  const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(layout);
  const viewport = root.querySelector<HTMLElement>("#desktop-viewport")!;
  observer?.observe(viewport);
  listen(binding, document, "fullscreenchange", layout);
  root.querySelectorAll("details").forEach(node => listen(binding, node, "toggle", layout));
  binding.removeLayout = () => {
    observer?.disconnect();
    if (document.fullscreenElement === root) void document.exitFullscreen().catch(() => {});
  };
  const setZoom = (zoom: number) => {
    cancelGesture(binding);
    pauseObservation(binding);
    state.zoom = Number.isFinite(zoom) ? Math.max(1, Math.min(3, zoom)) : 1;
    viewport.scrollTop = viewport.scrollLeft = 0;
    layout();
    schedule(binding);
  };
  listen(binding, root.querySelector("#desktop-fit"), "click", () => setZoom(1));
  listen(binding, root.querySelector("#desktop-zoom"), "input", event => setZoom(Number((event.target as HTMLInputElement).value) / 100));
  listen(binding, root.querySelector("#desktop-fullscreen"), "click", async () => {
    cancelGesture(binding);
    try {
      if (document.fullscreenElement === root) await document.exitFullscreen();
      else await root.requestFullscreen();
    } catch {
      if (current(binding)) notify("error", "浏览器未允许全屏");
    }
    layout();
  });
  root.querySelectorAll<HTMLButtonElement>("[data-desktop-video]").forEach(button => listen(binding, button, "click", () => {
    const mode = button.dataset.desktopVideo;
    if (mode !== "auto" && mode !== "quality" && mode !== "bandwidth") return;
    state.videoMode = mode;
    state.adaptive = {level: 1, slow: 0, fast: 0};
    updateDOM(binding);
    if (!binding.socket) void poll(binding);
  }));
  const chooser = root.querySelector<HTMLSelectElement>("#desktop-window")!;
  const beginChoosing = () => { if (!chooser.disabled) binding.chooserActive = true; };
  listen(binding, chooser, "pointerdown", beginChoosing);
  listen(binding, chooser, "focus", beginChoosing);
  listen(binding, chooser, "blur", () => { binding.chooserActive = false; updateDOM(binding); });
  listen(binding, root.querySelector("#desktop-window"), "change", event => {
    const value = (event.target as HTMLSelectElement).value;
    try {
      const choice = JSON.parse(value) as {kind: string; id: string};
      if (choice.kind === "window" && state.windows.some(item => item.id === choice.id)) {
        state.windowID = choice.id;
        state.targetKind = "window";
      } else if (choice.kind === "desktop" && state.targets?.desktops.some(item => item.id === choice.id)) {
        state.desktopID = choice.id;
        state.targetKind = "desktop";
      }
    } catch { state.targetKind = "new-desktop"; }
    updateDOM(binding);
  });
  listen(binding, root.querySelector("#desktop-monitor"), "change", event => {
    const id = (event.target as HTMLSelectElement).value;
    if (state.targets?.monitors.some(item => item.id === id)) state.monitorID = id;
  });
  root.querySelectorAll<HTMLButtonElement>("[data-desktop-mode]").forEach(button => listen(binding, button, "click", () => {
    if (state.busy || state.status?.session?.switching) return;
    state.mode = button.dataset.desktopMode === "background" ? "background" : "foreground";
    if (state.mode === "background" && state.windowID === "new-desktop") state.windowID = "";
    if (state.mode === "foreground" && !state.windowID) state.windowID = "new-desktop";
    updateDOM(binding);
  }));
  listen(binding, root.querySelector("#desktop-refresh"), "click", () => {
    state.denied = false;
    void poll(binding, true);
  });
  listen(binding, root.querySelector("#desktop-share"), "click", () => shareTarget(binding));
  listen(binding, root.querySelector("#desktop-new-desktop"), "click", () => shareTarget(binding, true));
  listen(binding, root.querySelector("#desktop-stop"), "click", () => {
    const session = state.status?.session;
    if (session) {
      setPopover(binding, "");
      void mutate(binding, "/stop", {session_id: session.id}, "正在停止共享...", true);
    }
  });
  listen(binding, root.querySelector("#desktop-elements"), "click", event => {
    const button = event.target instanceof Element ? event.target.closest<HTMLElement>("[data-desktop-element]") : null;
    if (button) chooseElement(binding, button.dataset.desktopElement || "");
  });
  listen(binding, root.querySelector("#desktop-image"), "click", event => {
    if (foreground(state)) return;
    const observation = state.observation;
    if (!observation) return;
    const bounds = (event.currentTarget as HTMLImageElement).getBoundingClientRect();
    const pointer = event as MouseEvent;
    const x = (pointer.clientX - bounds.left) / bounds.width * observation.width;
    const y = (pointer.clientY - bounds.top) / bounds.height * observation.height;
    const hits = observation.elements.filter(element => element.width > 0 && element.height > 0
      && x >= element.x && y >= element.y && x <= element.x + element.width && y <= element.y + element.height);
    hits.sort((a, b) => a.width * a.height - b.width * b.height);
    if (hits[0]) chooseElement(binding, hits[0].id);
  });
  listen(binding, root.querySelector("#desktop-image"), "error", () => {
    if (!current(binding)) return;
    root.querySelector<HTMLElement>("#desktop-preview")!.hidden = true;
    const message = root.querySelector<HTMLElement>("#desktop-capture-status")!;
    message.textContent = "窗口截图无法显示";
    message.hidden = false;
  });
  listen(binding, root.querySelector("#desktop-actions"), "click", event => {
    const button = event.target instanceof Element ? event.target.closest<HTMLElement>("[data-desktop-action]") : null;
    if (button) performAction(binding, button.dataset.desktopAction || "");
  });
  listen(binding, root.querySelector("#desktop-value-input"), "input", event => {
    state.draft = (event.target as HTMLTextAreaElement).value;
    state.dirty = true;
  });
  listen(binding, root.querySelector("#desktop-value-form"), "submit", event => {
    event.preventDefault();
    performAction(binding, "set_value");
  });
  bindInput(binding);
  const visibility = () => {
    window.clearTimeout(binding.timer);
    if (document.hidden) {
      pauseObservation(binding);
      if (binding.write) {
        state.actionError = "页面已隐藏，上一项操作结果未确认，请检查目标状态";
        state.error = state.actionError;
        if (binding.pendingText !== null) state.inputDraft = binding.pendingText + state.inputDraft;
      }
      binding.pendingText = null;
      binding.write?.abort();
      binding.write = null;
      state.busy = "";
      binding.lastStatusAt = 0;
      closeStream(binding);
      state.observation = null;
      state.observedAt = 0;
      updateDOM(binding);
    }
    if (!document.hidden && current(binding)) void poll(binding);
  };
  listen(binding, document, "visibilitychange", visibility);
  updateDOM(binding);
  void poll(binding, true);
}

export function stopDesktopPanel(): void {
  const binding = active;
  active = null;
  if (!binding) return;
  for (const remove of binding.listeners.splice(0)) remove();
  window.clearTimeout(binding.timer);
  binding.removeVisibility();
  binding.removeLayout();
  binding.removePopover();
  binding.read?.abort();
  binding.write?.abort();
  closeStream(binding);
  cancelGesture(binding);
  // Captures and element contents are transient, never browser storage or logs.
  binding.state.observation = null;
  binding.state.loading = false;
  binding.state.busy = "";
}

export function refreshDesktopPanel(detail: TaskDetail, notify: Notice): boolean {
  const root = document.querySelector<HTMLElement>("#desktop-tool");
  if (!root || root.dataset.taskId !== detail.task.id) return false;
  if (active?.root === root && active.state.remote === Boolean(detail.task.read_only)) return false;
  if (!root.parentElement) return false;
  const header = root.querySelector<HTMLElement>(".desktop-task-header");
  stopDesktopPanel();
  stateFor(detail).remote = Boolean(detail.task.read_only);
  const container = document.createElement("div");
  container.innerHTML = renderDesktopPanel(detail, header?.outerHTML || "");
  const next = container.firstElementChild!;
  root.replaceWith(next);
  if (header) next.querySelector(".desktop-task-header")?.replaceWith(header);
  bindDesktopPanel(detail, notify);
  return true;
}
