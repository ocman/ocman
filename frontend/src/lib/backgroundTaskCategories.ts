import type { groupBackgroundTasks } from './backgroundTaskGroups';

type TaskGroup = ReturnType<typeof groupBackgroundTasks>[number];

const categories = [
  { label: 'Housekeeping', jobs: ['auto-archive', 'worktree-cleanup', 'composer-attachment-sweep',
    'webhook-history', 'history-journal-prune', 'plugin-conversation-prune', 'plugin-session-prune'] },
  { label: 'Session and runtime coordination', jobs: ['idle-projects', 'autoapprove-watcher', 'platform-reload',
    'queue-sweep', 'session-reference-recovery', 'snooze', 'managed-runtime-health', 'machine-supervisor', 'opencode-version'] },
  { label: 'Notifications and plugins', jobs: ['web-push', 'web-push-delivery', 'inbox-reconcile',
    'plugin-conversation-delivery', 'plugin-session-delivery', 'plugin-conversation-reconcile', 'plugin-session-reconcile'] },
  { label: 'Scheduled automation and synchronization', jobs: ['routines', 'pr-watch', 'webhook-poll',
    'remote-project-inventory', 'projects-index'] },
  { label: 'Metrics and accounting', jobs: ['llm-metrics', 'subscription-usage', 'stats-metrics', 'database-size'] },
  { label: 'History synchronization', jobs: [] },
  { label: 'Other tasks', jobs: [] },
];

export function backgroundTaskCategory(kind: string): string {
  if (kind.startsWith('conversation.history')) return 'History synchronization';
  if (kind === 'session.preparation') return 'Session and runtime coordination';
  const job = kind.replace(/^schedule\./, '');
  return categories.find(category => category.jobs.some(name => job === name || job.startsWith(`${name}.`)))?.label ?? 'Other tasks';
}

export function categorizeBackgroundTasks(groups: TaskGroup[]) {
  return categories.map(({ label }) => ({
    label,
    groups: groups.filter(group => backgroundTaskCategory(group.current.kind) === label)
      .sort((a, b) => a.current.title.localeCompare(b.current.title) || a.key.localeCompare(b.key)),
  })).filter(category => category.groups.length > 0);
}
