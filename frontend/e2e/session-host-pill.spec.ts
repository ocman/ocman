import { test, expect, MOCK_SESSION, MOCK_PROJECT } from './fixtures';

test('keeps offline ownership visible beside a long title in a constrained header', async ({ mockedPage: page }) => {
  const remote = { remoteId: 'build-host', remoteName: 'Build host', stale: true };
  const session = { ...MOCK_SESSION, ...remote, title: 'A very long session title '.repeat(30) };
  await page.setViewportSize({ width: 1024, height: 800 });
  await page.route('/api/sessions*', route => route.fulfill({ json: [session] }));
  await page.route('/api/projects*', route => route.fulfill({ json: [{ ...MOCK_PROJECT, ...remote }] }));
  await page.route(new RegExp(`/api/session/${session.id}(\\?.*)?$`), route => route.fulfill({ json: {
    session, messages: [], parts: [], totalMessages: 0, defaultAgent: '', defaultModel: '',
  } }));
  await page.goto(`/session/${session.id}`);
  const heading = page.getByTestId('header-breadcrumb');
  const pill = heading.getByLabel('Build host (offline — last known)', { exact: true });
  await expect(pill).toBeVisible();
  await expect.poll(async () => {
    const header = await heading.boundingBox();
    const badge = await pill.boundingBox();
    return !!header && !!badge && badge.x >= header.x && badge.x + badge.width <= header.x + header.width + 1;
  }).toBe(true);
});

test('shows the remote host pill in the flat sidebar, session title and composer', async ({ mockedPage: page }, testInfo) => {
  const remote = { remoteId: 'build-host', remoteName: 'Build host' };
  const session = { ...MOCK_SESSION, ...remote };
  await page.route('/api/sessions*', route => route.fulfill({ json: [session] }));
  await page.route('/api/projects*', route => route.fulfill({ json: [{ ...MOCK_PROJECT, ...remote }] }));
  await page.route(new RegExp(`/api/session/${session.id}(\\?.*)?$`), route => route.fulfill({ json: {
    session, messages: [], parts: [], totalMessages: 0, defaultAgent: '', defaultModel: '',
  } }));
  await page.goto(`/session/${session.id}`);
  const sidebar = page.getByTestId('session-sidebar');
  await expect(sidebar.getByLabel('Build host', { exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { level: 1 }).getByLabel('Build host', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Build host', { exact: true })).toHaveCount(3);
  const appearances = [];
  for (const pill of await page.getByLabel('Build host', { exact: true }).all()) {
    appearances.push(await pill.evaluate(element => {
      const badge = getComputedStyle(element);
      const label = getComputedStyle(element.querySelector('.host-badge-label')!);
      return { fontFamily: label.fontFamily, fontSize: label.fontSize, fontWeight: label.fontWeight,
        lineHeight: label.lineHeight, letterSpacing: label.letterSpacing, color: label.color,
        padding: badge.padding, borderRadius: badge.borderRadius, height: element.getBoundingClientRect().height };
    }));
    const alignment = await pill.evaluate(element => {
      const icon = element.querySelector('i')!;
      const label = element.querySelector('.host-badge-label')!;
      const iconBox = icon.getBoundingClientRect();
      const labelBox = label.getBoundingClientRect();
      return {
        glyphOffset: getComputedStyle(icon, '::before').verticalAlign,
        centerDelta: Math.abs(iconBox.y + iconBox.height / 2 - labelBox.y - labelBox.height / 2),
      };
    });
    expect(alignment.glyphOffset).toBe('0px');
    expect(alignment.centerDelta).toBeLessThanOrEqual(1);
  }
  expect(appearances[1]).toEqual(appearances[0]);
  expect(appearances[2]).toEqual(appearances[0]);
  await page.screenshot({ path: testInfo.outputPath('session-host-pills.png') });
});
