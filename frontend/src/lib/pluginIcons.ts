import names from 'bootstrap-icons/font/bootstrap-icons.json';

export type PluginIconDescriptor = { bootstrap?: string; path?: string };

export function bootstrapIconName(icon?: PluginIconDescriptor): string {
  return icon?.bootstrap && !icon.path && Object.hasOwn(names, icon.bootstrap) ? icon.bootstrap : 'puzzle';
}

export function pluginIconURL(ownerId: string, pluginId: string, icon?: PluginIconDescriptor): string | undefined {
  const path = icon?.path;
  if (!path || icon.bootstrap || path.length > 256 || !path.endsWith('.svg') ||
    path.split('/').some((part) => !/^[A-Za-z0-9_.-]+$/.test(part) || part === '.' || part === '..')) return undefined;
  return `/api/plugins/icon?${new URLSearchParams({ ownerId, pluginId, path })}`;
}
