import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';
import { installPhoneReviewRoutes } from './phone-review-fixtures';

test.use({ viewport: { width: 390, height: 844 } });

test('browser back dismisses More before leaving the page', async ({ mockedPage: page }) => {
  await page.goto('/projects');
  await page.getByRole('navigation', { name: 'Phone navigation' }).getByRole('link', { name: 'Sessions', exact: true }).click();
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'More destinations' })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page).toHaveURL(/\/sessions$/);
  await page.goBack();
  await expect(page).toHaveURL(/\/projects$/);
});

test('closing More explicitly consumes its history entry', async ({ mockedPage: page }) => {
  await page.goto('/projects');
  await page.getByRole('navigation', { name: 'Phone navigation' }).getByRole('link', { name: 'Sessions', exact: true }).click();
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await page.getByRole('button', { name: 'Close more destinations' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.goBack();
  await expect(page).toHaveURL(/\/projects$/);
});

test('a destination selected in More does not leave an extra sheet entry', async ({ mockedPage: page }) => {
  await page.goto('/sessions');
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await page.getByRole('navigation', { name: 'More destinations' }).getByRole('link', { name: 'Settings', exact: true }).click();
  await expect(page).toHaveURL(/\/settings$/);
  await page.goBack();
  await expect(page).toHaveURL(/\/sessions$/);
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('back dismisses details on a directly opened conversation', async ({ mockedPage: page }) => {
  await installPhoneReviewRoutes(page);
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await page.getByRole('button', { name: 'Open session details' }).click();
  await expect(page.getByRole('button', { name: 'Close session details' })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole('button', { name: 'Open session details' })).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}`));
});

test('back returns from an inbox message to its list', async ({ mockedPage: page }) => {
  await installPhoneReviewRoutes(page);
  await page.goto('/inbox');
  await page.getByRole('button', { name: /Review the mobile layout/ }).click();
  await expect(page.getByRole('button', { name: 'Back to messages' })).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(/\/inbox$/);
  await expect(page.getByRole('button', { name: /Review the mobile layout/ })).toBeVisible();
});

test('back closes a conversation panel without losing the conversation or draft', async ({ mockedPage: page }) => {
  await installPhoneReviewRoutes(page);
  await page.goto('/sessions');
  await page.getByRole('link', { name: /Fix the login bug/ }).first().click();
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}`));
  await page.getByTestId('conversation-composer').getByRole('textbox').fill('Keep this draft');
  await expect(page.getByRole('button', { name: /^(Send message|Stop generation)$/ })).toHaveCSS('border-radius', '50%');
  await page.getByRole('button', { name: 'Open session list' }).click();
  await expect(page.getByRole('button', { name: 'Close session list' })).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}`));
  await expect(page.getByRole('button', { name: 'Open session list' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Close session list' })).toHaveCount(0);
  await expect(page.getByTestId('conversation-composer').getByRole('textbox')).toHaveValue('Keep this draft');
  await page.goBack();
  await expect(page).toHaveURL(/\/sessions$/);
});

test('Factory does not suppress native horizontal history gestures', async ({ mockedPage: page }) => {
  await installPhoneReviewRoutes(page);
  await page.goto('/factory/overview');
  await expect(page.getByRole('main').filter({ has: page.getByRole('navigation', { name: 'Factory', exact: true }) }).last()).toHaveCSS('overscroll-behavior-x', 'auto');
});

test('choosing another session replaces the drawer without racing a history pop', async ({ mockedPage: page }) => {
  await installPhoneReviewRoutes(page);
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await page.getByRole('button', { name: 'Open session list' }).click();
  await page.getByTestId('session-sidebar').getByRole('button', { name: /Refactor auth module/ }).click();
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION_2.id}$`));
  await expect(page.getByTestId('session-sidebar')).toBeHidden();
  await page.goBack();
  await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
  await expect(page.getByTestId('session-sidebar')).toBeHidden();
});
