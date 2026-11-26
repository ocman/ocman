import { test, expect, MOCK_SESSION } from './fixtures';

for (const width of [1280, 390]) {
  test(`the Sessions load error wraps long details and offers keyboard retry at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    let failed = true;
    await page.route(/\/api\/sessions(?:\?.*)?$/, route => failed
      ? route.fulfill({ status: 400, contentType: 'text/plain', body: `Could not load sessions from /projects/${'long-directory-'.repeat(15)}. Try again after restoring the connection.` })
      : route.fulfill({ json: [MOCK_SESSION] }));
    await page.goto('/sessions');
    const alert = page.getByRole('alert', { name: 'Could not load sessions', exact: true });
    await expect(alert).toBeVisible();
    await expect(alert.getByText(/Try again after restoring the connection/)).toBeVisible();
    const retry = alert.getByRole('button', { name: 'Retry', exact: true });
    if (width <= 640) {
      const target = await retry.boundingBox();
      expect(target?.width).toBeGreaterThanOrEqual(44);
      expect(target?.height).toBeGreaterThanOrEqual(44);
    }
    const bounds = await alert.boundingBox();
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width);
    expect(await alert.evaluate(el => el.scrollWidth)).toBeLessThanOrEqual(await alert.evaluate(el => el.clientWidth));
    await retry.focus();
    await expect(retry).toBeFocused();
    failed = false;
    await retry.press('Enter');
    await expect(alert).toBeHidden();
    await expect(page.getByRole('link', { name: /Fix the login bug/ })).toBeVisible();
  });
}
