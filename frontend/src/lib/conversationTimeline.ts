function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

export function formatTimelineMarker(timestamp: number, now: Date): string {
  const date = new Date(timestamp);
  const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
  const time = date.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
  if (sameDay(date, now)) return `Today ${time}`;
  if (sameDay(date, yesterday)) return `Yesterday ${time}`;

  const day = date.getDate();
  const suffix = day % 10 === 1 && day % 100 !== 11 ? 'st'
    : day % 10 === 2 && day % 100 !== 12 ? 'nd'
      : day % 10 === 3 && day % 100 !== 13 ? 'rd' : 'th';
  const weekday = date.toLocaleDateString('en-US', { weekday: 'long' });
  const month = date.toLocaleDateString('en-US', { month: 'long' });
  return `${weekday} ${month} ${day}${suffix}, ${time}`;
}
