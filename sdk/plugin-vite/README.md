# @ocman/plugin-vite

Vite 8 preset that builds an ocman plugin UI as one ESM file:

```ts
import { defineConfig } from 'vite';
import { ocmanPlugin } from '@ocman/plugin-vite';

export default defineConfig({ plugins: [ocmanPlugin({ entry: 'src/index.tsx', outDir: 'dist' })] });
```

The entry's default export (or `Index`) is the plugin's routed index component.
The preset:

- keeps `react`, `react/jsx-runtime`, `react-dom`, `react-router-dom`,
  `@tanstack/react-query` and `@ocman/plugin-ui` external and rewrites them to
  ocman's `/plugin-shared/*.js` URLs, so the bundle uses the host's single
  instances without an import map;
- fails the build when code imports another subpath of those packages, such as
  `react-dom/client`;
- inlines dynamic imports and assets, and injects imported CSS as one `<style>`
  element at load time;
- leaves other files in `outDir`, such as a `.gitkeep` for `go:embed`;
- applies production settings (`NODE_ENV`, the production JSX runtime) only to
  `vite build`, so a Vitest config can reuse the preset with React Testing
  Library, which needs React's development build.

Serve the resulting `dist/index.js` with the Go SDK's `plugin.UIHandler`.
