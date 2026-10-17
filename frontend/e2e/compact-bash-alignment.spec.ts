import { test, expect, MOCK_SESSION } from './fixtures';

for (const described of [false, true]) {
  test(`compact bash aligns multiline and wrapped text ${described ? 'with' : 'without'} a description`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width: 900, height: 800 });
    await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
      state: { compactToolCalls: true, changesSidebarOpenTabs: [] }, version: 7,
    })));
    const commands = ['echo first\necho second\n  echo indented', `echo ${'long-command-argument '.repeat(35)}`.trim()];
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: MOCK_SESSION,
      messages: [{ id: 'assistant', sessionId: MOCK_SESSION.id, timeCreated: 2000, data: {
        role: 'assistant', finish: 'tool-calls', time: { created: 2000, completed: 2001 },
      } }],
      parts: commands.map((command, i) => ({
        id: `bash-${i}`, messageId: 'assistant', sessionId: MOCK_SESSION.id, data: {
          type: 'tool', tool: 'bash', callID: `call-${i}`, state: {
            status: 'running', input: { command, ...(described ? { description: 'Run script' } : {}) },
            time: { start: 2000 },
          },
        },
      })),
      totalMessages: 1,
    } }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const viewport = page.getByTestId('conversation-viewport');
    await expect(viewport.getByTestId('bash-spinner')).toHaveCount(2);
    for (const [i, command] of commands.entries()) {
      const preview = viewport.getByText(command, { exact: true });
      await expect(preview).toBeVisible();
      const lines = await preview.evaluate(element => {
        const text = element.firstChild!;
        const range = document.createRange();
        const rows: { x: number; y: number }[] = [];
        for (let offset = 0; offset < text.textContent!.length; offset++) {
          if (/\s/.test(text.textContent![offset])) continue;
          range.setStart(text, offset);
          range.setEnd(text, offset + 1);
          const rect = range.getBoundingClientRect();
          if (!rows.some(row => row.y === rect.y)) rows.push({ x: rect.x, y: rect.y });
        }
        return rows;
      });
      expect(lines.length).toBeGreaterThanOrEqual(3);
      expect(lines[1].x).toBeCloseTo(lines[0].x, 0);
      if (i === 0) expect(lines[2].x).toBeGreaterThan(lines[0].x);
      else expect(lines[2].x).toBeCloseTo(lines[0].x, 0);
    }
  });
}
