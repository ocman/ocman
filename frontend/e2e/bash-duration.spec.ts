import { test, expect, MOCK_SESSION } from './fixtures';

test('bash descriptions show running elapsed time and completed duration inline', async ({ mockedPage: page }, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  const now = Date.now();
  await page.clock.install({ time: now - 1000 });
  await page.clock.pauseAt(now);
  await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
    state: { compactToolCalls: true, changesSidebarOpenTabs: [] }, version: 7,
  })));
  const descriptions = ['Run frontend tests', 'Check formatting'];
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
    session: MOCK_SESSION,
    messages: [{ id: 'a1', sessionId: MOCK_SESSION.id, timeCreated: now - 20000, data: {
      role: 'assistant', finish: 'tool-calls', modelID: 'test-model', providerID: 'test',
      time: { created: now - 20000 },
    } }],
    parts: descriptions.map((description, i) => ({
      id: `p${i}`, messageId: 'a1', sessionId: MOCK_SESSION.id, timeCreated: now - (i ? 20000 : 12000), data: {
        type: 'tool', tool: 'bash', callID: `bash-${i}`, time: { end: i ? now - 11500 : 0 },
        state: { status: i ? 'completed' : 'running', input: { command: i ? 'pnpm lint' : 'pnpm test', description }, output: i ? 'Passed' : '' },
      },
    })), totalMessages: 1,
  } }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const viewport = page.getByTestId('conversation-viewport');
  await expect(viewport.getByRole('button', { name: 'Bash: Run frontend tests 12.0s' })).toBeVisible();
  await expect(viewport.getByRole('button', { name: 'Bash: Check formatting 8.5s' })).toBeVisible();
  await page.clock.runFor(1000);
  const running = viewport.getByRole('button', { name: 'Bash: Run frontend tests 13.0s' });
  await expect(running).toBeVisible();
  await expect(viewport.getByRole('button', { name: 'Bash: Check formatting 8.5s' })).toBeVisible();
  const row = (await running.boundingBox())!;
  const bounds = (await viewport.boundingBox())!;
  await page.screenshot({ path: testInfo.outputPath('bash-inline-timers.png'), animations: 'disabled',
    clip: { x: bounds.x, y: row.y - 16, width: bounds.width, height: 80 },
  });
});
