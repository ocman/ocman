export function providerUsageLevel(percent: number) {
  if (percent >= 100) return 'danger';
  return percent > 80 ? 'warning' : 'normal';
}
