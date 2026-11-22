import { test, expect, MOCK_SESSION } from './fixtures';

test('the work timer matches the small, muted exploration row', async ({ mockedPage: page }) => {
  await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
    state: { collapseAgentActivity: true, compactToolCalls: true, changesSidebarOpenTabs: [] }, version: 7,
  })));
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
    session: { ...MOCK_SESSION, status: 'done' },
    messages: ['a', 'b', 'c'].map(id => ({ id, sessionId: MOCK_SESSION.id, timeCreated: 1000,
      data: { role: 'assistant', finish: 'stop', time: { created: 1000, completed: 97000 } } })),
    parts: [
      ['a', 'read', { filePath: 'README.md' }],
      ['b', 'bash', { command: 'echo one' }],
      ['c', 'bash', { command: 'echo two' }],
    ].map(([id, tool, input]) => ({ id: `p-${id}`, messageId: id, sessionId: MOCK_SESSION.id,
      data: { type: 'tool', tool, state: { status: 'completed', input, output: 'ok' } } })),
    totalMessages: 3,
  } }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const viewport = page.getByTestId('conversation-viewport');
  const timer = viewport.getByText('Worked for 1m 36s', { exact: true });
  const exploration = viewport.getByText('Explored: 1 file', { exact: true });
  await expect(exploration).not.toBeVisible();
  await timer.click();
  await expect(exploration).toBeVisible();
  const style = await exploration.evaluate(element => {
    const { color, fontSize, lineHeight } = getComputedStyle(element);
    return { color, fontSize, lineHeight };
  });
  await expect(timer).toHaveCSS('font-size', style.fontSize);
  await expect(timer).toHaveCSS('color', style.color);
  await expect(timer).toHaveCSS('line-height', style.lineHeight);
  const first = await viewport.getByRole('button', { name: /echo one/ }).boundingBox({ timeout: 5000 });
  const second = await viewport.getByRole('button', { name: /echo two/ }).boundingBox({ timeout: 5000 });
  expect(second!.y).toBeGreaterThanOrEqual(first!.y + first!.height);
});
