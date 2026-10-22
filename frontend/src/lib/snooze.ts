export type SnoozeChoice = { kind: 'duration'; minutes: number } | { kind: 'tomorrow' | 'monday'; time: string };
export const DEFAULT_SNOOZE_CHOICES: SnoozeChoice[] = [
  { kind: 'duration', minutes: 30 }, { kind: 'duration', minutes: 60 }, { kind: 'duration', minutes: 180 },
  { kind: 'tomorrow', time: '08:00' }, { kind: 'monday', time: '08:00' },
];

export function snoozeLabel(choice: SnoozeChoice): string {
  if (choice.kind === 'duration') {
    return choice.minutes % 60 === 0 ? `In ${choice.minutes / 60} ${choice.minutes === 60 ? 'hour' : 'hours'}` : `In ${choice.minutes} mins`;
  }
  const [hour, minute] = choice.time.split(':').map(Number);
  return `${choice.kind === 'tomorrow' ? 'Tomorrow' : 'Monday'} at ${hour % 12 || 12}:${String(minute).padStart(2, '0')} ${hour < 12 ? 'AM' : 'PM'}`;
}

export function snoozeDeadline(option: number | SnoozeChoice, now = new Date()): number {
  const choice = typeof option === 'number' ? DEFAULT_SNOOZE_CHOICES[option] : option;
  if (choice.kind === 'duration') return now.getTime() + choice.minutes * 60_000;
  const date = new Date(now);
  date.setDate(date.getDate() + (choice.kind === 'tomorrow' ? 1 : (8 - date.getDay()) % 7 || 7));
  const [hour, minute] = choice.time.split(':').map(Number);
  date.setHours(hour, minute, 0, 0);
  return date.getTime();
}
