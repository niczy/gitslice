export async function gotoWithRetry(page, path, { attempts = 30, delayMs = 1000 } = {}) {
  let lastError;

  for (let attempt = 0; attempt < attempts; attempt += 1) {
    try {
      await page.goto(path, { waitUntil: 'domcontentloaded' });
      return;
    } catch (error) {
      lastError = error;
      if (attempt < attempts - 1) {
        await page.waitForTimeout(delayMs);
      }
    }
  }

  throw lastError;
}
