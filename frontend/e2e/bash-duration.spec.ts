import { test, expect, MOCK_SESSION } from './fixtures';

test('bash descriptions show running elapsed time and completed duration inline', async ({ mockedPage: page }, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  const now = Date.now();
  // Freeze wall time without pausing query notifications or route resolution.
  await page.clock.setFixedTime(now);
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
  await expect(viewport.getByRole('button', { name: /Bash: Run frontend tests 12.0s/ })).toBeVisible();
  const completed = viewport.getByRole('button', { name: 'Bash: Check formatting 8.5s $ pnpm lint' });
  await expect(completed).toBeVisible();
  await expect(viewport.getByTestId('bash-spinner')).toBeVisible();
  await page.clock.setFixedTime(now + 1000);
  const running = viewport.getByRole('button', { name: /13.0s/ });
  await expect(running).toBeVisible();
  await expect(completed).toBeVisible();
  const command = running.getByText('pnpm test', { exact: true });
  await expect(command).toBeVisible();
  expect((await command.boundingBox())!.y).toBeGreaterThan((await running.getByText('Bash: Run frontend tests', { exact: true }).boundingBox())!.y);
  const completedPrompt = completed.getByText('$', { exact: true });
  expect(await completedPrompt.evaluate(el => getComputedStyle(el).color)).toBe(await completed.evaluate(el => getComputedStyle(el).color));
  expect(await viewport.getByTestId('bash-spinner').evaluate(el => getComputedStyle(el).color)).not.toBe(await running.evaluate(el => getComputedStyle(el).color));
  const row = (await running.boundingBox())!;
  const bounds = (await viewport.boundingBox())!;
  await page.screenshot({ path: testInfo.outputPath('bash-inline-timers.png'), animations: 'disabled',
    clip: { x: bounds.x, y: row.y - 24, width: bounds.width, height: 140 },
  });

  // Constrain the real row and lengthen the label to exercise CSS overflow.
  await running.evaluate(el => {
    el.style.width = '260px';
  });
  await running.getByText('Bash: Run frontend tests', { exact: true }).evaluate(el => {
    el.textContent = `Bash: ${'A very long description '.repeat(12)}`;
  });
  const label = running.getByText(/^Bash: A very long description/);
  expect(await label.evaluate(el => el.scrollWidth > el.clientWidth && getComputedStyle(el).textOverflow === 'ellipsis')).toBe(true);
  const timer = running.getByText('13.0s', { exact: true });
  await expect(timer).toBeVisible();
  const timerBounds = (await timer.boundingBox())!;
  const runningBounds = (await running.boundingBox())!;
  expect(timerBounds.x + timerBounds.width).toBeLessThanOrEqual(runningBounds.x + runningBounds.width);
  await page.screenshot({ path: testInfo.outputPath('bash-long-description.png'), animations: 'disabled',
    clip: { x: bounds.x, y: row.y - 24, width: bounds.width, height: 140 },
  });
});
