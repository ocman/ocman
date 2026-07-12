import type { Message, Part, PartData } from './api';

// Parse a Part's data (string JSON or already-decoded object), mirroring
// convertMessages.ts. Returns null when it can't be decoded.
function partData(p: Part): PartData | null {
  try {
    const d = typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
    return (d || null) as PartData | null;
  } catch {
    return null;
  }
}

const ROLE_HEADINGS: Record<string, string> = {
  user: '## User',
  assistant: '## Assistant',
  notice: '## Notice',
};

/**
 * Serialize a session's messages + parts to a Markdown transcript.
 *
 * Kept intentionally small: it renders the conversational text and
 * reasoning blocks (what a portable transcript needs), skipping the
 * tool-call detail the live UI expands. Formatting stays close to
 * OpenCode's own /export so output is portable.
 */
export function serializeSessionMarkdown(
  title: string | undefined,
  messages: Message[],
  parts: Part[],
): string {
  const partsByMsg = new Map<string, Part[]>();
  for (const p of parts) {
    const list = partsByMsg.get(p.messageId);
    if (list) list.push(p);
    else partsByMsg.set(p.messageId, [p]);
  }

  const lines: string[] = [`# ${title?.trim() || 'Session transcript'}`, ''];

  for (const m of messages) {
    const role = m.data?.role;
    if (role !== 'user' && role !== 'assistant' && role !== 'notice') continue;

    const blocks: string[] = [];
    for (const p of partsByMsg.get(m.id) ?? []) {
      const pd = partData(p);
      if (!pd) continue;
      if ((pd.type === 'text' || pd.type === 'reasoning') && pd.text?.trim()) {
        blocks.push(pd.text.trim());
      }
    }
    if (blocks.length === 0) continue;

    lines.push(ROLE_HEADINGS[role], '', blocks.join('\n\n'), '');
  }

  return lines.join('\n');
}

// Slugify a session title into a safe filename component.
export function exportFilename(title: string | undefined): string {
  const slug = (title || 'session')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60);
  return `session-${slug || 'session'}.md`;
}

// Trigger a browser download of the Markdown transcript. Split from the
// pure serializer so the serializer stays unit-testable.
export function downloadSessionMarkdown(
  title: string | undefined,
  messages: Message[],
  parts: Part[],
): void {
  const md = serializeSessionMarkdown(title, messages, parts);
  const blob = new Blob([md], { type: 'text/markdown;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = exportFilename(title);
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}
