import { test, expect, MOCK_SESSION } from './fixtures';

test('compact tool rows keep the same rhythm within and across messages', async ({ mockedPage: page }, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
    state: { compactToolCalls: true, changesSidebarOpenTabs: [] }, version: 7,
  })));
  const groups = [['skill'], ['read', 'grep'], ['skill'], ['bash'], ['bash']];
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
    session: MOCK_SESSION,
    messages: groups.map((_, i) => ({ id: `a${i}`, sessionId: MOCK_SESSION.id, timeCreated: 2000 + i, data: {
      role: 'assistant', finish: 'tool-calls', modelID: 'test-model', providerID: 'test',
      time: { created: 2000 + i, completed: 2001 + i },
    } })),
    parts: groups.flatMap((tools, i) => tools.map((tool, j) => ({
      id: `p${i}-${j}`, messageId: `a${i}`, sessionId: MOCK_SESSION.id, data: {
        type: 'tool', tool, callID: `call${i}-${j}`,
        state: { status: 'completed', input: tool === 'bash' ? { command: `echo command-${i}`, ...(i === 3 ? { description: 'Check the frontend build' } : {}) }
          : tool === 'skill' ? { name: `skill-${i}` } : tool === 'read' ? { filePath: '/example.ts' } : { pattern: 'example' },
        output: 'Done', time: { start: 2000 + i, end: 2001 + i } },
      },
    }))),
    totalMessages: groups.length,
  } }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const viewport = page.getByTestId('conversation-viewport');
  const labels = ['Skill "skill-0"', 'Read /example.ts', 'Grep example', 'Skill "skill-2"', 'Bash: Check the frontend build', '$ echo command-4'];
  // Measure the rendered text rather than implementation-specific row classes.
  await expect(viewport.getByText('Read /example.ts', { exact: true })).toBeVisible();
  const positions = [];
  for (const label of labels) {
    positions.push((await viewport.getByText(label, { exact: true }).boundingBox())!.y);
  }
  const withinMessage = positions[2] - positions[1];
  for (let i = 1; i < positions.length; i++) {
    expect(positions[i] - positions[i - 1]).toBeCloseTo(withinMessage, 0);
  }
  const bounds = (await viewport.boundingBox())!;
  const clip = { x: bounds.x, y: positions[0] - 12, width: bounds.width, height: positions.at(-1)! - positions[0] + 40 };
  await page.screenshot({ path: testInfo.outputPath('compact-tool-spacing.png'), clip, animations: 'disabled' });
  await viewport.getByRole('button', { name: 'Bash: Check the frontend build' }).click();
  await expect(viewport.getByTestId('shell-output-block')).toBeVisible();
  const output = (await viewport.getByTestId('shell-output-block').boundingBox())!;
  await page.screenshot({ path: testInfo.outputPath('expanded-shell.png'), clip: { ...clip, height: output.y + output.height - clip.y + 48 }, animations: 'disabled' });
});
