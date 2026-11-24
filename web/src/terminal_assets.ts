const stylesheets = new Map<string, Promise<void>>();
let terminalScript: Promise<void> | undefined;

function assetURL(path: string): string {
  const app = document.querySelector<HTMLScriptElement>('script[type="module"][src*="/app"]');
  return path + (app ? new URL(app.src, location.href).search : "");
}

export function loadStylesheet(path: string): Promise<void> {
  const existing = stylesheets.get(path);
  if (existing) return existing;
  const loading = new Promise<void>((resolve, reject) => {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = assetURL(path);
    link.onload = () => resolve();
    link.onerror = () => {
      link.remove();
      stylesheets.delete(path);
      reject(new Error("Unable to load terminal stylesheet"));
    };
    document.head.append(link);
  });
  stylesheets.set(path, loading);
  return loading;
}

export function loadTerminalAssets(): Promise<void> {
  if (!window.Terminal && !terminalScript) {
    terminalScript = new Promise<void>((resolve, reject) => {
      const script = document.createElement("script");
      script.src = assetURL("/vendor/xterm.js");
      script.onload = () => resolve();
      script.onerror = () => {
        script.remove();
        terminalScript = undefined;
        reject(new Error("Unable to load terminal component"));
      };
      document.head.append(script);
    });
  }
  return Promise.all([terminalScript, loadStylesheet("/vendor/xterm.css")]).then(() => {});
}
