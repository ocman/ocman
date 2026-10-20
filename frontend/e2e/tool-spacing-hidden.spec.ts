import { test, expect, MOCK_SESSION } from './fixtures';

test('hidden reasoning does not add gaps between compact tool rows', async ({ mockedPage: page }) => {
  await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
    state: { compactToolCalls: true, showReasoning: false, changesSidebarOpenTabs: [] }, version: 7,
  })));
  const groups = [['bash'], ['reasoning'], ['bash', 'read'], ['reasoning'], ['bash']];
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
    session: MOCK_SESSION,
    messages: groups.map((_, i) => ({ id: `a${i}`, sessionId: MOCK_SESSION.id, timeCreated: 2000 + i, data: {
      role: 'assistant', finish: 'tool-calls', modelID: 'test-model', providerID: 'test',
      time: { created: 2000 + i, completed: 2001 + i },
    } })),
    parts: groups.flatMap((tools, i) => tools.map((tool, j) => ({
      id: `p${i}-${j}`, messageId: `a${i}`, sessionId: MOCK_SESSION.id, data: tool === 'reasoning'
        ? { type: 'reasoning', text: 'Hidden thought' }
        : { type: 'tool', tool, callID: `call${i}-${j}`, state: {
          status: 'completed', input: tool === 'bash' ? { command: `echo command-${i}` } : { filePath: '/example.ts' }, output: 'Done',
        } },
    }))), totalMessages: groups.length,
  } }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const viewport = page.getByTestId('conversation-viewport');
  const labels = ['$ echo command-0', '$ echo command-2', 'Explored: 0 searches 1 file', '$ echo command-4'];
  await expect(viewport.getByText(labels[0], { exact: true })).toBeVisible();
  const positions = await Promise.all(labels.map(async label => (await viewport.getByText(label, { exact: true }).boundingBox())!.y));
  const withinMessage = positions[2] - positions[1];
  for (let i = 1; i < positions.length; i++) {
    expect(positions[i] - positions[i - 1]).toBeCloseTo(withinMessage, 0);
  }
});

test('shell completion notifications share the compact tool rhythm', async ({ mockedPage: page }, testInfo) => {
  await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
    state: { compactToolCalls: true, changesSidebarOpenTabs: [] }, version: 7,
  })));
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
    session: MOCK_SESSION,
    messages: ['assistant', 'user', 'user', 'assistant'].map((role, i) => ({
      id: `a${i}`, sessionId: MOCK_SESSION.id, timeCreated: 2000 + i,
      data: { role, finish: role === 'assistant' ? 'tool-calls' : undefined, time: { created: 2000 + i, completed: 2001 + i } },
    })),
    parts: [
      { id: 'p0', messageId: 'a0', sessionId: MOCK_SESSION.id, data: { type: 'tool', tool: 'read', callID: 'call0', state: {
        status: 'completed', input: { filePath: '/example.ts' }, output: 'Done',
      } } },
      ...[1, 2].map(i => ({ id: `p${i}`, messageId: `a${i}`, sessionId: MOCK_SESSION.id, data: {
        type: 'text', text: `<shell id="shell-${i}" state="completed" command="echo command-${i}">\nDone\n</shell>`,
      } })),
      ...[3, 4].map(i => ({ id: `p${i}`, messageId: 'a3', sessionId: MOCK_SESSION.id, data: { type: 'tool', tool: 'bash', callID: `call${i}`, state: {
        status: 'completed', input: { command: `echo command-${i}` }, output: 'Done',
      } } })),
    ], totalMessages: 4,
  } }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const viewport = page.getByTestId('conversation-viewport');
  const labels = ['Explored: 0 searches 1 file', '$ echo command-1', '$ echo command-2', '$ echo command-3', '$ echo command-4'];
  await expect(viewport.getByText(labels[0], { exact: true })).toBeVisible();
  const positions = await Promise.all(labels.map(async label => (await viewport.getByText(label, { exact: true }).boundingBox())!.y));
  const withinMessage = positions[4] - positions[3];
  for (let i = 1; i < positions.length; i++) {
    expect(positions[i] - positions[i - 1]).toBeCloseTo(withinMessage, 0);
  }
  const bounds = (await viewport.boundingBox())!;
  await page.screenshot({ path: testInfo.outputPath('shell-notification-spacing.png'), animations: 'disabled', clip: {
    x: bounds.x, y: positions[0] - 12, width: bounds.width, height: positions.at(-1)! - positions[0] + 40,
  } });
});
