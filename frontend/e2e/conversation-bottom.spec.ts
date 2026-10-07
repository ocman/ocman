import { test, expect, MOCK_SESSION } from './fixtures';

for (const width of [1280, 390]) {
  test(`last conversation line clears the composer at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 720 });
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route =>
      route.fulfill({ json: {
        session: MOCK_SESSION,
        messages: [
          { id: 'u1', sessionId: MOCK_SESSION.id, timeCreated: 1000, data: { role: 'user' } },
          { id: 'a1', sessionId: MOCK_SESSION.id, timeCreated: 2000, data: {
            role: 'assistant', finish: 'stop', modelID: 'test-model', providerID: 'test',
            time: { created: 2000, completed: 13000 },
          } },
        ],
        parts: [
          { id: 'p1', sessionId: MOCK_SESSION.id, messageId: 'u1', data: { type: 'text', text: 'yes' } },
          { id: 'p2', sessionId: MOCK_SESSION.id, messageId: 'a1', data: {
            type: 'text', text: `${'Earlier line.\n\n'.repeat(40)}Last conversation line.`,
          } },
        ],
        totalMessages: 2,
      } }),
    );
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const viewport = page.getByTestId('conversation-viewport');
    const composer = page.getByTestId('conversation-composer');
    await expect(composer.getByRole('textbox')).toBeVisible();
    await expect(viewport.getByText('Last conversation line.', { exact: true })).toBeAttached();
    await viewport.evaluate(el => { el.scrollTop = el.scrollHeight; });
    await expect.poll(async () => {
      const footer = await viewport.locator('[data-message-id="a1"]').evaluate(el => el.getBoundingClientRect().bottom);
      const top = (await composer.boundingBox())!.y;
      return top - footer;
    }).toBeGreaterThanOrEqual(8);
  });
}
