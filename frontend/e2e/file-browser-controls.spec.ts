import { test, expect, MOCK_SESSION } from './fixtures';

const contents: Record<string, string> = {
  'README.md': '# Shared controls\n\nConsistent fields and buttons.\n',
  'src/one.ts': 'export const enabled = true;\n',
  'src/deep/two.ts': 'export const version = 2;\n',
  'ignored.log': 'Local diagnostic log\n',
};
for (const width of [1280, 390]) {
  test(`file browsers keep their tree and content usable at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    const fileRequests: URL[] = [];
    await page.route('/api/git/files?*', route => {
      const ignored = new URL(route.request().url()).searchParams.get('ignored') === '1';
      return route.fulfill({ json: { root: MOCK_SESSION.directory, files: Object.keys(contents).filter(path => ignored || path !== 'ignored.log') } });
    });
    await page.route('/api/git/file?*', route => {
      const url = new URL(route.request().url());
      fileRequests.push(url);
      const path = url.searchParams.get('path')!;
      return route.fulfill({ json: { path, content: contents[path], size: contents[path].length } });
    });
    await page.route('/api/git/diff?*', route => route.fulfill({ json: {
      repo: MOCK_SESSION.directory, branch: 'main', ahead: 0, behind: 0, files: [
        { path: 'src/one.ts', status: 'modified', additions: 1, deletions: 1, isBinary: false, diff: 'diff --git a/src/one.ts b/src/one.ts\n--- a/src/one.ts\n+++ b/src/one.ts\n@@ -1 +1 @@\n-export const enabled = false;\n+export const enabled = true;\n' },
        { path: 'README.md', status: 'modified', additions: 1, deletions: 1, isBinary: false, diff: 'diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-# Project\n+# Shared controls\n' },
      ],
    } }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    await page.getByRole('button', { name: 'Explore files' }).click();
    const explore = page.getByRole('dialog', { name: 'Explore', exact: true });
    await explore.getByRole('treeitem', { name: 'README.md', exact: true }).click();
    await expect(explore.getByRole('treeitem', { name: 'README.md', exact: true })).toHaveCSS('border-radius', '0px');
    await expect(explore.getByTestId('file-browser-content').getByText('README.md', { exact: true })).toBeVisible();
    for (const name of ['file-browser-sidebar', 'file-browser-content']) {
      const bounds = await explore.getByTestId(name).boundingBox();
      expect(bounds!.width).toBeGreaterThan(240);
    }
    await explore.getByRole('checkbox', { name: 'Show ignored files' }).check();
    await expect(explore.getByText('Select a file to view it.', { exact: true })).toBeVisible();
    await explore.getByRole('treeitem', { name: 'ignored.log', exact: true }).click();
    await expect(explore.getByTestId('file-browser-content').getByText('ignored.log', { exact: true })).toBeVisible();
    expect(fileRequests.at(-1)!.searchParams.get('remoteId')).toBe('local');
    expect(fileRequests.at(-1)!.searchParams.get('ignored')).toBe('1');
    await explore.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(explore).toBeHidden();

    if (width === 390) await page.getByRole('button', { name: 'Open session details' }).click();
    const tab = page.getByRole('tab', { name: 'Working tree', exact: true });
    if (await tab.getAttribute('aria-selected') !== 'true') await tab.click();
    await page.getByRole('button', { name: 'Fullscreen', exact: true }).filter({ visible: true }).last().click();
    const diff = page.getByRole('dialog', { name: 'Working tree', exact: true });
    await diff.getByRole('treeitem', { name: 'README.md', exact: true }).click();
    await expect(diff.getByRole('treeitem', { name: 'README.md', exact: true })).toHaveAttribute('aria-selected', 'true');
    const selected = diff.getByRole('treeitem', { name: 'README.md', exact: true });
    await expect(selected).toHaveCSS('border-radius', '0px');
    const selection = await selected.evaluate(el => {
      const sample = document.createElement('div');
      sample.style.backgroundColor = 'color-mix(in srgb, var(--accent) 18%, var(--bg-card))';
      document.body.append(sample);
      const expectedBackground = getComputedStyle(sample).backgroundColor;
      sample.remove();
      return { background: getComputedStyle(el).backgroundColor, expectedBackground,
        color: getComputedStyle(el.querySelector('[data-item-section="content"]')!).color,
        expectedColor: getComputedStyle(el).color,
        outline: getComputedStyle(el, '::before').outlineColor };
    });
    expect(selection.background).toBe(selection.expectedBackground);
    expect(selection.color).toBe(selection.expectedColor);
    expect(selection.outline).toBe('rgba(0, 0, 0, 0)');
    await selected.press('ArrowUp');
    const keyboardRow = diff.getByRole('treeitem', { name: 'one.ts', exact: true });
    await expect(keyboardRow).toBeFocused();
    await expect(keyboardRow).toHaveCSS('outline-style', 'solid');
    await expect(keyboardRow).toHaveCSS('outline-width', '2px');
    const sidebar = await diff.getByTestId('file-browser-sidebar').boundingBox();
    const content = await diff.getByTestId('file-browser-content').boundingBox();
    expect(content!.width).toBeGreaterThan(240);
    if (width === 390) expect(content!.y).toBeGreaterThanOrEqual(sidebar!.y + sidebar!.height - 1);
    else expect(content!.x).toBeGreaterThanOrEqual(sidebar!.x + sidebar!.width - 1);
    await page.keyboard.press('Escape');
    await expect(diff).toBeHidden();
  });
}
