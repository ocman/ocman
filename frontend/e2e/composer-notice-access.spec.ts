import { test, expect, MOCK_SESSION } from './fixtures';

test.use({ hasTouch: true });

for (const interaction of ['keyboard', 'touch']) {
  test(`full composer feedback is readable with ${interaction}`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width: 390, height: 720 });
    const message = 'Provider unavailable. '.repeat(12) + '\nPlease retry.\nCheck your connection.';
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route =>
      route.fulfill({ json: {
        session: { ...MOCK_SESSION, notice: { kind: 'error', message, retryAt: 0, attempt: 0 } },
        messages: [], parts: [],
      } }),
    );
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const notices = page.getByRole('region', { name: 'Conversation status' });
    const text = notices.getByTitle(`Error — ${message}`, { exact: true });
    await expect(text).toBeVisible();
    const compactHeight = (await notices.boundingBox())!.height;
    if (interaction === 'touch') await notices.tap();
    else {
      await notices.focus();
      await page.keyboard.press('Tab');
      await page.keyboard.press('Shift+Tab');
    }
    await expect(notices).toBeFocused();
    await expect(text).toHaveCSS('white-space', 'pre-wrap');
    expect((await notices.boundingBox())!.height).toBeGreaterThan(compactHeight);
    expect(await text.evaluate(el => el.scrollWidth)).toBeLessThanOrEqual(await text.evaluate(el => el.clientWidth));
    expect(await text.evaluate(el => el.scrollHeight)).toBe(await text.evaluate(el => el.clientHeight));
    if (interaction === 'touch') await page.getByTestId('conversation-viewport').tap({ position: { x: 10, y: 10 } });
    else await page.keyboard.press('Tab');
    await expect(text).toHaveCSS('white-space', 'nowrap');
  });
}
