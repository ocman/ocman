import { test, expect } from './fixtures';

test('default agent lists known agents and saves the selection', async ({ mockedPage: page }) => {
  let saved = 'build';
  await page.route('**/api/settings/default-agent', async (route) => {
    if (route.request().method() === 'POST') saved = route.request().postDataJSON().defaultAgent;
    await route.fulfill({ json: { defaultAgent: saved, agents: ['build', 'plan', 'custom-agent'] } });
  });
  await page.goto('/settings');
  await page.getByRole('navigation', { name: 'Settings groups' }).getByRole('button', { name: 'Sessions', exact: true }).click();
  const picker = page.getByRole('combobox', { name: 'Default agent' });
  await expect(picker).toHaveText('build');
  await picker.click();
  await page.getByRole('textbox', { name: 'Search agents' }).fill('custom');
  await page.getByRole('option', { name: 'custom-agent', exact: true }).click();
  await expect(picker).toHaveText('custom-agent');
  expect(saved).toBe('custom-agent');
});
