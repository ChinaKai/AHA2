import {api} from "./api.js";
import {currentLocale, t as translate} from "./i18n.js";
import {createModelHealthState, forgetModelHealth, runModelHealthCheck} from "./model_health.js";
import type {OnboardingView} from "./types.js";

const copy: Record<string, Record<string, string>> = {
  "zh-CN": {
    "onboarding.title": "首装引导",
    "onboarding.dismiss": "收起引导",
    "onboarding.dismissFailed": "收起引导失败，请重试。",
    "onboarding.pet": "小A",
    "onboarding.sayModel": "还没配模型也可以先建工作区。",
    "onboarding.sayCLI": "模型配好了。接下来检查服务端上的 Backend CLI。",
    "onboarding.sayWorkspace": "可以让 AHA2 建一个练习目录，也可以用服务端已有的目录。",
    "onboarding.sayTask": "试着让第一个任务读一读这个目录。",
    "onboarding.sayDone": "只读权限已核对，可以打开任务开始了。",
    "onboarding.environment": "环境与模型",
    "onboarding.workspace": "第一个工作区",
    "onboarding.task": "第一个只读任务",
    "onboarding.done": "已就绪",
    "onboarding.models": "打开模型设置",
    "onboarding.model": "Provider 模型",
    "onboarding.modelMissing": "还没有 Provider 模型。到模型设置添加 Provider 和模型，再回到这里测试。",
    "onboarding.cliStatus": "服务端 Backend CLI",
    "onboarding.cliFound": "已找到",
    "onboarding.cliMissingStatus": "未找到",
    "onboarding.cliMissing": "在 AHA2 服务端安装 {backend}，让 AHA2 服务进程能找到它，然后重新检查。",
    "onboarding.test": "测试模型",
    "onboarding.testing": "正在测试模型，可能需要几十秒。",
    "onboarding.testOK": "模型测试通过。",
    "onboarding.testNetwork": "模型服务不可达。检查网络、代理和 Provider 地址后重试。",
    "onboarding.testAuth": "模型鉴权失败。到模型设置核对 API Key 和模型访问权限。",
    "onboarding.testQuota": "模型额度不足。检查 Provider 的余额或额度，或换一个模型。",
    "onboarding.testRate": "模型请求被限流。稍等片刻后重试。",
    "onboarding.testName": "Provider 未找到该模型。到模型设置核对模型名。",
    "onboarding.testFailed": "模型测试未通过。检查 Provider 与模型配置后重试。",
    "onboarding.recheck": "重新检查",
    "onboarding.workspaceName": "新手工作区",
    "onboarding.hostPath": "目录位于运行 AHA2 的服务端。用手机或远程浏览器时，请填写服务端上的绝对路径。",
    "onboarding.managed": "让 AHA2 在数据目录里创建新手工作区",
    "onboarding.existing": "使用服务端已有目录",
    "onboarding.directory": "服务端目录",
    "onboarding.createWorkspace": "创建工作区",
    "onboarding.workspaceBusy": "正在创建工作区…",
    "onboarding.workspaceFailed": "工作区创建失败。检查 AHA2 数据目录权限后重试。",
    "onboarding.directoryFailed": "目录不可用。请核对服务端绝对路径，并确认 AHA2 服务账号能访问它。",
    "onboarding.workspaceMissing": "先创建或选择一个本地工作区。",
    "onboarding.testNeeded": "先测试所选模型，测试通过后即可创建任务。",
    "onboarding.taskTitle": "任务标题",
    "onboarding.taskRequest": "任务内容",
    "onboarding.templateTitle": "了解我的工作区",
    "onboarding.templateRequest": "请查看工作区的顶层文件。如果有 README，请简要介绍其中的内容和这个目录的用途。",
    "onboarding.readOnly": "这个任务不会修改你的项目文件。AHA2 可能会写入 .aha2-context/ 和 .git/info/exclude。",
    "onboarding.createTask": "创建只读任务",
    "onboarding.taskBusy": "正在创建任务并核对权限…",
    "onboarding.taskFailed": "任务创建失败。请检查模型和工作区后重试。",
    "onboarding.permissionsFailed": "读回的任务权限与只读模板不一致。请在任务设置里检查权限。",
    "onboarding.openTask": "打开只读任务",
  },
  "en-US": {
    "onboarding.title": "Getting started",
    "onboarding.dismiss": "Hide setup checklist",
    "onboarding.dismissFailed": "Couldn't hide the checklist. Try again.",
    "onboarding.pet": "Little A",
    "onboarding.sayModel": "You can set up a workspace before adding a model.",
    "onboarding.sayCLI": "Your model is configured. Next, check the backend CLI on the server.",
    "onboarding.sayWorkspace": "AHA2 can make a practice folder, or you can use an existing server folder.",
    "onboarding.sayTask": "Try asking your first task to read this folder.",
    "onboarding.sayDone": "Read-only permissions are verified. Open the task to start it.",
    "onboarding.environment": "Environment and model",
    "onboarding.workspace": "First workspace",
    "onboarding.task": "First read-only task",
    "onboarding.done": "Ready",
    "onboarding.models": "Open model settings",
    "onboarding.model": "Provider model",
    "onboarding.modelMissing": "Add a provider and model in Model settings, then come back here to test it.",
    "onboarding.cliStatus": "Backend CLIs on the server",
    "onboarding.cliFound": "Found",
    "onboarding.cliMissingStatus": "Missing",
    "onboarding.cliMissing": "Install {backend} on the AHA2 server, make it available to the AHA2 service, then recheck.",
    "onboarding.test": "Test model",
    "onboarding.testing": "Testing the model. This can take up to about half a minute.",
    "onboarding.testOK": "Model test passed.",
    "onboarding.testNetwork": "The model endpoint is unreachable. Check the network, proxy and provider URL, then retry.",
    "onboarding.testAuth": "Authentication failed. Check the API key and model access in Model settings.",
    "onboarding.testQuota": "The model is out of quota. Check your provider balance or limits, or choose another model.",
    "onboarding.testRate": "The model was rate-limited. Wait a moment and retry.",
    "onboarding.testName": "The provider could not find this model. Check its model name in Model settings.",
    "onboarding.testFailed": "The model test did not pass. Check the provider and model settings, then retry.",
    "onboarding.recheck": "Recheck",
    "onboarding.workspaceName": "Starter Workspace",
    "onboarding.hostPath": "Enter an absolute path on the machine running AHA2. This also applies when using a phone or remote browser.",
    "onboarding.managed": "Create a starter workspace in the AHA2 data directory",
    "onboarding.existing": "Use an existing server folder",
    "onboarding.directory": "Server folder",
    "onboarding.createWorkspace": "Create workspace",
    "onboarding.workspaceBusy": "Creating the workspace…",
    "onboarding.workspaceFailed": "Could not create the workspace. Check access to the AHA2 data directory and try again.",
    "onboarding.directoryFailed": "The folder is unavailable. Check the absolute path on the server and make sure the AHA2 service account can access it.",
    "onboarding.workspaceMissing": "Create or select a local workspace first.",
    "onboarding.testNeeded": "Test the selected model before creating the task.",
    "onboarding.taskTitle": "Task title",
    "onboarding.taskRequest": "Task request",
    "onboarding.templateTitle": "Explore my workspace",
    "onboarding.templateRequest": "Look at the top-level files in this workspace. If there is a README, summarize it and tell me what this folder is for.",
    "onboarding.readOnly": "This task won't modify your project files. AHA2 may write to .aha2-context/ and .git/info/exclude.",
    "onboarding.createTask": "Create read-only task",
    "onboarding.taskBusy": "Creating the task and verifying its permissions…",
    "onboarding.taskFailed": "Could not create the task. Check the model and workspace, then retry.",
    "onboarding.permissionsFailed": "The saved task permissions differ from the read-only template. Check the task's permissions before starting it.",
    "onboarding.openTask": "Open read-only task",
  },
};

function t(key: string, values: Record<string, string> = {}): string {
  const message = copy[currentLocale()][key];
  return message ? message.replace(/\{(\w+)\}/g, (match, name) => values[name] ?? match) : translate(key);
}

function escapeHTML(value: string): string {
  return value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;").replaceAll("'", "&#39;");
}

const backendNames: Record<string, string> = {codex: "Codex", claude: "Claude Code", dsh: "DSH"};
const modelFeedback: Record<string, string> = {
  ok: "onboarding.testOK",
  checking: "onboarding.testing",
  unavailable: "onboarding.testNetwork",
  unauthorized: "onboarding.testAuth",
  quota_exhausted: "onboarding.testQuota",
  rate_limited: "onboarding.testRate",
  model_not_found: "onboarding.testName",
};

const failureMessages: Record<string, string> = {
  onboarding_directory_unavailable: "onboarding.directoryFailed",
  onboarding_workspace_unavailable: "onboarding.workspaceMissing",
  onboarding_model_unavailable: "onboarding.modelMissing",
  onboarding_permissions_mismatch: "onboarding.permissionsFailed",
};

type Operation = "workspace" | "task" | "dismiss";

interface OnboardingNavigation {
  openModels(): void;
  openTask(id: string): void;
  updateCatalog(): Promise<void>;
}

export class OnboardingChecklist {
  private view: OnboardingView | null = null;
  private host: HTMLElement | null = null;
  private card: HTMLElement | null = null;
  private load: Promise<void> | null = null;
  private generation = 0;
  private loadFailed = false;
  private manual = false;
  private suppressed = false;
  private fromModels = false;
  private existingDirectory = false;
  private path = "";
  private modelID = "";
  private workspaceID = "";
  private title: string | null = null;
  private request: string | null = null;
  private pending: Operation | null = null;
  private feedback = "";
  private health = createModelHealthState();
  private channel = typeof BroadcastChannel === "function" ? new BroadcastChannel("aha2.onboarding") : null;

  constructor(private navigation: OnboardingNavigation) {
    if (this.channel) this.channel.onmessage = ({data}) => {
      if (!this.view && !this.host) return;
      if (data === "dismiss") {
        this.suppressed = true;
        this.render();
      }
      void this.refresh();
    };
  }

  get showModelReturn(): boolean {
    return this.fromModels;
  }

  mount(host: HTMLElement | null): void {
    this.host = host;
    this.render();
  }

  open(): void {
    this.manual = true;
    this.fromModels = false;
    this.render();
    void this.refresh();
  }

  focus(): void {
    this.card?.querySelector<HTMLElement>("#onboarding-title")?.focus();
  }

  reset(): void {
    this.generation++;
    this.load = null;
    this.view = null;
    this.host = null;
    this.card = null;
    this.loadFailed = this.manual = this.suppressed = this.fromModels = this.existingDirectory = false;
    this.path = this.modelID = this.workspaceID = this.feedback = "";
    this.title = this.request = this.pending = null;
    this.health = createModelHealthState();
  }

  refresh(): Promise<void> {
    if (this.load) return this.load;
    const generation = this.generation;
    this.load = api.onboarding().then(view => {
      if (generation !== this.generation || typeof view.show_automatically !== "boolean") return;
      for (const model of this.view?.models || []) {
        if (view.models.find(next => next.id === model.id)?.updated_at !== model.updated_at) {
          forgetModelHealth(this.health, model.id);
        }
      }
      this.view = view;
      this.loadFailed = false;
      if (view.onboarding_dismissed_at) this.suppressed = true;
      if (!view.models.some(model => model.id === this.modelID)) {
        this.modelID = (view.models.find(model => view.backend_cli[model.backend]) || view.models[0])?.id || "";
      }
      if (!view.workspaces.some(workspace => workspace.id === this.workspaceID)) {
        this.workspaceID = view.task_workspace_id || view.workspaces[0]?.id || "";
      }
    }).catch(() => {
      if (generation === this.generation) this.loadFailed = true;
    }).finally(() => {
      if (generation === this.generation) {
        this.load = null;
        this.render();
      }
    });
    return this.load;
  }

  private createCard(): HTMLElement {
    const card = document.createElement("section");
    card.className = "panel onboarding-card";
    card.setAttribute("aria-labelledby", "onboarding-title");
    card.innerHTML = `<header class="onboarding-header">
      <h2 id="onboarding-title" tabindex="-1"></h2>
      <button type="button" id="onboarding-dismiss" data-onboarding-action="dismiss"></button>
    </header>
    <p class="onboarding-bubble" role="status" aria-live="polite" aria-atomic="true"></p>
    <div class="onboarding-content"></div>
    <p class="onboarding-feedback" role="status" aria-live="polite" aria-atomic="true"></p>`;
    card.addEventListener("click", event => {
      const button = (event.target as Element).closest<HTMLButtonElement>("[data-onboarding-action]");
      switch (button?.dataset.onboardingAction) {
        case "dismiss":
          void this.run("dismiss", () => api.dismissOnboarding());
          break;
        case "recheck":
          void this.refresh();
          break;
        case "models":
          this.fromModels = true;
          this.navigation.openModels();
          break;
        case "test":
          void runModelHealthCheck(this.health, this.modelID, async id => (await api.checkModelHealth(id)).health, () => this.render());
          break;
        case "task":
          if (this.view?.task_id) this.navigation.openTask(this.view.task_id);
          break;
      }
    });
    card.addEventListener("input", event => {
      const field = event.target as HTMLInputElement;
      if (field.name === "root_path") this.path = field.value;
      if (field.name === "title") this.title = field.value;
      if (field.name === "request") this.request = field.value;
    });
    card.addEventListener("change", event => {
      const field = event.target as HTMLInputElement;
      if (field.name === "workspace_mode") this.existingDirectory = field.value === "existing";
      if (field.name === "model_id") this.modelID = field.value;
      if (field.name === "workspace_id") this.workspaceID = field.value;
      if (["workspace_mode", "model_id", "workspace_id"].includes(field.name)) {
        this.feedback = "";
        this.render();
      }
    });
    card.addEventListener("submit", event => {
      event.preventDefault();
      const form = event.target as HTMLFormElement;
      if (form.id === "onboarding-workspace") {
        void this.run("workspace", () => api.createOnboardingWorkspace(t("onboarding.workspaceName"), this.existingDirectory ? this.path : undefined));
      }
      if (form.id === "onboarding-task") {
        void this.run("task", () => api.createOnboardingTask({
          workspace_id: this.workspaceID, model_id: this.modelID, title: this.title ?? t("onboarding.templateTitle"),
          request: this.request ?? t("onboarding.templateRequest"),
        }));
      }
    });
    return card;
  }

  private async run(operation: Operation, submit: () => Promise<unknown>): Promise<void> {
    if (this.pending) return;
    const generation = this.generation;
    this.pending = operation;
    this.feedback = "";
    this.render();
    try {
      await submit();
      if (generation !== this.generation) return;
      if (operation === "dismiss") {
        this.manual = false;
        this.suppressed = true;
        this.fromModels = false;
      } else {
        // Keep the result visible in the tab where the owner created it.
        if (operation === "task") this.manual = true;
        void this.navigation.updateCatalog().catch(() => {});
      }
      this.channel?.postMessage(operation);
      // A focus refresh may have read the state before this write committed.
      await this.load;
      if (generation === this.generation) await this.refresh();
    } catch (error) {
      if (generation !== this.generation) return;
      const code = (error as {code?: string}).code || "";
      const model = this.view?.models.find(option => option.id === this.modelID);
      this.feedback = error instanceof TypeError ? t("onboarding.loadFailed") : code === "onboarding_cli_missing" && model
        ? t("onboarding.cliMissing", {backend: backendNames[model.backend] || model.backend})
        : t(failureMessages[code] || ("onboarding." + operation + "Failed"));
    } finally {
      if (generation === this.generation) {
        this.pending = null;
        this.render();
        if (!this.feedback && operation === "dismiss") {
          const heading = this.host?.closest(".page")?.querySelector<HTMLElement>("h1");
          if (heading) {
            heading.tabIndex = -1;
            heading.focus();
          }
        } else if (!this.feedback && operation === "task") {
          this.card?.querySelector<HTMLElement>("#onboarding-open-task")?.focus();
        } else if (!this.feedback && operation === "workspace") {
          this.card?.querySelector<HTMLElement>("#onboarding-task-step")?.focus();
        }
      }
    }
  }

  private render(): void {
    const host = this.host;
    const view = this.view;
    if (!host || !(this.manual || (!this.suppressed && view?.show_automatically))) {
      this.card?.remove();
      return;
    }
    const card = this.card ??= this.createCard();
    const focused = card.contains(document.activeElement) ? (document.activeElement as HTMLElement).id : "";
    if (card.parentElement !== host) host.replaceChildren(card);
    const heading = card.querySelector<HTMLElement>("#onboarding-title")!;
    heading.textContent = t("onboarding.title");
    const close = card.querySelector<HTMLButtonElement>("#onboarding-dismiss")!;
    close.textContent = t("onboarding.dismiss");
    close.disabled = !!this.pending;
    const content = card.querySelector<HTMLElement>(".onboarding-content")!;
    const bubble = card.querySelector<HTMLElement>(".onboarding-bubble")!;
    const feedback = card.querySelector<HTMLElement>(".onboarding-feedback")!;
    let html = "";
    let speech = "";
    if (view) {
      const model = view.models.find(item => item.id === this.modelID);
      const cli = model ? view.backend_cli[model.backend] : false;
      const health = this.health.modelHealth[this.modelID]?.status;
      const workspaceReady = Boolean(this.workspaceID);
      speech = t("onboarding.pet") + " · " + t(view.task_id ? "onboarding.sayDone" : !model ? "onboarding.sayModel"
        : !cli ? "onboarding.sayCLI" : !workspaceReady ? "onboarding.sayWorkspace" : "onboarding.sayTask");
      html = this.steps(view, cli, health);
    } else if (this.loadFailed) {
      html = `<p>${t("onboarding.loadFailed")}</p><button type="button" data-onboarding-action="recheck">${t("common.retry")}</button>`;
    } else {
      html = `<p role="status">${t("common.loading")}</p>`;
    }
    if (content.dataset.rendered !== html) {
      content.dataset.rendered = html;
      content.innerHTML = html;
      if (focused) document.getElementById(focused)?.focus();
    }
    if (bubble.textContent !== speech) bubble.textContent = speech;
    const status = this.feedback || (this.pending === "workspace" || this.pending === "task"
      ? t("onboarding." + this.pending + "Busy") : "");
    if (feedback.textContent !== status) feedback.textContent = status;
  }

  private steps(view: OnboardingView, cli: boolean, health?: string): string {
    const model = view.models.find(item => item.id === this.modelID);
    const modelReady = cli && health === "ok";
    const disabled = this.pending ? "disabled" : "";
    const ready = (value: boolean) => value ? ` <small class="status good">${t("onboarding.done")}</small>` : "";
    const backend = model ? backendNames[model.backend] || model.backend : "";
    const missingCLI = model && !cli ? t("onboarding.cliMissing", {backend}) : "";
    const taskReason = view.task_error ? t("onboarding.permissionsFailed")
      : !this.workspaceID ? t("onboarding.workspaceMissing")
      : !model ? t("onboarding.modelMissing")
      : missingCLI || (health === "ok" ? "" : t("onboarding.testNeeded"));
    return `<ol class="onboarding-steps">
      <li>
        <h3>${t("onboarding.environment")}${ready(modelReady)}</h3>
        <div class="onboarding-cli" aria-label="${t("onboarding.cliStatus")}">
          ${Object.entries(backendNames).map(([id, name]) => `<span>${name} · ${t(view.backend_cli[id] ? "onboarding.cliFound" : "onboarding.cliMissingStatus")}</span>`).join("")}
        </div>
        ${model ? `<label for="onboarding-model">${t("onboarding.model")}</label>
          <select id="onboarding-model" name="model_id" ${disabled}>${view.models.map(item => `<option value="${escapeHTML(item.id)}" ${item.id === this.modelID ? "selected" : ""}>${escapeHTML(item.name)} · ${escapeHTML(backendNames[item.backend] || item.backend)}</option>`).join("")}</select>`
          : `<p class="field-help">${t("onboarding.modelMissing")}</p>`}
        ${missingCLI ? `<p class="field-help">${missingCLI}</p>` : ""}
        <div class="onboarding-actions">
          <button type="button" id="onboarding-model-settings" data-onboarding-action="models">${t("onboarding.models")}</button>
          <button type="button" id="onboarding-recheck" data-onboarding-action="recheck">${t("onboarding.recheck")}</button>
          ${model ? `<button type="button" id="onboarding-model-test" data-onboarding-action="test" aria-disabled="${health === "checking"}" ${disabled}>${t("onboarding.test")}</button>` : ""}
        </div>
        <p class="field-help" role="status" aria-live="polite">${health ? t(modelFeedback[health] || "onboarding.testFailed") : ""}</p>
        ${this.loadFailed ? `<p class="field-help">${t("onboarding.loadFailed")}</p>` : ""}
      </li>
      <li>
        <h3>${t("onboarding.workspace")}${ready(Boolean(this.workspaceID))}</h3>
        ${view.workspaces.length ? `<label for="onboarding-workspace-choice">${t("onboarding.workspace")}</label>
          <select id="onboarding-workspace-choice" name="workspace_id" ${disabled}>${view.workspaces.map(item => `<option value="${escapeHTML(item.id)}" ${item.id === this.workspaceID ? "selected" : ""}>${escapeHTML(item.name)} · ${escapeHTML(item.root_path)}</option>`).join("")}</select>`
          : `<form id="onboarding-workspace">
            <label class="onboarding-choice"><input id="onboarding-managed" type="radio" name="workspace_mode" value="managed" ${this.existingDirectory ? "" : "checked"} ${disabled}><span>${t("onboarding.managed")}</span></label>
            <label class="onboarding-choice"><input id="onboarding-existing" type="radio" name="workspace_mode" value="existing" ${this.existingDirectory ? "checked" : ""} ${disabled}><span>${t("onboarding.existing")}</span></label>
            ${this.existingDirectory ? `<label for="onboarding-folder">${t("onboarding.directory")}</label>
              <input id="onboarding-folder" name="root_path" value="${escapeHTML(this.path)}" required spellcheck="false" autocapitalize="off" aria-describedby="onboarding-host-path" ${disabled}>
              <p id="onboarding-host-path" class="field-help">${t("onboarding.hostPath")}</p>` : ""}
            <div class="onboarding-actions"><button id="onboarding-create-workspace" class="primary" type="submit" ${disabled}>${t("onboarding.createWorkspace")}</button></div>
          </form>`}
      </li>
      <li>
        <h3 id="onboarding-task-step" tabindex="-1">${t("onboarding.task")}${ready(Boolean(view.task_id))}</h3>
        <p class="field-help">${t("onboarding.readOnly")}</p>
        ${view.task_id ? `<button id="onboarding-open-task" type="button" data-onboarding-action="task">${t("onboarding.openTask")}</button>`
          : `<form id="onboarding-task">
            <label for="onboarding-task-title">${t("onboarding.taskTitle")}</label>
            <input id="onboarding-task-title" name="title" required value="${escapeHTML(this.title ?? t("onboarding.templateTitle"))}" ${disabled}>
            <label for="onboarding-task-request">${t("onboarding.taskRequest")}</label>
            <textarea id="onboarding-task-request" name="request" required rows="3" ${disabled}>${escapeHTML(this.request ?? t("onboarding.templateRequest"))}</textarea>
            <p id="onboarding-task-reason" class="field-help">${taskReason}</p>
            <div class="onboarding-actions"><button id="onboarding-create-task" class="primary" type="submit" aria-describedby="onboarding-task-reason" ${taskReason ? "disabled" : disabled}>${t("onboarding.createTask")}</button></div>
          </form>`}
      </li>
    </ol>`;
  }
}
