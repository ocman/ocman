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
