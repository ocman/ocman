// OpenCode v2 emits this both as a tool result and a synthetic completion.
// Like shell notifications, description and output are interpolated, not XML.
export function parseSubagentEnvelope(text: unknown) {
  if (typeof text !== 'string') return null;
  const envelope = /^\s*<subagent sessionID="([^"\r\n]+)" state="(completed|error|cancelled)"(?: description="([\s\S]*?)")?>\r?\n([\s\S]*)\r?\n<\/subagent>\s*$/.exec(text);
  if (!envelope || /<\/?subagent(?:\s|>)/.test(envelope[4])) return null;
  return { sessionID: envelope[1], status: envelope[2], description: envelope[3] || 'Subagent task', output: envelope[4] };
}
