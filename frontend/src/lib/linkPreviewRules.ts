import { fetchJSON, postJSON } from './api';

export interface LinkPreviewRule {
  pattern: string;
  replacement: string;
}

const endpoint = '/api/settings/link-preview-rules';
let cached: Promise<LinkPreviewRule[]> | undefined;

export function loadLinkPreviewRules(): Promise<LinkPreviewRule[]> {
  cached ??= fetchJSON<{ rules: LinkPreviewRule[] }>(endpoint).then(
    ({ rules }) => rules,
    (error: unknown) => { cached = undefined; throw error; },
  );
  return cached;
}

export async function saveLinkPreviewRules(rules: LinkPreviewRule[]): Promise<void> {
  const result = await postJSON<{ rules: LinkPreviewRule[] }>(endpoint, { rules });
  cached = Promise.resolve(result.rules);
  window.dispatchEvent(new Event('ocman:link-preview-rules-changed'));
}

export function extractCustomLinks(text: string, rules: LinkPreviewRule[]): { label: string; url: string }[] {
  const links: { label: string; url: string }[] = [];
  const seen = new Set<string>();
  for (const rule of rules) {
    try {
      const regex = new RegExp(rule.pattern, 'g');
      for (const match of text.matchAll(regex)) {
        if (!match[0]) continue;
        const destination = match[0].replace(new RegExp(rule.pattern), rule.replacement);
        const url = new URL(destination);
        if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password || seen.has(url.href)) continue;
        seen.add(url.href);
        links.push({ label: match[0], url: url.href });
      }
    } catch { /* Ignore invalid rules or expanded URLs from older settings. */ }
  }
  return links;
}
