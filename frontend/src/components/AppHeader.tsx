import type { ReactNode } from 'react';
import { useLocation } from 'react-router-dom';
import { useHeaderInfo } from '../lib/headerContext';
import { routeProjectDir, routeTitle } from '../lib/routeTitle';
import { ProjectLabel } from './ProjectLabel';
import { PlatformBadge } from './PlatformBadge';
import { HostBadge } from './HostBadge';
import './AppHeader.css';

export function AppHeader({ onOpenNav }: { onOpenNav: () => void }) {
  const location = useLocation();
  const path = location.pathname;
  const { info } = useHeaderInfo();
  const routeSessionId = path.startsWith('/session/')
    ? decodeURIComponent(path.slice('/session/'.length).split('/')[0])
    : undefined;
  const sessionInfo = routeSessionId && info.sessionId === routeSessionId ? info : {};

  let breadcrumb: ReactNode = routeTitle(path, sessionInfo.sessionTitle);
  const projectDir = routeProjectDir(path);
  if (projectDir?.split('/').pop()) {
    breadcrumb = (
      <>
        <ProjectLabel path={projectDir} />
        {path.endsWith('/worktrees') && ' / Worktrees'}
      </>
    );
  } else if (routeSessionId && sessionInfo.sessionTitle) {
    breadcrumb = (
      <>
        {sessionInfo.sessionPlatform && (
          <>
            <PlatformBadge platform={sessionInfo.sessionPlatform} />{' '}
          </>
        )}
        {sessionInfo.sessionTitle}
      </>
    );
  }

  return (
    <header className="app-header">
      <h1>
        <span id="header-navigation-slot" className="header-navigation-slot" />
        <button
          type="button"
          className="mobile-nav-toggle"
          aria-label="Open navigation"
          onClick={onOpenNav}
        >
          <img src="/favicon.svg" alt="" width={20} height={20} />
        </button>
        <span id="header-mobile-title-slot" className="header-mobile-title-slot" />
        <span className="header-breadcrumb">{breadcrumb}</span>
      </h1>
      <div className="header-right">
        {routeSessionId && sessionInfo.sessionProject && (
          <span
            className="header-project"
            title={sessionInfo.sessionProjectFull || sessionInfo.sessionProject}
          >
            <HostBadge
              remoteName={sessionInfo.sessionRemoteName}
              remoteId={sessionInfo.sessionRemoteId}
              stale={sessionInfo.sessionRemoteStale}
            />
            {sessionInfo.sessionProject}
          </span>
        )}
        {/* SessionDetail portals its per-route action buttons here. */}
        <div id="header-actions-slot" className="header-actions" />
      </div>
    </header>
  );
}
