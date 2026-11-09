import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { test, expect } from '@playwright/test';

const entry = '/component-e2e/platform-connections.html';
const screenshots = path.resolve(import.meta.dirname, '../../.dev/platform-connections-review');

test.beforeEach(async ({ page }) => {
  // A component review must never accidentally contact a running backend.
  await page.route('**/api/**', route => route.abort('blockedbyclient'));
});

test('settings: two Claude accounts, OpenCode, named defaults, rename and removal', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(entry);
  await expect(page.getByRole('button', { name: 'Claude Personal', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Claude Work', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'OpenCode Work', exact: true })).toBeVisible();
  await mkdir(screenshots, { recursive: true });
  await page.screenshot({ path: path.join(screenshots, 'settings-desktop.png'), fullPage: true });
  await page.getByRole('group', { name: 'Claude Work actions' }).getByRole('button', { name: 'Set default' }).click();
  await expect(page.getByRole('status')).toHaveText('Claude Work is now the default.');
  await page.getByRole('button', { name: 'Edit Claude Work' }).click();
  await expect(page.getByRole('combobox', { name: 'Platform', exact: true })).toBeDisabled();
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill('Company Claude');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.reload();
  const row = page.getByRole('listitem').filter({ has: page.getByRole('button', { name: 'Company Claude', exact: true }) });
  await expect(row.getByText('Default', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Claude Personal', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Remove Company Claude' }).click();
  await expect(page.getByRole('dialog')).toContainText('Session history and native credentials are kept.');
  await page.getByRole('button', { name: 'Remove connection', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Company Claude', exact: true })).toHaveCount(0);
});

test('onboarding: reuse the add flow, create isolated OpenCode, and choose a project', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(`${entry}?onboarding`);
  await expect(page.getByRole('button', { name: 'Choose a project' })).toBeDisabled();
  await mkdir(screenshots, { recursive: true });
  await page.screenshot({ path: path.join(screenshots, 'onboarding-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: 'Add platform', exact: true }).click();
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill('OpenCode Work');
  await page.getByRole('combobox', { name: 'Configuration', exact: true }).selectOption('isolated');
  await page.screenshot({ path: path.join(screenshots, 'add-opencode-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: 'Check setup', exact: true }).click();
  await expect(page.getByText('Account: work@example.com')).toBeVisible();
  await page.getByRole('dialog').getByRole('button', { name: 'Add platform', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole('button', { name: 'OpenCode Work', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Choose a project' }).click();
  await expect(page.getByRole('status')).toHaveText('Choose a project directory');
});

test('incomplete setup: show sign-in, save for later, and block project launch', async ({ page }) => {
  await page.goto(`${entry}?onboarding&sign-in`);
  await page.getByRole('button', { name: 'Add platform', exact: true }).click();
  await page.getByRole('combobox', { name: 'Platform', exact: true }).selectOption('codex');
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill('Codex Work');
  await page.getByRole('combobox', { name: 'Configuration', exact: true }).selectOption('isolated');
  await page.getByRole('button', { name: 'Check setup', exact: true }).click();
  await expect(page.getByRole('status').filter({ hasText: 'Sign-in required' })).toBeVisible();
  await mkdir(screenshots, { recursive: true });
  await page.screenshot({ path: path.join(screenshots, 'sign-in-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: 'Check again' }).click();
  await page.getByRole('button', { name: 'Save for later' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.getByText('Sign-in required')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Choose a project' })).toBeDisabled();
});

test('phone: stacked rows and keyboard setup without horizontal overflow', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(entry);
  await expect(page.getByRole('button', { name: 'Add platform', exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await mkdir(screenshots, { recursive: true });
  await page.screenshot({ path: path.join(screenshots, 'settings-phone.png'), fullPage: true });
  await page.getByRole('button', { name: 'Add platform', exact: true }).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toBeFocused();
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill('OpenCode Personal');
  await page.getByRole('button', { name: 'Check setup', exact: true }).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByText('Account: work@example.com')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

test('save failures preserve the editor and never show a saved connection', async ({ page }) => {
  await page.goto(`${entry}?onboarding&save-error`);
  await page.getByRole('button', { name: 'Add platform', exact: true }).click();
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill('OpenCode Work');
  await page.getByRole('button', { name: 'Check setup', exact: true }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Add platform', exact: true }).click();
  await expect(page.getByText('Could not save platform connection. Please retry.')).toBeVisible();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByRole('button', { name: 'Back', exact: true }).click();
  await expect(page.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('OpenCode Work');
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  await page.getByRole('button', { name: 'Discard changes' }).click();
  await expect(page.getByRole('listitem')).toHaveCount(0);
});

test('disable and re-enable one named default without affecting another account', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(entry);
  await page.getByRole('button', { name: 'Disable Claude Personal', exact: true }).click();
  const personal = page.getByRole('listitem').filter({ has: page.getByRole('button', { name: 'Claude Personal', exact: true }) });
  const work = page.getByRole('listitem').filter({ has: page.getByRole('button', { name: 'Claude Work', exact: true }) });
  await expect(personal.getByText('Default (disabled)', { exact: true })).toBeVisible();
  await expect(personal.getByText('Disabled', { exact: true })).toBeVisible();
  await expect(work.getByText('Ready', { exact: true })).toBeVisible();
  await expect(work.getByText('Default', { exact: true })).toHaveCount(0);
  await page.reload();
  await expect(personal.getByRole('button', { name: 'Enable Claude Personal', exact: true })).toBeVisible();
  await mkdir(screenshots, { recursive: true });
  await page.screenshot({ path: path.join(screenshots, 'disabled-default-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: 'Enable Claude Personal', exact: true }).click();
  await expect(personal.getByText('Ready', { exact: true })).toBeVisible();
  await expect(personal.getByText('Default', { exact: true })).toBeVisible();
  await expect(work.getByText('Ready', { exact: true })).toBeVisible();
});
