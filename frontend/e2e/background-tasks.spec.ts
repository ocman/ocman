import { test, expect, MOCK_SESSION, mockStream } from './fixtures';

for (const width of [1280, 390]) {
  test(`background agents have a count and no shell stop control at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 800 });
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: { ...MOCK_SESSION, status: 'busy', liveConnection: true }, messages: [], parts: [],
    } }));
    await mockStream(page, MOCK_SESSION.id, [{
      type: 'background.changed', count: 2, tasks: [
        { id: 'review', description: 'Review changes', kind: 'agent' },
        { id: 'tests', description: 'Run regression tests', kind: 'agent' },
      ],
    }]);
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const trigger = page.getByRole('button', { name: '2 background agents running' });
    await expect(trigger).toHaveText('2');
    await trigger.click();
    const panel = page.getByRole('dialog', { name: 'Background tasks' });
    await expect(panel.getByText('Review changes')).toBeVisible();
    await expect(panel.getByText('Run regression tests')).toBeVisible();
    await expect(panel.getByRole('button', { name: /^Stop / })).toHaveCount(0);
  });

  test(`background task stop controls fit the popover at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 800 });
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: { ...MOCK_SESSION, status: 'busy', liveConnection: true }, messages: [], parts: [],
    } }));
    await mockStream(page, MOCK_SESSION.id, [{
      type: 'background.changed', count: 1, tasks: [{ id: 'one', description: 'pnpm test '.repeat(12) }],
    }]);
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const trigger = page.getByRole('button', { name: '1 background task running' });
    await expect(trigger).toHaveText('1');
    await expect(page.locator('.oc-composer-footer-right').getByRole('button', { name: '1 background task running' })).toBeVisible();
    await expect(page.getByTestId('composer-notices').getByRole('button', { name: '1 background task running' })).toHaveCount(0);
    await trigger.click();
    const panel = page.getByRole('dialog', { name: 'Background tasks' });
    const stop = panel.getByRole('button', { name: /^Stop / });
    await expect(stop).toBeVisible();
    const panelBox = (await panel.boundingBox())!;
    const stopBox = (await stop.boundingBox())!;
    expect(stopBox.x + stopBox.width).toBeLessThanOrEqual(panelBox.x + panelBox.width - 10);
    expect(await panel.evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(await panel.evaluate(element => element.clientWidth));
  });
}
