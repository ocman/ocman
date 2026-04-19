import { createContext, useContext, useEffect } from 'react';

export interface HeaderInfo {
  sessionTitle?: string;
  /**
   * Stable identifier of the coding-agent platform that owns the
   * currently-viewed session. The header renders a PlatformBadge
   * when this is set so users can tell OpenCode / Claude Code
   * sessions apart at a glance.
   */
  sessionPlatform?: string;
  stats?: { label: string; value: string }[];
}

export const HeaderContext = createContext<{
  info: HeaderInfo;
  setInfo: (info: HeaderInfo) => void;
}>({ info: {}, setInfo: () => {} });

export function useHeaderInfo() {
  return useContext(HeaderContext);
}

export function usePageTitle(title: string) {
  useEffect(() => {
    document.title = title ? `${title} - ocman` : 'ocman';
  }, [title]);
}
