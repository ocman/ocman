export const SNOOZE_OPTIONS = ['In 30 mins', 'In 1 hour', 'In 3 hours', 'Tomorrow at 9:00 AM', 'Monday at 9:00 AM'] as const;

export function snoozeDeadline(option: number, now = new Date()): number {
  if (option < 3) return now.getTime() + [30, 60, 180][option] * 60_000;
  const date = new Date(now);
  date.setDate(date.getDate() + (option === 3 ? 1 : (8 - date.getDay()) % 7 || 7));
  date.setHours(9, 0, 0, 0);
  return date.getTime();
}
