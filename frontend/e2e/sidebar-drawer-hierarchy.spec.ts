import { test, expect, MOCK_SESSION } from './fixtures';

test.use({ factoryBeta: true });

for (const [label, origin] of [
  ['Background', { archived: true }],
  ['Routines', { routineId: 'review' }],
  ['Factory', { factoryAttemptId: 'attempt' }],
  ['Plugins', { origin: 'plugin' }],
  ['Snoozed', { snoozedUntil: 9999999999999 }],
] as const) {
  test(`${label} drawer renders parents before nested children`, async ({ mockedPage: page }, testInfo) => {
    await page.addInitScript(() => {
      const ui = JSON.parse(localStorage.getItem('ocman:ui') ?? '{"state":{},"version":7}');
      ui.state.sidebarWidth = 420;
      localStorage.setItem('ocman:ui', JSON.stringify(ui));
    });
    const now = Date.now();
    const parent = { ...MOCK_SESSION, ...origin, id: 'review-parent', title: 'Review ocman PRs', status: 'busy', timeUpdated: now - 3000 };
    const child = { ...parent, id: 'review-child', parentId: parent.id, title: 'Ponytail background task', sourceChild: true, timeUpdated: now - 1000 };
    const grandchild = { ...child, id: 'review-grandchild', parentId: undefined,
      creator: { platform: child.platform, sessionId: child.id, createdAt: 1 }, title: 'Check review findings', timeUpdated: now };
    await page.route('/api/sessions*', route => route.fulfill({ json: [grandchild, child, parent, MOCK_SESSION] }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const sidebar = page.getByTestId('session-sidebar');
    await sidebar.getByRole('button', { name: `${label} 3`, exact: true }).click();
    const drawer = sidebar.getByRole('region', { name: `${label} 3`, exact: true });
    const titles = drawer.locator('[data-session-key]');
    await expect(titles).toHaveCount(3);
    await expect(titles.nth(0)).toContainText(parent.title);
    await expect(titles.nth(1)).toContainText(child.title);
    await expect(titles.nth(2)).toContainText(grandchild.title);
    await expect(titles.nth(0).getByText('└─')).toHaveCount(0);
    const childBranch = titles.nth(1).getByText('└─');
    const grandchildBranch = titles.nth(2).getByText('└─');
    await expect(childBranch).toBeVisible();
    await expect(grandchildBranch).toBeVisible();
    expect((await grandchildBranch.boundingBox())!.x).toBeGreaterThan((await childBranch.boundingBox())!.x);
    if (label === 'Routines') await sidebar.getByRole('group', { name: 'Session drawers' })
      .screenshot({ path: testInfo.outputPath('routines-parent-child.png') });
  });
}
