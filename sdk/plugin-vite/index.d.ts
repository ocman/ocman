import type { Plugin } from 'vite';

/** Bare specifiers ocman provides at runtime, mapped to their host URLs. */
export declare const SHARED_MODULES: Readonly<Record<string, string>>;

export declare function styleInjection(css: string): string;

export interface OcmanPluginOptions {
  /** Module exporting the routed index component. Default `src/index.tsx`. */
  entry?: string;
  /** Output directory for `index.js`. Default `dist`. */
  outDir?: string;
}

export declare function ocmanPlugin(options?: OcmanPluginOptions): Plugin;
export default ocmanPlugin;
