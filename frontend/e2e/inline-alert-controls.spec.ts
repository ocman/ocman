import { test, expect, MOCK_SESSION } from './fixtures';

for (const width of [1280, 390]) {
  test(`inline errors wrap long details and use shared retry styling at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    let failed = true;
    await page.route(/\/api\/sessions(?:\?.*)?$/, route => failed
      ? route.fulfill({ status: 400, contentType: 'text/plain', body: `Could not load sessions from /projects/${'long-directory-'.repeat(15)}. Try again after restoring the connection.` })
      : route.fulfill({ json: [MOCK_SESSION] }));
    await page.goto('/sessions');
    const alert = page.getByRole('alert').filter({ hasText: 'Could not load sessions' });
    await expect(alert).toBeVisible();
    const retry = alert.getByRole('button', { name: 'Retry', exact: true });
    await expect(retry).toHaveCSS('font-size', '12px');
    await expect(retry).toHaveCSS('height', '28px');
    const bounds = await alert.boundingBox();
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width);
    expect(await alert.evaluate(el => el.scrollWidth)).toBeLessThanOrEqual(await alert.evaluate(el => el.clientWidth));
    await retry.focus();
    failed = false;
    await retry.press('Enter');
    await expect(alert).toBeHidden();
    await expect(page.getByRole('link', { name: /Fix the login bug/ })).toBeVisible();
  });
}
