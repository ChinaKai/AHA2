import type {HardwareTerminalStatus} from "./types.js";

type Transport = "serial" | "network";

interface XTermDisposable {
  dispose(): void;
}

interface XTerm {
  cols: number;
  rows: number;
  options: {disableStdin?: boolean};
  textarea?: HTMLTextAreaElement;
  buffer: {
    active: {
      viewportY: number;
      getLine(index: number): {translateToString(trimRight?: boolean): string} | undefined;
    };
  };
  open(element: HTMLElement): void;
  write(data: string | Uint8Array, callback?: () => void): void;
  reset(): void;
  clear(): void;
  focus(): void;
  resize(cols: number, rows: number): void;
  dispose(): void;
  onData(listener: (data: string) => void): XTermDisposable;
  onBinary(listener: (data: string) => void): XTermDisposable;
  onRender(listener: () => void): XTermDisposable;
}

declare global {
  interface Window {
    Terminal?: new(options: Record<string, unknown>) => XTerm;
  }
}

export interface HardwareTerminalBinding {
  key: string;
  clear(): void;
  focus(): void;
  send(data: string): void;
  writeFallback(data: Uint8Array): void;
  setStatus(status: HardwareTerminalStatus): void;
  dispose(): void;
}

interface MountOptions {
  taskID: string;
  hardwareID: string;
  transport: Transport;
  status: HardwareTerminalStatus;
  initialData: string;
  afterSequence: number;
  container: HTMLElement;
  onFallbackSend(data: Uint8Array): Promise<void>;
  onStatus(status: HardwareTerminalStatus): void;
  onError(message: string): void;
}

function socketURL(options: MountOptions): string {
  const protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const query = new URLSearchParams({
    transport: options.transport,
    after: String(Math.max(0, options.afterSequence)),
  });
  return `${protocol}//${location.host}/api/v1/tasks/${encodeURIComponent(options.taskID)}/hardware/${encodeURIComponent(options.hardwareID)}/terminal/ws?${query}`;
}

export async function terminalFrameBytes(data: unknown): Promise<Uint8Array | null> {
  if (data instanceof ArrayBuffer) return new Uint8Array(data);
  if (ArrayBuffer.isView(data)) {
    return new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
  }
  if (typeof Blob !== "undefined" && data instanceof Blob) {
    return new Uint8Array(await data.arrayBuffer());
  }
  return null;
}

function terminalSize(element: HTMLElement, terminal: XTerm): {cols: number; rows: number} {
  const terminalRect = element.querySelector<HTMLElement>(".xterm")?.getBoundingClientRect() || element.getBoundingClientRect();
  const screenRect = element.querySelector<HTMLElement>(".xterm-screen")?.getBoundingClientRect();
  const cellWidth = screenRect && terminal.cols > 0 ? screenRect.width/terminal.cols : 8.1;
  const cellHeight = screenRect && terminal.rows > 0 ? screenRect.height/terminal.rows : 15;
  return {
    cols: Math.max(20, Math.min(320, Math.floor(Math.max(180, terminalRect.width) / Math.max(1, cellWidth)))),
    rows: Math.max(8, Math.min(120, Math.floor(Math.max(120, terminalRect.height) / Math.max(1, cellHeight)))),
  };
}

function terminalOptions(status: HardwareTerminalStatus): Record<string, unknown> {
  return {
    cursorBlink: true,
    cursorStyle: "block",
    convertEol: false,
    disableStdin: status.read_only || !status.connected,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    fontSize: 13,
    scrollback: 10000,
    theme: {
      background: "#11151b",
      foreground: "#d7e0ea",
      cursor: "#f8fafc",
      selectionBackground: "#344054",
    },
  };
}

function usesTouchInput(): boolean {
  return navigator.maxTouchPoints > 0 || Boolean(window.matchMedia?.("(pointer: coarse)").matches);
}

export function mountHardwareTerminal(options: MountOptions): HardwareTerminalBinding | null {
  const Constructor = window.Terminal;
  if (!Constructor) {
    options.container.textContent = "终端组件加载失败";
    return null;
  }
  options.container.replaceChildren();
  const terminal = new Constructor(terminalOptions(options.status));
  terminal.open(options.container);
  if (terminal.textarea) {
    terminal.textarea.inputMode = "text";
    terminal.textarea.enterKeyHint = "enter";
  }
  const touchInput = usesTouchInput();
  const mobileView = touchInput ? document.createElement("div") : null;
  if (mobileView) {
    mobileView.className = "hardware-mobile-terminal-view";
    mobileView.setAttribute("aria-hidden", "true");
    options.container.classList.add("mobile-terminal-compat");
    options.container.appendChild(mobileView);
  }
  const mobileInput = touchInput ? document.createElement("textarea") : null;
  if (mobileInput) {
    mobileInput.className = "hardware-mobile-terminal-input";
    mobileInput.rows = 1;
    mobileInput.inputMode = "text";
    mobileInput.enterKeyHint = "enter";
    mobileInput.autocapitalize = "off";
    mobileInput.autocomplete = "off";
    mobileInput.spellcheck = false;
    mobileInput.readOnly = Boolean(terminal.options.disableStdin);
    mobileInput.setAttribute("aria-label", "终端输入");
    options.container.appendChild(mobileInput);
  }
  const key = `${options.taskID}:${options.hardwareID}:${options.transport}`;
  let socket: WebSocket | null = null;
  let disposed = false;
  let ready = false;
  let currentStatus = options.status;
  let connectionEnabled = currentStatus.connected;
  let reconnectTimer = 0;
  let readyTimer = 0;
  let resizeTimer = 0;
  let inputTimer = 0;
  let outputQueue = Promise.resolve();
  let fallbackInputQueue = Promise.resolve();
  let pendingInput: Uint8Array[] = [];
  let pendingInputBytes = 0;
  const disposables: XTermDisposable[] = [];
  const renderMobileView = () => {
    if (!mobileView) return;
    const sourceRows = options.container.querySelector<HTMLElement>(".xterm-rows");
    const rows = sourceRows ? Array.from(sourceRows.children) : [];
    if (rows.some(row => row.textContent?.trim())) {
      mobileView.replaceChildren(...rows.map(row => row.cloneNode(true)));
    } else {
      const fragment = document.createDocumentFragment();
      const buffer = terminal.buffer.active;
      for (let row = 0; row < terminal.rows; row++) {
        const element = document.createElement("div");
        element.textContent = buffer.getLine(buffer.viewportY+row)?.translateToString(true) || "";
        fragment.appendChild(element);
      }
      mobileView.replaceChildren(fragment);
    }
    mobileView.scrollTop = mobileView.scrollHeight;
  };
  disposables.push(terminal.onRender(renderMobileView));
  terminal.write(options.initialData, renderMobileView);
  const focusInput = () => {
    if (!currentStatus.connected || currentStatus.read_only) return;
    if (mobileInput) {
      mobileInput.readOnly = false;
      mobileInput.focus({preventScroll: true});
    } else {
      terminal.focus();
    }
  };
  const focusTerminal = () => {
    focusInput();
  };
  options.container.addEventListener("touchstart", focusTerminal, {passive: true});
  options.container.addEventListener("click", focusTerminal);

  const sendControl = (payload: Record<string, unknown>) => {
    if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(payload));
  };
  const fit = () => {
    if (disposed) return;
    const size = terminalSize(options.container, terminal);
    if (size.cols !== terminal.cols || size.rows !== terminal.rows) terminal.resize(size.cols, size.rows);
    if (ready) sendControl({type: "resize", ...size});
  };
  const scheduleFit = () => {
    if (resizeTimer) clearTimeout(resizeTimer);
    resizeTimer = window.setTimeout(fit, 60);
  };
  const flushInput = () => {
    inputTimer = 0;
    if (pendingInputBytes === 0) return;
    const merged = new Uint8Array(pendingInputBytes);
    let offset = 0;
    for (const chunk of pendingInput) {
      merged.set(chunk, offset);
      offset += chunk.length;
    }
    pendingInput = [];
    pendingInputBytes = 0;
    if (ready && socket?.readyState === WebSocket.OPEN) {
      socket.send(merged);
      return;
    }
    if (!currentStatus.connected || currentStatus.read_only) return;
    fallbackInputQueue = fallbackInputQueue
      .then(() => options.onFallbackSend(merged))
      .catch(error => {
        if (!disposed) options.onError(error instanceof Error ? error.message : String(error));
      });
  };
  const queueInput = (data: Uint8Array) => {
    if (!currentStatus.connected || currentStatus.read_only || data.length === 0) return;
    if (pendingInputBytes+data.length > 64*1024) flushInput();
    pendingInput.push(data);
    pendingInputBytes += data.length;
    if (!inputTimer) inputTimer = window.setTimeout(flushInput, 16);
  };
  const flushMobileInput = () => {
    if (!mobileInput || mobileInput.value === "") return;
    const value = mobileInput.value.replace(/\r\n|\r|\n/g, "\r");
    mobileInput.value = "";
    queueInput(new TextEncoder().encode(value));
  };
  const handleMobileInput = (event: Event) => {
    if (!(event as InputEvent).isComposing) flushMobileInput();
  };
  const handleMobileCompositionEnd = () => {
    window.setTimeout(flushMobileInput, 0);
  };
  const handleMobileKeydown = (event: KeyboardEvent) => {
    if (event.key === "Enter") {
      event.preventDefault();
      mobileInput!.value = "";
      queueInput(new Uint8Array([0x0d]));
    } else if (event.key === "Backspace" && mobileInput!.value === "") {
      event.preventDefault();
      queueInput(new Uint8Array([0x7f]));
    }
  };
  mobileInput?.addEventListener("input", handleMobileInput);
  mobileInput?.addEventListener("compositionend", handleMobileCompositionEnd);
  mobileInput?.addEventListener("keydown", handleMobileKeydown);
  const connect = () => {
    if (disposed || !connectionEnabled || socket) return;
    ready = false;
    terminal.options.disableStdin = currentStatus.read_only || !currentStatus.connected;
    if (mobileInput) mobileInput.readOnly = Boolean(terminal.options.disableStdin);
    const next = new WebSocket(socketURL(options));
    next.binaryType = "arraybuffer";
    socket = next;
    next.addEventListener("message", event => {
      if (disposed || socket !== next) return;
      if (typeof event.data !== "string") {
        outputQueue = outputQueue.then(async () => {
          const bytes = await terminalFrameBytes(event.data);
          if (!disposed && socket === next && bytes?.length) terminal.write(bytes);
        }).catch(error => {
          if (!disposed && socket === next) options.onError(error instanceof Error ? error.message : String(error));
        });
        return;
      }
      let message: Record<string, unknown>;
      try {
        message = JSON.parse(event.data);
      } catch {
        return;
      }
      if (message.type === "ready") {
        const status = message.status as HardwareTerminalStatus;
        if (readyTimer) clearTimeout(readyTimer);
        readyTimer = 0;
        terminal.reset();
        terminal.write(options.initialData, renderMobileView);
        ready = true;
        currentStatus = status;
        connectionEnabled = status.connected;
        terminal.options.disableStdin = status.read_only || !status.connected;
        if (mobileInput) mobileInput.readOnly = terminal.options.disableStdin;
        options.onStatus(status);
        fit();
      } else if (message.type === "status") {
        const status = message.status as HardwareTerminalStatus;
        currentStatus = status;
        connectionEnabled = status.connected;
        terminal.options.disableStdin = status.read_only || !status.connected;
        if (mobileInput) mobileInput.readOnly = terminal.options.disableStdin;
        options.onStatus(status);
      } else if (message.type === "error") {
        options.onError(String(message.message || "终端错误"));
      }
    });
    next.addEventListener("error", () => {
      if (!disposed && socket === next) ready = false;
    });
    next.addEventListener("close", () => {
      if (disposed || socket !== next) return;
      if (readyTimer) clearTimeout(readyTimer);
      readyTimer = 0;
      socket = null;
      ready = false;
      pendingInput = [];
      pendingInputBytes = 0;
      if (inputTimer) clearTimeout(inputTimer);
      inputTimer = 0;
      terminal.options.disableStdin = currentStatus.read_only || !currentStatus.connected;
      if (mobileInput) mobileInput.readOnly = Boolean(terminal.options.disableStdin);
      if (connectionEnabled) reconnectTimer = window.setTimeout(connect, 5000);
    });
    readyTimer = window.setTimeout(() => {
      if (!disposed && socket === next && !ready) next.close();
    }, 2500);
  };
  disposables.push(terminal.onData(data => {
    queueInput(new TextEncoder().encode(data));
  }));
  disposables.push(terminal.onBinary(data => {
    queueInput(Uint8Array.from(data, character => character.charCodeAt(0) & 0xff));
  }));
  const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(scheduleFit);
  observer?.observe(options.container);
  if (connectionEnabled) connect();
  scheduleFit();

  return {
    key,
    clear() {
      terminal.clear();
      renderMobileView();
      focusInput();
    },
    focus() {
      focusInput();
    },
    send(data: string) {
      queueInput(new TextEncoder().encode(data));
      focusInput();
    },
    writeFallback(data: Uint8Array) {
      if (!ready && data.length > 0) terminal.write(data, renderMobileView);
    },
    setStatus(status: HardwareTerminalStatus) {
      const wasEnabled = connectionEnabled;
      currentStatus = status;
      connectionEnabled = status.connected;
      terminal.options.disableStdin = status.read_only || !status.connected;
      if (mobileInput) mobileInput.readOnly = terminal.options.disableStdin;
      if (connectionEnabled && !wasEnabled && !socket) connect();
    },
    dispose() {
      if (disposed) return;
      disposed = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      if (readyTimer) clearTimeout(readyTimer);
      if (resizeTimer) clearTimeout(resizeTimer);
      if (inputTimer) clearTimeout(inputTimer);
      observer?.disconnect();
      options.container.classList.remove("mobile-terminal-compat");
      options.container.removeEventListener("touchstart", focusTerminal);
      options.container.removeEventListener("click", focusTerminal);
      mobileInput?.removeEventListener("input", handleMobileInput);
      mobileInput?.removeEventListener("compositionend", handleMobileCompositionEnd);
      mobileInput?.removeEventListener("keydown", handleMobileKeydown);
      for (const disposable of disposables) disposable.dispose();
      const current = socket;
      socket = null;
      if (current?.readyState === WebSocket.OPEN) {
        current.send(JSON.stringify({type: "close"}));
      }
      current?.close();
      terminal.dispose();
      options.container.replaceChildren();
    },
  };
}
