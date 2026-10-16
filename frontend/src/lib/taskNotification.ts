const entities: Record<string, string> = {
  '&amp;': '&', '&lt;': '<', '&gt;': '>', '&quot;': '"', '&apos;': "'",
};

function decode(text: string): string {
  return text.replace(/&(?:amp|lt|gt|quot|apos);/g, (entity) => entities[entity]);
}

export function parseAgentResult(text: string): { text: string; details: string[][] } {
  const footer = /(?:^|\n)agentId: ([^\s()]+)(?: (\([^\n]*\)))?\r?\n<usage>\s*subagent_tokens: (\d+)\s+tool_uses: (\d+)\s+duration_ms: (\d+)\s*<\/usage>\s*$/.exec(text);
  if (!footer) return { text, details: [] };
  return {
    text: text.slice(0, footer.index).trimEnd(),
    details: [
      ['Agent ID', footer[1]],
      ['Resume', footer[2] || ''],
      ['Subagent tokens', footer[3]],
      ['Tool uses', footer[4]],
      ['Duration (ms)', footer[5]],
    ].filter(([, value]) => value),
  };
}

// Only consume a complete standalone envelope, not examples embedded in prompts.
export function parseTaskNotification(text: string) {
  const envelope = /^\s*<task-notification>([\s\S]*?)<\/task-notification>\s*$/.exec(text);
  if (!envelope) return null;
  // Results contain Markdown, including raw ampersands and HTML. They are not XML.
  const result = /<result>([\s\S]*?)<\/result>/.exec(envelope[1]);
  const metadata = result ? envelope[1].replace(result[0], '') : envelope[1];
  if (/<\/?(?:task-notification|result)>/.test(metadata)) return null;
  const field = (name: string) => decode(new RegExp(`<${name}>([\\s\\S]*?)</${name}>`).exec(metadata)?.[1]?.trim() || '');
  const summary = field('summary');
  const status = field('status');
  // Monitor events report an <event> line and no status.
  const event = field('event');
  if (!summary || !(status || event)) return null;
  return {
    summary, status,
    result: decode(result?.[1]?.trim() || '') || event,
    details: [
      ['Task ID', field('task-id')],
      ['Tool use ID', field('tool-use-id')],
      ['Output file', field('output-file')],
      ['Note', field('note')],
      ['Subagent tokens', field('subagent_tokens')],
      ['Tool uses', field('tool_uses')],
      ['Duration (ms)', field('duration_ms')],
    ].filter(([, value]) => value),
  };
}
