import { test, expect, MOCK_SESSION } from './fixtures';

for (const { label, command, description, status } of [
  { label: 'multiline script', command: Array.from({ length: 20 }, (_, i) => `echo line-${i}`).join('\n'), description: 'Run script', status: 'completed' },
  { label: 'wrapped argument', command: `echo '${'long argument 🐚 '.repeat(100)}'`, description: 'Long argument', status: 'running' },
  { label: 'failed script', command: Array.from({ length: 20 }, (_, i) => `echo error-${i}`).join('\n'), description: 'Failed script', status: 'error' },
  { label: 'undescribed script', command: Array.from({ length: 20 }, (_, i) => `echo unnamed-${i}`).join('\n'), description: '', status: 'completed' },
]) {
  test(`compact Bash limits ${label} to three visual lines and expands the full command`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width: 800, height: 800 });
    await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
      state: { compactToolCalls: true, changesSidebarOpenTabs: [] }, version: 7,
    })));
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: MOCK_SESSION,
      messages: [{ id: 'a1', sessionId: MOCK_SESSION.id, timeCreated: 1000, data: {
        role: 'assistant', finish: 'stop', time: { created: 1000, completed: 4000 },
      } }],
      parts: [{ id: 'p1', messageId: 'a1', sessionId: MOCK_SESSION.id, data: {
        type: 'tool', tool: 'bash', callID: 'bash-1',
        state: { status, input: { command, description }, output: 'Result', time: { start: 1000, end: 2500 } },
      } }], totalMessages: 1,
    } }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const viewport = page.getByTestId('conversation-viewport');
    const preview = viewport.getByText(command, { exact: true });
    await expect(preview).toBeVisible();
    for (const width of [260, 500]) {
      // Exercise wrapping at both narrow and wider conversation widths.
      await preview.evaluate((el, width) => {
        el.closest('button')!.style.width = `${width}px`;
      }, width);
      const geometry = await preview.evaluate(el => {
        const style = getComputedStyle(el);
        return { height: el.getBoundingClientRect().height, lineHeight: parseFloat(style.lineHeight),
          clamp: style.webkitLineClamp, clipped: el.scrollHeight > el.clientHeight };
      });
      expect(geometry.height).toBeCloseTo(geometry.lineHeight * 3, 0);
      expect(geometry.clamp).toBe('3');
      expect(geometry.clipped).toBe(true);
    }
    const row = viewport.getByRole('button', { name: description ? new RegExp(`Bash: ${description}`) : /^\$ echo unnamed/ });
    await expect(row).toHaveAttribute('aria-expanded', 'false');
    await row.click();
    await expect(row).toHaveAttribute('aria-expanded', 'true');
    const full = viewport.getByTestId('shell-output-block');
    await expect(full).toBeVisible();
    expect(await full.textContent()).toContain(command);
    await row.click();
    await expect(full).not.toBeVisible();
    expect(await preview.textContent()).toBe(command);
  });
}
