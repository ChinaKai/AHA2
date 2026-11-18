// Cookie validation and shape conversion for the AHA2 browser host.
//
// AHA and the host both validate cookies. This module is the host-side defense:
// it rejects malformed domain/path/sameSite values before Playwright sees them
// and strips Playwright's shape back to the small AHA contract. Cookie values
// are never logged.

export const MAX_COOKIES = 300;
export const MAX_COOKIE_NAME_BYTES = 256;
export const MAX_COOKIE_VALUE_BYTES = 4096;
export const MAX_COOKIE_PATH_BYTES = 1024;
export const MAX_COOKIE_DOMAIN_BYTES = 253;

function invalid(message) {
  return Object.assign(new Error(message), {code: "browser_cookie_invalid"});
}

function hasControl(value) {
  return /[\u0000-\u001f\u007f]/.test(value);
}

export function normalizeCookieDomain(raw) {
  const domain = String(raw || "").trim().replace(/^\./, "").toLowerCase();
  if (!domain || domain.length > MAX_COOKIE_DOMAIN_BYTES) throw invalid("invalid cookie domain");
  if (/[\s/\\:@]/.test(domain) || hasControl(domain)) throw invalid("invalid cookie domain");
  for (const label of domain.split(".")) {
    if (!label || label.length > 63 || label.startsWith("-") || label.endsWith("-")) {
      throw invalid("invalid cookie domain");
    }
    if (!/^[a-z0-9-]+$/.test(label)) throw invalid("invalid cookie domain");
  }
  return domain;
}

export function normalizeCookie(input, nowMs = Date.now()) {
  const cookie = input || {};
  const name = String(cookie.name || "");
  if (!name || Buffer.byteLength(name, "utf8") > MAX_COOKIE_NAME_BYTES ||
      !/^[A-Za-z0-9!#$%&'*+\-.^_`|~]+$/.test(name)) {
    throw invalid("invalid cookie name");
  }
  const value = String(cookie.value === undefined || cookie.value === null ? "" : cookie.value);
  if (Buffer.byteLength(value, "utf8") > MAX_COOKIE_VALUE_BYTES || /[;\u0000-\u001f\u007f]/.test(value)) {
    throw invalid("invalid cookie value");
  }
  const domain = normalizeCookieDomain(cookie.domain);
  let path = String(cookie.path || "/");
  if (!path.startsWith("/") || Buffer.byteLength(path, "utf8") > MAX_COOKIE_PATH_BYTES || hasControl(path)) {
    throw invalid("invalid cookie path");
  }
  let expires = Number.isInteger(cookie.expires) ? cookie.expires : -1;
  if (expires === 0) expires = -1;
  if (expires !== -1) {
    const now = Math.floor(nowMs / 1000);
    if (expires <= now || expires > now + 5 * 365 * 24 * 60 * 60) throw invalid("invalid cookie expiry");
  }
  let sameSite = String(cookie.same_site || cookie.sameSite || "").trim();
  if (sameSite) {
    const normalized = sameSite.toLowerCase();
    if (normalized === "strict") sameSite = "Strict";
    else if (normalized === "lax") sameSite = "Lax";
    else if (normalized === "none") sameSite = "None";
    else throw invalid("invalid cookie sameSite");
    if (sameSite === "None" && cookie.secure !== true) throw invalid("SameSite=None requires Secure");
  } else {
    sameSite = "";
  }
  const secure = cookie.secure === true;
  if (sameSite === "None" && !secure) throw invalid("SameSite=None requires Secure");
  return {
    name, value, domain, path, expires,
    http_only: cookie.http_only === true || cookie.httpOnly === true,
    secure,
    same_site: sameSite,
  };
}

// Playwright returns httpOnly/sameSite; AHA stores http_only/same_site.
export function exportCookie(cookie) {
  return {
    name: String(cookie.name || ""),
    value: String(cookie.value || ""),
    domain: String(cookie.domain || ""),
    path: String(cookie.path || "/"),
    expires: Number.isInteger(cookie.expires) ? cookie.expires : -1,
    http_only: cookie.httpOnly === true,
    secure: cookie.secure === true,
    same_site: String(cookie.sameSite || ""),
  };
}

// A cookie applies to the filter when its domain is the filter itself or a
// parent domain of it (RFC 6265 domain matching).
export function cookieDomainMatches(cookieDomain, filter) {
  const domain = String(cookieDomain || "").replace(/^\./, "").toLowerCase();
  const target = String(filter || "").replace(/^\./, "").toLowerCase();
  if (!domain || !target) return false;
  return target === domain || target.endsWith(`.${domain}`);
}

export function toPlaywrightCookie(cookie) {
  return {
    name: cookie.name,
    value: cookie.value,
    domain: cookie.domain,
    path: cookie.path,
    expires: cookie.expires,
    httpOnly: cookie.http_only === true,
    secure: cookie.secure === true,
    ...(cookie.same_site ? {sameSite: cookie.same_site} : {}),
  };
}
