import { test, expect, MOCK_SESSION } from './fixtures';

test('explorer decodes images, fits the preview, and keeps SVG scripts inert', async ({ mockedPage: page }, testInfo) => {
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="1600" height="1000"><script>window.svgExecuted=true</script><rect width="1600" height="1000" fill="#385b87"/><circle cx="800" cy="430" r="240" fill="#f6ca76"/><text x="800" y="820" text-anchor="middle" font-family="sans-serif" font-size="80" fill="white">Explorer image preview</text></svg>`;
  const png = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=';
  await page.route('/api/git/files?*', route => route.fulfill({ json: {
    root: MOCK_SESSION.directory, files: ['preview.svg', 'pixel.png'],
  } }));
  await page.route('/api/git/file?*', route => {
    const path = new URL(route.request().url()).searchParams.get('path');
    return route.fulfill({ json: {
      path, content: path === 'pixel.png' ? png : Buffer.from(svg).toString('base64'),
      size: svg.length, binary: true, mimeType: path === 'pixel.png' ? 'image/png' : 'image/svg+xml',
    } });
  });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await page.getByRole('button', { name: 'Explore files' }).click();
  const dialog = page.getByRole('dialog', { name: 'Explore', exact: true });
  for (const name of ['pixel.png', 'preview.svg']) {
    await dialog.getByRole('treeitem', { name }).click();
    const image = dialog.getByRole('img', { name });
    await expect(image).toBeVisible();
    await expect.poll(() => image.evaluate(img => (img as HTMLImageElement).naturalWidth)).toBeGreaterThan(0);
    const bounds = await image.boundingBox();
    const modal = await dialog.boundingBox();
    expect(bounds!.width).toBeLessThan(modal!.width);
    expect(bounds!.height).toBeLessThan(modal!.height);
  }
  expect(await page.evaluate(() => 'svgExecuted' in window)).toBe(false);
  await page.screenshot({ path: testInfo.outputPath('explorer-image-preview.png') });
});
