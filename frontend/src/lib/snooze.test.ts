import { expect, it } from 'vitest';
import { snoozeDeadline, snoozeLabel } from './snooze';

it('formats configurable durations and clock times', () => {
  expect(snoozeLabel({ kind: 'duration', minutes: 15 })).toBe('In 15 mins');
  expect(snoozeLabel({ kind: 'duration', minutes: 60 })).toBe('In 1 hour');
  expect(snoozeLabel({ kind: 'duration', minutes: 120 })).toBe('In 2 hours');
  expect(snoozeLabel({ kind: 'tomorrow', time: '00:05' })).toBe('Tomorrow at 12:05 AM');
  expect(snoozeLabel({ kind: 'monday', time: '12:30' })).toBe('Monday at 12:30 PM');
  expect(snoozeLabel({ kind: 'tomorrow', time: '17:45' })).toBe('Tomorrow at 5:45 PM');
});

it('uses configured local times and calendar days across daylight saving changes', () => {
  const now = new Date(2026, 9, 24, 23, 30);
  expect(snoozeDeadline({ kind: 'duration', minutes: 15 }, now)).toBe(now.getTime() + 900_000);
  expect(new Date(snoozeDeadline({ kind: 'tomorrow', time: '07:45' }, now))).toEqual(new Date(2026, 9, 25, 7, 45));
  expect(new Date(snoozeDeadline({ kind: 'monday', time: '16:15' }, now))).toEqual(new Date(2026, 9, 26, 16, 15));
});
