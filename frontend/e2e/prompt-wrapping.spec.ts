import { test, expect, MOCK_SESSION } from './fixtures';

for (const width of [1280, 390]) {
  test(`long prompt words and URLs wrap at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 720 });
    const text = `Check ${'longword'.repeat(80)}\nhttps://example.com/${'path'.repeat(100)}`;
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route =>
      route.fulfill({ json: {
        session: MOCK_SESSION,
        messages: [{ id: 'u1', sessionId: MOCK_SESSION.id, timeCreated: 1000, data: { role: 'user' } }],
        parts: [{ id: 'p1', sessionId: MOCK_SESSION.id, messageId: 'u1', data: { type: 'text', text } }],
        totalMessages: 1,
      } }),
    );
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const prompt = page.getByText(text, { exact: true });
    await expect(prompt).toBeVisible();
    await expect(prompt).toHaveCSS('white-space', 'pre-wrap');
    expect(await prompt.evaluate(el => {
      const container = el.parentElement!;
      return container.scrollWidth <= container.clientWidth;
    })).toBe(true);
    const lines = await prompt.evaluate(el => {
      const range = document.createRange();
      range.selectNodeContents(el);
      return new Set(Array.from(range.getClientRects(), rect => rect.top)).size;
    });
    expect(lines).toBeGreaterThan(2);
  });
}
