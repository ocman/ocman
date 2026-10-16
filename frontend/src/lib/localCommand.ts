// Only consume complete transcript envelopes, never tags embedded in a prompt.
export function parseLocalCommand(text: string) {
  // Claude writes the tags in either order; skill invocations omit the args.
  if (/^\s*(?:<(command-name|command-message|command-args)>[\s\S]*?<\/\1>\s*)+$/.test(text)) {
    const tags = [...text.matchAll(/<(command-name|command-message|command-args)>([\s\S]*?)<\/\1>/g)];
    const value = (name: string) => tags.filter((t) => t[1] === name).map((t) => t[2]);
    const [name, ...extra] = value('command-name');
    if (name?.trim() && !name.includes('<') && !extra.length && value('command-message').length === 1 && value('command-args').length <= 1) {
      return { kind: 'command', text: [name.trim(), value('command-args')[0]?.trim()].filter(Boolean).join(' ') };
    }
  }
  const output = /^\s*<local-command-(stdout|stderr)>([\s\S]*?)<\/local-command-\1>\s*$/.exec(text);
  if (output) return { kind: output[1], text: output[2].trim() };
  return null;
}
