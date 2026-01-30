export async function gotoWithRetry(page, path, { attempts = 10, delayMs = 500 } = {}) {
  let lastError;

  for (let attempt = 0; attempt < attempts; attempt += 1) {
    try {
      await page.goto(path);
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
