import { test, expect, MOCK_SESSION } from './fixtures';

for (const width of [1280, 390]) {
  test(`approval locks follow command durations at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 800 });
    await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
      state: { compactToolCalls: true, changesSidebarOpenTabs: [] }, version: 7,
    })));
    const commands = ['git status --short', 'git add AGENTS.md && git commit -m "docs: clarify agent instructions"', 'pnpm test'];
    const messages = [
      { id: 'a1', sessionId: MOCK_SESSION.id, timeCreated: 1000, data: { role: 'assistant', finish: 'stop', time: { created: 1000, completed: 20000 } } },
      ...['ai', 'user'].map((_, i) => ({ id: `ocman-notice-${i}`, sessionId: MOCK_SESSION.id, timeCreated: 6300 + i * 5000, data: { role: 'notice' } })),
    ];
    const parts = [
      ...commands.map((command, i) => ({ id: `p${i}`, messageId: 'a1', sessionId: MOCK_SESSION.id, timeCreated: 1000 + i * 5000, data: {
        type: 'tool', tool: 'bash', callID: `bash-${i}`,
        state: { status: 'completed', input: { command }, output: 'Done', time: { start: 1000 + i * 5000, end: 2500 + i * 5000 } },
      } })),
      ...['ai', 'user'].map((approvedBy, i) => ({ id: `approval-${i}`, messageId: `ocman-notice-${i}`, sessionId: MOCK_SESSION.id, data: {
        type: 'auto-approved', permission: 'bash', patterns: [commands[i + 1]], approvedBy,
        reasoning: approvedBy === 'ai' ? 'Commits the requested instruction update in this repository.' : '',
        metadata: { command: commands[i + 1] }, askedAt: 6200 + i * 5000, approvedAt: 6300 + i * 5000,
      } })),
    ];
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: { ...MOCK_SESSION, title: 'Update agent instructions' }, messages, parts, totalMessages: messages.length,
    } }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const viewport = page.getByTestId('conversation-viewport');
    const aiLock = viewport.getByRole('button', { name: 'Approved by AI', exact: true });
    const userLock = viewport.getByRole('button', { name: 'Approved by user', exact: true });
    await expect(aiLock).toBeVisible();
    await expect(userLock).toBeVisible();
    await expect(viewport.getByText(/Permission approved by/)).toHaveCount(0);
    await page.evaluate(() => document.fonts.ready);
    for (const lock of [aiLock, userLock]) {
      const geometry = await lock.evaluate(el => {
        const duration = el.previousElementSibling!.querySelector('.oc-tool-duration')!;
        const time = duration.getBoundingClientRect();
        const icon = el.querySelector('i')!.getBoundingClientRect();
        return { gap: icon.left - time.right, topDelta: Math.abs(icon.top - time.top) };
      });
      expect(geometry.gap).toBeGreaterThanOrEqual(0);
      expect(geometry.gap).toBeLessThan(14);
      expect(geometry.topDelta).toBeLessThan(5);
    }
    const viewportBox = (await viewport.boundingBox())!;
    const firstRow = (await viewport.getByRole('button', { name: /git status --short/ }).boundingBox())!;
    const lastLock = (await userLock.boundingBox())!;
    const clip = { x: Math.max(0, viewportBox.x - 8), y: Math.max(0, firstRow.y - 16),
      width: width - Math.max(0, viewportBox.x - 8), height: lastLock.y + lastLock.height - firstRow.y + 40 };
    await page.screenshot({ path: testInfo.outputPath('inline-approval-locks.png'), animations: 'disabled', clip });
    await aiLock.click();
    const popover = page.getByRole('dialog', { name: 'Permission approval details' });
    await expect(popover).toContainText('Approved by AI');
    await expect(popover).toContainText('Commits the requested instruction update');
    const box = (await popover.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(width);
    await page.screenshot({ path: testInfo.outputPath('inline-approval-popover.png'), animations: 'disabled',
      clip: { ...clip, height: Math.max(clip.height, box.y + box.height - clip.y + 16) } });
    await page.keyboard.press('Escape');
    await expect(popover).not.toBeVisible();
    await expect(aiLock).toBeFocused();
    await expect(viewport.getByTestId('shell-output-block')).toHaveCount(0);
    await userLock.focus();
    await page.keyboard.press('Enter');
    await expect(popover).toContainText('Approved by user');
  });
}
