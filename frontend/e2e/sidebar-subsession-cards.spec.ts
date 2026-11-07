import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';

for (const link of ['parent', 'creator'] as const) {
test(`flat sidebar nests ${link}-linked full cards and compact agents with full-width selection`, async ({ mockedPage: page }) => {
  const child = { ...MOCK_SESSION_2, parentId: link === 'parent' ? MOCK_SESSION.id : undefined,
    creator: link === 'creator' ? { platform: MOCK_SESSION.platform, sessionId: MOCK_SESSION.id, createdAt: 1 } : undefined, title: 'Build login UI',
    directory: '/home/user/projects/web-app', branch: 'feature/login', sourceChild: false, status: 'done' };
  const agent = { ...child, id: 'native-agent', parentId: child.id, creator: undefined, sourceChild: true,
    title: 'Review accessibility', status: 'busy' };
  await page.route('/api/sessions*', route => route.fulfill({ json: [agent, child, MOCK_SESSION] }));
  await page.route(new RegExp(`/api/session/${child.id}(\\?|$)`), route => route.fulfill({
    json: { session: child, messages: [], parts: [], totalMessages: 0 },
  }));
  let releaseGit!: () => void;
  const gitReady = new Promise<void>((resolve) => { releaseGit = resolve; });
  await page.route('/api/git/info*', async route => {
    await gitReady;
    await route.fulfill({ json: {
      [MOCK_SESSION.directory]: { branch: 'feature/auth', ahead: 0, behind: 0, dirty: false },
      [child.directory]: { branch: 'feature/login', ahead: 0, behind: 0, dirty: false },
    } });
  });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const sidebar = page.getByTestId('session-sidebar');
  const parent = sidebar.getByRole('button', { name: /Fix the login bug/ });
  const card = sidebar.getByRole('button', { name: /Build login UI/ });
  const compact = sidebar.getByRole('button', { name: /Review accessibility/ });
  await expect(card).toBeVisible();
  await expect(card.getByText('└─')).toBeVisible();
  await expect(compact.getByText('└─')).toBeVisible();
  await expect(card.getByText('web-app')).toBeVisible();
  await expect(card.getByText('feature/login')).toBeVisible();
  // The child has a stored branch immediately; the parent must first load Git
  // metadata. Compare populated cards, not a populated card and an empty slot.
  await expect(parent.getByText('feature/auth')).toHaveCount(0);
  releaseGit();
  await expect(parent.getByText('feature/auth')).toBeVisible();
  await expect(compact.getByText('web-app')).toHaveCount(0);
  await expect(compact.getByText('feature/login')).toHaveCount(0);
  const parentBox = (await parent.boundingBox())!;
  const cardBox = (await card.boundingBox())!;
  const compactBox = (await compact.boundingBox())!;
  expect(cardBox.y).toBeGreaterThan(parentBox.y);
  expect(compactBox.y).toBeGreaterThan(cardBox.y);
  expect(cardBox.x).toBe(parentBox.x);
  expect(cardBox.width).toBe(parentBox.width);
  expect(cardBox.height).toBe(parentBox.height);
  expect(compactBox.height).toBeLessThan(cardBox.height);
  const cardBranchBox = (await card.getByText('└─').boundingBox())!;
  const agentBranchBox = (await compact.getByText('└─').boundingBox())!;
  expect(agentBranchBox.x - cardBranchBox.x).toBeGreaterThanOrEqual(28);

  // The leading nesting gutter is part of the clickable, selected row.
  await card.click({ position: { x: 5, y: cardBox.height / 2 } });
  await expect(page).toHaveURL(new RegExp(`/session/${child.id}`));
  await expect(card).toHaveAttribute('aria-selected', 'true');
  await expect(card).toHaveCSS('box-shadow', /inset/);
  await expect(card).not.toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
  const before = (await card.boundingBox())!;
  await card.hover();
  await expect(card.getByRole('button', { name: 'Archive session' })).toBeVisible();
  expect((await card.boundingBox())!.height).toBe(before.height);
});
}
