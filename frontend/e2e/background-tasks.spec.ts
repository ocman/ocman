import { test, expect, MOCK_SESSION, mockStream, makeCapabilitiesWithPort } from './fixtures';

for (const width of [1280, 390]) {
  test(`crowded background task list stays readable at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const capabilities = makeCapabilitiesWithPort();
    await page.route('/api/capabilities', route => route.fulfill({ json: {
      ...capabilities, platforms: capabilities.platforms.map(platform => ({ ...platform,
        capabilities: { ...platform.capabilities, backgroundTaskStop: true },
      })),
    } }));
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: { ...MOCK_SESSION, status: 'busy', liveConnection: true }, messages: [], parts: [],
    } }));
    const descriptions = ['Watch PR #42 until all checks pass', 'Run frontend regression tests',
      'Review background task lifecycle changes', 'Build the documentation site',
      'S=/Users/dries/.agents/skills/forgejo-tea/scripts; for i in $(seq 1 90); do "$S/fj-runs" 12; sleep 30; done; '.repeat(5),
      ...Array.from({ length: 12 }, (_, index) => `Verify integration test group ${index + 1}`)];
    await mockStream(page, MOCK_SESSION.id, [{ type: 'background.changed', count: descriptions.length,
      tasks: descriptions.map((description, index) => ({ id: `task-${index}`, description })),
    }]);
    await page.goto(`/session/${MOCK_SESSION.id}`);
    await page.getByRole('button', { name: `${descriptions.length} background tasks running` }).click();
    const panel = page.getByRole('dialog', { name: 'Background tasks' });
    await expect(panel.getByText(`${descriptions.length} running`)).toBeVisible();
    await expect(panel.getByRole('listitem')).toHaveCount(descriptions.length);
    const box = (await panel.boundingBox())!;
    expect(box.width).toBe(width === 1280 ? 600 : width - 24);
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(width);
    expect(box.y).toBeGreaterThanOrEqual(0);
    expect(await panel.evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(await panel.evaluate(element => element.clientWidth));
    const command = panel.locator('summary');
    expect((await command.boundingBox())!.height).toBeLessThan(45);
    const screenshot = testInfo.outputPath(`background-tasks-${width}.png`);
    await page.screenshot({ path: screenshot });
    await testInfo.attach('background-tasks', { path: screenshot, contentType: 'image/png' });
    await command.click();
    await expect(panel.locator('details')).toHaveAttribute('open', '');
    expect((await command.boundingBox())!.height).toBeGreaterThan(45);
    await command.click();
    await panel.getByText('Verify integration test group 12', { exact: true }).scrollIntoViewIfNeeded();
    await expect(panel.getByRole('heading', { name: 'Background tasks' })).toBeVisible();
  });

  test(`idle session shows its PR watch count without forge metadata at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 800 });
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: { ...MOCK_SESSION, status: 'done', liveConnection: true }, messages: [], parts: [],
    } }));
    let watching = true;
    let backgroundTask = true;
    await page.route(`/api/session/${MOCK_SESSION.id}/pull-requests*`, route => {
      if (new URL(route.request().url()).searchParams.get('background') !== 'true') return route.fulfill({ status: 503 });
      return route.fulfill({ json: {
      pullRequests: [{ watching, backgroundTask, number: 42, remote: { host: 'forge.example', repo: 'team/repo' },
        pr: { title: 'Fix login' } }],
      } });
    });
    await mockStream(page, MOCK_SESSION.id, []);
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const trigger = page.getByRole('button', { name: '1 background task running' });
    await expect(trigger).toBeVisible();
    await expect(trigger).toHaveText('1');
    await expect(page.getByTestId('running-dots')).toHaveCount(0);
    await expect(page.getByRole('dialog', { name: 'Background tasks' })).toHaveCount(0);
    const bounds = (await trigger.boundingBox())!;
    expect(bounds.x).toBeGreaterThanOrEqual(0);
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(width);
    await trigger.click();
    await expect(page.getByRole('dialog', { name: 'Background tasks' }).getByText('Watching team/repo#42: Fix login')).toBeVisible();
    watching = false;
    await page.reload();
    await expect(trigger).toHaveText('1');
    await trigger.click();
    await expect(page.getByRole('dialog', { name: 'Background tasks' }).getByText('PR update awaiting delivery for team/repo#42: Fix login')).toBeVisible();
    backgroundTask = false;
    await page.reload();
    await expect(trigger).toHaveCount(0);
  });

  test(`background task lifecycle can be overridden at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 800 });
    const capabilities = makeCapabilitiesWithPort();
    await page.route('/api/capabilities', route => route.fulfill({ json: {
      ...capabilities, platforms: capabilities.platforms.map(platform => ({ ...platform,
        capabilities: { ...platform.capabilities, backgroundTaskLifecycle: true, backgroundTaskStop: true },
      })),
    } }));
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: { ...MOCK_SESSION, status: 'done', liveConnection: true }, messages: [], parts: [],
    } }));
    let lifecycle: 'task' | 'service' = 'task';
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}/background(\\?|$)`), route => route.fulfill({ json: {
      count: 1, tasks: [{ id: 'one', description: 'Development server', lifecycle }],
    } }));
    await page.route(`**/api/session/${MOCK_SESSION.id}/background/one/lifecycle*`, route => {
      expect(route.request().method()).toBe('PUT');
      expect(route.request().postDataJSON()).toEqual({ lifecycle: lifecycle === 'task' ? 'service' : 'task' });
      lifecycle = route.request().postDataJSON().lifecycle;
      return route.fulfill({ status: 204 });
    });
    await mockStream(page, MOCK_SESSION.id, [{
      type: 'background.changed', count: 1, tasks: [{ id: 'one', description: 'Development server' }],
    }]);
    await page.goto(`/session/${MOCK_SESSION.id}`);
    await page.getByRole('button', { name: '1 background task running' }).click();
    const panel = page.getByRole('dialog', { name: 'Background tasks' });
    await panel.getByRole('button', { name: 'Mark Development server as service' }).click();
    await expect(panel.getByRole('button', { name: 'Mark Development server as task' })).toBeEnabled();
    expect(await panel.evaluate(element => element.scrollWidth)).toBeLessThanOrEqual(await panel.evaluate(element => element.clientWidth));
    const screenshot = testInfo.outputPath('background-task-service.png');
    await panel.screenshot({ path: screenshot });
    await testInfo.attach('background-task-service', { path: screenshot, contentType: 'image/png' });
    await panel.getByRole('button', { name: 'Mark Development server as task' }).click();
    await expect(panel.getByRole('button', { name: 'Mark Development server as service' })).toBeEnabled();
  });

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
