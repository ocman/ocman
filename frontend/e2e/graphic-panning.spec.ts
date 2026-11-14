import { test, expect, MOCK_SESSION } from './fixtures';

for (const graphic of [
  { label: 'Test image', markdown: '![Test image](/api/file/panning)' },
  { label: 'Mermaid diagram', markdown: '```mermaid\ngraph TD\nA[Start] --> B[End]\n```' },
]) {
  test(`${graphic.label} can pan at any zoom and reset its view`, async ({ mockedPage: page }) => {
    const session = { ...MOCK_SESSION, status: 'idle' };
    const message = { id: 'answer', sessionId: session.id, timeCreated: 2000, data: { role: 'assistant', finish: 'stop' } };
    await page.route(new RegExp(`/api/session/${session.id}(\\?|$)`), route => route.fulfill({
      json: { session, messages: [message], parts: [{ id: 'graphic', messageId: message.id, sessionId: session.id,
        data: { type: 'text', text: graphic.markdown } }], totalMessages: 1 },
    }));
    await page.route('**/api/file/panning', route => route.fulfill({
      contentType: 'image/svg+xml',
      body: '<svg xmlns="http://www.w3.org/2000/svg" width="800" height="480"><rect width="800" height="480" fill="blue"/></svg>',
    }));
    await page.clock.install({ time: new Date('2026-11-14T08:00:00Z') });
    await page.goto(`/session/${session.id}`);
    await page.getByRole('button', { name: `Expand ${graphic.label}` }).click();
    const dialog = page.getByRole('dialog', { name: graphic.label });
    const viewport = dialog.getByLabel(`${graphic.label} viewport`);
    // The viewport's first child is the library's transformed content container.
    const position = () => viewport.evaluate(element => {
      const matrix = new DOMMatrixReadOnly(getComputedStyle(element.firstElementChild!).transform);
      return { x: matrix.m41, y: matrix.m42, scale: matrix.a };
    });
    await expect(viewport).toBeVisible();
    // Reset before the next inertia frame, then advance it to catch stale motion.
    await page.clock.pauseAt(new Date('2026-11-14T10:00:00Z'));
    const initial = await position();
    for (const zoom of ['100%', '75%', '125%']) {
      if (zoom === '75%') await dialog.getByRole('button', { name: 'Zoom out' }).click();
      if (zoom === '125%') {
        await dialog.getByRole('button', { name: 'Zoom in' }).click();
      }
      await expect(dialog.getByText(zoom, { exact: true })).toBeVisible();
      const before = await position();
      const box = (await viewport.boundingBox())!;
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
      await page.mouse.down();
      for (let step = 1; step <= 8; step++) {
        await page.clock.runFor(16);
        await page.mouse.move(box.x + box.width / 2 + step * 10, box.y + box.height / 2 + step * 7.5);
      }
      await expect.poll(async () => (await position()).x - before.x).toBeGreaterThan(60);
      await expect.poll(async () => (await position()).y - before.y).toBeGreaterThan(40);
      await page.mouse.up();
      // Releasing may add momentum, but must not snap the graphic back.
      await expect.poll(async () => (await position()).x - before.x).toBeGreaterThanOrEqual(79);
      expect((await position()).scale).toBe(before.scale);
      await expect(dialog).toBeVisible();
      await dialog.getByRole('button', { name: 'Reset view' }).click();
      await page.clock.runFor(1000);
      await expect.poll(position).toEqual(initial);
      await expect(dialog.getByText('100%', { exact: true })).toBeVisible();
    }
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
  });
}
