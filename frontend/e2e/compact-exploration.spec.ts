import { test, expect, MOCK_SESSION } from './fixtures';

for (const running of [false, true]) {
  test(`compact exploration groups adjacent messages ${running ? 'while running' : 'when complete'}`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
      state: { compactToolCalls: true, changesSidebarOpenTabs: [] }, version: 7,
    })));
    const tools = [
      { tool: 'grep', input: { pattern: 'Session.*json|SessionID', include: '*.go' } },
      { tool: 'read', input: { filePath: 'internal/factorymod/http_epic.go', offset: 1, limit: 110 } },
      { tool: 'grep', input: { pattern: 'type WorkEpic|type Issue struct', include: '*.go' } },
      { tool: 'read', input: { filePath: 'internal/factory/native.go', offset: 45, limit: 145 } },
      { tool: 'read', input: { filePath: 'internal/server/handlers_client_activity.go', limit: 100 } },
      { tool: 'read', input: { filePath: 'internal/server/client_activity.go', limit: 100 } },
    ];
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: MOCK_SESSION,
      messages: tools.map((_, index) => ({
        id: `a${index}`, sessionId: MOCK_SESSION.id, timeCreated: 1000 + index * 1000,
        data: { role: 'assistant', finish: 'stop', time: { created: 1000 + index * 1000, completed: 2000 + index * 1000 } },
      })),
      parts: tools.map(({ tool, input }, index) => ({
        id: `p${index}`, messageId: `a${index}`, sessionId: MOCK_SESSION.id, data: {
          type: 'tool', tool, callID: `explore-${index}`,
          state: { status: running && index === 5 ? 'running' : 'completed', input },
        },
      })), totalMessages: tools.length,
    } }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const viewport = page.getByTestId('conversation-viewport');
    const summary = viewport.getByText(`${running ? 'Exploring' : 'Explored'}: 2 searches 4 files`, { exact: true });
    await expect(summary).toBeVisible();
    const mutedColor = await summary.evaluate(element => {
      const probe = document.createElement('span');
      probe.style.color = 'var(--overlay0)';
      element.append(probe);
      const color = getComputedStyle(probe).color;
      probe.remove();
      return color;
    });
    await expect(summary).toHaveCSS('color', mutedColor);
    await expect(summary).toHaveCSS('font-weight', '400');
    await summary.hover();
    await expect(summary).toHaveCSS('color', mutedColor);
    await expect(viewport.getByText('Read internal/server/client_activity.go [limit=100]', { exact: true })).not.toBeVisible();
    await summary.click();
    await expect(viewport.getByText('Read internal/server/client_activity.go [limit=100]', { exact: true })).toBeVisible();
    await expect(viewport.getByText('└', { exact: true })).toHaveCount(6);
    await page.screenshot({ path: testInfo.outputPath('compact-exploration-expanded.png'), animations: 'disabled' });
    await summary.click();
    await expect(viewport.getByText('Read internal/factory/native.go [offset=45, limit=145]', { exact: true })).not.toBeVisible();
  });
}
