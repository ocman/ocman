// @vitest-environment node
import { mkdtempSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { build } from 'vite';
import { expect, it } from 'vitest';
// The preset is plain JavaScript published from sdk/plugin-vite.
import { ocmanPlugin, SHARED_MODULES } from '../sdk/plugin-vite/index.js';

const sharedEntries = readdirSync(resolve(process.cwd(), 'plugin-shared')).map((f) => `/plugin-shared/${f}`).sort();

function project(source: string) {
  const root = mkdtempSync(join(tmpdir(), 'ocman-plugin-vite-'));
  writeFileSync(join(root, 'index.tsx'), source);
  writeFileSync(join(root, 'style.css'), '.catalog { color: red; }\n');
  return root;
}

async function buildProject(root: string) {
  await build({ root, logLevel: 'silent', configFile: false, plugins: [ocmanPlugin({ entry: join(root, 'index.tsx'), outDir: join(root, 'dist') })] });
  return readdirSync(join(root, 'dist'));
}

it('maps every shared module to an entry the host build serves', () => {
  expect(Object.values(SHARED_MODULES).sort()).toEqual(sharedEntries);
});

it('builds one ESM file importing host singletons and injecting its CSS', async () => {
  const root = project(`import { useState } from 'react';
import { Routes, Route } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Page } from '@ocman/plugin-ui';
import './style.css';
export default function Index() { useQueryClient(); const [n] = useState(1); return <Page title={String(n)}><Routes><Route index element={<p>Home</p>} /></Routes></Page>; }
`);
  expect(await buildProject(root)).toEqual(['index.js']);
  const code = readFileSync(join(root, 'dist', 'index.js'), 'utf8');
  for (const url of ['/plugin-shared/react.js', '/plugin-shared/react-router-dom.js', '/plugin-shared/tanstack-react-query.js', '/plugin-shared/ocman-plugin-ui.js', '/plugin-shared/react-jsx-runtime.js']) {
    expect(code).toContain(`"${url}"`);
  }
  expect(code).toContain('data-ocman-plugin-style');
  expect(code).toContain('color:red');
  expect(code).not.toMatch(/from\s*"react"/);
});

it('refuses to bundle a second copy of a host singleton', async () => {
  const root = project(`import { createRoot } from 'react-dom/client';\nexport default function Index() { return String(createRoot); }\n`);
  await expect(buildProject(root)).rejects.toThrow(/react-dom\/client is not shared by ocman/);
});

it('inlines dynamic imports into the single bundle', async () => {
  const root = project('');
  writeFileSync(join(root, 'lazy.ts'), 'export const x = 1;\n');
  writeFileSync(join(root, 'index.tsx'), `export default function Index() { return null; }\nexport const later = () => import('./lazy');\n`);
  expect(await buildProject(root)).toEqual(['index.js']);
});

it('leaves NODE_ENV and the JSX runtime alone outside vite build, so tests keep React act()', () => {
  const plugin = ocmanPlugin();
  const config = plugin.config as (config: object, env: { command: 'build' | 'serve'; mode: string }) => Record<string, unknown>;
  const serve = config({}, { command: 'serve', mode: 'test' });
  expect(serve.define).toBeUndefined();
  expect(serve.oxc).toEqual({ jsx: { runtime: 'automatic' } });
  const production = config({}, { command: 'build', mode: 'production' });
  expect(production.define).toEqual({ 'process.env.NODE_ENV': '"production"' });
  expect(production.oxc).toEqual({ jsx: { runtime: 'automatic', development: false } });
});
