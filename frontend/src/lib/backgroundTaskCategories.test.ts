import { expect, it } from 'vitest';
import { backgroundTaskCategory, categorizeBackgroundTasks } from './backgroundTaskCategories';
import { groupBackgroundTasks } from './backgroundTaskGroups';
import type { BackgroundTask } from './backgroundTasks';

it.each([
  ['Housekeeping', ['auto-archive', 'worktree-cleanup', 'composer-attachment-sweep', 'webhook-history',
    'history-journal-prune.connection', 'plugin-conversation-prune', 'plugin-session-prune']],
  ['Session and runtime coordination', ['idle-projects', 'autoapprove-watcher', 'platform-reload', 'queue-sweep',
    'session-reference-recovery', 'snooze', 'managed-runtime-health', 'machine-supervisor', 'opencode-version']],
  ['Notifications and plugins', ['web-push', 'web-push-delivery', 'inbox-reconcile', 'plugin-conversation-delivery',
    'plugin-session-delivery', 'plugin-conversation-reconcile', 'plugin-session-reconcile']],
  ['Scheduled automation and synchronization', ['routines', 'pr-watch', 'webhook-poll.inbox', 'remote-project-inventory', 'projects-index']],
  ['Metrics and accounting', ['llm-metrics', 'subscription-usage', 'stats-metrics', 'database-size']],
])('classifies %s jobs by kind rather than presentation titles', (label, jobs) => {
  for (const job of jobs) expect(backgroundTaskCategory(`schedule.${job}`)).toBe(label);
});

it('classifies history and preparation work and keeps unknown kinds visible', () => {
  expect(backgroundTaskCategory('conversation.history')).toBe('History synchronization');
  expect(backgroundTaskCategory('conversation.history.verify')).toBe('History synchronization');
  expect(backgroundTaskCategory('session.preparation')).toBe('Session and runtime coordination');
  expect(backgroundTaskCategory('schedule.new-job')).toBe('Other tasks');
  expect(backgroundTaskCategory('schedule.web-push-unrelated')).toBe('Other tasks');
  expect(categorizeBackgroundTasks([])).toEqual([]);
});

it('sorts categories and task names consistently, breaking tied names by group identity without mutating input', () => {
  const tasks = ['z', 'b', 'a'].map((key): BackgroundTask => ({
    id: key, key, title: key === 'z' ? 'Zebra' : 'Archive', kind: 'schedule.auto-archive', ownerId: 'local',
    status: 'succeeded', createdAt: 1, updatedAt: 2, progress: { phase: '', completed: 0, total: null, counters: {} },
  }));
  const groups = groupBackgroundTasks(tasks).reverse();
  const original = groups.map(group => group.key);
  expect(categorizeBackgroundTasks(groups)[0].groups.map(group => group.current.key)).toEqual(['a', 'b', 'z']);
  expect(groups.map(group => group.key)).toEqual(original);
});
