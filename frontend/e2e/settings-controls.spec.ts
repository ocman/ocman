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

test('custom permission reviewer saves both API formats without revealing its key', async ({ mockedPage: page }) => {
  let settings = { format: '', endpoint: '', model: '', minSafeProbability: .99, apiKeySet: false };
  let submitted: Record<string, unknown> | undefined;
  await page.route('**/api/settings/judge-endpoint', async (route) => {
    if (route.request().method() === 'POST') {
      submitted = route.request().postDataJSON();
      const { apiKey, ...fields } = submitted as typeof settings & { apiKey?: string };
      const apiKeySet = apiKey !== undefined ? apiKey !== '' : fields.endpoint === settings.endpoint && settings.apiKeySet;
      settings = { ...fields, apiKeySet };
    }
    await route.fulfill({ json: settings });
  });
  await page.goto('/settings');
  await page.getByRole('navigation', { name: 'Settings groups' }).getByRole('button', { name: 'Auto-approve', exact: true }).click();
  await page.getByLabel('Reviewer API').selectOption('openai');
  await page.getByLabel('Endpoint URL').fill('http://127.0.0.1:8080/v1/chat/completions');
  await page.getByLabel('Model ID', { exact: true }).fill('local-reviewer');
  await page.getByLabel('API key (optional)', { exact: true }).fill('test-provider-key');
  await page.getByRole('button', { name: 'Save reviewer endpoint' }).click();
  await expect(page.getByLabel('API key (optional)', { exact: true })).toHaveValue('');
  expect(submitted).toMatchObject({ format: 'openai', model: 'local-reviewer', apiKey: 'test-provider-key' });
  await page.getByLabel('Reviewer API').selectOption('typesafe');
  await page.getByLabel('Endpoint URL').fill('http://127.0.0.1:8080/v1/systemone');
  await page.getByLabel('Model ID (optional)', { exact: true }).fill('local-decision-model');
  await page.getByLabel('Minimum safe probability', { exact: true }).fill('0.9999');
  await page.getByRole('button', { name: 'Save reviewer endpoint' }).click();
  await expect(page.getByRole('button', { name: 'Save reviewer endpoint' })).toBeEnabled();
  expect(submitted).toMatchObject({ format: 'typesafe', endpoint: 'http://127.0.0.1:8080/v1/systemone', minSafeProbability: .9999 });
  expect(submitted).not.toHaveProperty('apiKey');
  await page.screenshot({ path: test.info().outputPath('custom-reviewer-desktop.png'), fullPage: true, animations: 'disabled' });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('button', { name: 'Open navigation' })).toBeVisible();
  await page.screenshot({ path: test.info().outputPath('custom-reviewer-mobile.png'), fullPage: true, animations: 'disabled' });
});
