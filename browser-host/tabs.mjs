// Playwright tab helpers kept separate from host.mjs so their failure cleanup
// can be unit-tested without launching a real browser.

export const NAVIGATION_TIMEOUT_MS = 30_000;

export async function openTabPage(context, targetURL, id, timeout = NAVIGATION_TIMEOUT_MS) {
  const page = await context.newPage();
  try {
    await page.goto(targetURL, {waitUntil: "domcontentloaded", timeout});
    return {
      id,
      page,
      url: page.url(),
      title: await page.title(),
    };
  } catch (error) {
    // A failed navigation must not leave an orphan page the Owner can see but
    // AHA has no tab record for. Close it before reporting the failure.
    await page.close().catch(() => {});
    throw error;
  }
}

export async function navigatePage(page, targetURL, timeout = NAVIGATION_TIMEOUT_MS) {
  await page.goto(targetURL, {waitUntil: "domcontentloaded", timeout});
  return {
    url: page.url(),
    title: await page.title(),
  };
}
