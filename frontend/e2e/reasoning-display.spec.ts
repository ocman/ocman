import { test, expect, MOCK_SESSION } from './fixtures';

test('reasoning only expands additional content and always shows duration', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
    version: 7, state: { changesSidebarOpenTabs: [] },
  })));
  const session = { ...MOCK_SESSION, title: 'Thinking trace display', status: 'idle' };
  const messages = [
    { id: 'user', sessionId: session.id, timeCreated: 1000, data: { role: 'user' } },
    { id: 'answer', sessionId: session.id, timeCreated: 2000, data: { role: 'assistant', finish: 'stop' } },
  ];
  const parts = [
    { id: 'prompt', messageId: 'user', sessionId: session.id, data: { type: 'text', text: 'Check the thinking trace display.' } },
    { id: 'short', messageId: 'answer', sessionId: session.id, data: {
      type: 'reasoning', text: 'A short trace has nothing more to expand.', time: { start: 0, end: 7800 },
    } },
    { id: 'timer', messageId: 'answer', sessionId: session.id, data: {
      type: 'reasoning', text: 'A trailing timer stays visible.\n\n', time: { start: 0, end: 11000 },
    } },
    { id: 'long', messageId: 'answer', sessionId: session.id, data: {
      type: 'reasoning', text: 'A longer trace can still expand. This first paragraph wraps onto another line, while the duration remains aligned to the right of its first line with space to its left.\n\nAdditional reasoning appears here.\n\n', time: { start: 0, end: 65000 },
    } },
    { id: 'result', messageId: 'answer', sessionId: session.id, data: {
      type: 'text', text: 'Expand controls appear only when there is more to read. Durations remain visible.',
    } },
  ];
  await page.route(new RegExp(`/api/session/${session.id}(\\?|$)`), route => route.fulfill({
    json: { session, messages, parts, totalMessages: messages.length },
  }));
  await page.goto(`/session/${session.id}`);
  await expect(page.locator('blockquote').filter({ hasText: 'A short trace has nothing more to expand.' })).toHaveText('Thought: A short trace has nothing more to expand. · 7.8s');
  await expect(page.locator('blockquote').filter({ hasText: 'A trailing timer stays visible.' })).toHaveText('Thought: A trailing timer stays visible. · 11s');
  const summary = page.locator('summary').filter({ hasText: 'A longer trace can still expand.' });
  await expect(summary).toContainText('· 1m 5s');
  const preview = summary.locator('.oc-reasoning-preview > span:first-child');
  const duration = summary.locator('.oc-reasoning-duration');
  const textBox = (await preview.boundingBox())!;
  const timerBox = (await duration.boundingBox())!;
  const summaryBox = (await summary.boundingBox())!;
  expect(textBox.y - summaryBox.y).toBeLessThan(timerBox.height / 2);
  expect(timerBox.y).toBe(textBox.y);
  expect(timerBox.x - textBox.x - textBox.width).toBeGreaterThanOrEqual(8);
  expect(textBox.height).toBeGreaterThan(timerBox.height);
  await expect(page.getByText('Additional reasoning appears here.')).not.toBeVisible();
  await expect(page.locator('blockquote.oc-reasoning details')).toHaveCount(1);
  await page.screenshot({ path: 'test-results/reasoning-collapsed.png', animations: 'disabled' });
  await summary.click();
  await expect(page.getByText('Additional reasoning appears here.')).toBeVisible();
  await expect(summary).toContainText('· 1m 5s');
  expect((await preview.boundingBox())!.y - (await summary.boundingBox())!.y).toBeLessThan(timerBox.height / 2);
  await page.screenshot({ path: 'test-results/reasoning-expanded.png', animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  expect((await preview.boundingBox())!.y - (await summary.boundingBox())!.y).toBeLessThan(timerBox.height / 2);
  await summary.click();
  await expect(page.getByText('Additional reasoning appears here.')).not.toBeVisible();
  expect((await preview.boundingBox())!.y - (await summary.boundingBox())!.y).toBeLessThan(timerBox.height / 2);
});
