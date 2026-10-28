import type { ComponentType } from 'react';

/**
 * Imports a plugin UI bundle from the host origin. Its bare imports were
 * rewritten at plugin build time to /plugin-shared/*.js, the host's own
 * module instances, so no import map (blocked by CSP) is needed.
 */
export async function importPluginModule(url: string): Promise<ComponentType> {
  const module = await import(/* @vite-ignore */ url) as { default?: unknown; Index?: unknown };
  const Index = module.default ?? module.Index;
  if (typeof Index !== 'function' && (typeof Index !== 'object' || Index === null)) {
    throw new Error('The plugin UI bundle exports no default or Index component.');
  }
  return Index as ComponentType;
}
