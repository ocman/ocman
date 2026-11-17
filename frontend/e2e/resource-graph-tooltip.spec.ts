import { test, expect } from './fixtures';

test('resource graph areas show process context on hover and keyboard focus', async ({ mockedPage: page }) => {
  const process = {
    pid: 101, parentPid: 100, name: 'coding-agent', command: '/usr/bin/coding-agent run',
    cpuPercent: 50, memoryBytes: 128 * 1024 ** 2,
    sessionId: 'one', sessionTitle: 'Explain resource graphs', projectName: 'ocman',
  };
  const now = Date.now();
  const point = { timestamp: now, cpuPercent: 50, memoryBytes: process.memoryBytes, processes: [process] };
  await page.route('/api/resources?*', (route) => route.fulfill({ json: {
    ...point, history: Array.from({ length: 151 }, (_, i) => ({ ...point, timestamp: now - 300_000 + i * 2000 })),
  } }));
  await page.goto('/sessions?activity=resources');
  const cpu = page.getByRole('button', { name: 'CPU: coding-agent, PID 101' });
  await cpu.hover();
  const tooltip = page.getByRole('tooltip');
  await expect(tooltip).toContainText('PID 101 · Parent PID 100');
  await expect(tooltip).toContainText('/usr/bin/coding-agent run');
  await expect(tooltip).toContainText('Project: ocman');
  await expect(tooltip).toContainText('Session: Explain resource graphs');
  await expect(tooltip).toContainText('Latest CPU: 50.0%');
  await expect(cpu).toHaveAttribute('aria-pressed', 'false');
  await page.mouse.move(0, 0, { steps: 5 });
  await expect(tooltip).toHaveCount(0);
  const memory = page.getByRole('button', { name: 'Memory: coding-agent, PID 101' });
  await memory.focus();
  await expect(tooltip).toContainText('Latest memory: 128.0 MiB');
  await page.keyboard.press('Enter');
  await expect(memory).toHaveAttribute('aria-pressed', 'true');
  await page.keyboard.press('Escape');
  await expect(tooltip).toHaveCount(0);
});
