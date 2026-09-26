import { shortPath } from './format';

const TITLES: Record<string, string> = {
  '/': 'Home',
  '/sessions': 'Sessions',
  '/inbox': 'Inbox',
  '/subscription-usage': 'Usage',
  '/projects': 'Projects',
  '/routines': 'Routines',
  '/factory/epics': 'Factory',
  '/factory/overview': 'Factory',
	'/factory/how-to': 'Factory how to',
  '/factory/issues': 'Factory',
	'/factory/queue': 'Factory queue',
	'/factory/configuration': 'Factory configuration',
  '/settings': 'Settings',
  '/import-share': 'Fork shared conversation',
};

export function routeTitle(path: string, sessionTitle?: string): string {
  if (path === '/analytics' || path.startsWith('/analytics/')) return 'Analytics';
  if (path.startsWith('/factory/')) return TITLES[path] || 'Factory';
  if (path.startsWith('/session/')) {
    const id = decodeURIComponent(path.slice('/session/'.length).split('/')[0]);
    return sessionTitle || (id === 'new' ? 'New session' : 'Session');
  }
  const dir = routeProjectDir(path);
  if (dir !== undefined) {
    const name = dir.split('/').pop() ? shortPath(dir) : 'Project';
    return path.endsWith('/worktrees') ? `${name} / Worktrees` : name;
  }
  return TITLES[path] || 'ocman';
}

/** Project directory of a `/project/<dir>[/...]` route, else undefined. */
export function routeProjectDir(path: string): string | undefined {
  if (!path.startsWith('/project/')) return undefined;
  return decodeURIComponent(path.slice('/project/'.length).split('/')[0]);
}
