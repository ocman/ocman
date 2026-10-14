import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';

test('child session keeps its height and following rows in place on hover and focus', async ({ mockedPage: page }) => {
  const child = { ...MOCK_SESSION_2, status: 'busy', parentId: MOCK_SESSION.id };
  const sibling = { ...child, id: 'sess-child-sibling', title: 'Following child' };
  await page.route('/api/sessions*', route => route.fulfill({ json: [MOCK_SESSION, child, sibling] }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const sidebar = page.getByTestId('session-sidebar');
  const row = sidebar.getByRole('button', { name: /Refactor auth module/ });
  const following = sidebar.getByRole('button', { name: /Following child/ });
  await expect(row).toBeVisible();
  await expect(following).toBeVisible();
  await page.mouse.move(1000, 0);
  const before = (await row.boundingBox())!;
  const followingBefore = (await following.boundingBox())!;
  await row.hover();
  await expect(row.getByRole('button', { name: 'Snooze conversation' })).toBeVisible();
  expect((await row.boundingBox())!.height).toBe(before.height);
  expect((await following.boundingBox())!.y).toBe(followingBefore.y);
  await page.mouse.move(1000, 0);
  await row.focus();
  await expect(row.getByRole('button', { name: 'Snooze conversation' })).toBeVisible();
  await row.getByRole('button', { name: 'Snooze conversation' }).focus();
  await expect(row.getByRole('button', { name: 'Snooze conversation' })).toBeFocused();
  expect((await row.boundingBox())!.height).toBe(before.height);
  expect((await following.boundingBox())!.y).toBe(followingBefore.y);
});
