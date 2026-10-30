import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { build } from 'vite';
import { expect, it } from 'vitest';
import config from './vite.config';

it.each(['static', 'dynamic'] as const)('rejects unresolved %s imports in production output', async (kind) => {
  const root = await mkdtemp(path.join(tmpdir(), 'ocman-build-'));
  try {
    await writeFile(path.join(root, 'entry.js'), kind === 'static'
      ? 'import value from "ocman-missing-dependency"; console.log(value);'
      : 'import("ocman-missing-dependency").then(console.log);');
    await expect(build({
      configFile: false,
      root,
      logLevel: 'silent',
      plugins: config.plugins,
      // Reproduce the unresolved dependency being left external by the bundler.
      build: {
        write: false,
        rollupOptions: { input: path.join(root, 'entry.js'), external: ['ocman-missing-dependency'] },
      },
    })).rejects.toThrow(/unresolved imports in the build output:[\s\S]*ocman-missing-dependency/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

it('allows bundled static and dynamic imports and emitted assets', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'ocman-build-'));
  try {
    await writeFile(path.join(root, 'entry.js'), 'import "./entry.css"; import("./lazy.js").then(console.log);');
    await writeFile(path.join(root, 'entry.css'), 'body { color: red; }');
    await writeFile(path.join(root, 'lazy.js'), 'export default 42;');
    await expect(build({
      configFile: false,
      root,
      logLevel: 'silent',
      plugins: config.plugins,
      build: { write: false, rollupOptions: { input: path.join(root, 'entry.js') } },
    })).resolves.toBeDefined();
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
