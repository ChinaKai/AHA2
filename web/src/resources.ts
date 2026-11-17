import {icon} from "./icons.js";
import {bindHardwareResources, renderHardwareResources} from "./hardware_resources.js";
import {bindMailResources, renderMailResources} from "./mail_resources.js";
import {bindBrowserResources, renderBrowserResources} from "./browser_resources.js";
import type {MailResourcesData, MailResourceActions} from "./mail_resources.js";
import type {BrowserResourcesData, BrowserResourceActions} from "./browser_resources.js";
import type {HardwareResource} from "./types.js";

export type ResourcesTab = "hardware" | "mail" | "browser";

type Notice = (type: "error" | "notice", message: string) => void;

export interface ResourcesPageData {
  tab: ResourcesTab;
  hardware: HardwareResource[];
  mail: MailResourcesData;
  browser: BrowserResourcesData;
}

export interface ResourcesPageActions {
  reloadHardware: () => Promise<void>;
  mail: MailResourceActions;
  browser: BrowserResourceActions;
  notify: Notice;
}

function tabButton(active: ResourcesTab, id: ResourcesTab, label: string, iconName: string): string {
  return `<button type="button" class="resource-tab ${active === id ? "active" : ""}" data-resources-tab="${id}" role="tab" aria-selected="${active === id}">${icon(iconName)}${label}</button>`;
}

export function renderResourcesPage(data: ResourcesPageData): string {
  const panel = data.tab === "mail" ? renderMailResources(data.mail)
    : data.tab === "browser" ? renderBrowserResources(data.browser)
    : renderHardwareResources(data.hardware);
  return `<section class="resources-view">
    <header class="page-head"><div><h1>资源</h1><p>系统级硬件、邮件与浏览器能力集中在这里维护；Task 只选择绑定，不复制凭据。</p></div></header>
    <div class="resource-tabs" role="tablist" aria-label="资源类型">${tabButton(data.tab, "hardware", "硬件", "hardware")}${tabButton(data.tab, "mail", "邮件", "mail")}${tabButton(data.tab, "browser", "浏览器", "browser")}</div>
    <div class="resources-panel" role="tabpanel">${panel}</div>
  </section>`;
}

export function bindResourcesPage(data: ResourcesPageData, actions: ResourcesPageActions): void {
  bindHardwareResources(data.hardware, actions.reloadHardware, actions.notify);
  if (data.tab === "mail") bindMailResources(data.mail, actions.mail);
  if (data.tab === "browser") bindBrowserResources(data.browser, actions.browser);
  document.querySelectorAll<HTMLElement>("[data-resources-tab]").forEach(button => button.addEventListener("click", () => {
    const tab = (button.dataset.resourcesTab === "mail" ? "mail"
      : button.dataset.resourcesTab === "browser" ? "browser" : "hardware") as ResourcesTab;
    window.dispatchEvent(new CustomEvent("aha-resources-tab", {detail: tab}));
  }));
}
