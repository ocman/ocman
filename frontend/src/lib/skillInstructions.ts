export function parseSkillInstructions(text: string) {
  const header = /^Base directory for this skill: [^\r\n]+[/\\]([^/\\\r\n]+)\r?\n\r?\n/.exec(text);
  if (header) return { name: header[1], instructions: text, rest: '' };
  const footer = /^Base directory for this skill: [^\r\n]+[/\\]([^/\\\r\n]+)\r?\nRelative paths in this skill [^\r\n]*are relative to this base directory\./m.exec(text);
  if (!footer) return null;
  const end = footer.index + footer[0].length;
  return { name: footer[1], instructions: text.slice(0, end), rest: text.slice(end).trimStart() };
}
