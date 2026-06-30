import { createContext, useContext } from 'react';

/**
 * When true, tool-output bodies stay collapsed while printing instead of
 * being force-expanded. Lets the shared-conversation page offer a global
 * "collapse tool outputs" toggle so the user controls what lands in the
 * PDF; individually-expanded blocks still print expanded.
 *
 * Defaults to false so the authenticated session view keeps its existing
 * "force-expand everything for print" behaviour.
 */
export const PrintCollapseContext = createContext(false);

export function usePrintCollapse(): boolean {
  return useContext(PrintCollapseContext);
}
