export function formatNumber(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K';
  return n.toLocaleString();
}

export function formatCompactNumber(n: number): string {
  if (n >= 1e9) return (n / 1e9).toFixed(1) + 'B';
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K';
  return n.toLocaleString();
}

export function formatDuration(ms: number): string {
  if (ms < 1000) return '< 1s';
  const s = Math.floor(ms / 1000);
  if (s < 60) return s + 's';
  const m = Math.floor(s / 60);
  if (m < 60) return m + 'm ' + (s % 60) + 's';
  const h = Math.floor(m / 60);
  return h + 'h ' + (m % 60) + 'm';
}

export function formatSeconds(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '0s';
  if (seconds < 10) return seconds.toFixed(1) + 's';
  if (seconds < 60) return Math.round(seconds) + 's';
  const mins = Math.floor(seconds / 60);
  const secs = Math.round(seconds % 60);
  return mins + 'm ' + secs + 's';
}

export function formatPercent(value: number, digits = 0): string {
  return `${(value * 100).toFixed(digits)}%`;
}

export function formatCurrency(value: number, digits = 4): string {
  return `$${value.toFixed(digits)}`;
}

export function formatTokenCache(read: number, write: number): string {
  return `${formatCompactNumber(read)}r/${formatCompactNumber(write)}w`;
}

export function formatDateTimeShort(ts: number): string {
  return new Date(ts).toLocaleString('en-US', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  });
}

export function formatDate(ts: number): string {
  return new Date(ts).toLocaleDateString('en-US', {
    month: 'short', day: 'numeric', year: 'numeric',
    hour: '2-digit', minute: '2-digit',
  });
}

export function relativeTime(ts: number): string {
  const diff = Date.now() - ts;
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return mins + 'm ago';
  const hours = Math.floor(mins / 60);
  if (hours < 24) return hours + 'h ago';
  const days = Math.floor(hours / 24);
  if (days < 30) return days + 'd ago';
  return formatDate(ts);
}

export function shortPath(path: string): string {
  if (!path) return '';
  const parts = path.split('/');
  return parts.slice(-2).join('/');
}

export function escapeHtml(str: string): string {
  if (!str) return '';
  return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// cleanTitle strips the common markdown decorations that LLMs tend to emit in
// generated session titles: leading `#` headings, `**bold**` / `*italic*` /
// `__bold__` / `_italic_` emphasis, `~~strike~~`, inline `` `code` ``, and
// `[text](url)` links (keeping the link text). It is tolerant of unbalanced
// markers (e.g. `**Title` with no closing pair) — those are also stripped so
// users never see raw asterisks in lists and titles.
//
// This operates on a single line of text. It is NOT a full markdown parser and
// does not try to be clever about escapes; titles are short and the goal is
// purely visual cleanup for list/heading display.
export function cleanTitle(str: string | null | undefined): string {
  if (!str) return '';
  let out = str;
  // [text](url) -> text. Do links first so we don't strip emphasis inside URLs.
  out = out.replace(/\[([^\]]+)\]\([^)]*\)/g, '$1');
  // Inline code `code` -> code.
  out = out.replace(/`([^`]*)`/g, '$1');
  // Strip leading heading markers and any trailing closing `#` (ATX style).
  out = out.replace(/^\s{0,3}#{1,6}\s+/, '').replace(/\s+#+\s*$/, '');
  // Bold/italic: ** __ * _ ~~. Replace both balanced pairs and any stray
  // markers. We walk from longest to shortest so ** is handled before *.
  out = out.replace(/\*\*/g, '');
  out = out.replace(/__/g, '');
  out = out.replace(/~~/g, '');
  out = out.replace(/(^|[^\w])[*_]([^*_\s][^*_]*?)[*_](?=[^\w]|$)/g, '$1$2');
  // Any remaining stray * or _ at word boundaries are noise — drop them.
  out = out.replace(/(^|\s)[*_]+(\s|$)/g, '$1$2');
  return out.trim();
}
