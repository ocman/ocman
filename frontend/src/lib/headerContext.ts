import { createContext, useContext, useEffect } from 'react';

export interface HeaderInfo {
  /** Session id the header metadata belongs to. Used to avoid showing stale session context after route changes. */
  sessionId?: string;
  sessionTitle?: string;
  /**
   * Stable identifier of the coding-agent platform that owns the
   * currently-viewed session. The header renders a PlatformBadge
   * when this is set so users can tell OpenCode / Claude Code
   * sessions apart at a glance.
   */
  sessionPlatform?: string;
  /**
   * Display string for the session's project directory (typically
   * the output of `shortPath`). Rendered as a muted, right-aligned
   * label in the page header — fills the slot that used to hold the
   * full session-stats strip. The full path is carried in
   * `sessionProjectFull` so the rendered span can use it as a
   * `title` for hover-discovery.
   */
  sessionProject?: string;
  sessionProjectFull?: string;
  /** Host badge metadata for remote sessions. */
  sessionRemoteId?: string;
  sessionRemoteName?: string;
  sessionRemoteStale?: boolean;
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
