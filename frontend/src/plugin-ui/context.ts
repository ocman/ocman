import { createContext, useContext } from 'react';
import type { PluginContextValue } from '@ocman/plugin-ui';

export interface PluginHostValue extends PluginContextValue {
  pluginLabel: string;
  /** Opens the host action dialog and resolves when it closes. */
  runAction(actionId: string, context?: Record<string, string>): Promise<void>;
}

export const PluginHostContext = createContext<PluginHostValue | null>(null);

export function usePluginHost(): PluginHostValue {
  const value = useContext(PluginHostContext);
  if (!value) throw new Error('@ocman/plugin-ui hooks are only available inside a mounted plugin UI');
  return value;
}
