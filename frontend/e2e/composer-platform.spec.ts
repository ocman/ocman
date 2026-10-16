import { test, expect, MOCK_SESSION, mockSessionWithLiveConnection } from './fixtures';

for (const [platform, label, agentSelection] of [['opencode', 'OpenCode', true], ['claudecode', 'Claude Code', false]] as const) {
  test(`composer identifies ${label} and respects agent selection support`, async ({ mockedPage: page }) => {
    await page.route('/api/capabilities', (route) => route.fulfill({ json: {
      hosts: [], platforms: [{ id: platform, displayName: label, available: true,
        capabilities: { composer: true, agentCatalog: agentSelection, modelCatalog: true } }],
    } }));
    await page.route('**/api/session/*/models*', (route) => route.fulfill({ json: ['anthropic/claude-sonnet-4-6'] }));
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) => route.fulfill({
      json: {
        session: { ...mockSessionWithLiveConnection(), platform },
        messages: [], parts: [], totalMessages: 0,
        defaultAgent: 'build', defaultModel: 'anthropic/claude-sonnet-4-6',
      },
    }));
    await page.setViewportSize({ width: 1200, height: 800 });
    await page.goto(`/session/${MOCK_SESSION.id}?platform=${platform}`);
    const agent = page.getByRole('button', { name: 'build', exact: true });
    await expect(page.getByRole('textbox')).toBeVisible();
    await page.getByRole('tab', { name: 'Session changes', exact: true }).click();
    const model = page.getByRole('button', { name: 'claude-sonnet-4-6', exact: true });
    const toolbar = model.locator('..');
    const icon = toolbar.getByLabel(label, { exact: true });
    await expect(icon).toBeVisible();
    const iconBounds = await icon.boundingBox();
    if (agentSelection) {
      await expect(agent).toBeVisible();
      const agentBounds = await agent.boundingBox();
      expect(iconBounds!.x + iconBounds!.width).toBeLessThanOrEqual(agentBounds!.x);
    } else {
      await expect(agent).toHaveCount(0);
      const modelBounds = await model.boundingBox();
      expect(iconBounds!.x + iconBounds!.width).toBeLessThanOrEqual(modelBounds!.x);
    }
    await icon.hover();
    await expect(page.getByRole('tooltip')).toHaveText(`This session runs on ${label}.`);
    await page.keyboard.press('Escape');
    await expect(page.getByRole('tooltip')).not.toBeVisible();
    if (process.env.COMPOSER_SCREENSHOT_DIR) {
      await page.getByRole('textbox').locator('..').screenshot({ path: `${process.env.COMPOSER_SCREENSHOT_DIR}/composer-${platform}.png` });
    }
    await page.setViewportSize({ width: 400, height: 800 });
    await expect(icon).toBeInViewport();
    await expect(page.getByRole('button', { name: 'Send message', exact: true })).toBeInViewport();
  });
}

test('Claude running dots remain visible and animate without an agent colour', async ({ mockedPage: page }) => {
  let status = 'busy';
  await page.route('/api/capabilities', (route) => route.fulfill({ json: {
    hosts: [], platforms: [{ id: 'claudecode', displayName: 'Claude Code', available: true,
      capabilities: { composer: true, agentCatalog: false, modelCatalog: true } }],
  } }));
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) => route.fulfill({ json: {
    session: { ...mockSessionWithLiveConnection(), platform: 'claudecode', status },
    messages: [
      { id: 'u', sessionId: MOCK_SESSION.id, timeCreated: 1000, data: { role: 'user' } },
      { id: 'a', sessionId: MOCK_SESSION.id, timeCreated: 2000,
        data: { role: 'assistant', finish: 'tool-calls', time: { created: 2000, completed: 3000 } } },
    ],
    parts: [{ id: 'text', sessionId: MOCK_SESSION.id, messageId: 'a', data: { type: 'text', text: 'Working on it' } }],
    totalMessages: 2,
  } }));
  await page.goto(`/session/${MOCK_SESSION.id}?platform=claudecode`);
  await expect(page.getByText('Esc to interrupt', { exact: true })).toBeVisible();
  const metadataDot = page.getByTitle('Turn in progress', { exact: true });
  await expect(metadataDot).toBeVisible();
  await expect(metadataDot).toHaveCSS('animation-name', 'oc-turn-pulse');
  const dots = page.getByTestId('running-dots').locator('span');
  await expect(dots).toHaveCount(5);
  for (const dot of await dots.all()) {
    await expect(dot).not.toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
    await expect(dot).toHaveCSS('animation-name', 'oc-bounce');
    await expect(dot).toHaveCSS('animation-play-state', 'running');
  }
  status = 'done';
  await page.reload();
  await expect(page.getByText('Working on it', { exact: true })).toBeVisible();
  await expect(page.getByText('Esc to interrupt', { exact: true })).toHaveCount(0);
  await expect(page.getByTestId('running-dots')).toHaveCount(0);
  await expect(metadataDot).toHaveCount(0);
});
