import { test, expect, MOCK_PROJECT } from './fixtures';

const context = 'remoteId=build&sort=-sessions&t=0&a=1&tag=one&tag=two';
const otherProject = { ...MOCK_PROJECT, directory: '/home/user/projects/website', remoteId: 'build' };

test('project search survives reload and Back without adding keystrokes to history', async ({ mockedPage: page }) => {
  await page.route('/api/projects*', route => route.fulfill({ json: [MOCK_PROJECT, otherProject] }));
  await page.goto(`/projects?${context}`);
  const search = page.getByRole('searchbox', { name: 'Search projects' });
  await expect(page.getByRole('link', { name: /website/ })).toBeVisible();
  const historyLength = await page.evaluate(() => history.length);
  await search.pressSequentially('myapp');
  await expect(page).toHaveURL(new RegExp(`/projects\\?${context}&q=myapp$`));
  expect(await page.evaluate(() => history.length)).toBe(historyLength);
  await page.reload();
  await expect(search).toHaveValue('myapp');
  await expect(page.getByRole('link', { name: /myapp/ })).toBeVisible();
  await expect(page.getByRole('link', { name: /website/ })).toHaveCount(0);
  await page.getByRole('link', { name: /myapp/ }).click();
  await expect(page).toHaveURL(/\/project\//);
  await page.goBack();
  await expect(search).toHaveValue('myapp');
  await expect(page.getByRole('link', { name: /website/ })).toHaveCount(0);
  await search.fill('');
  await expect(page).toHaveURL(new RegExp(`/projects\\?${context}$`));
  await expect(page.getByRole('link', { name: /website/ })).toBeVisible();
});

test('Clear filters resets search and directory together without losing URL context', async ({ mockedPage: page }) => {
  await page.route('/api/projects*', route => route.fulfill({ json: [MOCK_PROJECT, otherProject] }));
  await page.goto(`/projects?${context}&dir=%2Fmissing&q=unmatched#list`);
  await expect(page.getByRole('heading', { name: 'No matching projects' })).toBeVisible();
  await expect(page.getByTestId('getting-started-empty')).toHaveCount(0);
  await page.getByRole('button', { name: 'Clear filters' }).click();
  await expect(page).toHaveURL(new RegExp(`/projects\\?${context}#list$`));
  await expect(page.getByRole('searchbox', { name: 'Search projects' })).toHaveValue('');
  await expect(page.getByRole('link', { name: /myapp/ })).toBeVisible();
  await expect(page.getByRole('link', { name: /website/ })).toBeVisible();
});

test('an empty inventory with filters is distinct from first use', async ({ mockedPage: page }) => {
  await page.route('/api/projects*', route => route.fulfill({ json: [] }));
  await page.goto('/projects?q=unmatched');
  await expect(page.getByRole('heading', { name: 'No matching projects' })).toBeVisible();
  await expect(page.getByTestId('getting-started-empty')).toHaveCount(0);
  await page.getByRole('button', { name: 'Clear filters' }).click();
  await expect(page).toHaveURL('/projects');
  await expect(page.getByTestId('getting-started-empty')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Clear filters' })).toHaveCount(0);
});
