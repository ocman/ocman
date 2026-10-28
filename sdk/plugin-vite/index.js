// @ocman/plugin-vite: builds an ocman plugin UI as one ESM file whose shared
// imports resolve to the host's own module instances.

/**
 * Host singletons. ocman serves each at a stable same-origin URL, so the
 * bundle needs no import map (inline import maps are blocked by ocman's CSP).
 */
export const SHARED_MODULES = Object.freeze({
  react: '/plugin-shared/react.js',
  'react/jsx-runtime': '/plugin-shared/react-jsx-runtime.js',
  'react-dom': '/plugin-shared/react-dom.js',
  'react-router-dom': '/plugin-shared/react-router-dom.js',
  '@tanstack/react-query': '/plugin-shared/tanstack-react-query.js',
  '@ocman/plugin-ui': '/plugin-shared/ocman-plugin-ui.js',
});

// Bundling a second copy of these would split React state from the host.
const SINGLETON_FAMILY = /^(react|react-dom|react-router|react-router-dom|@tanstack\/react-query|@ocman\/plugin-ui)(\/|$)/;

/** CSS emitted by the build is injected at runtime as one <style> element. */
export function styleInjection(css) {
  return `(()=>{if(typeof document!=="undefined"){const s=document.createElement("style");s.setAttribute("data-ocman-plugin-style","");s.textContent=${JSON.stringify(css)};document.head.appendChild(s)}})();\n`;
}

/**
 * Vite plugin for an ocman plugin UI. The entry module must export the
 * routed index component as its default export (or as `Index`).
 *
 * @param {{ entry?: string, outDir?: string }} [options]
 * @returns {import('vite').Plugin}
 */
export function ocmanPlugin({ entry = 'src/index.tsx', outDir = 'dist' } = {}) {
  return {
    name: 'ocman-plugin-ui',
    enforce: 'pre',
    config() {
      return {
        define: { 'process.env.NODE_ENV': JSON.stringify('production') },
        oxc: { jsx: { runtime: 'automatic', development: false } },
        build: {
          lib: { entry, formats: ['es'], fileName: () => 'index.js', cssFileName: 'index' },
          outDir,
          // Keep tracked placeholders, such as a .gitkeep for go:embed.
          emptyOutDir: false,
          cssCodeSplit: false,
          assetsInlineLimit: Number.MAX_SAFE_INTEGER,
          target: 'es2022',
          sourcemap: false,
          minify: true,
          copyPublicDir: false,
          rollupOptions: {
            external: Object.keys(SHARED_MODULES),
            output: { paths: { ...SHARED_MODULES }, inlineDynamicImports: true },
          },
        },
      };
    },
    resolveId(id) {
      if (SINGLETON_FAMILY.test(id) && !(id in SHARED_MODULES)) {
        this.error(`${id} is not shared by ocman. Import from ${Object.keys(SHARED_MODULES).join(', ')} only, so the plugin uses the host's single instance.`);
      }
      return null;
    },
    generateBundle: { order: 'post', handler(_options, bundle) {
      const files = Object.values(bundle);
      const styles = files.filter((f) => f.type === 'asset' && f.fileName.endsWith('.css'));
      const chunks = files.filter((f) => f.type === 'chunk');
      const others = files.filter((f) => f.type === 'asset' && !f.fileName.endsWith('.css'));
      if (chunks.length !== 1 || others.length) {
        this.error(`An ocman plugin UI must build to one JavaScript file; got ${files.map((f) => f.fileName).join(', ')}.`);
      }
      if (styles.length) {
        chunks[0].code = styleInjection(styles.map((s) => String(s.source)).join('\n')) + chunks[0].code;
        for (const s of styles) delete bundle[s.fileName];
      }
    } },
  };
}

export default ocmanPlugin;
