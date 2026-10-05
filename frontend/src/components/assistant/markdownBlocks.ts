/**
 * Split markdown into top-level chunks that parse to exactly the same
 * output separately as together, so a streaming answer can keep its
 * completed chunks memoized and only re-parse the growing tail.
 *
 * A split happens only at a blank line outside a fenced code block whose
 * next line starts at column 0 and is not a list marker: such a line always
 * starts a new top-level block (it cannot continue a list item, an indented
 * code block or a loose list). Documents whose meaning can span chunks
 * (link reference / footnote definitions, raw HTML blocks that survive blank
 * lines) are returned whole.
 */
const NON_LOCAL = /^ {0,3}(\[[^\]]+\]:|<(script|pre|style|textarea|!--|\?|![A-Za-z]|!\[CDATA\[))/im;
const FENCE = /^ {0,3}(`{3,}|~{3,})(.*)$/;
const LIST_MARKER = /^([-+*]|\d{1,9}[.)])(\s|$)/;

export function splitMarkdownBlocks(text: string): string[] {
  if (NON_LOCAL.test(text)) return [text];
  const lines = text.split('\n');
  const blocks: string[] = [];
  let start = 0;
  let fence: { char: string; len: number } | null = null;
  let prevBlank = false;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const m = FENCE.exec(line);
    if (fence) {
      if (m && m[1][0] === fence.char && m[1].length >= fence.len && !m[2].trim()) fence = null;
      prevBlank = false;
      continue;
    }
    const blank = !line.trim();
    if (prevBlank && !blank && i > start && !/^\s/.test(line) && !LIST_MARKER.test(line)) {
      // Drop the separating blank lines; the caller re-joins with '\n'.
      let end = i;
      while (end > start && !lines[end - 1].trim()) end--;
      if (end > start) blocks.push(lines.slice(start, end).join('\n'));
      start = i;
    }
    if (m && !(m[1][0] === '`' && m[2].includes('`'))) fence = { char: m[1][0], len: m[1].length };
    prevBlank = blank;
  }
  blocks.push(lines.slice(start).join('\n'));
  return blocks;
}
