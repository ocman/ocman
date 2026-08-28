const TITLES: Record<string, string> = {
  '/': 'Home',
  '/sessions': 'Sessions',
  '/projects': 'Projects',
  '/workflows': 'Workflows',
  '/factory': 'Mission Control',
  '/settings': 'Settings',
  '/import-share': 'Fork shared conversation',
};

export function routeTitle(path: string, sessionTitle?: string): string {
  if (path === '/analytics' || path.startsWith('/analytics/')) return 'Analytics';
  if (path.startsWith('/session/')) {
    const id = decodeURIComponent(path.slice('/session/'.length).split('/')[0]);
    return sessionTitle || (id === 'new' ? 'New session' : 'Session');
  }
  if (path.startsWith('/project/')) {
    const dir = decodeURIComponent(path.slice('/project/'.length).split('/')[0]);
    const name = dir.split('/').pop() || 'Project';
    return path.endsWith('/worktrees') ? `${name} / Worktrees` : name;
  }
  return TITLES[path] || 'ocman';
}
